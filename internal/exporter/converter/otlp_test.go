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

package converter

import (
	"errors"
	"os"
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

func TestNewOTLPConverter(t *testing.T) {
	converter := NewOTLPConverter()
	assert.NotNil(t, converter)
}

func TestOTLPConverter_Convert_EmptyData(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	assert.NotNil(t, otlpData.Metrics)
	assert.NotNil(t, otlpData.Logs)

	// Should have resource metrics even with empty data
	assert.Len(t, otlpData.Metrics.ResourceMetrics, 1)
	// Should have resource logs even with empty data
	assert.Len(t, otlpData.Logs.ResourceLogs, 1)
}

func TestOTLPConverter_Convert_WithMetrics(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Metrics: metrics.Metrics{
			{
				Component:        "gpu",
				Name:             "temperature",
				UnixMilliseconds: 1699200000000,
				Value:            65.5,
				Labels:           map[string]string{"gpu_id": "0"},
			},
			{
				Component:        "cpu",
				Name:             "usage",
				UnixMilliseconds: 1699200001000,
				Value:            75.0,
				Labels:           map[string]string{"core": "0"},
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Metrics)
	require.Len(t, otlpData.Metrics.ResourceMetrics, 1)

	rm := otlpData.Metrics.ResourceMetrics[0]
	require.Len(t, rm.ScopeMetrics, 1)

	// Should have 2 source metrics plus generated agent metrics.
	metrics := rm.ScopeMetrics[0].Metrics
	assert.GreaterOrEqual(t, len(metrics), 2)

	// Verify first metric
	assert.Equal(t, "temperature", metrics[0].Name)
	assert.Contains(t, metrics[0].Description, "gpu")
}

func TestOTLPConverter_Convert_CounterMetricsBecomeCumulativeSums(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Metrics: metrics.Metrics{
			{
				Component:        "gpu",
				Name:             "dcgm_fi_dev_pcie_replay_counter",
				Type:             metrics.MetricTypeCounter,
				UnixMilliseconds: 1699200000000,
				Value:            42,
				Labels:           map[string]string{"uuid": "GPU-0", "gpu": "0"},
			},
			{
				Component:        "gpu",
				Name:             "dcgm_fi_dev_gpu_temp",
				Type:             metrics.MetricTypeGauge,
				UnixMilliseconds: 1699200001000,
				Value:            65,
				Labels:           map[string]string{"uuid": "GPU-0", "gpu": "0"},
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	convertedMetrics := otlpData.Metrics.ResourceMetrics[0].ScopeMetrics[0].Metrics
	counterMetric := findOTLPMetric(convertedMetrics, "dcgm_fi_dev_pcie_replay_counter")
	require.NotNil(t, counterMetric)
	assert.Empty(t, counterMetric.Unit)
	sum := counterMetric.GetSum()
	require.NotNil(t, sum)
	assert.True(t, sum.IsMonotonic)
	assert.Equal(t, metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, sum.AggregationTemporality)
	require.Len(t, sum.DataPoints, 1)
	assert.Equal(t, 42.0, sum.DataPoints[0].GetAsDouble())

	gaugeMetric := findOTLPMetric(convertedMetrics, "dcgm_fi_dev_gpu_temp")
	require.NotNil(t, gaugeMetric)
	assert.Empty(t, gaugeMetric.Unit)
	gauge := gaugeMetric.GetGauge()
	require.NotNil(t, gauge)
	require.Len(t, gauge.DataPoints, 1)
	assert.Equal(t, 65.0, gauge.DataPoints[0].GetAsDouble())
}

func TestOTLPConverter_Convert_WithEvents(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Events: eventstore.Events{
			{
				EventID:   "123e4567-e89b-12d3-a456-426614174000",
				Time:      time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC),
				Component: "gpu",
				Name:      "temperature_warning",
				Type:      "warning",
				Message:   "GPU temperature high",
				ExtraInfo: map[string]string{
					"xid": "79",
				},
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Logs)
	require.Len(t, otlpData.Logs.ResourceLogs, 1)

	rl := otlpData.Logs.ResourceLogs[0]
	require.Len(t, rl.ScopeLogs, 1)

	// Should have at least 1 log record
	logs := rl.ScopeLogs[0].LogRecords
	assert.GreaterOrEqual(t, len(logs), 1)

	// Verify log record contains event information
	logRecord := logs[0]
	body := logRecord.Body.GetStringValue()
	assert.Contains(t, body, "gpu")
	assert.Equal(t, "123e4567-e89b-12d3-a456-426614174000", findAttribute(t, logs[0].Attributes, "event_id").GetStringValue())
	// Body should contain either the event name or message
	assert.True(t, contains(body, "temperature_warning") || contains(body, "GPU temperature high"),
		"Log should contain event name or message")

	extraInfo := findAttribute(t, logs[0].Attributes, "extra_info").GetKvlistValue()
	require.NotNil(t, extraInfo, "event log should include structured extra_info attribute")
	assert.Equal(t, float64(79), findMapValue(t, extraInfo.Values, "xid").GetDoubleValue())
}

func TestOTLPConverter_Convert_WithEvents_EmptyExtraInfo(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Events: eventstore.Events{
			{
				Time:      time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC),
				Component: "gpu",
				Name:      "temperature_warning",
				Type:      "warning",
				Message:   "GPU temperature high",
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Logs)
	require.Len(t, otlpData.Logs.ResourceLogs, 1)

	logs := otlpData.Logs.ResourceLogs[0].ScopeLogs[0].LogRecords
	require.GreaterOrEqual(t, len(logs), 1)

	extraInfo := findAttribute(t, logs[0].Attributes, "extra_info").GetKvlistValue()
	require.NotNil(t, extraInfo, "event log should always include extra_info")
	assert.Empty(t, extraInfo.Values, "event log should export empty extra_info as an empty OTLP map")
}

func TestOTLPConverter_Convert_WithEvents_StructuredExtraInfo(t *testing.T) {
	rawData := `{"time":"2026-02-20T23:22:44Z","data_source":"kmsg","xid":149}`
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Events: eventstore.Events{
			{
				Time:      time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC),
				Component: "accelerator-nvidia-error-xid",
				Name:      "error_xid",
				Type:      "Fatal",
				Message:   "XID 149 NETIR",
				ExtraInfo: map[string]string{
					"data":        rawData,
					"device_uuid": "PCI:0000:04:00",
				},
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Logs)
	require.Len(t, otlpData.Logs.ResourceLogs, 1)

	logs := otlpData.Logs.ResourceLogs[0].ScopeLogs[0].LogRecords
	require.GreaterOrEqual(t, len(logs), 1)

	extraInfo := findAttribute(t, logs[0].Attributes, "extra_info").GetKvlistValue()
	require.NotNil(t, extraInfo)
	assert.Equal(t, "PCI:0000:04:00", findMapValue(t, extraInfo.Values, "device_uuid").GetStringValue())

	dataValue := findMapValue(t, extraInfo.Values, "data").GetKvlistValue()
	require.NotNil(t, dataValue)
	assert.Equal(t, "2026-02-20T23:22:44Z", findMapValue(t, dataValue.Values, "time").GetStringValue())
	assert.Equal(t, "kmsg", findMapValue(t, dataValue.Values, "data_source").GetStringValue())
	assert.Equal(t, float64(149), findMapValue(t, dataValue.Values, "xid").GetDoubleValue())
}

func TestOTLPConverter_Convert_WithEvents_InvalidExtraInfoRemainsString(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Events: eventstore.Events{
			{
				Time:      time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC),
				Component: "gpu",
				Name:      "temperature_warning",
				Type:      "warning",
				Message:   "GPU temperature high",
				ExtraInfo: map[string]string{
					"data": "{invalid",
				},
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	logs := otlpData.Logs.ResourceLogs[0].ScopeLogs[0].LogRecords
	require.GreaterOrEqual(t, len(logs), 1)

	extraInfo := findAttribute(t, logs[0].Attributes, "extra_info").GetKvlistValue()
	require.NotNil(t, extraInfo)
	assert.Equal(t, "{invalid", findMapValue(t, extraInfo.Values, "data").GetStringValue())
}

func TestOTLPConverter_Convert_WithComponentData(t *testing.T) {
	rawData := `{"time":"2026-02-20T23:22:44Z","data_source":"kmsg","xid":149}`
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		ComponentData: map[string]interface{}{
			"gpu": map[string]any{
				"time":           metav1.Time{Time: time.Now()},
				"component_name": "gpu",
				"health":         "Unhealthy",
				"reason":         "failed to get recent events",
				"error":          "database is locked",
				"extra_info": map[string]any{
					"device_uuid": "PCI:0000:04:00",
					"data":        rawData,
				},
				"suggested_actions": &apiv1.SuggestedActions{
					Description:   "reboot the node",
					RepairActions: []apiv1.RepairActionType{apiv1.RepairActionTypeRebootSystem},
				},
				"incidents": []apiv1.HealthStateIncident{
					{
						EntityID: "GPU-1234",
						Message:  "Clock throttled",
						Health:   apiv1.HealthStateTypeDegraded,
						Error:    "DCGM_FR_CLOCK_THROTTLE_POWER",
					},
				},
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Logs)

	rl := otlpData.Logs.ResourceLogs[0]
	logs := rl.ScopeLogs[0].LogRecords

	// Should have at least 1 log for component data
	assert.GreaterOrEqual(t, len(logs), 1)

	// Find component data log
	found := false
	for _, log := range logs {
		if contains(log.Body.GetStringValue(), "gpu") && contains(log.Body.GetStringValue(), "Unhealthy") {
			assert.Equal(t, "database is locked", findAttribute(t, log.Attributes, "error").GetStringValue())

			extraInfo := findAttribute(t, log.Attributes, "extra_info").GetStringValue()
			require.NotEmpty(t, extraInfo)
			assert.Contains(t, extraInfo, `"device_uuid":"PCI:0000:04:00"`)
			assert.Contains(t, extraInfo, `"data":"{\"time\":\"2026-02-20T23:22:44Z\",\"data_source\":\"kmsg\",\"xid\":149}"`)

			suggestedActions := findAttribute(t, log.Attributes, "suggested_actions").GetStringValue()
			require.NotEmpty(t, suggestedActions)
			assert.Contains(t, suggestedActions, `"description":"reboot the node"`)
			assert.Contains(t, suggestedActions, `"REBOOT_SYSTEM"`)

			incidents := findAttribute(t, log.Attributes, "incidents").GetArrayValue()
			require.NotNil(t, incidents)
			require.Len(t, incidents.Values, 1)
			incident := incidents.Values[0].GetKvlistValue()
			require.NotNil(t, incident)
			assert.Equal(t, "GPU-1234", findMapValue(t, incident.Values, "entity_id").GetStringValue())
			assert.Equal(t, "Clock throttled", findMapValue(t, incident.Values, "message").GetStringValue())
			assert.Equal(t, "Degraded", findMapValue(t, incident.Values, "severity").GetStringValue())
			assert.Equal(t, "DCGM_FR_CLOCK_THROTTLE_POWER", findMapValue(t, incident.Values, "error").GetStringValue())
			found = true
			break
		}
	}
	assert.True(t, found, "Should find component data log")
}

func TestOTLPConverter_Convert_IgnoresMachineInfoInResource(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		MachineInfo: &machineinfo.MachineInfo{
			DCGMVersion: "4.2.3",
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Metrics)

	rm := otlpData.Metrics.ResourceMetrics[0]
	assert.NotNil(t, rm.Resource)
	assert.Greater(t, len(rm.Resource.Attributes), 0)

	for _, attr := range rm.Resource.Attributes {
		assert.NotEqual(t, "dcgmVersion", attr.Key)
	}
}

func TestOTLPConverter_ConvertStructToOTLPAttributes(t *testing.T) {
	type TestStruct struct {
		StringField string
		IntField    int
		BoolField   bool
		TimeField   time.Time
		FloatField  float64
	}

	testData := TestStruct{
		StringField: "test-value",
		IntField:    42,
		BoolField:   true,
		TimeField:   time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC),
		FloatField:  3.14,
	}

	attrs := convertStructToOTLPAttributes(testData)

	assert.Greater(t, len(attrs), 0)

	// Find and verify attributes
	foundString := false
	foundInt := false
	foundBool := false
	foundTime := false

	for _, attr := range attrs {
		switch attr.Key {
		case "StringField":
			foundString = true
			assert.Equal(t, "test-value", attr.Value.GetStringValue())
		case "IntField":
			foundInt = true
		case "BoolField":
			foundBool = true
			assert.Equal(t, "true", attr.Value.GetStringValue())
		case "TimeField":
			foundTime = true
			assert.Contains(t, attr.Value.GetStringValue(), "2025-11-05")
		}
	}

	assert.True(t, foundString, "Should have string field")
	assert.True(t, foundInt, "Should have int field")
	assert.True(t, foundBool, "Should have bool field")
	assert.True(t, foundTime, "Should have time field")
}

func TestOTLPConverter_ConvertStructToOTLPAttributesWithPrefix(t *testing.T) {
	type NestedStruct struct {
		Name  string
		Value int
	}

	nested := NestedStruct{
		Name:  "nested",
		Value: 100,
	}

	attrs := convertStructToOTLPAttributesWithPrefix(nested, "prefix")

	assert.Greater(t, len(attrs), 0)

	// All keys should have prefix
	for _, attr := range attrs {
		assert.Contains(t, attr.Key, "prefix.")
	}
}

func TestOTLPConverter_ConvertStructToOTLPAttributes_NilStruct(t *testing.T) {
	var nilStruct *struct{}
	attrs := convertStructToOTLPAttributes(nilStruct)
	assert.Empty(t, attrs)
}

func TestOTLPConverter_ConvertStructToOTLPAttributes_NestedStruct(t *testing.T) {
	type Nested struct {
		Field1 string
		Field2 int
	}

	type Parent struct {
		Name   string
		Nested Nested
	}

	parent := Parent{
		Name: "parent",
		Nested: Nested{
			Field1: "nested-value",
			Field2: 42,
		},
	}

	attrs := convertStructToOTLPAttributes(parent)

	assert.Greater(t, len(attrs), 0)

	// Should have nested attributes with prefix
	foundNestedField := false
	for _, attr := range attrs {
		if contains(attr.Key, "Nested.Field1") {
			foundNestedField = true
			assert.Equal(t, "nested-value", attr.Value.GetStringValue())
		}
	}
	assert.True(t, foundNestedField, "Should have nested struct attributes")
}

func TestOTLPConverter_ConvertStructToOTLPAttributes_SliceField(t *testing.T) {
	type StructWithSlice struct {
		Name  string
		Items []string
	}

	data := StructWithSlice{
		Name:  "test",
		Items: []string{"item1", "item2", "item3"},
	}

	attrs := convertStructToOTLPAttributes(data)

	assert.Greater(t, len(attrs), 0)

	// Should have items as JSON string
	foundSlice := false
	for _, attr := range attrs {
		if attr.Key == "Items" {
			foundSlice = true
			// Should be JSON array
			assert.Contains(t, attr.Value.GetStringValue(), "item1")
			break
		}
	}
	assert.True(t, foundSlice, "Should have slice field as JSON")
}

func TestOTLPConverter_ConvertStructToOTLPAttributes_EmptySlice(t *testing.T) {
	type StructWithSlice struct {
		Items []string
	}

	data := StructWithSlice{
		Items: []string{},
	}

	attrs := convertStructToOTLPAttributes(data)

	// Empty slices should not be included
	for _, attr := range attrs {
		assert.NotEqual(t, "Items", attr.Key, "Empty slice should not be included")
	}
}

func TestOTLPConverter_ConvertLabelsToOTLPAttributes(t *testing.T) {
	labels := map[string]string{
		"gpu_id": "0",
		"type":   "memory",
		"status": "healthy",
	}

	converter := &otlpConverter{}
	attrs := converter.convertLabelsToOTLPAttributes(labels, nil)

	assert.Len(t, attrs, 3)

	// Verify all labels are converted
	labelMap := make(map[string]string)
	for _, attr := range attrs {
		labelMap[attr.Key] = attr.Value.GetStringValue()
	}

	assert.Equal(t, "0", labelMap["gpu_id"])
	assert.Equal(t, "memory", labelMap["type"])
	assert.Equal(t, "healthy", labelMap["status"])
}

func TestOTLPConverter_ConvertLabelsToOTLPAttributes_EmptyLabels(t *testing.T) {
	labels := map[string]string{}

	converter := &otlpConverter{}
	attrs := converter.convertLabelsToOTLPAttributes(labels, nil)

	assert.Empty(t, attrs)
}

func TestOTLPConverter_ConvertLabelsToOTLPAttributes_EnrichesGPUIndex(t *testing.T) {
	gpuUUIDToIndex := map[string]string{
		"GPU-abc-123": "0",
		"GPU-def-456": "1",
	}

	t.Run("adds gpu label when uuid present but gpu absent", func(t *testing.T) {
		labels := map[string]string{
			"uuid":           "GPU-abc-123",
			"gpud_component": "accelerator-nvidia-utilization",
		}

		converter := &otlpConverter{}
		attrs := converter.convertLabelsToOTLPAttributes(labels, gpuUUIDToIndex)

		attrMap := make(map[string]string)
		for _, attr := range attrs {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}

		assert.Equal(t, "0", attrMap["gpu"], "should enrich with gpu index from machine info")
		assert.Equal(t, "GPU-abc-123", attrMap["uuid"])
	})

	t.Run("skips enrichment when gpu label already present (DCGM)", func(t *testing.T) {
		labels := map[string]string{
			"uuid":           "GPU-abc-123",
			"gpu":            "0",
			"gpud_component": "accelerator-nvidia-dcgm-clock",
		}

		converter := &otlpConverter{}
		attrs := converter.convertLabelsToOTLPAttributes(labels, gpuUUIDToIndex)

		gpuCount := 0
		for _, attr := range attrs {
			if attr.Key == "gpu" {
				gpuCount++
			}
		}
		assert.Equal(t, 1, gpuCount, "should not duplicate gpu label for DCGM metrics")
	})

	t.Run("skips enrichment when uuid not in mapping", func(t *testing.T) {
		labels := map[string]string{
			"uuid":           "GPU-unknown-999",
			"gpud_component": "accelerator-nvidia-utilization",
		}

		converter := &otlpConverter{}
		attrs := converter.convertLabelsToOTLPAttributes(labels, gpuUUIDToIndex)

		attrMap := make(map[string]string)
		for _, attr := range attrs {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}

		_, hasGPU := attrMap["gpu"]
		assert.False(t, hasGPU, "should not add gpu label when uuid not found in mapping")
	})

	t.Run("skips enrichment when no uuid label", func(t *testing.T) {
		labels := map[string]string{
			"gpud_component": "os",
			"mount_point":    "/",
		}

		converter := &otlpConverter{}
		attrs := converter.convertLabelsToOTLPAttributes(labels, gpuUUIDToIndex)

		attrMap := make(map[string]string)
		for _, attr := range attrs {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}

		_, hasGPU := attrMap["gpu"]
		assert.False(t, hasGPU, "should not add gpu label for non-GPU metrics")
	})

	t.Run("works with nil map", func(t *testing.T) {
		labels := map[string]string{
			"uuid":           "GPU-abc-123",
			"gpud_component": "accelerator-nvidia-utilization",
		}

		converter := &otlpConverter{}
		attrs := converter.convertLabelsToOTLPAttributes(labels, nil)

		attrMap := make(map[string]string)
		for _, attr := range attrs {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}

		_, hasGPU := attrMap["gpu"]
		assert.False(t, hasGPU, "should not add gpu label when mapping is nil")
	})
}

func TestBuildGPUUUIDToIndexMap(t *testing.T) {
	t.Run("builds map from machine info", func(t *testing.T) {
		data := &collector.HealthData{
			GPUUUIDToIndex: map[string]string{
				"GPU-abc-123": "0",
				"GPU-def-456": "1",
			},
		}

		m := buildGPUUUIDToIndexMap(data)
		assert.Equal(t, "0", m["GPU-abc-123"])
		assert.Equal(t, "1", m["GPU-def-456"])
		assert.Len(t, m, 2)
	})

	t.Run("returns empty map when machine info is nil", func(t *testing.T) {
		data := &collector.HealthData{}
		m := buildGPUUUIDToIndexMap(data)
		assert.Empty(t, m)
	})

	t.Run("returns empty map when mapping is nil", func(t *testing.T) {
		data := &collector.HealthData{
			GPUUUIDToIndex: nil,
		}
		m := buildGPUUUIDToIndexMap(data)
		assert.Empty(t, m)
	})

	t.Run("skips entries with empty uuid or index", func(t *testing.T) {
		data := &collector.HealthData{
			GPUUUIDToIndex: map[string]string{
				"GPU-abc-123": "0",
				"":            "1",
				"GPU-ghi-789": "",
			},
		}

		m := buildGPUUUIDToIndexMap(data)
		assert.Equal(t, "0", m["GPU-abc-123"])
		assert.Len(t, m, 1)
	})
}

func TestOTLPConverter_SummaryMetric(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Metrics: metrics.Metrics{
			{Component: "gpu", Name: "temp", Value: 65.0},
		},
		Events: eventstore.Events{
			{Component: "gpu", Name: "event1"},
		},
		ComponentData: map[string]interface{}{
			"comp1": map[string]any{"health": "healthy"},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	rm := otlpData.Metrics.ResourceMetrics[0]
	metrics := rm.ScopeMetrics[0].Metrics

	// Find summary metric
	var summaryMetric *metricsv1.Metric
	for _, m := range metrics {
		if m.Name == "fleetint_agent_collection_summary" {
			summaryMetric = m
			break
		}
	}

	require.NotNil(t, summaryMetric, "Should have summary metric")
	assert.Contains(t, summaryMetric.Description, "collection")

	// Verify summary attributes
	gauge := summaryMetric.Data.(*metricsv1.Metric_Gauge).Gauge
	require.Len(t, gauge.DataPoints, 1)

	attrs := gauge.DataPoints[0].Attributes
	attrMap := make(map[string]int64)
	for _, attr := range attrs {
		attrMap[attr.Key] = attr.Value.GetIntValue()
	}

	assert.Equal(t, int64(1), attrMap["metrics_count"])
	assert.Equal(t, int64(1), attrMap["events_count"])
	assert.Equal(t, int64(1), attrMap["component_data_count"])
}

func TestOTLPConverter_UpMetric(t *testing.T) {
	timestamp := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	data := &collector.HealthData{
		Timestamp: timestamp,
		MachineID: "test-machine",
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	rm := otlpData.Metrics.ResourceMetrics[0]
	metrics := rm.ScopeMetrics[0].Metrics

	var upMetric *metricsv1.Metric
	for _, m := range metrics {
		if m.Name == "fleetint_agent_up" {
			upMetric = m
			break
		}
	}

	require.NotNil(t, upMetric, "Should have fleetint_agent_up metric")
	assert.Empty(t, upMetric.Unit)
	assert.Contains(t, upMetric.Description, "liveness")

	gauge := upMetric.Data.(*metricsv1.Metric_Gauge).Gauge
	require.Len(t, gauge.DataPoints, 1)

	point := gauge.DataPoints[0]
	assert.Equal(t, uint64(timestamp.UnixNano()), point.TimeUnixNano)
	assert.Equal(t, int64(1), point.GetAsInt())
	assert.Empty(t, point.Attributes)
}

func findOTLPMetric(metrics []*metricsv1.Metric, name string) *metricsv1.Metric {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric
		}
	}
	return nil
}

func TestOTLPConverter_ResourceAttributes(t *testing.T) {
	data := &collector.HealthData{
		Timestamp:   time.Now(),
		MachineID:   "test-machine-123",
		NodeGroup:   "group-a",
		ComputeZone: "zone-a",
		ComponentData: map[string]interface{}{
			"comp1": map[string]any{},
			"comp2": map[string]any{},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	rm := otlpData.Metrics.ResourceMetrics[0]
	attrs := rm.Resource.Attributes

	// Find specific required attributes
	attrMap := make(map[string]string)
	for _, attr := range attrs {
		if attr.Value.GetStringValue() != "" {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}
	}

	assert.Equal(t, "fleet-intelligence-agent", attrMap["service.name"])
	assert.Equal(t, "test-machine-123", attrMap["machine.id"])
	assert.Equal(t, "group-a", attrMap["node_group"])
	assert.Equal(t, "zone-a", attrMap["compute_zone"])

	logResourceAttrMap := make(map[string]string)
	for _, attr := range otlpData.Logs.ResourceLogs[0].Resource.Attributes {
		if attr.Value.GetStringValue() != "" {
			logResourceAttrMap[attr.Key] = attr.Value.GetStringValue()
		}
	}
	assert.Equal(t, "group-a", logResourceAttrMap["node_group"])
	assert.Equal(t, "zone-a", logResourceAttrMap["compute_zone"])
}

func TestOTLPConverter_ResourceAttributesOmitEmptyOptionalValues(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine-123",
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	rm := otlpData.Metrics.ResourceMetrics[0]
	attrMap := make(map[string]string)
	for _, attr := range rm.Resource.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}

	_, nodeGroupExists := attrMap["node_group"]
	_, computeZoneExists := attrMap["compute_zone"]
	assert.False(t, nodeGroupExists)
	assert.False(t, computeZoneExists)
}

func TestOTLPConverter_ResourceAttributes_IncludesOnlyGPUInfoGPUs(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine-123",
		MachineInfo: &machineinfo.MachineInfo{
			GPUInfo: &apiv1.MachineGPUInfo{
				Product:      "NVIDIA-H100",
				Manufacturer: "NVIDIA",
				Architecture: "hopper",
				Memory:       "85899345920",
				GPUs: []apiv1.MachineGPUInstance{
					{
						UUID:         "GPU-123",
						GPUIndex:     "0",
						BusID:        "0000:01:00.0",
						SN:           "serial-123",
						MinorID:      "0",
						BoardID:      7,
						VBIOSVersion: "96.00.68.00.01",
						ChassisSN:    "chassis-123",
					},
				},
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	attrs := otlpData.Metrics.ResourceMetrics[0].Resource.Attributes
	gpus := findAttribute(t, attrs, "gpuInfo.gpus").GetStringValue()
	assert.JSONEq(t, `[{"uuid":"GPU-123","gpuIndex":"0","busID":"0000:01:00.0","sn":"serial-123","minorID":"0","boardID":7,"vbiosVersion":"96.00.68.00.01","chassisSN":"chassis-123"}]`, gpus)

	for _, attr := range attrs {
		assert.NotContains(t, []string{
			"gpuInfo.product",
			"gpuInfo.manufacturer",
			"gpuInfo.architecture",
			"gpuInfo.memory",
		}, attr.Key)
	}
}

func TestOTLPConverter_Interface(t *testing.T) {
	// Verify otlpConverter implements OTLPConverter interface
	var _ OTLPConverter = (*otlpConverter)(nil)

	converter := NewOTLPConverter()
	assert.NotNil(t, converter)
}

func TestResolveOTLPHostname(t *testing.T) {
	origHostEnv, hadHostEnv := os.LookupEnv("HOSTNAME")
	origOSHostname := osHostname
	t.Cleanup(func() {
		osHostname = origOSHostname
		if hadHostEnv {
			_ = os.Setenv("HOSTNAME", origHostEnv)
		} else {
			_ = os.Unsetenv("HOSTNAME")
		}
	})

	t.Run("falls back to hostname env", func(t *testing.T) {
		_ = os.Setenv("HOSTNAME", "pod-host-a")
		osHostname = func() (string, error) { return "os-host-a", nil }
		assert.Equal(t, "pod-host-a", resolveOTLPHostname())
	})

	t.Run("falls back to os hostname", func(t *testing.T) {
		_ = os.Unsetenv("HOSTNAME")
		osHostname = func() (string, error) { return "os-host-a", nil }
		assert.Equal(t, "os-host-a", resolveOTLPHostname())
	})

	t.Run("returns empty on hostname error", func(t *testing.T) {
		_ = os.Unsetenv("HOSTNAME")
		osHostname = func() (string, error) { return "", errors.New("boom") }
		assert.Equal(t, "", resolveOTLPHostname())
	})
}

func TestOTLPConverter_Convert_AllData(t *testing.T) {
	// Test with all data types combined
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Metrics: metrics.Metrics{
			{Component: "gpu", Name: "temp", Value: 65.0, UnixMilliseconds: time.Now().UnixMilli()},
		},
		Events: eventstore.Events{
			{Time: time.Now(), Component: "gpu", Name: "event1", Type: "info", Message: "Test event"},
		},
		ComponentData: map[string]interface{}{
			"gpu": map[string]any{
				"health": "healthy",
				"reason": "All OK",
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	// Verify all data types are present
	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Metrics)
	require.NotNil(t, otlpData.Logs)

	// Verify metrics
	rm := otlpData.Metrics.ResourceMetrics[0]
	assert.Greater(t, len(rm.ScopeMetrics[0].Metrics), 0)

	// Verify logs (events + component data)
	rl := otlpData.Logs.ResourceLogs[0]
	assert.Greater(t, len(rl.ScopeLogs[0].LogRecords), 0)

	assert.Greater(t, len(rm.Resource.Attributes), 0)
}

func TestOTLPConverter_ComponentDataWithNilValues(t *testing.T) {
	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		ComponentData: map[string]interface{}{
			"comp1": map[string]any{
				"health":     "healthy",
				"reason":     "OK",
				"time":       nil, // nil time value
				"extra_info": nil, // nil extra info
			},
		},
	}

	converter := NewOTLPConverter()
	otlpData := converter.Convert(data)

	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Logs)

	// Should handle nil values gracefully
	rl := otlpData.Logs.ResourceLogs[0]
	logs := rl.ScopeLogs[0].LogRecords

	// Should have at least the component log
	assert.GreaterOrEqual(t, len(logs), 1)
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func findAttribute(t *testing.T, attrs []*commonv1.KeyValue, key string) *commonv1.AnyValue {
	t.Helper()

	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}

	t.Fatalf("attribute %q not found", key)
	return nil
}

func findMapValue(t *testing.T, attrs []*commonv1.KeyValue, key string) *commonv1.AnyValue {
	t.Helper()

	return findAttribute(t, attrs, key)
}
