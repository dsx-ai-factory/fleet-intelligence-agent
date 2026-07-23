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

// Package converter handles conversion of health data to different formats
package converter

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/exporter/collector"
)

var osHostname = os.Hostname

// OTLPData holds both metrics and logs for OTLP export
type OTLPData struct {
	Metrics *metricsv1.MetricsData
	Logs    *logsv1.LogsData
}

// OTLPConverter defines the interface for converting health data to OTLP format
type OTLPConverter interface {
	Convert(data *collector.HealthData) *OTLPData
}

// otlpConverter implements the OTLPConverter interface
type otlpConverter struct{}

// NewOTLPConverter creates a new OTLP converter
func NewOTLPConverter() OTLPConverter {
	return &otlpConverter{}
}

// Convert converts HealthData to OTLP metrics and logs format
func (c *otlpConverter) Convert(data *collector.HealthData) *OTLPData {
	// Create shared resource for both metrics and logs
	resource := c.createOTLPResource(data)

	// Convert metrics
	metricsData := &metricsv1.MetricsData{
		ResourceMetrics: []*metricsv1.ResourceMetrics{
			{
				Resource: resource,
				ScopeMetrics: []*metricsv1.ScopeMetrics{
					{
						Scope: &commonv1.InstrumentationScope{
							Name:    "fleetint-exporter",
							Version: "1.0.0",
						},
						Metrics: c.convertMetricsToOTLP(data),
					},
				},
			},
		},
	}

	// Convert logs (events + component data)
	logsData := &logsv1.LogsData{
		ResourceLogs: []*logsv1.ResourceLogs{
			{
				Resource: resource,
				ScopeLogs: []*logsv1.ScopeLogs{
					{
						Scope: &commonv1.InstrumentationScope{
							Name:    "fleetint-exporter",
							Version: "1.0.0",
						},
						LogRecords: c.convertToOTLPLogs(data),
					},
				},
			},
		},
	}

	return &OTLPData{
		Metrics: metricsData,
		Logs:    logsData,
	}
}

// createOTLPResource creates a minimal OTLP resource for telemetry correlation.
func (c *otlpConverter) createOTLPResource(data *collector.HealthData) *resourcev1.Resource {
	attributes := []*commonv1.KeyValue{
		{
			Key: "service.name",
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: "fleet-intelligence-agent"},
			},
		},
		{
			Key: "machine.id",
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: data.MachineID},
			},
		},
	}

	if hostname := resolveOTLPHostname(); hostname != "" {
		attributes = append(attributes, &commonv1.KeyValue{
			Key: "host.name",
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: hostname},
			},
		})
	}
	if data.NodeGroup != "" {
		attributes = append(attributes, &commonv1.KeyValue{
			Key: "node_group",
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: data.NodeGroup},
			},
		})
	}
	if data.ComputeZone != "" {
		attributes = append(attributes, &commonv1.KeyValue{
			Key: "compute_zone",
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: data.ComputeZone},
			},
		})
	}

	if data.MachineInfo != nil && data.MachineInfo.GPUInfo != nil && len(data.MachineInfo.GPUInfo.GPUs) > 0 {
		if gpus, err := json.Marshal(data.MachineInfo.GPUInfo.GPUs); err == nil {
			attributes = append(attributes, &commonv1.KeyValue{
				Key: "gpuInfo.gpus",
				Value: &commonv1.AnyValue{
					Value: &commonv1.AnyValue_StringValue{StringValue: string(gpus)},
				},
			})
		}
	}

	return &resourcev1.Resource{
		Attributes: attributes,
	}
}

func resolveOTLPHostname() string {
	if hostname := strings.TrimSpace(os.Getenv("HOSTNAME")); hostname != "" {
		return hostname
	}
	hostname, err := osHostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(hostname)
}

