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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDefault(t *testing.T) {
	ctx := context.Background()

	t.Run("default values", func(t *testing.T) {
		cfg, err := Default(ctx)
		require.NoError(t, err)

		// Check basic properties
		assert.Equal(t, DefaultAPIVersion, cfg.APIVersion)
		assert.Equal(t, DefaultListenAddress, cfg.Address)
		assert.Equal(t, DefaultRetentionPeriod, cfg.RetentionPeriod)
		require.NotNil(t, cfg.Inventory)
		assert.True(t, cfg.Inventory.Enabled)
		assert.Equal(t, metav1.Duration{Duration: 1 * time.Hour}, cfg.Inventory.Interval)
		require.NotNil(t, cfg.Attestation)
		assert.True(t, cfg.Attestation.Enabled)
		assert.Equal(t, metav1.Duration{Duration: 24 * time.Hour}, cfg.Attestation.Interval)

		// State path should be set
		assert.NotEmpty(t, cfg.State, "State path should be set")
	})

	t.Run("with infiniband class dir option", func(t *testing.T) {
		classDir := "/custom/class"

		cfg, err := Default(ctx, WithInfinibandClassRootDir(classDir))
		require.NoError(t, err)

		assert.Equal(t, classDir, cfg.NvidiaToolOverwrites.InfinibandClassRootDir)
	})
}

func TestConfigValidation(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("missing address", func(t *testing.T) {
		cfg := &Config{
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "address is required")
	})

	t.Run("retention period too short", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: 30 * time.Second},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "retention_period must be at least 1 minute")
	})

	t.Run("inventory sync enabled without interval", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			Inventory: &InventoryConfig{
				Enabled: true,
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "inventory.interval is required")
	})

	t.Run("inventory sync interval too short", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			Inventory: &InventoryConfig{
				Enabled:  true,
				Interval: metav1.Duration{Duration: 500 * time.Millisecond},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "inventory.interval must be at least 5m0s")
	})

	t.Run("attestation enabled without interval", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			Attestation: &AttestationConfig{
				Enabled: true,
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "attestation.interval is required")
	})

	t.Run("attestation interval too short", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			Attestation: &AttestationConfig{
				Enabled:  true,
				Interval: metav1.Duration{Duration: 500 * time.Millisecond},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "attestation.interval must be at least 5m0s")
	})

}

func TestComponentSelection(t *testing.T) {
	t.Run("enable all components by default", func(t *testing.T) {
		cfg := &Config{}

		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("cpu"))
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
		assert.False(t, cfg.ShouldDisable("cpu"))
	})

	t.Run("enable specific components", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"gpu-memory", "cpu"},
		}

		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("cpu"))
		assert.False(t, cfg.ShouldEnable("disk"))
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
	})

	t.Run("disable specific components", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"-disk", "-network"},
		}

		assert.True(t, cfg.ShouldDisable("disk"))
		assert.True(t, cfg.ShouldDisable("network"))
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
		assert.False(t, cfg.ShouldEnable("disk"))
	})

	t.Run("wildcard enables all", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"*"},
		}

		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("any-component"))
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
	})

	t.Run("all keyword enables all", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"all"},
		}

		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("any-component"))
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
	})

	t.Run("all with exclusions", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"all", "-kubelet", "-docker", "-tailscale", "-accelerator-nvidia-gsp-firmware"},
		}

		// Should enable all components
		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("cpu"))
		assert.True(t, cfg.ShouldEnable("kubelet"))
		assert.True(t, cfg.ShouldEnable("docker"))

		// Should disable the excluded components
		assert.True(t, cfg.ShouldDisable("kubelet"))
		assert.True(t, cfg.ShouldDisable("docker"))
		assert.True(t, cfg.ShouldDisable("tailscale"))
		assert.True(t, cfg.ShouldDisable("accelerator-nvidia-gsp-firmware"))

		// Should not disable non-excluded components
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
		assert.False(t, cfg.ShouldDisable("cpu"))
	})
}

