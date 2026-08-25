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
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/machineinfo"
)

func TestNewCSVConverter(t *testing.T) {
	converter := NewCSVConverter()
	assert.NotNil(t, converter)
}

func TestCSVConverter_Convert_Metrics(t *testing.T) {
	tmpDir := t.TempDir()
	timestamp := "20251105_120000"

	metrics := metrics.Metrics{
		{
			Component:        "gpu-memory",
			Name:             "memory_used",
			UnixMilliseconds: 1699200000000,
			Value:            1024.5,
			Labels:           map[string]string{"gpu": "0", "type": "used"},
		},
		{
			Component:        "cpu",
			Name:             "cpu_usage",
			UnixMilliseconds: 1699200001000,
			Value:            75.3,
			Labels:           map[string]string{},
		},
	}

	data := &collector.HealthData{
		Timestamp: time.Now(),
		Metrics:   metrics,
	}

	converter := NewCSVConverter()
	files, err := converter.Convert(data, tmpDir, timestamp)

	require.NoError(t, err)
	assert.NotNil(t, files)
	assert.NotEmpty(t, files.MetricsFile)

	// Verify metrics file was created and contains correct data
	metricsPath := filepath.Join(tmpDir, files.MetricsFile)
	assert.FileExists(t, metricsPath)

	// Read and verify content
	file, err := os.Open(metricsPath)
	require.NoError(t, err)
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Should have header + 2 data rows
	assert.Len(t, records, 3)
	assert.Equal(t, []string{"timestamp", "component", "metric_name", "value", "labels"}, records[0])
}

func TestCSVConverter_Convert_Events(t *testing.T) {
	tmpDir := t.TempDir()
	timestamp := "20251105_120000"

	events := eventstore.Events{
		{
			Time:      time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC),
			Component: "gpu-memory",
			Name:      "memory_threshold_exceeded",
			Type:      "warning",
			Message:   "GPU memory usage exceeded threshold",
		},
	}

	data := &collector.HealthData{
		Timestamp: time.Now(),
		Events:    events,
	}

	converter := NewCSVConverter()
	files, err := converter.Convert(data, tmpDir, timestamp)

	require.NoError(t, err)
	assert.NotEmpty(t, files.EventsFile)

	// Verify events file
	eventsPath := filepath.Join(tmpDir, files.EventsFile)
	assert.FileExists(t, eventsPath)

	file, err := os.Open(eventsPath)
	require.NoError(t, err)
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Should have header + 1 data row
	assert.Len(t, records, 2)
	assert.Equal(t, []string{"timestamp", "component", "event_name", "event_type", "message"}, records[0])
}

func TestCSVConverter_Convert_ComponentData(t *testing.T) {
	tmpDir := t.TempDir()
	timestamp := "20251105_120000"

	componentData := map[string]interface{}{
		"gpu-memory": map[string]any{
			"time":           metav1.Time{Time: time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC)},
			"component_name": "gpu-memory",
			"health":         "healthy",
			"reason":         "All checks passed",
		},
	}

	data := &collector.HealthData{
		Timestamp:     time.Now(),
		ComponentData: componentData,
	}

	converter := NewCSVConverter()
	files, err := converter.Convert(data, tmpDir, timestamp)

	require.NoError(t, err)
	assert.NotEmpty(t, files.ComponentsFile)

	// Verify components file
	componentsPath := filepath.Join(tmpDir, files.ComponentsFile)
	assert.FileExists(t, componentsPath)

	file, err := os.Open(componentsPath)
	require.NoError(t, err)
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Should have header + 1 data row
	assert.Len(t, records, 2)
	assert.Equal(t, []string{"timestamp", "component_name", "health", "reason"}, records[0])
}

