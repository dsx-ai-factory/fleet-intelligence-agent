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

// Package healthexporter provides functionality to export health data from local SQLite
// to a global health endpoint for centralized monitoring and long-term storage using OTLP format.
//
// This package follows Go best practices with separated concerns:
// - collector: Data collection from various sources
// - converter: Conversion to different formats (OTLP, CSV)
// - writer: Output to files or HTTP endpoints
package exporter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/agentstate"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/backendclient"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/endpoint"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/exporter/converter"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/exporter/writer"
)

// Ensure healthExporter implements the Exporter interface
var _ Exporter = (*healthExporter)(nil)

var newBackendClient = backendclient.New

// healthExporter implements the Exporter interface with improved architecture
type healthExporter struct {
	ctx        context.Context
	cancel     context.CancelFunc
	options    *exporterOptions
	collector  collector.Collector
	fileWriter writer.FileWriter
	httpWriter writer.HTTPWriter

	// Last export timestamp for tracking
	lastExport time.Time
}

// New creates a new health exporter instance using functional options
func New(ctx context.Context, opts ...ExporterOption) (Exporter, error) {
	cctx, cancel := context.WithCancel(ctx)

	// Apply options
	options := &exporterOptions{}
	for _, opt := range opts {
		if err := opt(options); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	// Validate and set defaults
	if err := options.validate(); err != nil {
		cancel()
		return nil, fmt.Errorf("invalid options: %w", err)
	}
	options.setDefaults()

	dataCollector := collector.New(
		options.config,
		options.metricsStore,
		options.eventStore,
		options.componentsRegistry,
		options.machineID,
		options.dcgmGPUIndexes,
		collector.WithMachineInfoCollector(options.collectMachineInfo),
	)

	otlpConverter := converter.NewOTLPConverter()
	csvConverter := converter.NewCSVConverter()

	fileWriter := writer.NewFileWriter(otlpConverter, csvConverter)
	httpWriter := writer.NewHTTPWriter(options.httpClient, otlpConverter)

	exporter := &healthExporter{
		ctx:        cctx,
		cancel:     cancel,
		options:    options,
		collector:  dataCollector,
		fileWriter: fileWriter,
		httpWriter: httpWriter,
	}

	// Set JWT refresh function on the HTTP writer
	httpWriter.SetJWTRefreshFunc(exporter.refreshJWTToken)

	return exporter, nil
}

// Start begins the periodic export process
func (e *healthExporter) Start() error {
	if e.options.config.Interval.Duration <= 0 {
		log.Logger.Debug("health exporter: no interval configured, skipping")
		return nil
	}

	log.Logger.Infow("Starting health exporter")

	// In offline mode, emit one export immediately so short runs don't exit
	// before the first ticker cycle and leave an empty output directory.
	if e.options.config.OfflineMode {
		if err := e.export(); err != nil {
			log.Logger.Errorw("Initial offline export failed", "error", err)
		} else {
			e.lastExport = time.Now().UTC()
		}
	}

	// Start the health export ticker
	go func() {
		ticker := time.NewTicker(e.options.config.Interval.Duration)
		defer ticker.Stop()

		for {
			select {
			case <-e.ctx.Done():
				log.Logger.Infow("Context done, stopping periodic export")
				return
			case <-ticker.C:
				if err := e.export(); err != nil {
					log.Logger.Errorw("Export failed", "error", err)
				} else {
					e.lastExport = time.Now().UTC()
				}
			}
		}
	}()

	return nil
}

// Stop gracefully shuts down the exporter
func (e *healthExporter) Stop() error {
	log.Logger.Infow("Stopping health exporter")
	e.cancel()
	return nil
}

// ExportNow triggers an immediate export
func (e *healthExporter) ExportNow(ctx context.Context) error {
	return e.export()
}

// export performs the actual data export operation
func (e *healthExporter) export() error {
	log.Logger.Infow("Starting health export")
	collectionCtx, cancelCollection := context.WithTimeout(e.ctx, e.options.timeout)
	defer cancelCollection()

	// Refresh configuration from metadata on every export
	// If the endpoints/auth token are not empty, export will continue
	// If the endpoints/auth token are empty, exportHTTP will skip
	e.refreshConfigFromMetadata(collectionCtx)

	// Collect health data
	healthData, err := e.collector.Collect(collectionCtx)
	if err != nil {
		return fmt.Errorf("collection failed: %w", err)
	}
	e.populateOptionalResourceMetadata(collectionCtx, healthData)

	// Export data based on mode
	if e.options.config.OfflineMode {
		return e.exportToFile(healthData)
	} else {
		exportCtx, cancelExport := context.WithTimeout(e.ctx, e.options.timeout)
		defer cancelExport()
		return e.exportToHTTP(exportCtx, healthData)
	}
}

func (e *healthExporter) populateOptionalResourceMetadata(ctx context.Context, data *collector.HealthData) {
	if data == nil || e.options.dbRO == nil {
		return
	}

	nodeGroup, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, agentstate.MetadataKeyNodeGroup)
	if err != nil {
		log.Logger.Debugw("node_group metadata not available for telemetry resource", "error", err)
	} else {
		data.NodeGroup = nodeGroup
	}

	computeZone, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, agentstate.MetadataKeyComputeZone)
	if err != nil {
		log.Logger.Debugw("compute zone metadata not available for telemetry resource", "error", err)
	} else {
		data.ComputeZone = computeZone
	}
}

