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
	"errors"
	"net/http"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

// ExporterOption defines a function that configures the health exporter
// Note: For configuration values, use config.HealthExporterConfig only.
// This options struct is strictly for wiring dependencies and runtime options.
type ExporterOption func(*exporterOptions) error

// exporterOptions holds dependencies and runtime options for the health exporter.
// Configuration values should be sourced from config.HealthExporterConfig.
type exporterOptions struct {
	config             *config.HealthExporterConfig
	metricsStore       pkgmetrics.Store
	eventStore         eventstore.Store
	componentsRegistry components.Registry
	nvmlInstance       nvidianvml.Instance
	httpClient         *http.Client
	timeout            time.Duration
	dbRW               *sql.DB           // Read-write database connection
	dbRO               *sql.DB           // Read-only database connection
	machineID          string            // Agent's stable identity from server initialization
	dcgmGPUIndexes     map[string]string // UUID → DCGM device ID for GPU index override
}

// WithConfig sets the health exporter configuration
func WithConfig(config *config.HealthExporterConfig) ExporterOption {
	return func(c *exporterOptions) error {
		if config == nil {
			return errors.New("configuration cannot be nil")
		}
		c.config = config
		c.timeout = config.Timeout.Duration
		return nil
	}
}

// WithMetricsStore sets the metrics store
func WithMetricsStore(store pkgmetrics.Store) ExporterOption {
	return func(c *exporterOptions) error {
		c.metricsStore = store
		return nil
	}
}

// WithEventStore sets the event store
func WithEventStore(store eventstore.Store) ExporterOption {
	return func(c *exporterOptions) error {
		c.eventStore = store
		return nil
	}
}

// WithComponentsRegistry sets the components registry
func WithComponentsRegistry(registry components.Registry) ExporterOption {
	return func(c *exporterOptions) error {
		c.componentsRegistry = registry
		return nil
	}
}

// WithNVMLInstance sets the NVML instance used for cached machine-info collection.
func WithNVMLInstance(instance nvidianvml.Instance) ExporterOption {
	return func(c *exporterOptions) error {
		c.nvmlInstance = instance
		return nil
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) ExporterOption {
	return func(c *exporterOptions) error {
		if client == nil {
			return errors.New("HTTP client cannot be nil")
		}
		c.httpClient = client
		return nil
	}
}

// WithTimeout sets a custom timeout (overrides config timeout)
func WithTimeout(timeout time.Duration) ExporterOption {
	return func(c *exporterOptions) error {
		if timeout <= 0 {
			return errors.New("timeout must be positive")
		}
		c.timeout = timeout
		return nil
	}
}

// WithDatabaseConnections sets the database connections for metadata access
func WithDatabaseConnections(dbRW, dbRO *sql.DB) ExporterOption {
	return func(c *exporterOptions) error {
		if dbRW == nil {
			return errors.New("read-write database connection cannot be nil")
		}
		if dbRO == nil {
			return errors.New("read-only database connection cannot be nil")
		}
		c.dbRW = dbRW
		c.dbRO = dbRO
		return nil
	}
}

// WithMachineID sets the machine ID for the exporter
// This should be the stable agent identity initialized by the server
func WithMachineID(machineID string) ExporterOption {
	return func(c *exporterOptions) error {
		if machineID == "" {
			return errors.New("machine ID cannot be empty")
		}
		c.machineID = machineID
		return nil
	}
}

// WithDCGMGPUIndexes sets the UUID→DCGM-device-ID mapping so that
// MachineInfo GPU indices match the "gpu" label emitted by DCGM metrics.
func WithDCGMGPUIndexes(m map[string]string) ExporterOption {
	return func(c *exporterOptions) error {
		c.dcgmGPUIndexes = m
		return nil
	}
}

// validateConfig validates the exporter options and required dependencies
func (c *exporterOptions) validate() error {
	if c.config == nil {
		return errors.New("configuration is required")
	}

	// Only validate dependencies that are actually needed based on config
	if c.config.IncludeMetrics && c.metricsStore == nil {
		return errors.New("metrics store is required when IncludeMetrics is enabled")
	}

	if c.config.IncludeEvents && c.eventStore == nil {
		return errors.New("event store is required when IncludeEvents is enabled")
	}

	if c.config.IncludeComponentData && c.componentsRegistry == nil {
		return errors.New("components registry is required when IncludeComponentData is enabled")
	}

	// Machine ID is always required - it should be set by server via WithMachineID
	if c.machineID == "" {
		return errors.New("machine ID is required - must be set via WithMachineID()")
	}

	return nil
}

// setDefaults sets default values for unspecified options
func (c *exporterOptions) setDefaults() {
	if c.httpClient == nil {
		if c.timeout <= 0 {
			return
		}
		c.httpClient = &http.Client{
			Timeout: c.timeout,
		}
	} else {
		// Copy caller-supplied clients so redirect hardening does not mutate shared state.
		clonedClient := *c.httpClient
		c.httpClient = &clonedClient
	}

	c.httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
}
