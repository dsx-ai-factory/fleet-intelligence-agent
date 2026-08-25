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

package exporter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/writer"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/machineinfo"
)

// Mock implementations

// MockCollector is a mock implementation of collector.Collector
type MockCollector struct {
	mock.Mock
}

func (m *MockCollector) Collect(ctx context.Context) (*collector.HealthData, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*collector.HealthData), args.Error(1)
}

type MockHTTPWriter struct {
	mock.Mock
}

func (m *MockHTTPWriter) Send(ctx context.Context, data *collector.HealthData, metricsEndpoint string, logsEndpoint string, maxRetries int, authToken string) (string, error) {
	args := m.Called(ctx, data, metricsEndpoint, logsEndpoint, maxRetries, authToken)
	return args.String(0), args.Error(1)
}

func (m *MockHTTPWriter) SetJWTRefreshFunc(refreshFunc writer.JWTRefreshFunc) {
	m.Called(refreshFunc)
}

// MockMetricsStore is a mock implementation of pkgmetrics.Store
type MockMetricsStore struct {
	mock.Mock
}

func (m *MockMetricsStore) Read(ctx context.Context, opts ...pkgmetrics.OpOption) (pkgmetrics.Metrics, error) {
	return nil, nil
}

func (m *MockMetricsStore) Purge(ctx context.Context, since time.Time) (int, error) {
	return 0, nil
}

func (m *MockMetricsStore) Record(ctx context.Context, metrics ...pkgmetrics.Metric) error {
	return nil
}

// MockEventStore is a mock implementation of eventstore.Store
type MockEventStore struct {
	mock.Mock
}

func (m *MockEventStore) Close(ctx context.Context) error {
	return nil
}

func (m *MockEventStore) Bucket(name string, opts ...eventstore.OpOption) (eventstore.Bucket, error) {
	return nil, nil
}

// MockComponentsRegistry is a mock implementation of components.Registry
type MockComponentsRegistry struct {
	mock.Mock
}

func (m *MockComponentsRegistry) All() []components.Component {
	return nil
}

func (m *MockComponentsRegistry) Deregister(name string) components.Component {
	return nil
}

func (m *MockComponentsRegistry) Get(name string) components.Component {
	return nil
}

func (m *MockComponentsRegistry) MustRegister(initFunc components.InitFunc) {
}

func (m *MockComponentsRegistry) Register(initFunc components.InitFunc) (components.Component, error) {
	return nil, nil
}

// TestNew tests the New function
func TestNew(t *testing.T) {
	ctx := context.Background()

	t.Run("creates exporter with minimal config", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)
		assert.NotNil(t, he.ctx)
		assert.NotNil(t, he.cancel)
		assert.Equal(t, cfg, he.options.config)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("creates exporter with all options", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval:             metav1.Duration{Duration: 1 * time.Minute},
			Timeout:              metav1.Duration{Duration: 30 * time.Second},
			IncludeMetrics:       true,
			IncludeEvents:        true,
			IncludeComponentData: true,
		}

		mockMetrics := &MockMetricsStore{}
		mockEvents := &MockEventStore{}
		mockRegistry := &MockComponentsRegistry{}
		httpClient := &http.Client{}
		dbRW := &sql.DB{}
		dbRO := &sql.DB{}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithMetricsStore(mockMetrics),
			WithEventStore(mockEvents),
			WithComponentsRegistry(mockRegistry),
			WithHTTPClient(httpClient),
			WithDatabaseConnections(dbRW, dbRO),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)
		assert.Equal(t, mockMetrics, he.options.metricsStore)
		assert.Equal(t, mockEvents, he.options.eventStore)
		assert.Equal(t, mockRegistry, he.options.componentsRegistry)
		require.NotNil(t, he.options.httpClient)
		assert.NotSame(t, httpClient, he.options.httpClient)
		require.NotNil(t, he.options.httpClient.CheckRedirect)
		assert.Equal(t, http.ErrUseLastResponse, he.options.httpClient.CheckRedirect(&http.Request{}, nil))
		assert.Nil(t, httpClient.CheckRedirect)
		assert.Equal(t, dbRW, he.options.dbRW)
		assert.Equal(t, dbRO, he.options.dbRO)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("fails with invalid option", func(t *testing.T) {
		invalidOption := func(opts *exporterOptions) error {
			return errors.New("invalid option")
		}

		exporter, err := New(ctx, invalidOption)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to apply option")
		assert.Nil(t, exporter)
	})

	t.Run("fails with missing config", func(t *testing.T) {
		exporter, err := New(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid options")
		assert.Nil(t, exporter)
	})

	t.Run("fails with incomplete dependencies", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval:       metav1.Duration{Duration: 1 * time.Minute},
			Timeout:        metav1.Duration{Duration: 30 * time.Second},
			IncludeMetrics: true,
			// Missing metrics store
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "metrics store is required")
		assert.Nil(t, exporter)
	})

	t.Run("fails with nil HTTP client", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx, WithConfig(cfg), WithHTTPClient(nil), WithMachineID("test-machine-id"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP client cannot be nil")
		assert.Nil(t, exporter)
	})
}

