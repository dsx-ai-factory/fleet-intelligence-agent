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

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/process"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/systemd"
	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/cmdutil"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/endpoint"
)

type enrollmentStatus struct {
	baseURL         string
	metricsEndpoint string
	logsEndpoint    string
}

func statusCommand(cliContext *cli.Context) error {
	logLevel := cliContext.String("log-level")
	serverURL := cliContext.String("server-url")
	zapLvl, err := log.ParseLogLevel(logLevel)
	if err != nil {
		return err
	}
	log.Logger = log.CreateLogger(zapLvl, "")

	log.Logger.Debugw("starting status command")

	validatedServerURL, err := endpoint.ValidateLocalServerURL(serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	rootCtx, rootCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer rootCancel()

	log.Logger.Debugw("getting state file")
	stateFile, err := config.DefaultStateFile()
	if err != nil {
		return fmt.Errorf("failed to get state file: %w", err)
	}
	log.Logger.Debugw("successfully got state file")

	// Check if we have read permission to the state file
	if _, err := os.Open(stateFile); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("insufficient permissions to read state file %s. Please run with sudo", stateFile)
		}
		// If it's not a permission error, continue - the sqlite.Open call below will handle other issues
	}

	log.Logger.Debugw("opening state file for reading")
	dbRO, err := sqlite.Open(stateFile, sqlite.WithReadOnly(true))
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err)
	}
	defer dbRO.Close()
	log.Logger.Debugw("successfully opened state file for reading")

	enrollment, err := readEnrollmentStatus(rootCtx, dbRO)
	if err != nil {
		return err
	}

	if enrollment.baseURL != "" || enrollment.metricsEndpoint != "" || enrollment.logsEndpoint != "" {
		fmt.Printf("%s enrolled\n", cmdutil.CheckMark)
		if enrollment.metricsEndpoint != "" {
			fmt.Printf("  metrics endpoint: %s\n", enrollment.metricsEndpoint)
		}
		if enrollment.logsEndpoint != "" {
			fmt.Printf("  logs endpoint:    %s\n", enrollment.logsEndpoint)
		}
	} else {
		fmt.Printf("%s not enrolled (no backend or legacy endpoints configured)\n", cmdutil.WarningSign)
	}

	var active bool
	if systemd.SystemctlExists() {
		active, err = systemd.IsActive("fleetintd.service")
		if err != nil {
			return err
		}
		if !active {
			fmt.Printf("%s fleetintd.service is not active\n", cmdutil.WarningSign)
		} else {
			fmt.Printf("%s fleetintd.service is active\n", cmdutil.CheckMark)
		}
	}
	if !active {
		// fallback to process list
		// in case it's not using systemd
		proc, err := process.FindProcessByName(rootCtx, "fleetint")
		if err != nil {
			return err
		}
		if proc == nil {
			fmt.Printf("%s fleetint process is not running\n", cmdutil.WarningSign)
			return nil
		}

		fmt.Printf("%s fleetint process is running (PID %d)\n", cmdutil.CheckMark, proc.PID())
	}
	fmt.Printf("%s successfully checked fleetint status\n", cmdutil.CheckMark)

	// Check server health. If the default unix socket fails, fall back to the
	// legacy TCP address so upgrades don't break existing --listen-address
	// overrides or monitoring scripts.
	client := endpoint.NewAgentHTTPClient(validatedServerURL)

	healthURL, err := endpoint.JoinPath(endpoint.AgentBaseURL(validatedServerURL), "healthz")
	if err != nil {
		return fmt.Errorf("failed to construct health URL: %w", err)
	}

	resp, err := client.Get(healthURL)
	if err != nil && validatedServerURL.Scheme == "unix" {
		tcpFallback := fmt.Sprintf("http://localhost:%d", config.DefaultHealthPort)
		tcpURL, parseErr := endpoint.ValidateLocalServerURL(tcpFallback)
		if parseErr == nil {
			tcpClient := endpoint.NewAgentHTTPClient(tcpURL)
			tcpHealthURL, _ := endpoint.JoinPath(endpoint.AgentBaseURL(tcpURL), "healthz")
			resp, err = tcpClient.Get(tcpHealthURL)
		}
	}
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	fmt.Printf("%s successfully checked fleetint health\n", cmdutil.CheckMark)
	return nil
}

func readEnrollmentStatus(ctx context.Context, dbRO *sql.DB) (*enrollmentStatus, error) {
	baseURL, err := pkgmetadata.ReadMetadata(ctx, dbRO, agentstate.MetadataKeyBackendBaseURL)
	if err != nil && !isMissingMetadata(err) {
		return nil, fmt.Errorf("failed to read backend base URL: %w", err)
	}

	status := &enrollmentStatus{baseURL: baseURL}
	if baseURL != "" {
		validated, err := endpoint.ValidateBackendEndpoint(baseURL)
		if err != nil {
			log.Logger.Warnw("ignoring invalid backend base URL in metadata", "backend_base_url", baseURL, "error", err)
			status.baseURL = ""
		} else {
			status.metricsEndpoint, err = endpoint.JoinPath(validated, "api", "v1", "health", "metrics")
			if err != nil {
				return nil, fmt.Errorf("failed to construct metrics endpoint: %w", err)
			}
			status.logsEndpoint, err = endpoint.JoinPath(validated, "api", "v1", "health", "logs")
			if err != nil {
				return nil, fmt.Errorf("failed to construct logs endpoint: %w", err)
			}
			return status, nil
		}
	}

	status.metricsEndpoint, err = readLegacyEndpoint(ctx, dbRO, "metrics_endpoint")
	if err != nil {
		return nil, err
	}
	status.logsEndpoint, err = readLegacyEndpoint(ctx, dbRO, "logs_endpoint")
	if err != nil {
		return nil, err
	}
	return status, nil
}

func readLegacyEndpoint(ctx context.Context, dbRO *sql.DB, key string) (string, error) {
	value, err := pkgmetadata.ReadMetadata(ctx, dbRO, key)
	if err != nil && !isMissingMetadata(err) {
		return "", fmt.Errorf("failed to read %s: %w", key, err)
	}
	return value, nil
}

func isMissingMetadata(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "no such table")
}
