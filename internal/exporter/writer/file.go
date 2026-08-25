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

// Package writer handles writing health data to various outputs
package writer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/converter"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/validation/outbound"
)

// FileWriter defines the interface for writing health data to files
type FileWriter interface {
	WriteJSON(data *collector.HealthData, outputPath string) error
	WriteCSV(data *collector.HealthData, outputPath string) error
}

// fileWriter implements the FileWriter interface
type fileWriter struct {
	otlpConverter converter.OTLPConverter
	csvConverter  converter.CSVConverter
}

// NewFileWriter creates a new file writer
func NewFileWriter(otlpConverter converter.OTLPConverter, csvConverter converter.CSVConverter) FileWriter {
	return &fileWriter{
		otlpConverter: otlpConverter,
		csvConverter:  csvConverter,
	}
}

// WriteJSON writes health data in JSON format
func (w *fileWriter) WriteJSON(data *collector.HealthData, outputPath string) error {
	timestamp := data.Timestamp.Format("20060102_150405")
	otlpData := w.otlpConverter.Convert(data)
	outbound.LogIssues("exporter-file-writer", "OTLPData", outbound.ValidateOTLPPayload(otlpData),
		"collection_id", data.CollectionID,
		"machine_id", data.MachineID,
		"format", "json")

	// Write OTLP JSON files for direct use with OTEL collectors
	if otlpData.Metrics != nil {
		filename := filepath.Join(outputPath, fmt.Sprintf("fleetint_metrics_%s.json", timestamp))
		if err := w.writeOTLPJSONFile(filename, otlpData.Metrics); err != nil {
			return fmt.Errorf("failed to write OTLP metrics file: %w", err)
		}
	}

	if otlpData.Logs != nil {
		filename := filepath.Join(outputPath, fmt.Sprintf("fleetint_logs_%s.json", timestamp))
		if err := w.writeOTLPJSONFile(filename, otlpData.Logs); err != nil {
			return fmt.Errorf("failed to write OTLP logs file: %w", err)
		}
	}

	log.Logger.Infow("Successfully wrote health data JSON files", "path", outputPath)
	return nil
}

// WriteCSV writes health data in CSV format
func (w *fileWriter) WriteCSV(data *collector.HealthData, outputPath string) error {
	timestamp := data.Timestamp.Format("20060102_150405")
	otlpData := w.otlpConverter.Convert(data)
	outbound.LogIssues("exporter-file-writer", "OTLPData", outbound.ValidateOTLPPayload(otlpData),
		"collection_id", data.CollectionID,
		"machine_id", data.MachineID,
		"format", "csv")

	files, err := w.csvConverter.Convert(data, outputPath, timestamp)
	if err != nil {
		return fmt.Errorf("failed to convert data to CSV: %w", err)
	}

	log.Logger.Infow("Successfully wrote health data CSV files",
		"path", outputPath,
		"metrics_file", files.MetricsFile,
		"events_file", files.EventsFile,
		"components_file", files.ComponentsFile,
		"machine_info_file", files.MachineInfoFile)

	return nil
}

// writeOTLPJSONFile writes protobuf message as standard OTLP JSON format
func (w *fileWriter) writeOTLPJSONFile(filename string, message proto.Message) error {
	// Use protojson to ensure proper OTLP field naming and format
	marshaler := protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		UseProtoNames:   false, // Use JSON field names (camelCase)
		EmitUnpopulated: false, // Don't emit empty fields
	}

	jsonData, err := marshaler.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal OTLP message to JSON: %w", err)
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer file.Close()

	if _, err := file.Write(jsonData); err != nil {
		return fmt.Errorf("failed to write OTLP JSON to file %s: %w", filename, err)
	}

	return nil
}