// convertMetricsToOTLP converts health metrics to OTLP metrics format
func (c *otlpConverter) convertMetricsToOTLP(data *collector.HealthData) []*metricsv1.Metric {
	var otlpMetrics []*metricsv1.Metric

	gpuUUIDToIndex := buildGPUUUIDToIndexMap(data)

	// Convert regular metrics if available
	if len(data.Metrics) > 0 {
		for _, metric := range data.Metrics {
			otlpMetrics = append(otlpMetrics, c.convertMetricToOTLP(metric, gpuUUIDToIndex))
		}
	}

	// Add a summary metric with collection info
	summaryMetric := &metricsv1.Metric{
		Name:        "fleetint_agent_collection_summary",
		Description: "Summary of Fleet Intelligence data collection including counts of metrics, events, and components",
		Data: &metricsv1.Metric_Gauge{
			Gauge: &metricsv1.Gauge{
				DataPoints: []*metricsv1.NumberDataPoint{
					{
						TimeUnixNano: uint64(data.Timestamp.UnixNano()),
						Value: &metricsv1.NumberDataPoint_AsInt{
							AsInt: 1,
						},
						Attributes: []*commonv1.KeyValue{
							{
								Key: "metrics_count",
								Value: &commonv1.AnyValue{
									Value: &commonv1.AnyValue_IntValue{IntValue: int64(len(data.Metrics))},
								},
							},
							{
								Key: "events_count",
								Value: &commonv1.AnyValue{
									Value: &commonv1.AnyValue_IntValue{IntValue: int64(len(data.Events))},
								},
							},
							{
								Key: "component_data_count",
								Value: &commonv1.AnyValue{
									Value: &commonv1.AnyValue_IntValue{IntValue: int64(len(data.ComponentData))},
								},
							},
						},
					},
				},
			},
		},
	}
	otlpMetrics = append(otlpMetrics, summaryMetric)

	upMetric := &metricsv1.Metric{
		Name:        "fleetint_agent_up",
		Description: "Fleet Intelligence agent liveness. A value of 1 indicates the agent was running when telemetry was exported.",
		Data: &metricsv1.Metric_Gauge{
			Gauge: &metricsv1.Gauge{
				DataPoints: []*metricsv1.NumberDataPoint{
					{
						TimeUnixNano: uint64(data.Timestamp.UnixNano()),
						Value: &metricsv1.NumberDataPoint_AsInt{
							AsInt: 1,
						},
					},
				},
			},
		},
	}
	otlpMetrics = append(otlpMetrics, upMetric)

	return otlpMetrics
}

func (c *otlpConverter) convertMetricToOTLP(metric pkgmetrics.Metric, gpuUUIDToIndex map[string]string) *metricsv1.Metric {
	dataPoint := &metricsv1.NumberDataPoint{
		TimeUnixNano: uint64(metric.UnixMilliseconds) * 1_000_000,
		Value: &metricsv1.NumberDataPoint_AsDouble{
			AsDouble: metric.Value,
		},
		Attributes: c.convertLabelsToOTLPAttributes(metric.Labels, gpuUUIDToIndex),
	}

	otlpMetric := &metricsv1.Metric{
		Name:        metric.Name,
		Description: fmt.Sprintf("Metric from component %s", metric.Component),
	}

	if metric.Type == pkgmetrics.MetricTypeCounter {
		otlpMetric.Data = &metricsv1.Metric_Sum{
			Sum: &metricsv1.Sum{
				AggregationTemporality: metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
				IsMonotonic:            true,
				DataPoints:             []*metricsv1.NumberDataPoint{dataPoint},
			},
		}
		return otlpMetric
	}

	otlpMetric.Data = &metricsv1.Metric_Gauge{
		Gauge: &metricsv1.Gauge{
			DataPoints: []*metricsv1.NumberDataPoint{dataPoint},
		},
	}
	return otlpMetric
}