func TestCSVConverter_Convert_MachineInfo(t *testing.T) {
	tmpDir := t.TempDir()
	timestamp := "20251105_120000"

	machineInfo := &machineinfo.MachineInfo{
		AgentVersion:  "0.1.5",
		DCGMVersion:   "4.2.3",
		OSImage:       "Ubuntu 22.04",
		KernelVersion: "5.15.0",
		CPUInfo: &apiv1.MachineCPUInfo{
			Type:         "Intel",
			Manufacturer: "Intel",
			Architecture: "x86_64",
			LogicalCores: 8,
		},
		MemoryInfo: &apiv1.MachineMemoryInfo{
			TotalBytes: 16000000000,
		},
		GPUInfo: &apiv1.MachineGPUInfo{
			Product:      "NVIDIA A100",
			Manufacturer: "NVIDIA",
			Architecture: "Ampere",
			Memory:       "40GB",
		},
	}

	data := &collector.HealthData{
		Timestamp:   time.Now(),
		MachineInfo: machineInfo,
	}

	converter := NewCSVConverter()
	files, err := converter.Convert(data, tmpDir, timestamp)

	require.NoError(t, err)
	assert.NotEmpty(t, files.MachineInfoFile)

	// Verify machine info file
	machineinfoPath := filepath.Join(tmpDir, files.MachineInfoFile)
	assert.FileExists(t, machineinfoPath)

	file, err := os.Open(machineinfoPath)
	require.NoError(t, err)
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Should have multiple rows for machine info attributes
	assert.Greater(t, len(records), 5)
	assert.Equal(t, []string{"attribute_name", "attribute_value"}, records[0])
	assert.Contains(t, records, []string{"DCGM Version", "4.2.3"})
}

func TestCSVConverter_Convert_EmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	timestamp := "20251105_120000"

	data := &collector.HealthData{
		Timestamp: time.Now(),
	}

	converter := NewCSVConverter()
	files, err := converter.Convert(data, tmpDir, timestamp)

	require.NoError(t, err)
	assert.NotNil(t, files)
	// All file fields should be empty when no data provided
	assert.Empty(t, files.MetricsFile)
	assert.Empty(t, files.EventsFile)
	assert.Empty(t, files.ComponentsFile)
	assert.Empty(t, files.MachineInfoFile)
}

func TestCSVConverter_Convert_AllData(t *testing.T) {
	tmpDir := t.TempDir()
	timestamp := "20251105_120000"

	data := &collector.HealthData{
		Timestamp: time.Now(),
		Metrics: metrics.Metrics{
			{
				Component:        "gpu",
				Name:             "temp",
				UnixMilliseconds: 1699200000000,
				Value:            65.0,
			},
		},
		Events: eventstore.Events{
			{
				Time:      time.Now(),
				Component: "gpu",
				Name:      "temp_warning",
				Type:      "warning",
				Message:   "Temperature high",
			},
		},
		ComponentData: map[string]interface{}{
			"gpu": map[string]any{
				"time":           metav1.Time{Time: time.Now()},
				"component_name": "gpu",
				"health":         "degraded",
				"reason":         "High temperature",
			},
		},
		MachineInfo: &machineinfo.MachineInfo{
			AgentVersion: "0.1.5",
		},
	}

	converter := NewCSVConverter()
	files, err := converter.Convert(data, tmpDir, timestamp)

	require.NoError(t, err)
	assert.NotEmpty(t, files.MetricsFile)
	assert.NotEmpty(t, files.EventsFile)
	assert.NotEmpty(t, files.ComponentsFile)
	assert.NotEmpty(t, files.MachineInfoFile)

	// Verify all files exist
	assert.FileExists(t, filepath.Join(tmpDir, files.MetricsFile))
	assert.FileExists(t, filepath.Join(tmpDir, files.EventsFile))
	assert.FileExists(t, filepath.Join(tmpDir, files.ComponentsFile))
	assert.FileExists(t, filepath.Join(tmpDir, files.MachineInfoFile))
}

func TestCSVConverter_ExtractTimeFromInterface(t *testing.T) {
	tests := []struct {
		name      string
		timeValue any
		expectNil bool
	}{
		{
			name:      "metav1.Time",
			timeValue: metav1.Time{Time: time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC)},
			expectNil: false,
		},
		{
			name:      "time.Time_pointer",
			timeValue: func() *time.Time { t := time.Date(2025, 11, 5, 12, 0, 0, 0, time.UTC); return &t }(),
			expectNil: false,
		},
		{
			name:      "nil_value",
			timeValue: nil,
			expectNil: true,
		},
		{
			name:      "string_value",
			timeValue: "2025-11-05",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTimeFromInterface(tt.timeValue)
			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
			}
		})
	}
}

func TestCSVConverter_Interface(t *testing.T) {
	// Verify csvConverter implements CSVConverter interface
	var _ CSVConverter = (*csvConverter)(nil)

	converter := NewCSVConverter()
	assert.NotNil(t, converter)
}
