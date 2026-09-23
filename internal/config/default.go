// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"context"
	"fmt"
	stdos "os"
	"path/filepath"
	"time"

	"github.com/mitchellh/go-homedir"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nvidiacommon "github.com/NVIDIA/fleet-intelligence-sdk/pkg/config/common"
)

const (
	// DefaultAPIVersion for health server
	DefaultAPIVersion = "v1"

	// DefaultHealthPort for health metrics export
	DefaultHealthPort = 15133

	// DefaultListenHost is the default host to bind to (localhost only for security)
	DefaultListenHost = "127.0.0.1"

	// DefaultUnixSocketPath is the default unix socket path for the fleetint server.
	// Using a unix socket instead of a TCP port restricts access via filesystem permissions,
	// preventing unauthenticated network access even if a firewall rule is misconfigured.
	DefaultUnixSocketPath = "/run/fleetint/fleetint.sock"

	// DefaultInventoryTimeout is the maximum duration for one inventory loop attempt.
	DefaultInventoryTimeout = time.Minute

	// DefaultAttestationTimeout is the maximum duration for one attestation loop attempt.
	DefaultAttestationTimeout = time.Minute

	// MinInventoryInterval is the shortest supported steady-state inventory interval.
	MinInventoryInterval = 5 * time.Minute

	// MinAttestationInterval is the shortest supported steady-state attestation interval.
	MinAttestationInterval = 5 * time.Minute
)

var (
	// DefaultListenAddress is the default listen address (unix socket for security).
	// Override with --listen-address 127.0.0.1:15133 to use TCP instead.
	DefaultListenAddress = DefaultUnixSocketPath

	// DefaultClientURL is the default URL for client commands to connect to the server.
	// A bare absolute path is treated as a unix socket by ValidateLocalServerURL.
	DefaultClientURL = DefaultUnixSocketPath

	// DefaultRetentionPeriod - keep health data for 24 hours by default
	DefaultRetentionPeriod = metav1.Duration{Duration: 24 * time.Hour}

	osGeteuid  = stdos.Geteuid
	osStat     = stdos.Stat
	osMkdirAll = stdos.MkdirAll
	osChmod    = stdos.Chmod
	homeDirFn  = homedir.Dir
)

// Default creates a default health configuration
func Default(ctx context.Context, opts ...OpOption) (*Config, error) {
	options := &Op{}
	if err := options.ApplyOpts(opts); err != nil {
		return nil, err
	}

	cfg := &Config{
		APIVersion:           DefaultAPIVersion,
		Address:              DefaultListenAddress,
		RetentionPeriod:      DefaultRetentionPeriod,
		EnableFaultInjection: false, // Disabled by default for security
		Inventory: &InventoryConfig{
			Enabled:  true,
			Interval: metav1.Duration{Duration: 1 * time.Hour},
		},
		Attestation: &AttestationConfig{
			Enabled:  true,
			Interval: metav1.Duration{Duration: 24 * time.Hour},
		},
		NvidiaToolOverwrites: nvidiacommon.ToolOverwrites{
			InfinibandClassRootDir: options.InfinibandClassRootDir,
		},
		// Health exporter is enabled by default
		HealthExporter: &HealthExporterConfig{
			MetricsEndpoint:      "",
			LogsEndpoint:         "",
			AuthToken:            "",
			Interval:             metav1.Duration{Duration: 1 * time.Minute},
			Timeout:              metav1.Duration{Duration: 30 * time.Second},
			IncludeMetrics:       true,
			IncludeEvents:        true,
			IncludeMachineInfo:   true,
			IncludeComponentData: true,
			MetricsLookback:      metav1.Duration{Duration: 1 * time.Minute},
			EventsLookback:       metav1.Duration{Duration: 1 * time.Minute},
			HealthCheckInterval:  metav1.Duration{Duration: 30 * time.Second}, // Default 30 seconds for component health checks and metric scraping
			RetryMaxAttempts:     3,                                           // Retry up to 3 times
			OutputFormat:         "json",                                      // Default to JSON format for offline mode
		},
	}

	if cfg.State == "" {
		var err error
		cfg.State, err = DefaultStateFile()
		if err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

const (
	defaultVarLibDir     = "/var/lib/fleetint"
	defaultStateDirMode  = 0o700
	defaultStateFileMode = 0o600
)

// setupDefaultDir creates the default directory for health data
func setupDefaultDir() (string, error) {
	asRoot := osGeteuid() == 0 // running as root

	d := defaultVarLibDir
	_, err := osStat("/var/lib")
	if !asRoot || stdos.IsNotExist(err) {
		homeDir, err := homeDirFn()
		if err != nil {
			return "", err
		}
		d = filepath.Join(homeDir, ".fleetint")
	}

	if _, err := osStat(d); stdos.IsNotExist(err) {
		if err = osMkdirAll(d, defaultStateDirMode); err != nil {
			return "", err
		}
	}
	if err := osChmod(d, defaultStateDirMode); err != nil {
		return "", err
	}
	return d, nil
}

// DefaultStateFile returns the default path for the health state database
func DefaultStateFile() (string, error) {
	dir, err := setupDefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fleetint.state"), nil
}

// SecureStateFilePermissions ensures the state database file and SQLite sidecars are owner-readable and owner-writable only.
func SecureStateFilePermissions(path string) error {
	if path == "" || path == ":memory:" {
		return nil
	}

	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := osStat(candidate)
		if err != nil {
			if stdos.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("state file path %q is a directory", candidate)
		}
		if err := osChmod(candidate, defaultStateFileMode); err != nil {
			return err
		}
	}

	return nil
}
