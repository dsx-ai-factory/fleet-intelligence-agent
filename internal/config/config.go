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
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pkgconfigcommon "github.com/NVIDIA/fleet-intelligence-sdk/pkg/config/common"
)

// ConfigEntry represents a single configuration option as a key-value pair
type ConfigEntry struct {
	Key   string
	Value string
}

// Config provides configuration for the health metrics exporter
type Config struct {
	// APIVersion for the health server
	APIVersion string `json:"api_version"`

	// Address for the health server to listen on
	Address string `json:"address"`

	// State file that persists health status and metrics
	// If empty, states are not persisted to file
	State string `json:"state"`

	// Amount of time to retain health states/metrics
	// Once elapsed, old data is automatically purged
	RetentionPeriod metav1.Duration `json:"retention_period"`

	// NVIDIA tool command paths to overwrite defaults
	NvidiaToolOverwrites pkgconfigcommon.ToolOverwrites `json:"nvidia_tool_overwrites"`

	// Components specifies which health components to enable
	// Leave empty, "*", or "all" to enable all components
	// Prefix component names with "-" to disable them
	Components         []string       `json:"components"`
	selectedComponents map[string]any `json:"-"`
	disabledComponents map[string]any `json:"-"`

	// EnableFaultInjection enables the /inject-fault endpoint for testing.
	// This endpoint allows injecting faults (kernel messages, component errors, events) into the system.
	// SECURITY: Only accessible from localhost (127.0.0.0/8 or ::1). Disabled by default.
	EnableFaultInjection bool `json:"enable_fault_injection"`

	// Inventory controls the periodic inventory loop.
	Inventory *InventoryConfig `json:"inventory,omitempty"`

	// Attestation controls the periodic attestation loop.
	Attestation *AttestationConfig `json:"attestation,omitempty"`

	// Health Exporter Configuration
	HealthExporter *HealthExporterConfig `json:"health_exporter,omitempty"`
}

// InventoryConfig holds configuration for the periodic inventory loop.
type InventoryConfig struct {
	// Enabled controls whether the periodic inventory loop runs.
	Enabled bool `json:"enabled"`

	// Interval is how often to collect and export inventory.
	Interval metav1.Duration `json:"interval"`
}

// AttestationConfig holds configuration for the periodic attestation loop.
type AttestationConfig struct {
	// Enabled controls whether the periodic attestation loop runs.
	Enabled bool `json:"enabled"`

	// Interval is how often to run attestation.
	Interval metav1.Duration `json:"interval"`
}

// HealthExporterConfig holds configuration for the health data exporter
type HealthExporterConfig struct {
	// MetricsEndpoint is the specific endpoint for sending metrics data
	MetricsEndpoint string `json:"metrics_endpoint"`

	// LogsEndpoint is the specific endpoint for sending logs/events data
	LogsEndpoint string `json:"logs_endpoint"`

	// AuthToken is the authentication token for HTTP requests
	AuthToken string `json:"auth_token,omitempty"`

	// Interval is how often to export health data
	Interval metav1.Duration `json:"interval"`

	// Timeout for HTTP requests to the global health endpoint
	Timeout metav1.Duration `json:"timeout"`

	// IncludeMetrics controls whether to include metrics data in exports
	IncludeMetrics bool `json:"include_metrics"`

	// IncludeEvents controls whether to include events data in exports
	IncludeEvents bool `json:"include_events"`

	// IncludeMachineInfo controls whether to include machine hardware info in exports
	IncludeMachineInfo bool `json:"include_machine_info"`

	// IncludeComponentData controls whether to include actual component data/numbers in exports
	IncludeComponentData bool `json:"include_component_data"`

	// MetricsLookback determines how far back to look for metrics data
	MetricsLookback metav1.Duration `json:"metrics_lookback"`

	// EventsLookback determines how far back to look for events data
	EventsLookback metav1.Duration `json:"events_lookback"`

	// HealthCheckInterval determines how often individual components perform their health checks
	// Valid range: 1 second (minimum) to 24 hours (maximum), default is 1 minute
	HealthCheckInterval metav1.Duration `json:"health_check_interval"`

	// RetryMaxAttempts is the maximum number of retry attempts for failed requests
	RetryMaxAttempts int `json:"retry_max_attempts"`

	// CollectorEndpoint is the base URL of an OTel gateway collector (e.g. http://otel-gateway:4318).
	// When set, the agent sends OTLP to {CollectorEndpoint}/v1/metrics and /v1/logs.
	// Agent enrollment remains independent, and backend credentials are not forwarded
	// to the gateway. Agent identity flows through OTLP resource attributes.
	CollectorEndpoint string `json:"collector_endpoint,omitempty"`

	// Offline mode configuration
	// OfflineMode controls whether to use offline mode (write to files instead of HTTP endpoint)
	OfflineMode bool `json:"offline_mode"`

	// OutputPath is the directory path where files will be written (required when OfflineMode is true)
	OutputPath string `json:"output_path"`

	// OutputFormat specifies the format for offline mode output files: "json" (default) or "csv"
	OutputFormat string `json:"output_format"`

	// Duration is how long to collect telemetry data in offline mode
	Duration time.Duration `json:"duration"`
}