// convertLabelsToOTLPAttributes converts metric labels to OTLP attributes.
// If the labels contain a "uuid" key but no "gpu" key, it enriches the
// attributes with the GPU index looked up from the machine info mapping.
// DCGM metrics already carry a "gpu" label and are left unchanged.
func (c *otlpConverter) convertLabelsToOTLPAttributes(labels map[string]string, gpuUUIDToIndex map[string]string) []*commonv1.KeyValue {
	var attributes []*commonv1.KeyValue
	for key, value := range labels {
		attributes = append(attributes, &commonv1.KeyValue{
			Key: key,
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: value},
			},
		})
	}

	if _, hasGPU := labels["gpu"]; !hasGPU {
		if uuid, hasUUID := labels["uuid"]; hasUUID {
			if gpuIndex, ok := gpuUUIDToIndex[uuid]; ok {
				attributes = append(attributes, &commonv1.KeyValue{
					Key: "gpu",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: gpuIndex},
					},
				})
			}
		}
	}

	return attributes
}

// buildGPUUUIDToIndexMap builds a UUID → GPU index lookup from the collector snapshot.
func buildGPUUUIDToIndexMap(data *collector.HealthData) map[string]string {
	m := make(map[string]string)
	if len(data.GPUUUIDToIndex) == 0 {
		return m
	}
	for uuid, gpuIndex := range data.GPUUUIDToIndex {
		if uuid == "" || gpuIndex == "" {
			continue
		}
		m[uuid] = gpuIndex
	}
	return m
}

// convertToOTLPLogs converts HealthData events and component data to OTLP log records
func (c *otlpConverter) convertToOTLPLogs(data *collector.HealthData) []*logsv1.LogRecord {
	var logRecords []*logsv1.LogRecord

	// Add events as log records
	if len(data.Events) > 0 {
		for _, event := range data.Events {
			attributes := []*commonv1.KeyValue{
				{
					Key: "component",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: event.Component},
					},
				},
				{
					Key: "event_id",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: event.EventID},
					},
				},
				{
					Key: "event_name",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: event.Name},
					},
				},
				{
					Key: "event_type",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: event.Type},
					},
				},
				{
					Key: "log_type",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: "event"},
					},
				},
			}
			extraInfo := event.ExtraInfo
			if extraInfo == nil {
				extraInfo = map[string]string{}
			}
			attributes = append(attributes, &commonv1.KeyValue{
				Key:   "extra_info",
				Value: extraInfoToAnyValue(extraInfo),
			})

			logRecord := &logsv1.LogRecord{
				TimeUnixNano:   uint64(event.Time.UnixNano()),
				SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_INFO,
				SeverityText:   "INFO",
				Body: &commonv1.AnyValue{
					Value: &commonv1.AnyValue_StringValue{
						StringValue: fmt.Sprintf("[%s] %s: %s", event.Type, event.Component, event.Message),
					},
				},
				Attributes: attributes,
			}
			logRecords = append(logRecords, logRecord)
		}
	}

	// Add component data as log records
	if len(data.ComponentData) > 0 {
		for componentName, componentResult := range data.ComponentData {
			componentInfo, ok := componentResult.(map[string]interface{})
			if !ok {
				continue
			}

			health := componentInfo["health"]
			reason := componentInfo["reason"]
			errorMsg := componentInfo["error"]
			timeVal := componentInfo["time"]
			extraInfo := componentInfo["extra_info"]
			suggestedActions := componentInfo["suggested_actions"]
			incidents := componentInfo["incidents"]

			attributes := []*commonv1.KeyValue{
				{
					Key: "component",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: componentName},
					},
				},
				{
					Key: "log_type",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: "component_data"},
					},
				},
				{
					Key: "health",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: fmt.Sprintf("%v", health)},
					},
				},
				{
					Key: "reason",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: fmt.Sprintf("%v", reason)},
					},
				},
			}

			// Add optional fields
			if errStr, ok := errorMsg.(string); ok && errStr != "" {
				attributes = append(attributes, &commonv1.KeyValue{
					Key: "error",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: errStr},
					},
				})
			}

			if timeVal != nil {
				attributes = append(attributes, &commonv1.KeyValue{
					Key: "time",
					Value: &commonv1.AnyValue{
						Value: &commonv1.AnyValue_StringValue{StringValue: fmt.Sprintf("%v", timeVal)},
					},
				})
			}

			if extraInfo != nil {
				jsonBytes, err := json.Marshal(extraInfo)
				if err == nil {
					attributes = append(attributes, &commonv1.KeyValue{
						Key: "extra_info",
						Value: &commonv1.AnyValue{
							Value: &commonv1.AnyValue_StringValue{StringValue: string(jsonBytes)},
						},
					})
				}
			}

			if suggestedActions != nil {
				jsonBytes, err := json.Marshal(suggestedActions)
				if err == nil && string(jsonBytes) != "null" {
					attributes = append(attributes, &commonv1.KeyValue{
						Key: "suggested_actions",
						Value: &commonv1.AnyValue{
							Value: &commonv1.AnyValue_StringValue{StringValue: string(jsonBytes)},
						},
					})
				}
			}

			if typedIncidents, ok := toHealthStateIncidents(incidents); ok && len(typedIncidents) > 0 {
				attributes = append(attributes, &commonv1.KeyValue{
					Key:   "incidents",
					Value: incidentsToOTLPArrayValue(typedIncidents),
				})
			}

			logRecord := &logsv1.LogRecord{
				TimeUnixNano:   uint64(data.Timestamp.UnixNano()),
				SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_INFO,
				SeverityText:   "INFO",
				Body: &commonv1.AnyValue{
					Value: &commonv1.AnyValue_StringValue{
						StringValue: fmt.Sprintf("Component [%s]: %v - %v", componentName, health, reason),
					},
				},
				Attributes: attributes,
			}
			logRecords = append(logRecords, logRecord)
		}
	}

	return logRecords
}