func TestDefaultStateFile(t *testing.T) {
	origGeteuid := osGeteuid
	origHomeDirFn := homeDirFn
	t.Cleanup(func() {
		osGeteuid = origGeteuid
		homeDirFn = origHomeDirFn
	})

	tmpHome := t.TempDir()
	osGeteuid = func() int { return 1000 }
	homeDirFn = func() (string, error) { return tmpHome, nil }

	path, err := DefaultStateFile()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpHome, ".fleetint", "fleetint.state"), path)
	assert.Contains(t, path, "fleetint.state")

	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestDefaultStateFileRepairsExistingDirectoryPermissions(t *testing.T) {
	origGeteuid := osGeteuid
	origHomeDirFn := homeDirFn
	t.Cleanup(func() {
		osGeteuid = origGeteuid
		homeDirFn = origHomeDirFn
	})

	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".fleetint")
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.Chmod(stateDir, 0o755))

	osGeteuid = func() int { return 1000 }
	homeDirFn = func() (string, error) { return tmpHome, nil }

	_, err := DefaultStateFile()
	require.NoError(t, err)

	info, err := os.Stat(stateDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestSecureStateFilePermissions(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "fleetint.state")
	require.NoError(t, os.WriteFile(stateFile, []byte("test"), 0o644))
	require.NoError(t, os.WriteFile(stateFile+"-wal", []byte("wal"), 0o644))
	require.NoError(t, os.WriteFile(stateFile+"-shm", []byte("shm"), 0o644))
	require.NoError(t, os.Chmod(stateFile, 0o644))
	require.NoError(t, os.Chmod(stateFile+"-wal", 0o644))
	require.NoError(t, os.Chmod(stateFile+"-shm", 0o644))

	err := SecureStateFilePermissions(stateFile)
	require.NoError(t, err)

	for _, candidate := range []string{stateFile, stateFile + "-wal", stateFile + "-shm"} {
		info, err := os.Stat(candidate)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestValidateHealthExporter(t *testing.T) {
	t.Run("valid collector endpoint", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				CollectorEndpoint: "http://fleetint-otel-gateway:4318",
			},
		}
		require.NoError(t, cfg.Validate())
	})

	for _, endpoint := range []string{
		"collector:4318",
		"ftp://collector.example",
		"http://user:password@collector.example",
		"http://collector.example?token=secret",
		"http://collector.example#fragment",
		// url.Parse reports these as ForceQuery and as no fragment at all,
		// so they slip past the credentials/query/fragment checks.
		"http://collector.example?",
		"http://collector.example#",
	} {
		t.Run("invalid collector endpoint "+endpoint, func(t *testing.T) {
			cfg := &Config{
				Address:         ":8080",
				RetentionPeriod: metav1.Duration{Duration: time.Hour},
				HealthExporter: &HealthExporterConfig{
					CollectorEndpoint: endpoint,
				},
			}
			require.ErrorContains(t, cfg.Validate(), "collector_endpoint")
		})
	}

	t.Run("valid health check interval", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				HealthCheckInterval: metav1.Duration{Duration: 5 * time.Minute},
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("health check interval too short", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				HealthCheckInterval: metav1.Duration{Duration: 500 * time.Millisecond},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "health_check_interval must be at least 1 second")
	})

	t.Run("health check interval too long", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				HealthCheckInterval: metav1.Duration{Duration: 25 * time.Hour},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "health_check_interval must be at most 24 hours")
	})

	t.Run("health check interval at minimum boundary", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				HealthCheckInterval: metav1.Duration{Duration: time.Second},
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("health check interval at maximum boundary", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				HealthCheckInterval: metav1.Duration{Duration: 24 * time.Hour},
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("health check interval zero (not set)", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				HealthCheckInterval: metav1.Duration{Duration: 0},
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})
}