// Validate checks if the configuration is valid
func (config *Config) Validate() error {
	// In offline mode, address is not required
	isOfflineMode := config.HealthExporter != nil && config.HealthExporter.OfflineMode
	if !isOfflineMode {
		if config.Address == "" {
			return errors.New("address is required")
		}
	}

	if config.RetentionPeriod.Duration < time.Minute {
		return fmt.Errorf("retention_period must be at least 1 minute, got %v", config.RetentionPeriod.Duration)
	}

	if err := validateLoopConfig("inventory", config.Inventory, MinInventoryInterval); err != nil {
		return err
	}
	if err := validateLoopConfig("attestation", config.Attestation, MinAttestationInterval); err != nil {
		return err
	}
	// Validate health exporter configuration if present
	if config.HealthExporter != nil {
		if endpoint := config.HealthExporter.CollectorEndpoint; endpoint != "" {
			parsed, err := url.Parse(endpoint)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return fmt.Errorf("collector_endpoint must be an absolute HTTP(S) URL")
			}
			if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return fmt.Errorf("collector_endpoint must not contain credentials, a query, or a fragment")
			}
			// url.Parse records a bare trailing "?" as ForceQuery with an empty
			// RawQuery, and drops a bare trailing "#" altogether, so neither is
			// caught above. Both corrupt the URL once "/v1/metrics" is appended.
			if parsed.ForceQuery || strings.HasSuffix(endpoint, "#") {
				return fmt.Errorf("collector_endpoint must not end with a bare '?' or '#'")
			}
		}

		// Validate health check interval
		if config.HealthExporter.HealthCheckInterval.Duration != 0 {
			if config.HealthExporter.HealthCheckInterval.Duration < time.Second {
				return fmt.Errorf("health_check_interval must be at least 1 second, got %v", config.HealthExporter.HealthCheckInterval.Duration)
			}
			if config.HealthExporter.HealthCheckInterval.Duration > 24*time.Hour {
				return fmt.Errorf("health_check_interval must be at most 24 hours, got %v", config.HealthExporter.HealthCheckInterval.Duration)
			}
		}

		// Validate offline mode configuration if present
		if config.HealthExporter.OfflineMode {
			if config.HealthExporter.OutputPath == "" {
				return errors.New("offline mode: output_path is required")
			}
			if config.HealthExporter.Duration <= 0 {
				return errors.New("offline mode: duration is required")
			}
			// Validate output format
			if config.HealthExporter.OutputFormat != "" {
				if config.HealthExporter.OutputFormat != "json" && config.HealthExporter.OutputFormat != "csv" {
					return fmt.Errorf("offline mode: output_format must be 'json' or 'csv', got '%s'", config.HealthExporter.OutputFormat)
				}
			}
		}
	}

	return nil
}