// TestStart tests the Start function
func TestStart(t *testing.T) {
	ctx := context.Background()

	t.Run("starts with valid interval", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 100 * time.Millisecond},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.Start()
		require.NoError(t, err)

		// Give it time to start
		time.Sleep(50 * time.Millisecond)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("skips when interval is zero", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 0},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.Start()
		require.NoError(t, err)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("offline mode performs initial export before first tick", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 1 * time.Hour},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   tmpDir,
			OutputFormat: "json",
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.Start()
		require.NoError(t, err)

		// Initial export should be written immediately, without waiting for ticker.
		time.Sleep(100 * time.Millisecond)
		entries, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		assert.Greater(t, len(entries), 0, "Expected offline files from initial export")

		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("offline initial export includes collected data", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 1 * time.Hour},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   tmpDir,
			OutputFormat: "json",
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)
		mockCollector := &MockCollector{}
		mockCollector.On("Collect", mock.Anything).Return(&collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
			Metrics: []pkgmetrics.Metric{
				{
					Name:             "test_metric",
					Value:            42.0,
					UnixMilliseconds: time.Now().UnixMilli(),
				},
			},
		}, nil).Once()
		he.collector = mockCollector

		err = exporter.Start()
		require.NoError(t, err)

		entries, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		require.Greater(t, len(entries), 0, "Expected offline files from initial export")

		allContent := ""
		for _, entry := range entries {
			fullPath := filepath.Join(tmpDir, entry.Name())
			content, readErr := os.ReadFile(fullPath)
			require.NoError(t, readErr)
			allContent += string(content)
		}
		assert.Contains(t, allContent, "test_metric")
		assert.Contains(t, allContent, "machine.id")

		mockCollector.AssertExpectations(t)

		err = exporter.Stop()
		require.NoError(t, err)
	})

}

// TestStop tests the Stop function
func TestStop(t *testing.T) {
	ctx := context.Background()

	t.Run("stops successfully", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.Start()
		require.NoError(t, err)

		err = exporter.Stop()
		require.NoError(t, err)

		// Verify context is canceled
		he := exporter.(*healthExporter)
		select {
		case <-he.ctx.Done():
			// Expected
		case <-time.After(100 * time.Millisecond):
			t.Fatal("context not canceled")
		}
	})

}

// TestExportToFile tests the exportToFile function
func TestExportToFile(t *testing.T) {
	t.Run("exports to JSON file", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputPath := tmpDir // Use directory path, not file path

		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 1 * time.Minute},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   outputPath,
			OutputFormat: "json",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		// Create test health data
		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
			MachineInfo: &machineinfo.MachineInfo{
				MachineID: "test-machine",
			},
			Metrics: []pkgmetrics.Metric{
				{
					Name:             "test_metric",
					Value:            42.0,
					UnixMilliseconds: time.Now().UnixMilli(),
				},
			},
		}

		err = he.exportToFile(healthData)
		require.NoError(t, err)

		// Verify directory exists and files were created
		entries, err := os.ReadDir(outputPath)
		require.NoError(t, err)
		assert.Greater(t, len(entries), 0, "Expected files to be created")

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("exports to CSV file", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputPath := tmpDir // Use directory path, not file path

		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 1 * time.Minute},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   outputPath,
			OutputFormat: "csv",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		// Create test health data
		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
			Metrics: []pkgmetrics.Metric{
				{
					Name:             "test_metric",
					Value:            42.0,
					UnixMilliseconds: time.Now().UnixMilli(),
				},
			},
		}

		err = he.exportToFile(healthData)
		require.NoError(t, err)

		// Verify directory exists and files were created
		entries, err := os.ReadDir(outputPath)
		require.NoError(t, err)
		assert.Greater(t, len(entries), 0, "Expected files to be created")

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("defaults to JSON format when format is empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputPath := tmpDir // Use directory path, not file path

		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 1 * time.Minute},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   outputPath,
			OutputFormat: "", // Empty format
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
		}

		err = he.exportToFile(healthData)
		require.NoError(t, err)

		// Verify directory exists and files were created
		entries, err := os.ReadDir(outputPath)
		require.NoError(t, err)
		assert.Greater(t, len(entries), 0, "Expected files to be created")

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("fails with unsupported format", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputPath := tmpDir // Use directory path, not file path

		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 1 * time.Minute},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   outputPath,
			OutputFormat: "xml",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
		}

		err = he.exportToFile(healthData)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported output format")

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})
}

