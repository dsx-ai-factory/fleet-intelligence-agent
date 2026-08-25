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

package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

// TestGetHealthCheckInterval tests the getHealthCheckInterval function.
func TestGetHealthCheckInterval(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.Config
		expected time.Duration
	}{
		{
			name:     "nil_health_exporter",
			config:   &config.Config{},
			expected: time.Minute,
		},
		{
			name: "zero_interval",
			config: &config.Config{
				HealthExporter: &config.HealthExporterConfig{
					HealthCheckInterval: metav1.Duration{Duration: 0},
				},
			},
			expected: time.Minute,
		},
		{
			name: "custom_interval",
			config: &config.Config{
				HealthExporter: &config.HealthExporterConfig{
					HealthCheckInterval: metav1.Duration{Duration: 30 * time.Second},
				},
			},
			expected: 30 * time.Second,
		},
		{
			name: "large_interval",
			config: &config.Config{
				HealthExporter: &config.HealthExporterConfig{
					HealthCheckInterval: metav1.Duration{Duration: 10 * time.Minute},
				},
			},
			expected: 10 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := getHealthCheckInterval(tt.config)
			assert.Equal(t, tt.expected, interval)
		})
	}
}

func TestGetInventorySyncInterval(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.Config
		expected time.Duration
	}{
		{
			name:     "nil config",
			config:   nil,
			expected: 0,
		},
		{
			name: "uses inventory sync config",
			config: &config.Config{
				Inventory: &config.InventoryConfig{
					Enabled:  true,
					Interval: metav1.Duration{Duration: 2 * time.Minute},
				},
			},
			expected: 2 * time.Minute,
		},
		{
			name: "disabled inventory sync",
			config: &config.Config{
				Inventory: &config.InventoryConfig{
					Enabled:  false,
					Interval: metav1.Duration{Duration: 2 * time.Minute},
				},
			},
			expected: 0,
		},
		{
			name:     "no inventory or exporter config",
			config:   &config.Config{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, getInventorySyncInterval(tt.config))
		})
	}
}

func TestGetInventorySyncTimeout(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.Config
		expected time.Duration
	}{
		{
			name:     "nil config",
			config:   nil,
			expected: 0,
		},
		{
			name: "falls back to default when enabled and unset",
			config: &config.Config{
				Inventory: &config.InventoryConfig{Enabled: true},
			},
			expected: config.DefaultInventoryTimeout,
		},
		{
			name: "disabled inventory sync",
			config: &config.Config{
				Inventory: &config.InventoryConfig{Enabled: false},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, getInventorySyncTimeout(tt.config))
		})
	}
}

func TestWaitForWaitGroup(t *testing.T) {
	t.Run("returns true when waitgroup completes", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
		}()
		assert.True(t, waitForWaitGroup(&wg, 200*time.Millisecond))
	})

	t.Run("returns false on timeout", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		assert.False(t, waitForWaitGroup(&wg, 10*time.Millisecond))
		wg.Done()
		assert.True(t, waitForWaitGroup(&wg, 100*time.Millisecond))
	})
}

func TestGetAttestationSettings(t *testing.T) {
	tests := []struct {
		name         string
		config       *config.Config
		wantInterval time.Duration
		wantTimeout  time.Duration
	}{
		{
			name:         "nil config",
			config:       nil,
			wantInterval: 0,
			wantTimeout:  0,
		},
		{
			name:         "no attestation",
			config:       &config.Config{},
			wantInterval: 0,
			wantTimeout:  0,
		},
		{
			name: "uses attestation settings",
			config: &config.Config{
				Attestation: &config.AttestationConfig{
					Enabled:  true,
					Interval: metav1.Duration{Duration: 6 * time.Hour},
				},
			},
			wantInterval: 6 * time.Hour,
			wantTimeout:  config.DefaultAttestationTimeout,
		},
		{
			name: "uses default attestation timeout when enabled and unset",
			config: &config.Config{
				Attestation: &config.AttestationConfig{
					Enabled:  true,
					Interval: metav1.Duration{Duration: 6 * time.Hour},
				},
			},
			wantInterval: 6 * time.Hour,
			wantTimeout:  config.DefaultAttestationTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantInterval, getAttestationInterval(tt.config))
			assert.Equal(t, tt.wantTimeout, getAttestationTimeout(tt.config))
		})
	}
}

