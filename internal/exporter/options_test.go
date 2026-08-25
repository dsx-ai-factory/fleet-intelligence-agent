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
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

func TestWithConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      *config.HealthExporterConfig
		wantErr     bool
		expectedErr string
	}{
		{
			name: "valid config",
			config: &config.HealthExporterConfig{
				Interval: metav1.Duration{Duration: 1 * time.Minute},
				Timeout:  metav1.Duration{Duration: 30 * time.Second},
			},
			wantErr: false,
		},
		{
			name:        "nil config",
			config:      nil,
			wantErr:     true,
			expectedErr: "configuration cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &exporterOptions{}
			err := WithConfig(tt.config)(opts)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.config, opts.config)
				assert.Equal(t, tt.config.Timeout.Duration, opts.timeout)
			}
		})
	}
}

func TestWithMetricsStore(t *testing.T) {
	opts := &exporterOptions{}
	err := WithMetricsStore(nil)(opts)
	require.NoError(t, err)
	assert.Nil(t, opts.metricsStore)
}

func TestWithEventStore(t *testing.T) {
	opts := &exporterOptions{}
	err := WithEventStore(nil)(opts)
	require.NoError(t, err)
	assert.Nil(t, opts.eventStore)
}

func TestWithComponentsRegistry(t *testing.T) {
	opts := &exporterOptions{}
	err := WithComponentsRegistry(nil)(opts)
	require.NoError(t, err)
	assert.Nil(t, opts.componentsRegistry)
}

func TestWithHTTPClient(t *testing.T) {
	tests := []struct {
		name        string
		client      *http.Client
		wantErr     bool
		expectedErr string
	}{
		{
			name:    "valid client",
			client:  &http.Client{},
			wantErr: false,
		},
		{
			name:        "nil client",
			client:      nil,
			wantErr:     true,
			expectedErr: "HTTP client cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &exporterOptions{}
			err := WithHTTPClient(tt.client)(opts)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.client, opts.httpClient)
			}
		})
	}
}

func TestWithTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeout     time.Duration
		wantErr     bool
		expectedErr string
	}{
		{
			name:    "valid timeout",
			timeout: 30 * time.Second,
			wantErr: false,
		},
		{
			name:        "zero timeout",
			timeout:     0,
			wantErr:     true,
			expectedErr: "timeout must be positive",
		},
		{
			name:        "negative timeout",
			timeout:     -1 * time.Second,
			wantErr:     true,
			expectedErr: "timeout must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &exporterOptions{}
			err := WithTimeout(tt.timeout)(opts)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.timeout, opts.timeout)
			}
		})
	}
}

func TestExporterOptions_setDefaults_DisablesRedirects(t *testing.T) {
	opts := &exporterOptions{
		timeout: 30 * time.Second,
	}

	opts.setDefaults()

	require.NotNil(t, opts.httpClient)
	assert.Equal(t, 30*time.Second, opts.httpClient.Timeout)
	require.NotNil(t, opts.httpClient.CheckRedirect)
	assert.Equal(t, http.ErrUseLastResponse, opts.httpClient.CheckRedirect(&http.Request{}, nil))
}

func TestWithDatabaseConnections(t *testing.T) {
	tests := []struct {
		name        string
		dbRW        *sql.DB
		dbRO        *sql.DB
		wantErr     bool
		expectedErr string
	}{
		{
			name:    "valid connections",
			dbRW:    &sql.DB{},
			dbRO:    &sql.DB{},
			wantErr: false,
		},
		{
			name:        "nil read-write connection",
			dbRW:        nil,
			dbRO:        &sql.DB{},
			wantErr:     true,
			expectedErr: "read-write database connection cannot be nil",
		},
		{
			name:        "nil read-only connection",
			dbRW:        &sql.DB{},
			dbRO:        nil,
			wantErr:     true,
			expectedErr: "read-only database connection cannot be nil",
		},
		{
			name:        "both nil",
			dbRW:        nil,
			dbRO:        nil,
			wantErr:     true,
			expectedErr: "read-write database connection cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &exporterOptions{}
			err := WithDatabaseConnections(tt.dbRW, tt.dbRO)(opts)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.dbRW, opts.dbRW)
				assert.Equal(t, tt.dbRO, opts.dbRO)
			}
		})
	}
}

func TestExporterOptionsValidate(t *testing.T) {
	tests := []struct {
		name        string
		setupOpts   func() *exporterOptions
		wantErr     bool
		expectedErr string
	}{
		{
			name: "valid options with no dependencies required",
			setupOpts: func() *exporterOptions {
				return &exporterOptions{
					config: &config.HealthExporterConfig{
						IncludeMetrics:       false,
						IncludeEvents:        false,
						IncludeComponentData: false,
						IncludeMachineInfo:   false,
					},
					machineID: "test-machine-id",
				}
			},
			wantErr: false,
		},
		{
			name: "missing config",
			setupOpts: func() *exporterOptions {
				return &exporterOptions{}
			},
			wantErr:     true,
			expectedErr: "configuration is required",
		},
		{
			name: "metrics enabled but no metrics store",
			setupOpts: func() *exporterOptions {
				return &exporterOptions{
					config: &config.HealthExporterConfig{
						IncludeMetrics: true,
					},
				}
			},
			wantErr:     true,
			expectedErr: "metrics store is required when IncludeMetrics is enabled",
		},
		{
			name: "events enabled but no event store",
			setupOpts: func() *exporterOptions {
				return &exporterOptions{
					config: &config.HealthExporterConfig{
						IncludeEvents: true,
					},
				}
			},
			wantErr:     true,
			expectedErr: "event store is required when IncludeEvents is enabled",
		},
		{
			name: "component data enabled but no components registry",
			setupOpts: func() *exporterOptions {
				return &exporterOptions{
					config: &config.HealthExporterConfig{
						IncludeComponentData: true,
					},
				}
			},
			wantErr:     true,
			expectedErr: "components registry is required when IncludeComponentData is enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.setupOpts()
			err := opts.validate()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExporterOptionsSetDefaults(t *testing.T) {
	t.Run("sets default HTTP client when timeout is set", func(t *testing.T) {
		opts := &exporterOptions{
			timeout: 30 * time.Second,
		}

		opts.setDefaults()

		assert.NotNil(t, opts.httpClient)
		assert.Equal(t, 30*time.Second, opts.httpClient.Timeout)
	})

	t.Run("does not override existing HTTP client", func(t *testing.T) {
		existingClient := &http.Client{Timeout: 1 * time.Minute}
		opts := &exporterOptions{
			httpClient: existingClient,
			timeout:    30 * time.Second,
		}

		opts.setDefaults()

		require.NotNil(t, opts.httpClient)
		assert.NotSame(t, existingClient, opts.httpClient)
		assert.Equal(t, 1*time.Minute, opts.httpClient.Timeout)
		require.NotNil(t, opts.httpClient.CheckRedirect)
		assert.Equal(t, http.ErrUseLastResponse, opts.httpClient.CheckRedirect(&http.Request{}, nil))
		assert.Nil(t, existingClient.CheckRedirect)
	})

	t.Run("does not set client when timeout is zero", func(t *testing.T) {
		opts := &exporterOptions{
			timeout: 0,
		}

		opts.setDefaults()

		assert.Nil(t, opts.httpClient)
	})
}