// TestExportToHTTP tests the exportToHTTP function
func TestExportToHTTP(t *testing.T) {
	t.Run("exports successfully to HTTP endpoint", func(t *testing.T) {
		// Create mock server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Contains(t, r.Header.Get("Content-Type"), "application/x-protobuf")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("SUCCESS"))
		}))
		defer server.Close()

		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 1 * time.Minute},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: server.URL,
			LogsEndpoint:    server.URL,
			AuthToken:       "test-token",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
			Metrics: []pkgmetrics.Metric{
				{
					Name:             "test_metric",
					Value:            42.0,
					UnixMilliseconds: time.Now().UnixMilli(),
				},
			},
		}

		err = he.exportToHTTP(ctx, healthData)
		require.NoError(t, err)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("skips when no endpoints configured", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 1 * time.Minute},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: "",
			LogsEndpoint:    "",
			AuthToken:       "test-token",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
		}

		err = he.exportToHTTP(ctx, healthData)
		require.NoError(t, err) // Should not error, just skip gracefully

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("updates token from server response", func(t *testing.T) {
		newToken := "new-test-token"

		// Create mock server that returns new token
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("jwt_assertion", newToken)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("SUCCESS"))
		}))
		defer server.Close()

		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 1 * time.Minute},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: server.URL,
			LogsEndpoint:    server.URL,
			AuthToken:       "old-token",
		}

		ctx := context.Background()

		// Create temporary database for testing token updates
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
			Metrics: []pkgmetrics.Metric{
				{
					Name:             "test_metric",
					Value:            42.0,
					UnixMilliseconds: time.Now().UnixMilli(),
				},
			},
		}

		err = he.exportToHTTP(ctx, healthData)
		require.NoError(t, err)

		// Verify token was updated in config
		assert.Equal(t, newToken, he.options.config.AuthToken, "token should be refreshed in config")

		// Verify token was persisted to DB
		storedToken, err := pkgmetadata.ReadMetadata(ctx, tmpDB, pkgmetadata.MetadataKeyToken)
		require.NoError(t, err)
		assert.Equal(t, newToken, storedToken, "token should be persisted to database")

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("exports without auth token when endpoints configured", func(t *testing.T) {
		// Create mock server that accepts requests without Authorization header
		var requestCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Empty(t, r.Header.Get("Authorization"), "should not send Authorization header when token is empty")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 1 * time.Minute},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: server.URL,
			LogsEndpoint:    server.URL,
			AuthToken:       "", // No auth token - should still export
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		healthData := &collector.HealthData{
			MachineID: "test-machine",
			Timestamp: time.Now(),
			Metrics: []pkgmetrics.Metric{
				{
					Name:             "test_metric",
					Value:            42.0,
					UnixMilliseconds: time.Now().UnixMilli(),
				},
			},
		}

		err = he.exportToHTTP(ctx, healthData)
		require.NoError(t, err)
		assert.Greater(t, requestCount.Load(), int32(0), "expected at least one request to the server")

		err = exporter.Stop()
		require.NoError(t, err)
	})

}

// TestExportNow tests the ExportNow function
func TestExportNow(t *testing.T) {
	t.Run("triggers immediate export in offline mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputPath := tmpDir // Use directory path, not file path

		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 1 * time.Minute},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   outputPath,
			OutputFormat: "json",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.ExportNow(ctx)
		require.NoError(t, err)

		// Verify directory exists and files were created
		entries, err := os.ReadDir(outputPath)
		require.NoError(t, err)
		assert.Greater(t, len(entries), 0, "Expected files to be created")

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})
}