// exportToFile writes health data to files
func (e *healthExporter) exportToFile(data *collector.HealthData) error {
	outputFormat := e.options.config.OutputFormat
	if outputFormat == "" {
		outputFormat = "json"
	}

	switch outputFormat {
	case "csv":
		if err := e.fileWriter.WriteCSV(data, e.options.config.OutputPath); err != nil {
			return fmt.Errorf("failed to write CSV file: %w", err)
		}
	case "json":
		if err := e.fileWriter.WriteJSON(data, e.options.config.OutputPath); err != nil {
			return fmt.Errorf("failed to write JSON file: %w", err)
		}
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}

	// Log the timestamp of the export on successful export
	log.Logger.Infow("Successfully exported health data to file", "timestamp", time.Now().UTC())
	return nil
}

// exportToHTTP sends health data via HTTP
func (e *healthExporter) exportToHTTP(ctx context.Context, data *collector.HealthData) error {
	// Skip export if no endpoints are configured
	if e.options.config.MetricsEndpoint == "" && e.options.config.LogsEndpoint == "" {
		log.Logger.Infow("No endpoints configured, skipping HTTP export")
		return nil
	}

	if e.options.config.AuthToken == "" {
		log.Logger.Warnw("No auth token configured, exporting without authentication")
	}

	newToken, err := e.httpWriter.Send(ctx, data, e.options.config.MetricsEndpoint, e.options.config.LogsEndpoint, e.options.config.RetryMaxAttempts, e.options.config.AuthToken)
	if err != nil {
		return fmt.Errorf("failed to send data: %w", err)
	}

	// Log the timestamp of the export on successful export - this won't be logged if not enrolled
	log.Logger.Infow("Successfully exported health data to", "timestamp", time.Now().UTC())

	// If we received a new JWT token, update it in metadata and config
	if newToken != "" && newToken != e.options.config.AuthToken {
		log.Logger.Info("Updating JWT token from server response")

		if err := e.updateTokenInMetadata(ctx, newToken); err != nil {
			log.Logger.Errorw("Failed to update JWT token in metadata", "error", err)
			// Don't fail the export if token update fails
		} else {
			e.options.config.AuthToken = newToken
			log.Logger.Infow("Successfully updated JWT token")
		}
	}

	return nil
}

// refreshConfigFromMetadata updates the exporter configuration from metadata table
func (e *healthExporter) refreshConfigFromMetadata(ctx context.Context) {
	// Collector mode is explicit runtime configuration and must take precedence
	// over enrollment metadata, which points directly at the backend.
	if collectorEndpoint := strings.TrimRight(e.options.config.CollectorEndpoint, "/"); collectorEndpoint != "" {
		e.options.config.MetricsEndpoint = collectorEndpoint + "/v1/metrics"
		e.options.config.LogsEndpoint = collectorEndpoint + "/v1/logs"
		// Authentication to the backend is owned by the gateway. Do not forward
		// a node JWT to the cluster-local collector.
		e.options.config.AuthToken = ""
		return
	}

	// Use the passed database connection instead of opening a new one
	if e.options.dbRO == nil {
		log.Logger.Debugw("no database connection available for metadata refresh")
		return
	}

	metricsEndpoint := ""
	logsEndpoint := ""
	baseURL, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, "backend_base_url")
	if err != nil {
		log.Logger.Errorw("failed to read backend base URL from metadata", "error", err)
		baseURL = ""
	}
	if baseURL != "" {
		validated, validateErr := endpoint.ValidateBackendEndpoint(baseURL)
		if validateErr != nil {
			log.Logger.Errorw("ignoring invalid backend base URL from metadata", "error", validateErr)
			metricsEndpoint = e.readValidatedEndpoint(ctx, "metrics_endpoint")
			logsEndpoint = e.readValidatedEndpoint(ctx, "logs_endpoint")
		} else {
			if joined, joinErr := endpoint.JoinPath(validated, "api", "v1", "health", "metrics"); joinErr == nil {
				metricsEndpoint = joined
			} else {
				log.Logger.Errorw("failed to derive metrics endpoint from backend base URL", "error", joinErr)
				metricsEndpoint = e.readValidatedEndpoint(ctx, "metrics_endpoint")
			}
			if joined, joinErr := endpoint.JoinPath(validated, "api", "v1", "health", "logs"); joinErr == nil {
				logsEndpoint = joined
			} else {
				log.Logger.Errorw("failed to derive logs endpoint from backend base URL", "error", joinErr)
				logsEndpoint = e.readValidatedEndpoint(ctx, "logs_endpoint")
			}
		}
	} else {
		metricsEndpoint = e.readValidatedEndpoint(ctx, "metrics_endpoint")
		logsEndpoint = e.readValidatedEndpoint(ctx, "logs_endpoint")
	}

	{
		if e.options.config.MetricsEndpoint != metricsEndpoint {
			e.options.config.MetricsEndpoint = metricsEndpoint
			if metricsEndpoint == "" {
				log.Logger.Infow("cleared metrics endpoint from metadata")
			} else {
				log.Logger.Infow("updated metrics endpoint from metadata", "metrics_endpoint", metricsEndpoint)
			}
		}
	}

	{
		if e.options.config.LogsEndpoint != logsEndpoint {
			e.options.config.LogsEndpoint = logsEndpoint
			if logsEndpoint == "" {
				log.Logger.Infow("cleared logs endpoint from metadata")
			} else {
				log.Logger.Infow("updated logs endpoint from metadata", "logs_endpoint", logsEndpoint)
			}
		}
	}

	// Load auth token (update even if empty to handle un-enrollment)
	if token, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, pkgmetadata.MetadataKeyToken); err == nil {
		if e.options.config.AuthToken != token {
			e.options.config.AuthToken = token
			if token == "" {
				log.Logger.Infow("cleared auth token from metadata")
			} else {
				log.Logger.Infow("updated auth token from metadata")
			}
		}
	} else {
		log.Logger.Errorw("failed to read auth token from metadata", "error", err)
	}
}