func TestValidateOfflineMode(t *testing.T) {
	t.Run("valid offline mode configuration", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				OfflineMode: true,
				OutputPath:  "/tmp/output",
				Duration:    10 * time.Minute,
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("offline mode with json format", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				OfflineMode:  true,
				OutputPath:   "/tmp/output",
				Duration:     10 * time.Minute,
				OutputFormat: "json",
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("offline mode with csv format", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				OfflineMode:  true,
				OutputPath:   "/tmp/output",
				Duration:     10 * time.Minute,
				OutputFormat: "csv",
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("offline mode missing output path", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				OfflineMode: true,
				Duration:    10 * time.Minute,
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "offline mode: output_path is required")
	})

	t.Run("offline mode missing duration", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				OfflineMode: true,
				OutputPath:  "/tmp/output",
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "offline mode: duration is required")
	})

	t.Run("offline mode with invalid output format", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				OfflineMode:  true,
				OutputPath:   "/tmp/output",
				Duration:     10 * time.Minute,
				OutputFormat: "xml",
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "offline mode: output_format must be 'json' or 'csv'")
	})

	t.Run("offline mode allows missing address", func(t *testing.T) {
		cfg := &Config{
			Address:         "",
			RetentionPeriod: metav1.Duration{Duration: time.Hour},
			HealthExporter: &HealthExporterConfig{
				OfflineMode: true,
				OutputPath:  "/tmp/output",
				Duration:    10 * time.Minute,
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})
}

func TestValidateRetentionPeriod(t *testing.T) {
	t.Run("retention period at minimum boundary", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: time.Minute},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("retention period zero", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: 0},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "retention_period must be at least 1 minute")
	})

	t.Run("retention period negative", func(t *testing.T) {
		cfg := &Config{
			Address:         ":8080",
			RetentionPeriod: metav1.Duration{Duration: -1 * time.Hour},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "retention_period must be at least 1 minute")
	})
}

func TestComponentSelectionEdgeCases(t *testing.T) {
	t.Run("empty string component", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"", "gpu-memory"},
		}

		// Empty strings should be ignored
		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.False(t, cfg.ShouldEnable("cpu"))
	})

	t.Run("multiple wildcards", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"*", "*"},
		}

		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("cpu"))
	})

	t.Run("all and wildcard mixed", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"all", "*"},
		}

		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("any-component"))
	})

	t.Run("specific components then wildcard", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"gpu-memory", "*"},
		}

		// First call initializes based on first element
		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		// Cache is initialized after first call
		assert.False(t, cfg.ShouldEnable("cpu"))
	})

	t.Run("disable component with hyphen in name", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"-gpu-memory"},
		}

		assert.True(t, cfg.ShouldDisable("gpu-memory"))
		assert.False(t, cfg.ShouldDisable("cpu"))
	})

	t.Run("enable and disable mixed", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"gpu-memory", "-cpu"},
		}

		// Should enable gpu-memory
		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		// Should not enable cpu (not in enabled list)
		assert.False(t, cfg.ShouldEnable("cpu"))
		// Should disable cpu (in disabled list)
		assert.True(t, cfg.ShouldDisable("cpu"))
	})

	t.Run("wildcard with single exclusion", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"*", "-kubelet"},
		}

		assert.True(t, cfg.ShouldEnable("gpu-memory"))
		assert.True(t, cfg.ShouldEnable("kubelet"))
		assert.True(t, cfg.ShouldDisable("kubelet"))
	})

	t.Run("ShouldDisable with wildcard", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"*"},
		}

		// Wildcard doesn't disable anything
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
		assert.False(t, cfg.ShouldDisable("cpu"))
	})

	t.Run("ShouldDisable with all keyword", func(t *testing.T) {
		cfg := &Config{
			Components: []string{"all"},
		}

		// "all" doesn't disable anything
		assert.False(t, cfg.ShouldDisable("gpu-memory"))
		assert.False(t, cfg.ShouldDisable("cpu"))
	})
}