// TestRefreshConfigFromMetadata tests the refreshConfigFromMetadata function
func TestRefreshConfigFromMetadata(t *testing.T) {
	t.Run("collector endpoint overrides backend metadata and credentials", func(t *testing.T) {
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()
		require.NoError(t, pkgmetadata.SetMetadata(ctx, tmpDB, "backend_base_url", "https://backend.example.com"))
		require.NoError(t, pkgmetadata.SetMetadata(ctx, tmpDB, pkgmetadata.MetadataKeyToken, "backend-jwt"))

		cfg := &config.HealthExporterConfig{
			Interval:          metav1.Duration{Duration: time.Minute},
			Timeout:           metav1.Duration{Duration: 30 * time.Second},
			CollectorEndpoint: "http://fleetint-otel-gateway:4318/",
			AuthToken:         "stale-token",
		}
		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)

		he := exporter.(*healthExporter)
		he.refreshConfigFromMetadata(ctx)

		assert.Equal(t, "http://fleetint-otel-gateway:4318/v1/metrics", he.options.config.MetricsEndpoint)
		assert.Equal(t, "http://fleetint-otel-gateway:4318/v1/logs", he.options.config.LogsEndpoint)
		assert.Empty(t, he.options.config.AuthToken)
		require.NoError(t, exporter.Stop())
	})

	t.Run("skips when no database connection", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		// Should not panic or error
		he.refreshConfigFromMetadata(ctx)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("refreshes config from database", func(t *testing.T) {
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()

		err := pkgmetadata.SetMetadata(ctx, tmpDB, "backend_base_url", "https://backend.example.com")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, pkgmetadata.MetadataKeyToken, "new-test-token")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 1 * time.Minute},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: "https://old-metrics.example.com",
			LogsEndpoint:    "https://old-logs.example.com",
			AuthToken:       "old-token",
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		// Refresh config
		he.refreshConfigFromMetadata(ctx)

		// Verify config was updated
		assert.Equal(t, "https://backend.example.com/api/v1/health/metrics", he.options.config.MetricsEndpoint)
		assert.Equal(t, "https://backend.example.com/api/v1/health/logs", he.options.config.LogsEndpoint)
		assert.Equal(t, "new-test-token", he.options.config.AuthToken)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("clears endpoints when metadata is empty", func(t *testing.T) {
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()

		err := pkgmetadata.SetMetadata(ctx, tmpDB, "backend_base_url", "")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 1 * time.Minute},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: "https://old-metrics.example.com",
			LogsEndpoint:    "https://old-logs.example.com",
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		// Refresh config
		he.refreshConfigFromMetadata(ctx)

		// Verify endpoints were cleared
		assert.Empty(t, he.options.config.MetricsEndpoint)
		assert.Empty(t, he.options.config.LogsEndpoint)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("ignores invalid endpoints from metadata", func(t *testing.T) {
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()

		err := pkgmetadata.SetMetadata(ctx, tmpDB, "backend_base_url", "http://bad-backend.example.com")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 1 * time.Minute},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: "https://old-metrics.example.com",
			LogsEndpoint:    "https://old-logs.example.com",
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)
		he.refreshConfigFromMetadata(ctx)

		assert.Empty(t, he.options.config.MetricsEndpoint)
		assert.Empty(t, he.options.config.LogsEndpoint)

		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("falls back to legacy endpoints when backend base URL is invalid", func(t *testing.T) {
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()

		err := pkgmetadata.SetMetadata(ctx, tmpDB, "backend_base_url", "http://bad-backend.example.com")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "metrics_endpoint", "https://legacy.example.com/api/v1/health/metrics")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "logs_endpoint", "https://legacy.example.com/api/v1/health/logs")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)

		he := exporter.(*healthExporter)
		he.refreshConfigFromMetadata(ctx)

		assert.Equal(t, "https://legacy.example.com/api/v1/health/metrics", he.options.config.MetricsEndpoint)
		assert.Equal(t, "https://legacy.example.com/api/v1/health/logs", he.options.config.LogsEndpoint)

		err = exporter.Stop()
		require.NoError(t, err)
	})
}

