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

package writer

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/converter"
)

// mockOTLPConverter implements converter.OTLPConverter for testing
type mockOTLPConverter struct {
	convertFunc func(*collector.HealthData) *converter.OTLPData
}

func (m *mockOTLPConverter) Convert(data *collector.HealthData) *converter.OTLPData {
	if m.convertFunc != nil {
		return m.convertFunc(data)
	}
	// Default implementation
	return &converter.OTLPData{
		Metrics: &metricsv1.MetricsData{},
		Logs:    &logsv1.LogsData{},
	}
}

// mockCSVConverter implements converter.CSVConverter for testing
type mockCSVConverter struct {
	convertFunc func(*collector.HealthData, string, string) (*converter.CSVFiles, error)
}

func (m *mockCSVConverter) Convert(data *collector.HealthData, outputDir, timestamp string) (*converter.CSVFiles, error) {
	if m.convertFunc != nil {
		return m.convertFunc(data, outputDir, timestamp)
	}
	// Default implementation
	return &converter.CSVFiles{
		MetricsFile:     "metrics.csv",
		EventsFile:      "events.csv",
		ComponentsFile:  "components.csv",
		MachineInfoFile: "machine_info.csv",
	}, nil
}

func TestNewFileWriter(t *testing.T) {
	otlpConverter := &mockOTLPConverter{}
	csvConverter := &mockCSVConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)

	assert.NotNil(t, writer)
}

func TestFileWriter_WriteJSON_Success(t *testing.T) {
	tmpDir := t.TempDir()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{},
				},
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{},
				},
			}
		},
	}
	csvConverter := &mockCSVConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)
	err := writer.WriteJSON(testData, tmpDir)

	require.NoError(t, err)

	// Verify files were created
	timestamp := testData.Timestamp.Format("20060102_150405")
	metricsFile := filepath.Join(tmpDir, "fleetint_metrics_"+timestamp+".json")
	logsFile := filepath.Join(tmpDir, "fleetint_logs_"+timestamp+".json")

	assert.FileExists(t, metricsFile)
	assert.FileExists(t, logsFile)
}

func TestFileWriter_WriteJSON_ValidationDoesNotBlock(t *testing.T) {
	tmpDir := t.TempDir()
	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}
	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{
						Resource: &resourcev1.Resource{
							Attributes: []*commonv1.KeyValue{},
						},
						ScopeMetrics: []*metricsv1.ScopeMetrics{{
							Metrics: []*metricsv1.Metric{{
								Name: "bad.metric",
								Data: &metricsv1.Metric_Gauge{
									Gauge: &metricsv1.Gauge{
										DataPoints: []*metricsv1.NumberDataPoint{{
											Value: &metricsv1.NumberDataPoint_AsDouble{AsDouble: math.NaN()},
										}},
									},
								},
							}},
						}},
					}},
				},
			}
		},
	}

	writer := NewFileWriter(otlpConverter, &mockCSVConverter{})
	err := writer.WriteJSON(testData, tmpDir)
	require.NoError(t, err)

	timestamp := testData.Timestamp.Format("20060102_150405")
	metricsFile := filepath.Join(tmpDir, "fleetint_metrics_"+timestamp+".json")
	assert.FileExists(t, metricsFile)
}

func TestFileWriter_WriteJSON_NilMetrics(t *testing.T) {
	tmpDir := t.TempDir()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: nil,
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{},
				},
			}
		},
	}
	csvConverter := &mockCSVConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)
	err := writer.WriteJSON(testData, tmpDir)

	require.NoError(t, err)

	// Only logs file should be created
	timestamp := testData.Timestamp.Format("20060102_150405")
	metricsFile := filepath.Join(tmpDir, "fleetint_metrics_"+timestamp+".json")
	logsFile := filepath.Join(tmpDir, "fleetint_logs_"+timestamp+".json")

	assert.NoFileExists(t, metricsFile)
	assert.FileExists(t, logsFile)
}

func TestFileWriter_WriteJSON_NilLogs(t *testing.T) {
	tmpDir := t.TempDir()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{},
				},
				Logs: nil,
			}
		},
	}
	csvConverter := &mockCSVConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)
	err := writer.WriteJSON(testData, tmpDir)

	require.NoError(t, err)

	// Only metrics file should be created
	timestamp := testData.Timestamp.Format("20060102_150405")
	metricsFile := filepath.Join(tmpDir, "fleetint_metrics_"+timestamp+".json")
	logsFile := filepath.Join(tmpDir, "fleetint_logs_"+timestamp+".json")

	assert.FileExists(t, metricsFile)
	assert.NoFileExists(t, logsFile)
}

func TestFileWriter_WriteJSON_InvalidPath(t *testing.T) {
	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{},
				},
				Logs: nil,
			}
		},
	}
	csvConverter := &mockCSVConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)
	// Use an invalid path that should fail
	err := writer.WriteJSON(testData, "/invalid/nonexistent/path")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write OTLP metrics file")
}