// updateTokenInMetadata updates the JWT token in the metadata database
func (e *healthExporter) updateTokenInMetadata(ctx context.Context, newToken string) error {
	// Use the passed database connection instead of opening a new one
	if e.options.dbRW == nil {
		return fmt.Errorf("no read-write database connection available for token update")
	}

	if err := pkgmetadata.SetMetadata(ctx, e.options.dbRW, pkgmetadata.MetadataKeyToken, newToken); err != nil {
		return fmt.Errorf("failed to update JWT token in metadata: %w", err)
	}

	return nil
}

// refreshJWTToken attempts to get a new JWT token using the stored SAK token
func (e *healthExporter) refreshJWTToken(ctx context.Context) (string, error) {
	// Use the passed database connection to read SAK token and endpoints
	if e.options.dbRO == nil {
		return "", fmt.Errorf("no database connection available for JWT refresh")
	}

	// Read SAK token from metadata
	sakToken, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, "sak_token")
	if err != nil || sakToken == "" {
		return "", fmt.Errorf("no SAK token available for JWT refresh")
	}

	baseURL, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, "backend_base_url")
	if err == nil && baseURL != "" {
		if _, validateErr := endpoint.ValidateBackendEndpoint(baseURL); validateErr != nil {
			log.Logger.Errorw("ignoring invalid backend base URL for JWT refresh", "backend_base_url", baseURL, "error", validateErr)
			baseURL = ""
		}
	}
	if err != nil || baseURL == "" {
		baseURL, err = e.readLegacyBackendBaseURL(ctx)
		if err != nil {
			return "", err
		}
		if baseURL == "" {
			return "", fmt.Errorf("no backend base URL available for JWT refresh")
		}
	}

	client, err := newBackendClient(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to create backend client for JWT refresh: %w", err)
	}
	newJWT, err := client.Enroll(ctx, sakToken)
	if err != nil {
		return "", fmt.Errorf("failed to refresh JWT token: %w", err)
	}

	// Update JWT token in metadata using the read-write connection
	if e.options.dbRW != nil {
		if err := pkgmetadata.SetMetadata(ctx, e.options.dbRW, pkgmetadata.MetadataKeyToken, newJWT); err != nil {
			log.Logger.Errorw("Failed to update refreshed JWT token in metadata", "error", err)
			// Don't fail the refresh, just log the error
		}
	}

	log.Logger.Infow("Successfully refreshed JWT token")
	return newJWT, nil
}

func (e *healthExporter) readValidatedEndpoint(ctx context.Context, key string) string {
	value, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, key)
	if err != nil || value == "" {
		return ""
	}
	validated, err := endpoint.ValidateBackendEndpoint(value)
	if err != nil {
		log.Logger.Errorw("ignoring invalid legacy endpoint from metadata", "key", key, "error", err)
		return ""
	}
	return validated.String()
}

func (e *healthExporter) readLegacyBackendBaseURL(ctx context.Context) (string, error) {
	for _, key := range []string{"enroll_endpoint", "metrics_endpoint", "logs_endpoint", "nonce_endpoint"} {
		value, err := pkgmetadata.ReadMetadata(ctx, e.options.dbRO, key)
		if err != nil || value == "" {
			continue
		}
		baseURL, err := endpoint.DeriveBackendBaseURL(value)
		if err != nil {
			log.Logger.Errorw("ignoring invalid legacy backend endpoint for JWT refresh", "key", key, "value", value, "error", err)
			continue
		}
		return baseURL, nil
	}
	return "", nil
}