// TestShouldEnableComponent tests the shouldEnableComponent function.
func TestShouldEnableComponent(t *testing.T) {
	tests := []struct {
		name             string
		componentName    string
		enabledByDefault bool
		config           *config.Config
		expected         bool
	}{
		{
			name:             "enabled_by_default_no_config",
			componentName:    "test-component",
			enabledByDefault: true,
			config:           &config.Config{},
			expected:         true,
		},
		{
			name:             "disabled_by_default_no_config",
			componentName:    "test-component",
			enabledByDefault: false,
			config:           &config.Config{},
			expected:         false,
		},
		{
			name:             "all_components_enabled",
			componentName:    "test-component",
			enabledByDefault: false,
			config: &config.Config{
				Components: []string{"*"},
			},
			expected: false,
		},
		{
			name:             "all_components_keyword",
			componentName:    "test-component",
			enabledByDefault: false,
			config: &config.Config{
				Components: []string{"all"},
			},
			expected: false,
		},
		{
			name:             "specific_component_enabled",
			componentName:    "test-component",
			enabledByDefault: false,
			config: &config.Config{
				Components: []string{"test-component", "other-component"},
			},
			expected: true,
		},
		{
			name:             "specific_component_not_in_list",
			componentName:    "test-component",
			enabledByDefault: true,
			config: &config.Config{
				Components: []string{"other-component"},
			},
			expected: false,
		},
		{
			name:             "explicitly_disabled",
			componentName:    "test-component",
			enabledByDefault: true,
			config: &config.Config{
				Components: []string{"-test-component"}, // Prefix with "-" to disable
			},
			expected: false,
		},
		{
			name:             "enabled_but_explicitly_disabled",
			componentName:    "test-component",
			enabledByDefault: true,
			config: &config.Config{
				Components: []string{"test-component", "-test-component"}, // Disable wins
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldEnableComponent(tt.componentName, tt.enabledByDefault, tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestInitializeDatabases tests the initializeDatabases function.
func TestInitializeDatabases(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		config      *config.Config
		expectError bool
	}{
		{
			name: "memory_database",
			config: &config.Config{
				State: "",
			},
			expectError: false,
		},
		{
			name: "with_state_file",
			config: &config.Config{
				State: t.TempDir() + "/test.db",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbRW, dbRO, err := initializeDatabases(ctx, tt.config)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, dbRW)
				assert.Nil(t, dbRO)
			} else {
				require.NoError(t, err)
				require.NotNil(t, dbRW)
				require.NotNil(t, dbRO)

				// Clean up
				dbRW.Close()
				dbRO.Close()
			}
		})
	}
}

func TestInitializeDatabasesSecuresFreshStateFile(t *testing.T) {
	ctx := context.Background()
	stateFile := filepath.Join(t.TempDir(), "fleetint.state")

	dbRW, dbRO, err := initializeDatabases(ctx, &config.Config{State: stateFile})
	require.NoError(t, err)
	defer dbRW.Close()
	defer dbRO.Close()

	for _, candidate := range []string{stateFile, stateFile + "-wal", stateFile + "-shm"} {
		info, err := os.Stat(candidate)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

// TestInitializeMachineID tests the initializeMachineID function.
func TestInitializeMachineID(t *testing.T) {
	ctx := context.Background()
	dbRW, dbRO, err := initializeDatabases(ctx, &config.Config{State: filepath.Join(t.TempDir(), "fleetint.state")})
	require.NoError(t, err)
	defer dbRW.Close()
	defer dbRO.Close()

	machineID, err := initializeMachineID(ctx, dbRW, dbRO)
	require.NoError(t, err)
	assert.NotEmpty(t, machineID)

	machineID2, err := initializeMachineID(ctx, dbRW, dbRO)
	require.NoError(t, err)
	assert.Equal(t, machineID, machineID2, "Machine ID should be consistent across calls")
}

// TestServerStop tests the Stop method.
func TestServerStop(t *testing.T) {
	tests := []struct {
		name   string
		server *Server
	}{
		{
			name: "nil_databases",
			server: &Server{
				dbRW: nil,
				dbRO: nil,
			},
		},
		{
			name: "nil_health_exporter",
			server: &Server{
				healthExporter: nil,
			},
		},
		{
			name: "nil_components_registry",
			server: &Server{
				componentsRegistry: nil,
			},
		},
		{
			name: "nil_gpud_instance",
			server: &Server{
				gpudInstance: nil,
			},
		},
		{
			name:   "all_nil",
			server: &Server{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Stop should not panic with any combination of nil fields
			assert.NotPanics(t, func() {
				tt.server.Stop()
			})
		})
	}
}

func TestServerStopCancelsBackgroundLoops(t *testing.T) {
	loopCtx, loopCancel := context.WithCancel(context.Background())
	s := &Server{
		loopCtx:    loopCtx,
		loopCancel: loopCancel,
	}
	s.loopWG.Add(1)
	go func() {
		defer s.loopWG.Done()
		<-loopCtx.Done()
	}()

	s.Stop()

	assert.ErrorIs(t, loopCtx.Err(), context.Canceled)
	assert.True(t, waitForWaitGroup(&s.loopWG, 100*time.Millisecond))
}

// TestServerStopWithDatabases tests Stop with actual database connections.
func TestServerStopWithDatabases(t *testing.T) {
	ctx := context.Background()
	config := &config.Config{State: ""} // in-memory

	dbRW, dbRO, err := initializeDatabases(ctx, config)
	require.NoError(t, err)

	s := &Server{
		dbRW: dbRW,
		dbRO: dbRO,
	}

	// Stop should close the databases without panicking
	assert.NotPanics(t, func() {
		s.Stop()
	})

	// Verify databases are closed by trying to use them
	_, err = dbRW.Exec("SELECT 1")
	assert.Error(t, err, "database should be closed")
}

// TestServerStopIdempotent verifies Stop() can be called multiple times safely.
func TestServerStopIdempotent(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{State: ""} // in-memory

	dbRW, dbRO, err := initializeDatabases(ctx, cfg)
	require.NoError(t, err)

	s := &Server{
		config: cfg,
		dbRW:   dbRW,
		dbRO:   dbRO,
	}

	assert.NotPanics(t, func() {
		s.Stop()
		s.Stop() // second call must not panic or double-close
		s.Stop() // third call for good measure
	})
}

// TestRemoveStaleSocket verifies the socket cleanup logic.
func TestRemoveStaleSocket(t *testing.T) {
	t.Run("nonexistent_is_noop", func(t *testing.T) {
		err := removeStaleSocket("/nonexistent/path.sock")
		assert.NoError(t, err)
	})

	t.Run("regular_file_rejected", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "not-a-socket")
		require.NoError(t, os.WriteFile(f, []byte("data"), 0o600))

		err := removeStaleSocket(f)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a unix socket")
		// File must still exist
		_, statErr := os.Stat(f)
		assert.NoError(t, statErr, "regular file must not be deleted")
	})

	t.Run("symlink_rejected", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		require.NoError(t, os.WriteFile(target, []byte("data"), 0o600))
		link := filepath.Join(dir, "link")
		require.NoError(t, os.Symlink(target, link))

		err := removeStaleSocket(link)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
	})

	t.Run("actual_socket_removed", func(t *testing.T) {
		dir := t.TempDir()
		sockPath := filepath.Join(dir, "test.sock")

		// Create a socket and keep the listener open so the file persists.
		ln, err := net.Listen("unix", sockPath)
		require.NoError(t, err)

		err = removeStaleSocket(sockPath)
		assert.NoError(t, err)

		// Socket file should be gone
		_, err = os.Lstat(sockPath)
		assert.True(t, os.IsNotExist(err))

		_ = ln.Close()
	})
}

// TestServerGetHealthExporter tests the GetHealthExporter method.
func TestServerGetHealthExporter(t *testing.T) {
	s := &Server{
		healthExporter: nil,
	}

	exporter := s.GetHealthExporter()
	assert.Nil(t, exporter)
}

// TestShouldEnableComponentEdgeCases tests edge cases for shouldEnableComponent.
func TestShouldEnableComponentEdgeCases(t *testing.T) {
	tests := []struct {
		name             string
		componentName    string
		enabledByDefault bool
		config           *config.Config
		expected         bool
	}{
		{
			name:             "empty_component_name",
			componentName:    "",
			enabledByDefault: true,
			config:           &config.Config{},
			expected:         true,
		},
		{
			name:             "nil_components_list",
			componentName:    "test",
			enabledByDefault: true,
			config: &config.Config{
				Components: nil,
			},
			expected: true,
		},
		{
			name:             "empty_components_list",
			componentName:    "test",
			enabledByDefault: true,
			config: &config.Config{
				Components: []string{},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldEnableComponent(tt.componentName, tt.enabledByDefault, tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetHealthCheckIntervalEdgeCases tests edge cases for getHealthCheckInterval.
func TestGetHealthCheckIntervalEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.Config
		expected time.Duration
	}{
		{
			name: "negative_interval",
			config: &config.Config{
				HealthExporter: &config.HealthExporterConfig{
					HealthCheckInterval: metav1.Duration{Duration: -1 * time.Second},
				},
			},
			expected: time.Minute, // Should use default for invalid values
		},
		{
			name: "very_small_interval",
			config: &config.Config{
				HealthExporter: &config.HealthExporterConfig{
					HealthCheckInterval: metav1.Duration{Duration: 1 * time.Nanosecond},
				},
			},
			expected: 1 * time.Nanosecond,
		},
		{
			name: "very_large_interval",
			config: &config.Config{
				HealthExporter: &config.HealthExporterConfig{
					HealthCheckInterval: metav1.Duration{Duration: 24 * time.Hour},
				},
			},
			expected: 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := getHealthCheckInterval(tt.config)
			assert.Equal(t, tt.expected, interval)
		})
	}
}

// TestURLPathInjectFault tests the constant URL path.
func TestURLPathInjectFault(t *testing.T) {
	assert.Equal(t, "/inject-fault", URLPathInjectFault)
}

// Benchmark tests

// BenchmarkGetHealthCheckInterval benchmarks the getHealthCheckInterval function.
func BenchmarkGetHealthCheckInterval(b *testing.B) {
	config := &config.Config{
		HealthExporter: &config.HealthExporterConfig{
			HealthCheckInterval: metav1.Duration{Duration: 30 * time.Second},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getHealthCheckInterval(config)
	}
}

// BenchmarkShouldEnableComponent benchmarks the shouldEnableComponent function.
func BenchmarkShouldEnableComponent(b *testing.B) {
	config := &config.Config{
		Components: []string{"component1", "component2", "component3"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = shouldEnableComponent("component2", true, config)
	}
}

// TestInitializeDatabasesErrorCases tests error handling in initializeDatabases.
func TestInitializeDatabasesErrorCases(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		config      *config.Config
		expectError bool
	}{
		{
			name: "invalid_directory_path",
			config: &config.Config{
				State: "/nonexistent/directory/that/does/not/exist/test.db",
			},
			expectError: true,
		},
		{
			name: "valid_memory_db",
			config: &config.Config{
				State: ":memory:",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbRW, dbRO, err := initializeDatabases(ctx, tt.config)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if dbRW != nil {
					dbRW.Close()
				}
				if dbRO != nil {
					dbRO.Close()
				}
			}
		})
	}
}

// TestHealthzHandler tests the healthz handler.
func TestHealthzHandler(t *testing.T) {
	s := &Server{}
	handler := s.healthz()

	assert.NotNil(t, handler)
}