func TestFileWriter_WriteCSV_Success(t *testing.T) {
	tmpDir := t.TempDir()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	expectedFiles := &converter.CSVFiles{
		MetricsFile:     "metrics.csv",
		EventsFile:      "events.csv",
		ComponentsFile:  "components.csv",
		MachineInfoFile: "machine_info.csv",
	}

	csvConverter := &mockCSVConverter{
		convertFunc: func(data *collector.HealthData, outputDir, timestamp string) (*converter.CSVFiles, error) {
			// Create actual CSV files for testing
			for _, filename := range []string{
				expectedFiles.MetricsFile,
				expectedFiles.EventsFile,
				expectedFiles.ComponentsFile,
				expectedFiles.MachineInfoFile,
			} {
				if filename != "" {
					file, err := os.Create(filepath.Join(outputDir, filename))
					if err != nil {
						return nil, err
					}
					file.Close()
				}
			}
			return expectedFiles, nil
		},
	}
	otlpConverter := &mockOTLPConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)
	err := writer.WriteCSV(testData, tmpDir)

	require.NoError(t, err)

	// Verify all files were created
	assert.FileExists(t, filepath.Join(tmpDir, "metrics.csv"))
	assert.FileExists(t, filepath.Join(tmpDir, "events.csv"))
	assert.FileExists(t, filepath.Join(tmpDir, "components.csv"))
	assert.FileExists(t, filepath.Join(tmpDir, "machine_info.csv"))
}

func TestFileWriter_WriteCSV_ValidationDoesNotBlock(t *testing.T) {
	tmpDir := t.TempDir()
	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{
						Resource: &resourcev1.Resource{},
					}},
				},
			}
		},
	}
	csvConverter := &mockCSVConverter{
		convertFunc: func(data *collector.HealthData, outputDir, timestamp string) (*converter.CSVFiles, error) {
			return &converter.CSVFiles{
				MetricsFile:     "metrics.csv",
				EventsFile:      "events.csv",
				ComponentsFile:  "components.csv",
				MachineInfoFile: "machine_info.csv",
			}, nil
		},
	}

	writer := NewFileWriter(otlpConverter, csvConverter)
	err := writer.WriteCSV(testData, tmpDir)
	require.NoError(t, err)
}

func TestFileWriter_WriteCSV_ConversionError(t *testing.T) {
	tmpDir := t.TempDir()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	csvConverter := &mockCSVConverter{
		convertFunc: func(data *collector.HealthData, outputDir, timestamp string) (*converter.CSVFiles, error) {
			return nil, assert.AnError
		},
	}
	otlpConverter := &mockOTLPConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)
	err := writer.WriteCSV(testData, tmpDir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to convert data to CSV")
}

func TestFileWriter_WriteOTLPJSONFile_ValidProtobuf(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.json")

	// Create a simple protobuf message
	message := &metricsv1.MetricsData{
		ResourceMetrics: []*metricsv1.ResourceMetrics{},
	}

	otlpConverter := &mockOTLPConverter{}
	csvConverter := &mockCSVConverter{}
	writer := NewFileWriter(otlpConverter, csvConverter).(*fileWriter)

	err := writer.writeOTLPJSONFile(filename, message)

	require.NoError(t, err)
	assert.FileExists(t, filename)

	// Verify file content is valid JSON
	content, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.NotEmpty(t, content)
}

func TestFileWriter_WriteOTLPJSONFile_EmptyMessage(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.json")

	// Test with empty logs message - should still work
	message := &logsv1.LogsData{}

	otlpConverter := &mockOTLPConverter{}
	csvConverter := &mockCSVConverter{}
	writer := NewFileWriter(otlpConverter, csvConverter).(*fileWriter)

	err := writer.writeOTLPJSONFile(filename, message)

	// Empty message should marshal successfully
	require.NoError(t, err)
	assert.FileExists(t, filename)
}

func TestFileWriter_WriteOTLPJSONFile_InvalidPath(t *testing.T) {
	// Use an invalid path
	filename := "/invalid/nonexistent/path/test.json"

	message := &metricsv1.MetricsData{}

	otlpConverter := &mockOTLPConverter{}
	csvConverter := &mockCSVConverter{}
	writer := NewFileWriter(otlpConverter, csvConverter).(*fileWriter)

	err := writer.writeOTLPJSONFile(filename, message)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create file")
}

func TestFileWriter_TimestampFormatting(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with a specific timestamp
	specificTime := time.Date(2025, 11, 5, 14, 30, 45, 0, time.UTC)
	testData := &collector.HealthData{
		Timestamp:    specificTime,
		MachineID:    "test-machine-id",
		CollectionID: "test-collection-id",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{},
				},
				Logs: nil,
			}
		},
	}
	csvConverter := &mockCSVConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)
	err := writer.WriteJSON(testData, tmpDir)

	require.NoError(t, err)

	// Verify file was created with correct timestamp format
	expectedTimestamp := "20251105_143045"
	metricsFile := filepath.Join(tmpDir, "fleetint_metrics_"+expectedTimestamp+".json")
	assert.FileExists(t, metricsFile)
}

func TestFileWriter_Interface(t *testing.T) {
	// Verify that fileWriter implements FileWriter interface
	var _ FileWriter = (*fileWriter)(nil)

	otlpConverter := &mockOTLPConverter{}
	csvConverter := &mockCSVConverter{}

	writer := NewFileWriter(otlpConverter, csvConverter)

	// Verify both methods are available
	assert.NotNil(t, writer)
}