func TestDefaultWithHealthExporter(t *testing.T) {
	ctx := context.Background()

	t.Run("default includes health exporter", func(t *testing.T) {
		cfg, err := Default(ctx)
		require.NoError(t, err)

		assert.NotNil(t, cfg.HealthExporter)
		assert.Equal(t, metav1.Duration{Duration: 1 * time.Minute}, cfg.HealthExporter.Interval)
		assert.Equal(t, metav1.Duration{Duration: 30 * time.Second}, cfg.HealthExporter.Timeout)
		assert.True(t, cfg.HealthExporter.IncludeMetrics)
		assert.True(t, cfg.HealthExporter.IncludeEvents)
		assert.True(t, cfg.HealthExporter.IncludeMachineInfo)
		assert.True(t, cfg.HealthExporter.IncludeComponentData)
		assert.Equal(t, metav1.Duration{Duration: 1 * time.Minute}, cfg.HealthExporter.MetricsLookback)
		assert.Equal(t, metav1.Duration{Duration: 1 * time.Minute}, cfg.HealthExporter.EventsLookback)
		assert.Equal(t, metav1.Duration{Duration: 1 * time.Minute}, cfg.HealthExporter.HealthCheckInterval)
		assert.Equal(t, 3, cfg.HealthExporter.RetryMaxAttempts)
		assert.Equal(t, "json", cfg.HealthExporter.OutputFormat)
		assert.Equal(t, "", cfg.HealthExporter.MetricsEndpoint)
		assert.Equal(t, "", cfg.HealthExporter.LogsEndpoint)
		assert.Equal(t, "", cfg.HealthExporter.AuthToken)
		assert.False(t, cfg.HealthExporter.OfflineMode)
	})
}

