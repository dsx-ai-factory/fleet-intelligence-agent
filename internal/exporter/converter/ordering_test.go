// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
)

// Map iteration order is randomized per range statement, so these fixtures use
// enough keys that an unsorted implementation is overwhelmingly unlikely to
// emit them in lexical order by chance.
const orderingFixtureSize = 12

func TestOTLPComponentLogRecordsAreOrderedByComponentName(t *testing.T) {
	data := &collector.HealthData{
		Timestamp:     time.Now(),
		MachineID:     "test-machine",
		ComponentData: orderingComponentData(),
	}

	otlpData := NewOTLPConverter().Convert(data)
	require.NotNil(t, otlpData)
	require.NotNil(t, otlpData.Logs)
	require.Len(t, otlpData.Logs.ResourceLogs, 1)
	require.Len(t, otlpData.Logs.ResourceLogs[0].ScopeLogs, 1)

	got := componentNamesInEmittedOrder(otlpData.Logs.ResourceLogs[0].ScopeLogs[0].LogRecords)
	require.Len(t, got, orderingFixtureSize)

	want := append([]string(nil), got...)
	sort.Strings(want)
	assert.Equal(t, want, got,
		"component log records must be emitted in a stable order; see CONTRIBUTING.md 'Deterministic output'")
}

func TestEventExtraInfoAttributesAreOrderedByKey(t *testing.T) {
	extraInfo := make(map[string]string, orderingFixtureSize)
	for i := 0; i < orderingFixtureSize; i++ {
		extraInfo[fmt.Sprintf("key_%02d", i)] = fmt.Sprintf("value-%d", i)
	}

	data := &collector.HealthData{
		Timestamp: time.Now(),
		MachineID: "test-machine",
		Events: eventstore.Events{
			{
				EventID:   "123e4567-e89b-12d3-a456-426614174000",
				Time:      time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
				Component: "gpu",
				Name:      "temperature_warning",
				Type:      "warning",
				Message:   "GPU temperature high",
				ExtraInfo: extraInfo,
			},
		},
	}

	otlpData := NewOTLPConverter().Convert(data)
	require.NotNil(t, otlpData)
	require.Len(t, otlpData.Logs.ResourceLogs, 1)
	require.Len(t, otlpData.Logs.ResourceLogs[0].ScopeLogs, 1)

	logs := otlpData.Logs.ResourceLogs[0].ScopeLogs[0].LogRecords
	require.NotEmpty(t, logs)

	kvlist := findAttribute(t, logs[0].Attributes, "extra_info").GetKvlistValue()
	require.NotNil(t, kvlist)

	got := kvlistKeys(kvlist.Values)
	require.Len(t, got, orderingFixtureSize)

	want := append([]string(nil), got...)
	sort.Strings(want)
	assert.Equal(t, want, got, "extra_info keys must be emitted in a stable order")
}

func TestNestedExtraInfoObjectKeysAreOrdered(t *testing.T) {
	// Mirrors the shape of a component's ExtraInfo["data"] payload: a JSON object
	// whose nested objects are decoded recursively.
	nestedFields := make([]string, 0, orderingFixtureSize)
	for i := orderingFixtureSize - 1; i >= 0; i-- {
		nestedFields = append(nestedFields, fmt.Sprintf("%q:%d", fmt.Sprintf("nested_%02d", i), i))
	}
	nestedObject := "{" + strings.Join(nestedFields, ",") + "}"

	nested := make([]string, 0, orderingFixtureSize)
	for i := 0; i < orderingFixtureSize; i++ {
		nested = append(nested, fmt.Sprintf("%q:%s", fmt.Sprintf("field_%02d", i), nestedObject))
	}
	raw := "{" + strings.Join(nested, ",") + "}"

	kvlist := stringToStructuredAnyValue(raw).GetKvlistValue()
	require.NotNil(t, kvlist, "a JSON object payload should decode to a kvlist")

	got := kvlistKeys(kvlist.Values)
	require.Len(t, got, orderingFixtureSize)

	want := append([]string(nil), got...)
	sort.Strings(want)
	assert.Equal(t, want, got, "nested extra_info object keys must be emitted in a stable order")

	for _, field := range kvlist.Values {
		nestedKVList := field.Value.GetKvlistValue()
		require.NotNil(t, nestedKVList, "field %q should contain a nested object", field.Key)

		gotNested := kvlistKeys(nestedKVList.Values)
		require.Len(t, gotNested, orderingFixtureSize)

		wantNested := append([]string(nil), gotNested...)
		sort.Strings(wantNested)
		assert.Equal(t, wantNested, gotNested, "keys within field %q must be emitted in a stable order", field.Key)
	}
}

func TestComponentHealthCSVRowsAreOrderedByComponentName(t *testing.T) {
	tmpDir := t.TempDir()

	data := &collector.HealthData{
		Timestamp:     time.Now(),
		ComponentData: orderingComponentData(),
	}

	files, err := NewCSVConverter().Convert(data, tmpDir, "20260911_120000")
	require.NoError(t, err)
	require.NotEmpty(t, files.ComponentsFile)

	file, err := os.Open(filepath.Join(tmpDir, files.ComponentsFile))
	require.NoError(t, err)
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, orderingFixtureSize+1, "expected a header row plus one row per component")

	got := make([]string, 0, orderingFixtureSize)
	for _, record := range records[1:] {
		got = append(got, record[1]) // component_name column
	}

	want := append([]string(nil), got...)
	sort.Strings(want)
	assert.Equal(t, want, got, "CSV component rows must be written in a stable order")
}