func validateLoopConfig(name string, cfg interface {
	GetEnabled() bool
	GetInterval() time.Duration
}, minInterval time.Duration) error {
	if cfg == nil || !cfg.GetEnabled() {
		return nil
	}
	if cfg.GetInterval() <= 0 {
		return fmt.Errorf("%s.interval is required when %s is enabled", name, name)
	}
	if cfg.GetInterval() < minInterval {
		return fmt.Errorf("%s.interval must be at least %v, got %v", name, minInterval, cfg.GetInterval())
	}
	return nil
}

func (c *InventoryConfig) GetEnabled() bool {
	return c != nil && c.Enabled
}

func (c *InventoryConfig) GetInterval() time.Duration {
	if c == nil {
		return 0
	}
	return c.Interval.Duration
}

func (c *AttestationConfig) GetEnabled() bool {
	return c != nil && c.Enabled
}

func (c *AttestationConfig) GetInterval() time.Duration {
	if c == nil {
		return 0
	}
	return c.Interval.Duration
}

// ShouldEnable returns true if the component should be enabled.
// If no components are specified, all components are enabled by default.
func (config *Config) ShouldEnable(componentName string) bool {
	// Not specified, enable all components
	if len(config.Components) == 0 || config.Components[0] == "*" || config.Components[0] == "all" {
		return true
	}

	if config.selectedComponents == nil {
		config.selectedComponents = make(map[string]any)

		for _, c := range config.Components {
			if c == "*" || c == "all" {
				// Enable all components
				return true
			}

			// Prefix "-" is used to disable a component
			if strings.HasPrefix(c, "-") {
				continue
			}
			config.selectedComponents[c] = struct{}{}
		}
	}

	_, shouldEnable := config.selectedComponents[componentName]
	return shouldEnable
}

// ShouldDisable returns true if the component should be disabled.
// If no disable components are specified, all components are enabled by default.
func (config *Config) ShouldDisable(componentName string) bool {
	// Not specified, enable all components (don't disable any)
	if len(config.Components) == 0 {
		return false
	}

	if config.disabledComponents == nil {
		config.disabledComponents = make(map[string]any)

		for _, c := range config.Components {
			// Skip "all" and "*" markers
			if c == "*" || c == "all" {
				continue
			}

			// Prefix "-" is used to disable a component
			if strings.HasPrefix(c, "-") {
				// Store without the "-" prefix for matching
				config.disabledComponents[strings.TrimPrefix(c, "-")] = struct{}{}
			}
		}
	}

	_, shouldDisable := config.disabledComponents[componentName]
	return shouldDisable
}

// ToConfigEntries converts the Config struct into a slice of ConfigEntry for export.
func (config *Config) ToConfigEntries(allComponentNames []string) []ConfigEntry {
	entries := []ConfigEntry{
		{Key: "api_version", Value: config.APIVersion},
		{Key: "address", Value: config.Address},
		{Key: "state", Value: config.State},
		{Key: "retention_period", Value: fmt.Sprintf("%d", int64(config.RetentionPeriod.Seconds()))},
	}

	enabled, disabled := config.getComponentLists(allComponentNames)
	enabledJSON, _ := json.Marshal(enabled)
	disabledJSON, _ := json.Marshal(disabled)
	entries = append(entries,
		ConfigEntry{Key: "enabled_components", Value: string(enabledJSON)},
		ConfigEntry{Key: "disabled_components", Value: string(disabledJSON)},
	)

	if config.HealthExporter != nil {
		entries = append(entries, extractHealthExporterEntries(config.HealthExporter)...)
	}
	return entries
}

// InventoryAgentConfig returns the useful, non-sensitive subset of agent config that should be
// persisted with inventory rather than exported through telemetry.
func (config *Config) InventoryAgentConfig(allComponentNames []string) (retentionPeriodSeconds int64, enabled, disabled []string) {
	if config == nil {
		return 0, nil, nil
	}

	enabled, disabled = config.getComponentLists(allComponentNames)
	return int64(config.RetentionPeriod.Seconds()), enabled, disabled
}