func extraInfoToAnyValue(extraInfo map[string]string) *commonv1.AnyValue {
	values := make([]*commonv1.KeyValue, 0, len(extraInfo))
	for key, raw := range extraInfo {
		values = append(values, &commonv1.KeyValue{
			Key:   key,
			Value: stringToStructuredAnyValue(raw),
		})
	}

	return &commonv1.AnyValue{
		Value: &commonv1.AnyValue_KvlistValue{
			KvlistValue: &commonv1.KeyValueList{Values: values},
		},
	}
}

func incidentsToOTLPArrayValue(incidents []apiv1.HealthStateIncident) *commonv1.AnyValue {
	values := make([]*commonv1.AnyValue, 0, len(incidents))
	for _, inc := range incidents {
		kvs := []*commonv1.KeyValue{
			{Key: "entity_id", Value: stringAnyValue(inc.EntityID)},
			{Key: "message", Value: stringAnyValue(inc.Message)},
			{Key: "severity", Value: stringAnyValue(string(inc.Health))},
			{Key: "error", Value: stringAnyValue(inc.Error)},
		}
		values = append(values, &commonv1.AnyValue{
			Value: &commonv1.AnyValue_KvlistValue{
				KvlistValue: &commonv1.KeyValueList{Values: kvs},
			},
		})
	}

	return &commonv1.AnyValue{
		Value: &commonv1.AnyValue_ArrayValue{
			ArrayValue: &commonv1.ArrayValue{Values: values},
		},
	}
}

func toHealthStateIncidents(v interface{}) ([]apiv1.HealthStateIncident, bool) {
	switch typed := v.(type) {
	case []apiv1.HealthStateIncident:
		return typed, true
	case apiv1.HealthStateIncident:
		return []apiv1.HealthStateIncident{typed}, true
	default:
		return nil, false
	}
}

func stringAnyValue(v string) *commonv1.AnyValue {
	return &commonv1.AnyValue{
		Value: &commonv1.AnyValue_StringValue{StringValue: v},
	}
}

func stringToStructuredAnyValue(raw string) *commonv1.AnyValue {
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil || decoded == nil {
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_StringValue{StringValue: raw},
		}
	}

	return jsonValueToAnyValue(decoded)
}