func TestToConfigEntries(t *testing.T) {
	allComponents := []string{"cpu", "disk", "memory", "gpu"}

	t.Run("basic config entries", func(t *testing.T) {
		cfg := &Config{
			APIVersion:      "v1",
			Address:         "0.0.0.0:8080",
			State:           "/var/lib/fleetint",
			RetentionPeriod: metav1.Duration{Duration: 24 * time.Hour},
			Components:      []string{},
		}

		entries := cfg.ToConfigEntries(allComponents)

		// Find specific entries
		var apiVersion, address, state, retentionPeriod, enabledComponents, disabledComponents string
		for _, entry := range entries {
			switch entry.Key {
			case "api_version":
				apiVersion = entry.Value
			case "address":
				address = entry.Value
			case "state":
				state = entry.Value
			case "retention_period":
				retentionPeriod = entry.Value
			case "enabled_components":
				enabledComponents = entry.Value
			case "disabled_components":
				disabledComponents = entry.Value
			}
		}

		assert.Equal(t, "v1", apiVersion)
		assert.Equal(t, "0.0.0.0:8080", address)
		assert.Equal(t, "/var/lib/fleetint", state)
		assert.Equal(t, "86400", retentionPeriod)

		// Verify JSONB array format
		var enabled []string
		err := json.Unmarshal([]byte(enabledComponents), &enabled)
		require.NoError(t, err)
		assert.ElementsMatch(t, allComponents, enabled)

		var disabled []string
		err = json.Unmarshal([]byte(disabledComponents), &disabled)
		require.NoError(t, err)
		assert.Empty(t, disabled)
	})

	t.Run("with health exporter config", func(t *testing.T) {
		cfg := &Config{
			APIVersion:      "v1",
			Address:         "0.0.0.0:8080",
			State:           "/var/lib/fleetint",
			RetentionPeriod: metav1.Duration{Duration: 24 * time.Hour},
			Components:      []string{},
			HealthExporter: &HealthExporterConfig{
				MetricsEndpoint:      "https://example.com/metrics",
				LogsEndpoint:         "https://example.com/logs",
				Interval:             metav1.Duration{Duration: 1 * time.Minute},
				Timeout:              metav1.Duration{Duration: 30 * time.Second},
				IncludeMetrics:       true,
				IncludeEvents:        true,
				IncludeMachineInfo:   true,
				IncludeComponentData: true,
				MetricsLookback:      metav1.Duration{Duration: 5 * time.Minute},
				EventsLookback:       metav1.Duration{Duration: 5 * time.Minute},
				HealthCheckInterval:  metav1.Duration{Duration: 1 * time.Minute},
				RetryMaxAttempts:     3,
				OfflineMode:          false,
				OutputPath:           "",
				OutputFormat:         "json",
				Duration:             0,
			},
		}

		entries := cfg.ToConfigEntries(allComponents)

		// Check for health exporter entries
		foundMetricsEndpoint := false
		foundLogsEndpoint := false
		foundAuthToken := false

		for _, entry := range entries {
			if entry.Key == "health_exporter.metrics_endpoint" {
				foundMetricsEndpoint = true
			}
			if entry.Key == "health_exporter.logs_endpoint" {
				foundLogsEndpoint = true
			}
			if entry.Key == "auth_token" || entry.Key == "health_exporter.auth_token" {
				foundAuthToken = true
			}
		}

		// Endpoints are excluded - they're enrollment-assigned, not user config
		assert.False(t, foundMetricsEndpoint, "metrics_endpoint should not be exported")
		assert.False(t, foundLogsEndpoint, "logs_endpoint should not be exported")
		assert.False(t, foundAuthToken, "auth_token should not be exported")
	})

	t.Run("components with disabled list", func(t *testing.T) {
		cfg := &Config{
			APIVersion:      "v1",
			Address:         "0.0.0.0:8080",
			State:           "/var/lib/fleetint",
			RetentionPeriod: metav1.Duration{Duration: 24 * time.Hour},
			Components:      []string{"*", "-memory", "-disk"},
		}

		entries := cfg.ToConfigEntries(allComponents)

		var enabledComponents, disabledComponents string
		for _, entry := range entries {
			if entry.Key == "enabled_components" {
				enabledComponents = entry.Value
			}
			if entry.Key == "disabled_components" {
				disabledComponents = entry.Value
			}
		}

		// Verify JSONB array format
		var enabled []string
		err := json.Unmarshal([]byte(enabledComponents), &enabled)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"cpu", "gpu"}, enabled)

		var disabled []string
		err = json.Unmarshal([]byte(disabledComponents), &disabled)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"memory", "disk"}, disabled)
	})

	t.Run("specific components only", func(t *testing.T) {
		cfg := &Config{
			APIVersion:      "v1",
			Address:         "0.0.0.0:8080",
			State:           "/var/lib/fleetint",
			RetentionPeriod: metav1.Duration{Duration: 24 * time.Hour},
			Components:      []string{"cpu", "memory"},
		}

		entries := cfg.ToConfigEntries(allComponents)

		var enabledComponents, disabledComponents string
		for _, entry := range entries {
			if entry.Key == "enabled_components" {
				enabledComponents = entry.Value
			}
			if entry.Key == "disabled_components" {
				disabledComponents = entry.Value
			}
		}

		var enabled []string
		err := json.Unmarshal([]byte(enabledComponents), &enabled)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"cpu", "memory"}, enabled)

		var disabled []string
		err = json.Unmarshal([]byte(disabledComponents), &disabled)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"disk", "gpu"}, disabled)
	})
}