func (config *Config) InventoryLoopAgentConfig() (enabled bool, intervalSeconds int64) {
	if config == nil || config.Inventory == nil {
		return false, 0
	}
	return config.Inventory.Enabled, int64(config.Inventory.Interval.Seconds())
}

func (config *Config) AttestationLoopAgentConfig() (enabled bool, intervalSeconds int64) {
	if config == nil || config.Attestation == nil {
		return false, 0
	}
	return config.Attestation.Enabled, int64(config.Attestation.Interval.Seconds())
}

// HealthCheckInterval returns the resolved component health-check interval.
func (config *Config) HealthCheckInterval() time.Duration {
	if config == nil || config.HealthExporter == nil || config.HealthExporter.HealthCheckInterval.Duration <= 0 {
		return time.Minute
	}
	return config.HealthExporter.HealthCheckInterval.Duration
}

// MetricScrapeIntervalSeconds returns the resolved metric scrape interval stored in inventory.
func (config *Config) MetricScrapeIntervalSeconds() int64 {
	return int64(config.HealthCheckInterval().Seconds())
}

// getComponentLists computes enabled/disabled lists from config rules against all available components.
func (config *Config) getComponentLists(allComponentNames []string) (enabled, disabled []string) {
	enabled, disabled = []string{}, []string{}

	disabledSet := make(map[string]bool)
	for _, c := range config.Components {
		if strings.HasPrefix(c, "-") {
			disabledSet[strings.TrimPrefix(c, "-")] = true
		}
	}

	allMode := len(config.Components) == 0 ||
		(len(config.Components) > 0 && (config.Components[0] == "*" || config.Components[0] == "all"))

	if allMode {
		for _, name := range allComponentNames {
			if disabledSet[name] {
				disabled = append(disabled, name)
			} else {
				enabled = append(enabled, name)
			}
		}
		return
	}

	enabledSet := make(map[string]bool)
	for _, c := range config.Components {
		if !strings.HasPrefix(c, "-") && c != "*" && c != "all" {
			enabledSet[c] = true
		}
	}
	for _, name := range allComponentNames {
		if disabledSet[name] {
			disabled = append(disabled, name)
		} else if enabledSet[name] {
			enabled = append(enabled, name)
		} else {
			disabled = append(disabled, name)
		}
	}
	return
}

// extractHealthExporterEntries uses reflection to extract config entries from HealthExporterConfig.
func extractHealthExporterEntries(cfg *HealthExporterConfig) []ConfigEntry {
	var entries []ConfigEntry
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		key := strings.Split(jsonTag, ",")[0]
		// Skip sensitive/enrollment-assigned fields
		if key == "auth_token" || key == "metrics_endpoint" || key == "logs_endpoint" ||
			key == "collector_endpoint" {
			continue
		}

		var strValue string
		switch value.Kind() {
		case reflect.String:
			strValue = value.String()
		case reflect.Bool:
			strValue = fmt.Sprintf("%t", value.Bool())
		case reflect.Int, reflect.Int64:
			// Check if it's a time.Duration (which is int64 under the hood)
			if d, ok := value.Interface().(time.Duration); ok {
				strValue = fmt.Sprintf("%d", int64(d.Seconds()))
			} else {
				strValue = fmt.Sprintf("%d", value.Int())
			}
		case reflect.Struct:
			if duration, ok := value.Interface().(metav1.Duration); ok {
				strValue = fmt.Sprintf("%d", int64(duration.Seconds()))
			} else if d, ok := value.Interface().(time.Duration); ok {
				strValue = fmt.Sprintf("%d", int64(d.Seconds()))
			}
		default:
			strValue = fmt.Sprintf("%v", value.Interface())
		}

		entries = append(entries, ConfigEntry{Key: "health_exporter." + key, Value: strValue})
	}
	return entries
}