func jsonValueToAnyValue(v any) *commonv1.AnyValue {
	switch value := v.(type) {
	case map[string]any:
		values := make([]*commonv1.KeyValue, 0, len(value))
		for key, nested := range value {
			values = append(values, &commonv1.KeyValue{
				Key:   key,
				Value: jsonValueToAnyValue(nested),
			})
		}
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_KvlistValue{
				KvlistValue: &commonv1.KeyValueList{Values: values},
			},
		}
	case []any:
		values := make([]*commonv1.AnyValue, 0, len(value))
		for _, nested := range value {
			values = append(values, jsonValueToAnyValue(nested))
		}
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_ArrayValue{
				ArrayValue: &commonv1.ArrayValue{Values: values},
			},
		}
	case bool:
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_BoolValue{BoolValue: value},
		}
	case float64:
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_DoubleValue{DoubleValue: value},
		}
	case string:
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_StringValue{StringValue: value},
		}
	case nil:
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_StringValue{StringValue: "null"},
		}
	default:
		return &commonv1.AnyValue{
			Value: &commonv1.AnyValue_StringValue{StringValue: fmt.Sprintf("%v", value)},
		}
	}
}

// convertStructToOTLPAttributes converts a struct to OTLP attributes using reflection
func convertStructToOTLPAttributes(v interface{}) []*commonv1.KeyValue {
	return convertStructToOTLPAttributesWithPrefix(v, "")
}

// convertStructToOTLPAttributesWithPrefix converts a struct to OTLP attributes with a key prefix
func convertStructToOTLPAttributesWithPrefix(v interface{}, prefix string) []*commonv1.KeyValue {
	var attributes []*commonv1.KeyValue

	if v == nil {
		return attributes
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return attributes
		}
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return attributes
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// Skip unexported fields
		if !field.CanInterface() {
			continue
		}

		// Get JSON tag for field name, fall back to field name
		jsonTag := fieldType.Tag.Get("json")
		fieldName := fieldType.Name
		if jsonTag != "" && jsonTag != "-" {
			if commaIdx := strings.Index(jsonTag, ","); commaIdx != -1 {
				fieldName = jsonTag[:commaIdx]
			} else {
				fieldName = jsonTag
			}
		}

		// Add prefix if provided
		fullFieldName := fieldName
		if prefix != "" {
			fullFieldName = prefix + "." + fieldName
		}

		// Convert field value to string if it's not empty/nil
		var stringValue string
		switch field.Kind() {
		case reflect.String:
			stringValue = field.String()
		case reflect.Bool:
			stringValue = fmt.Sprintf("%t", field.Bool()) // Always include bool (even false)
		case reflect.Int, reflect.Int32, reflect.Int64:
			stringValue = fmt.Sprintf("%d", field.Int()) // Always include int (even 0)
		case reflect.Uint, reflect.Uint32, reflect.Uint64:
			stringValue = fmt.Sprintf("%d", field.Uint()) // Always include uint (even 0)
		case reflect.Struct:
			// Handle time.Time specially
			if field.Type().String() == "time.Time" {
				if timeVal, ok := field.Interface().(time.Time); ok && !timeVal.IsZero() {
					stringValue = timeVal.Format(time.RFC3339)
				}
			} else {
				// Recursively process nested structs
				nestedAttributes := convertStructToOTLPAttributesWithPrefix(field.Interface(), fullFieldName)
				attributes = append(attributes, nestedAttributes...)
				continue
			}
		case reflect.Pointer:
			// Handle pointer fields by dereferencing and processing recursively
			if !field.IsNil() {
				nestedAttributes := convertStructToOTLPAttributesWithPrefix(field.Interface(), fullFieldName)
				attributes = append(attributes, nestedAttributes...)
			}
			continue
		case reflect.Slice:
			// Handle slices by converting to JSON string
			if field.Len() > 0 {
				if jsonBytes, err := json.Marshal(field.Interface()); err == nil {
					stringValue = string(jsonBytes)
				}
			}
		default:
			// Skip other types
			continue
		}

		// Only add non-empty values
		if stringValue != "" {
			attributes = append(attributes, &commonv1.KeyValue{
				Key: fullFieldName,
				Value: &commonv1.AnyValue{
					Value: &commonv1.AnyValue_StringValue{StringValue: stringValue},
				},
			})
		}
	}

	return attributes
}