// TestOTLPConversionIsStableAcrossRepeatedConversions is the general regression
// guard: it does not care which order is chosen, only that the same input always
// serializes the same way. Use this shape when adding a new output boundary.
func TestOTLPConversionIsStableAcrossRepeatedConversions(t *testing.T) {
	extraInfo := make(map[string]string, orderingFixtureSize)
	for i := 0; i < orderingFixtureSize; i++ {
		extraInfo[fmt.Sprintf("key_%02d", i)] = fmt.Sprintf("value-%d", i)
	}

	newData := func() *collector.HealthData {
		return &collector.HealthData{
			Timestamp:     time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
			MachineID:     "test-machine",
			ComponentData: orderingComponentData(),
			Events: eventstore.Events{
				{
					EventID:   "123e4567-e89b-12d3-a456-426614174000",
					Time:      time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
					Component: "gpu",
					Name:      "temperature_warning",
					Type:      "warning",
					Message:   "GPU temperature high",
					ExtraInfo: extraInfo,
				},
			},
		}
	}

	const runs = 30

	var first string
	for i := 0; i < runs; i++ {
		otlpData := NewOTLPConverter().Convert(newData())
		require.NotNil(t, otlpData)
		require.Len(t, otlpData.Logs.ResourceLogs, 1)
		require.Len(t, otlpData.Logs.ResourceLogs[0].ScopeLogs, 1)

		dump := canonicalLogDump(otlpData.Logs.ResourceLogs[0].ScopeLogs[0].LogRecords)
		if i == 0 {
			first = dump
			continue
		}
		require.Equal(t, first, dump,
			"conversion %d of %d differed from the first: map iteration order is leaking into the OTLP payload", i+1, runs)
	}
}

func orderingComponentData() map[string]interface{} {
	// Deliberately not inserted in lexical order.
	names := []string{
		"nvidia-nvlink", "disk", "os", "accelerator-nvidia-sxid", "cpu",
		"library", "memory", "accelerator-nvidia-xid", "network-ethernet",
		"kubelet", "containerd", "accelerator-nvidia-power",
	}

	componentData := make(map[string]interface{}, len(names))
	for i, name := range names {
		componentData[name] = map[string]any{
			"component_name": name,
			"health":         "healthy",
			"reason":         fmt.Sprintf("no issue found (%d)", i),
			"extra_info": map[string]string{
				"data": fmt.Sprintf(`{"index":%d,"name":%q}`, i, name),
			},
		}
	}
	return componentData
}

func componentNamesInEmittedOrder(logs []*logsv1.LogRecord) []string {
	names := make([]string, 0, len(logs))
	for _, record := range logs {
		logType, ok := attributeString(record.Attributes, "log_type")
		if !ok || logType != "component_data" {
			continue
		}
		if name, ok := attributeString(record.Attributes, "component"); ok {
			names = append(names, name)
		}
	}
	return names
}

func attributeString(attrs []*commonv1.KeyValue, key string) (string, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value.GetStringValue(), true
		}
	}
	return "", false
}

func kvlistKeys(values []*commonv1.KeyValue) []string {
	keys := make([]string, 0, len(values))
	for _, value := range values {
		keys = append(keys, value.Key)
	}
	return keys
}

// canonicalLogDump renders log records preserving emitted order, so that any
// reordering changes the result. It must never sort.
func canonicalLogDump(logs []*logsv1.LogRecord) string {
	var sb strings.Builder
	for _, record := range logs {
		sb.WriteString(record.Body.GetStringValue())
		sb.WriteByte('\n')
		for _, attr := range record.Attributes {
			sb.WriteString("  ")
			sb.WriteString(attr.Key)
			sb.WriteByte('=')
			sb.WriteString(anyValueString(attr.Value))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func anyValueString(v *commonv1.AnyValue) string {
	if v == nil {
		return "<nil>"
	}
	// Deliberately avoids proto String(), whose whitespace is intentionally
	// unstable and would make this helper report spurious differences.
	switch value := v.Value.(type) {
	case *commonv1.AnyValue_KvlistValue:
		parts := make([]string, 0, len(value.KvlistValue.Values))
		for _, kv := range value.KvlistValue.Values {
			parts = append(parts, kv.Key+":"+anyValueString(kv.Value))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case *commonv1.AnyValue_ArrayValue:
		parts := make([]string, 0, len(value.ArrayValue.Values))
		for _, nested := range value.ArrayValue.Values {
			parts = append(parts, anyValueString(nested))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *commonv1.AnyValue_StringValue:
		return "s:" + value.StringValue
	case *commonv1.AnyValue_BoolValue:
		return fmt.Sprintf("b:%t", value.BoolValue)
	case *commonv1.AnyValue_IntValue:
		return fmt.Sprintf("i:%d", value.IntValue)
	case *commonv1.AnyValue_DoubleValue:
		return fmt.Sprintf("d:%g", value.DoubleValue)
	case *commonv1.AnyValue_BytesValue:
		return "y:" + string(value.BytesValue)
	default:
		return "<unset>"
	}
}