// TestUpdateTokenInMetadata tests the updateTokenInMetadata function
func TestUpdateTokenInMetadata(t *testing.T) {
	t.Run("fails when no database connection", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		err = he.updateTokenInMetadata(ctx, "new-token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no read-write database connection available")

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("updates token in database", func(t *testing.T) {
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		ctx := context.Background()
		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		// Update token - just verify it doesn't error
		err = he.updateTokenInMetadata(ctx, "new-test-token")
		require.NoError(t, err)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})
}

// TestRefreshJWTToken tests the refreshJWTToken function
func TestRefreshJWTToken(t *testing.T) {
	t.Run("fails when no database connection", func(t *testing.T) {
		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		token, err := he.refreshJWTToken(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no database connection available")
		assert.Empty(t, token)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("fails when required metadata missing", func(t *testing.T) {
		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		ctx := context.Background()
		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		token, err := he.refreshJWTToken(ctx)
		require.Error(t, err)
		assert.Empty(t, token)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("refreshes token successfully", func(t *testing.T) {
		expectedToken := "new-jwt-token"
		originalFactory := newBackendClient
		t.Cleanup(func() { newBackendClient = originalFactory })
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			assert.Equal(t, "https://backend.example.com", rawBaseURL)
			return &fakeJWTRefreshClient{
				expectedSAK: "test-sak-token",
				token:       expectedToken,
			}, nil
		}

		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()
		// Setup metadata
		err := pkgmetadata.SetMetadata(ctx, tmpDB, "sak_token", "test-sak-token")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "backend_base_url", "https://backend.example.com")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		token, err := he.refreshJWTToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, expectedToken, token)

		// Verify DB was updated
		storedToken, err := pkgmetadata.ReadMetadata(ctx, tmpDB, pkgmetadata.MetadataKeyToken)
		require.NoError(t, err)
		assert.Equal(t, expectedToken, storedToken)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("refreshes token successfully using legacy enroll endpoint", func(t *testing.T) {
		expectedToken := "new-jwt-token"
		originalFactory := newBackendClient
		t.Cleanup(func() { newBackendClient = originalFactory })
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			assert.Equal(t, "https://backend.example.com", rawBaseURL)
			return &fakeJWTRefreshClient{
				expectedSAK: "test-sak-token",
				token:       expectedToken,
			}, nil
		}

		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()
		err := pkgmetadata.SetMetadata(ctx, tmpDB, "sak_token", "test-sak-token")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "enroll_endpoint", "https://backend.example.com/api/v1/health/enroll")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)
		require.NotNil(t, exporter)

		he := exporter.(*healthExporter)

		token, err := he.refreshJWTToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, expectedToken, token)

		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("refreshes token using legacy endpoint when backend base URL is invalid", func(t *testing.T) {
		expectedToken := "new-jwt-token"
		originalFactory := newBackendClient
		t.Cleanup(func() { newBackendClient = originalFactory })
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			assert.Equal(t, "https://backend.example.com", rawBaseURL)
			return &fakeJWTRefreshClient{
				expectedSAK: "test-sak-token",
				token:       expectedToken,
			}, nil
		}

		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()
		err := pkgmetadata.SetMetadata(ctx, tmpDB, "sak_token", "test-sak-token")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "backend_base_url", "http://bad-backend.example.com")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "enroll_endpoint", "https://backend.example.com/api/v1/health/enroll")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)

		he := exporter.(*healthExporter)

		token, err := he.refreshJWTToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, expectedToken, token)

		err = exporter.Stop()
		require.NoError(t, err)
	})

	t.Run("refreshes token using later legacy endpoint when earlier one is malformed", func(t *testing.T) {
		expectedToken := "new-jwt-token"
		originalFactory := newBackendClient
		t.Cleanup(func() { newBackendClient = originalFactory })
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			assert.Equal(t, "https://backend.example.com", rawBaseURL)
			return &fakeJWTRefreshClient{
				expectedSAK: "test-sak-token",
				token:       expectedToken,
			}, nil
		}

		tmpDB := setupTestDB(t)
		defer tmpDB.Close()

		ctx := context.Background()
		err := pkgmetadata.SetMetadata(ctx, tmpDB, "sak_token", "test-sak-token")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "enroll_endpoint", "https://backend.example.com?bad=query")
		require.NoError(t, err)
		err = pkgmetadata.SetMetadata(ctx, tmpDB, "metrics_endpoint", "https://backend.example.com/api/v1/health/metrics")
		require.NoError(t, err)

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx,
			WithConfig(cfg),
			WithDatabaseConnections(tmpDB, tmpDB),
			WithMachineID("test-machine-id"),
		)
		require.NoError(t, err)

		he := exporter.(*healthExporter)

		token, err := he.refreshJWTToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, expectedToken, token)

		err = exporter.Stop()
		require.NoError(t, err)
	})
}