func TestGetComponentLists(t *testing.T) {
	allComponents := []string{"cpu", "disk", "memory", "gpu", "network"}

	t.Run("empty components enables all", func(t *testing.T) {
		cfg := &Config{Components: []string{}}
		enabled, disabled := cfg.getComponentLists(allComponents)

		assert.ElementsMatch(t, allComponents, enabled)
		assert.Empty(t, disabled)
		assert.NotNil(t, disabled, "disabled should be empty slice, not nil")
	})

	t.Run("wildcard enables all", func(t *testing.T) {
		cfg := &Config{Components: []string{"*"}}
		enabled, disabled := cfg.getComponentLists(allComponents)

		assert.ElementsMatch(t, allComponents, enabled)
		assert.Empty(t, disabled)
	})

	t.Run("all keyword enables all", func(t *testing.T) {
		cfg := &Config{Components: []string{"all"}}
		enabled, disabled := cfg.getComponentLists(allComponents)

		assert.ElementsMatch(t, allComponents, enabled)
		assert.Empty(t, disabled)
	})

	t.Run("wildcard with exclusions", func(t *testing.T) {
		cfg := &Config{Components: []string{"*", "-memory", "-disk"}}
		enabled, disabled := cfg.getComponentLists(allComponents)

		assert.ElementsMatch(t, []string{"cpu", "gpu", "network"}, enabled)
		assert.ElementsMatch(t, []string{"memory", "disk"}, disabled)
	})

	t.Run("specific components", func(t *testing.T) {
		cfg := &Config{Components: []string{"cpu", "memory"}}
		enabled, disabled := cfg.getComponentLists(allComponents)

		assert.ElementsMatch(t, []string{"cpu", "memory"}, enabled)
		assert.ElementsMatch(t, []string{"disk", "gpu", "network"}, disabled)
	})

	t.Run("specific with explicit disables", func(t *testing.T) {
		cfg := &Config{Components: []string{"cpu", "memory", "-network"}}
		enabled, disabled := cfg.getComponentLists(allComponents)

		assert.ElementsMatch(t, []string{"cpu", "memory"}, enabled)
		assert.ElementsMatch(t, []string{"disk", "gpu", "network"}, disabled)
	})

	t.Run("returns empty slices not nil", func(t *testing.T) {
		cfg := &Config{Components: []string{"*"}}
		enabled, disabled := cfg.getComponentLists(allComponents)

		// Verify JSON marshaling produces empty array, not null
		enabledJSON, err := json.Marshal(enabled)
		require.NoError(t, err)
		disabledJSON, err := json.Marshal(disabled)
		require.NoError(t, err)

		// Should contain all components
		assert.NotEqual(t, "null", string(enabledJSON))
		// Should be empty array "[]", not null
		assert.Equal(t, "[]", string(disabledJSON))
	})
}

func TestInventoryAgentConfig(t *testing.T) {
	allComponents := []string{"cpu", "disk", "memory", "gpu"}
	cfg := &Config{
		APIVersion:      "v1",
		RetentionPeriod: metav1.Duration{Duration: 24 * time.Hour},
		Components:      []string{"*", "-memory", "-disk"},
		HealthExporter: &HealthExporterConfig{
			HealthCheckInterval: metav1.Duration{Duration: 30 * time.Second},
		},
		Inventory: &InventoryConfig{
			Enabled:  true,
			Interval: metav1.Duration{Duration: time.Hour},
		},
		Attestation: &AttestationConfig{
			Enabled:  true,
			Interval: metav1.Duration{Duration: 24 * time.Hour},
		},
	}

	retentionPeriodSeconds, enabled, disabled := cfg.InventoryAgentConfig(allComponents)
	assert.Equal(t, int64(86400), retentionPeriodSeconds)
	assert.ElementsMatch(t, []string{"cpu", "gpu"}, enabled)
	assert.ElementsMatch(t, []string{"memory", "disk"}, disabled)

	inventoryEnabled, inventoryIntervalSeconds := cfg.InventoryLoopAgentConfig()
	assert.True(t, inventoryEnabled)
	assert.Equal(t, int64(3600), inventoryIntervalSeconds)

	attestationEnabled, attestationIntervalSeconds := cfg.AttestationLoopAgentConfig()
	assert.True(t, attestationEnabled)
	assert.Equal(t, int64(86400), attestationIntervalSeconds)

	assert.Equal(t, 30*time.Second, cfg.HealthCheckInterval())
	assert.Equal(t, int64(30), cfg.MetricScrapeIntervalSeconds())
	assert.Equal(t, time.Minute, (*Config)(nil).HealthCheckInterval())
	assert.Equal(t, int64(60), (*Config)(nil).MetricScrapeIntervalSeconds())
}