type fakeJWTRefreshClient struct {
	expectedSAK string
	token       string
}

func (f *fakeJWTRefreshClient) Enroll(_ context.Context, sakToken string) (string, error) {
	if f.expectedSAK != "" && sakToken != f.expectedSAK {
		return "", fmt.Errorf("unexpected sak token %q", sakToken)
	}
	return f.token, nil
}

func (f *fakeJWTRefreshClient) UpsertNode(context.Context, string, *backendclient.NodeUpsertRequest, string) (*backendclient.NodeUpsertResponse, error) {
	return nil, nil
}

func (f *fakeJWTRefreshClient) GetNonce(context.Context, string, string) (*backendclient.NonceResponse, error) {
	return nil, nil
}

func (f *fakeJWTRefreshClient) SubmitAttestation(context.Context, string, *backendclient.AttestationRequest, string) error {
	return nil
}

// TestIntegration provides integration tests
func TestIntegration(t *testing.T) {
	t.Run("full lifecycle with file export", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputPath := tmpDir // Use directory path, not file path

		cfg := &config.HealthExporterConfig{
			Interval:     metav1.Duration{Duration: 200 * time.Millisecond},
			Timeout:      metav1.Duration{Duration: 30 * time.Second},
			OfflineMode:  true,
			OutputPath:   outputPath,
			OutputFormat: "json",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.Start()
		require.NoError(t, err)

		// Wait for at least one export
		time.Sleep(300 * time.Millisecond)

		err = exporter.Stop()
		require.NoError(t, err)

		// Verify directory exists and files were created
		entries, err := os.ReadDir(outputPath)
		require.NoError(t, err)
		assert.Greater(t, len(entries), 0, "Expected files to be created")
	})

	t.Run("full lifecycle with HTTP export", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("SUCCESS"))
		}))
		defer server.Close()

		cfg := &config.HealthExporterConfig{
			Interval:        metav1.Duration{Duration: 200 * time.Millisecond},
			Timeout:         metav1.Duration{Duration: 30 * time.Second},
			MetricsEndpoint: server.URL,
			LogsEndpoint:    server.URL,
			AuthToken:       "test-token",
		}

		ctx := context.Background()
		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.Start()
		require.NoError(t, err)

		// Wait for at least one export
		time.Sleep(300 * time.Millisecond)

		err = exporter.Stop()
		require.NoError(t, err)

		// Verify server received requests
		assert.Greater(t, requestCount, 0)
	})
}

// TestContextCancellation tests context cancellation behavior
func TestContextCancellation(t *testing.T) {
	t.Run("stops when parent context is canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		cfg := &config.HealthExporterConfig{
			Interval: metav1.Duration{Duration: 100 * time.Millisecond},
			Timeout:  metav1.Duration{Duration: 30 * time.Second},
		}

		exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
		require.NoError(t, err)
		require.NotNil(t, exporter)

		err = exporter.Start()
		require.NoError(t, err)

		// Cancel parent context
		cancel()

		// Wait for goroutine to stop
		time.Sleep(200 * time.Millisecond)

		// Cleanup
		err = exporter.Stop()
		require.NoError(t, err)
	})
}

// Helper function to setup test database
func setupTestDB(t *testing.T) *sql.DB {
	// Create an in-memory SQLite database for testing
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	// Create metadata table using the proper function
	ctx := context.Background()
	err = pkgmetadata.CreateTableMetadata(ctx, db)
	require.NoError(t, err)

	return db
}

func TestPopulateOptionalResourceMetadataReadsNodePlacementTogether(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	nodeGroup := "group-a"
	computeZone := "zone-a"
	require.NoError(t, agentstate.UpdateNodePlacementMetadata(
		context.Background(),
		db,
		&nodeGroup,
		&computeZone,
	))

	exporter := &healthExporter{options: &exporterOptions{dbRO: db}}
	data := &collector.HealthData{}
	exporter.populateOptionalResourceMetadata(context.Background(), data)

	require.Equal(t, nodeGroup, data.NodeGroup)
	require.Equal(t, computeZone, data.ComputeZone)
}

// TestExportWithCollectorError tests export behavior when collector fails
func TestExportWithCollectorError(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "health.json")

	cfg := &config.HealthExporterConfig{
		Interval:     metav1.Duration{Duration: 1 * time.Minute},
		Timeout:      metav1.Duration{Duration: 30 * time.Second},
		OfflineMode:  true,
		OutputPath:   outputPath,
		OutputFormat: "json",
	}

	ctx := context.Background()
	exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
	require.NoError(t, err)
	require.NotNil(t, exporter)

	he := exporter.(*healthExporter)

	// Replace collector with mock that returns error
	mockCollector := &MockCollector{}
	mockCollector.On("Collect", mock.Anything).Return(nil, errors.New("collection failed"))
	he.collector = mockCollector

	err = he.export()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collection failed")

	// Cleanup
	err = exporter.Stop()
	require.NoError(t, err)
}

func TestExportUsesSeparateContextsForCollectionAndHTTP(t *testing.T) {
	cfg := &config.HealthExporterConfig{
		Interval:        metav1.Duration{Duration: 1 * time.Minute},
		Timeout:         metav1.Duration{Duration: 50 * time.Millisecond},
		MetricsEndpoint: "https://metrics.example.com",
		LogsEndpoint:    "https://logs.example.com",
		AuthToken:       "test-token",
	}

	ctx := context.Background()
	exporter, err := New(ctx, WithConfig(cfg), WithMachineID("test-machine-id"))
	require.NoError(t, err)
	require.NotNil(t, exporter)

	he := exporter.(*healthExporter)

	mockCollector := &MockCollector{}
	mockCollector.
		On("Collect", mock.Anything).
		Run(func(args mock.Arguments) {
			time.Sleep(75 * time.Millisecond)
		}).
		Return(&collector.HealthData{
			CollectionID: "collection-1",
			MachineID:    "test-machine-id",
			Timestamp:    time.Now().UTC(),
		}, nil)

	mockHTTPWriter := &MockHTTPWriter{}
	mockHTTPWriter.
		On("Send", mock.Anything, mock.Anything, cfg.MetricsEndpoint, cfg.LogsEndpoint, cfg.RetryMaxAttempts, cfg.AuthToken).
		Run(func(args mock.Arguments) {
			sendCtx, ok := args.Get(0).(context.Context)
			require.True(t, ok)
			assert.NoError(t, sendCtx.Err(), "export should receive a fresh context even if collection overran its deadline")
		}).
		Return("", nil)

	he.collector = mockCollector
	he.httpWriter = mockHTTPWriter

	err = he.export()
	require.NoError(t, err)

	mockCollector.AssertExpectations(t)
	mockHTTPWriter.AssertExpectations(t)

	err = exporter.Stop()
	require.NoError(t, err)
}

// TestExportHTTPWithReturnedToken tests handling of JWT token returned from server
func TestExportHTTPWithReturnedToken(t *testing.T) {
	newJWT := "new-jwt-token-from-server"
	tokenUpdated := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return new JWT token in header
		w.Header().Set("X-New-JWT-Token", newJWT)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "SUCCESS")
	}))
	defer server.Close()

	cfg := &config.HealthExporterConfig{
		Interval:        metav1.Duration{Duration: 1 * time.Minute},
		Timeout:         metav1.Duration{Duration: 30 * time.Second},
		MetricsEndpoint: server.URL,
		LogsEndpoint:    server.URL,
		AuthToken:       "old-token",
	}

	ctx := context.Background()

	// Setup test database
	tmpDB := setupTestDB(t)
	defer tmpDB.Close()

	exporter, err := New(ctx,
		WithConfig(cfg),
		WithDatabaseConnections(tmpDB, tmpDB),
		WithMachineID("test-machine-id"),
	)
	require.NoError(t, err)
	require.NotNil(t, exporter)

	he := exporter.(*healthExporter)

	healthData := &collector.HealthData{
		MachineID: "test-machine",
		Timestamp: time.Now(),
		Metrics: []pkgmetrics.Metric{
			{
				Name:             "test_metric",
				Value:            42.0,
				UnixMilliseconds: time.Now().UnixMilli(),
			},
		},
	}

	err = he.exportToHTTP(ctx, healthData)
	require.NoError(t, err)

	// Token update may or may not succeed depending on DB setup
	// but the export should succeed regardless
	_ = tokenUpdated

	// Cleanup
	err = exporter.Stop()
	require.NoError(t, err)
}
