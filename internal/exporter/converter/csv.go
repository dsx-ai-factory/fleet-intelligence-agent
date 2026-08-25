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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
)

// CSVFiles represents the collection of CSV files that can be generated
type CSVFiles struct {
	MetricsFile     string
	EventsFile      string
	ComponentsFile  string
	MachineInfoFile string
}

// CSVConverter defines the interface for converting health data to CSV format
type CSVConverter interface {
	Convert(data *collector.HealthData, outputDir, timestamp string) (*CSVFiles, error)
}

// csvConverter implements the CSVConverter interface
type csvConverter struct{}

// NewCSVConverter creates a new CSV converter
func NewCSVConverter() CSVConverter {
	return &csvConverter{}
}

// Convert converts health data to CSV format and writes to files
func (c *csvConverter) Convert(data *collector.HealthData, outputDir, timestamp string) (*CSVFiles, error) {
	files := &CSVFiles{}

	// Write metrics CSV
	if len(data.Metrics) > 0 {
		filename := fmt.Sprintf("fleetint_metrics_%s.csv", timestamp)
		files.MetricsFile = filename
		if err := c.writeMetricsCSV(outputDir, filename, data); err != nil {
			return nil, fmt.Errorf("failed to write metrics CSV: %w", err)
		}
	}

	// Write events CSV
	if len(data.Events) > 0 {
		filename := fmt.Sprintf("fleetint_events_%s.csv", timestamp)
		files.EventsFile = filename
		if err := c.writeEventsCSV(outputDir, filename, data); err != nil {
			return nil, fmt.Errorf("failed to write events CSV: %w", err)
		}
	}

	// Write component health CSV
	if len(data.ComponentData) > 0 {
		filename := fmt.Sprintf("fleetint_component_health_%s.csv", timestamp)
		files.ComponentsFile = filename
		if err := c.writeComponentHealthCSV(outputDir, filename, data); err != nil {
			return nil, fmt.Errorf("failed to write component health CSV: %w", err)
		}
	}

	// Write machine info CSV
	if data.MachineInfo != nil {
		filename := fmt.Sprintf("fleetint_machine_info_%s.csv", timestamp)
		files.MachineInfoFile = filename
		if err := c.writeMachineInfoCSV(outputDir, filename, data); err != nil {
			return nil, fmt.Errorf("failed to write machine info CSV: %w", err)
		}
	}

	return files, nil
}

// writeMetricsCSV writes metrics data to CSV format
func (c *csvConverter) writeMetricsCSV(outputDir, filename string, data *collector.HealthData) error {
	fullPath := filepath.Join(outputDir, filename)
	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", fullPath, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	header := []string{"timestamp", "component", "metric_name", "value", "labels"}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write metrics data
	for _, metric := range data.Metrics {
		// Convert labels map to JSON string for CSV storage
		labelsJSON := ""
		if len(metric.Labels) > 0 {
			if labelsBytes, err := json.Marshal(metric.Labels); err == nil {
				labelsJSON = string(labelsBytes)
			}
		}

		// Convert Unix milliseconds to readable timestamp
		timestamp := time.Unix(metric.UnixMilliseconds/1000, (metric.UnixMilliseconds%1000)*1000000).UTC().Format(time.RFC3339)

		record := []string{
			timestamp,
			metric.Component,
			metric.Name,
			fmt.Sprintf("%v", metric.Value),
			labelsJSON,
		}

		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}

// writeEventsCSV writes events data to CSV format
func (c *csvConverter) writeEventsCSV(outputDir, filename string, data *collector.HealthData) error {
	fullPath := filepath.Join(outputDir, filename)
	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", fullPath, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	header := []string{"timestamp", "component", "event_name", "event_type", "message"}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write events data
	for _, event := range data.Events {
		// Format event timestamp
		timestamp := event.Time.Format(time.RFC3339)

		record := []string{
			timestamp,
			event.Component,
			event.Name,
			event.Type,
			event.Message,
		}

		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}

// writeComponentHealthCSV writes component health data to CSV format
func (c *csvConverter) writeComponentHealthCSV(outputDir, filename string, data *collector.HealthData) error {
	fullPath := filepath.Join(outputDir, filename)
	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", fullPath, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	header := []string{"timestamp", "component_name", "health", "reason"}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write component health data
	for componentName, componentResult := range data.ComponentData {
		componentInfo, ok := componentResult.(map[string]any)
		if !ok {
			log.Logger.Warnw("Unexpected component data format for CSV export", "component", componentName)
			continue
		}

		// Extract values safely, handling nil cases
		timestampStr := "N/A"
		if timeValue := componentInfo["time"]; timeValue != nil {
			// Handle different timestamp formats (metav1.Time, time.Time, etc.)
			switch t := timeValue.(type) {
			case time.Time:
				// Check for zero time (uninitialized timestamp)
				if !t.IsZero() {
					timestampStr = t.Format(time.RFC3339)
				}
			default:
				// Handle metav1.Time and other time types by extracting the underlying time.Time
				// metav1.Time has a Time field that contains the actual time.Time
				if timeVal := extractTimeFromInterface(t); timeVal != nil && !timeVal.IsZero() {
					timestampStr = timeVal.Format(time.RFC3339)
				} else {
					// Fallback: try to parse as string for other formats
					timeStr := fmt.Sprintf("%v", t)
					// Don't include the problematic zero timestamp
					if !strings.Contains(timeStr, "0001-01-01") {
						timestampStr = timeStr
					}
				}
			}
		}

		componentNameStr := ""
		if name := componentInfo["component_name"]; name != nil {
			componentNameStr = fmt.Sprintf("%v", name)
		}

		healthStr := ""
		if health := componentInfo["health"]; health != nil {
			healthStr = fmt.Sprintf("%v", health)
		}

		reasonStr := ""
		if reason := componentInfo["reason"]; reason != nil {
			reasonStr = fmt.Sprintf("%v", reason)
		}

		record := []string{timestampStr, componentNameStr, healthStr, reasonStr}

		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}

// writeMachineInfoCSV writes machine info to CSV format using the same structure as RenderTable
func (c *csvConverter) writeMachineInfoCSV(outputDir, filename string, data *collector.HealthData) error {
	fullPath := filepath.Join(outputDir, filename)
	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", fullPath, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Extract machine info attributes following the same structure as RenderTable method
	i := data.MachineInfo
	if i == nil {
		return nil
	}

	// Collect all records first
	var records [][]string

	// Header and basic system info
	records = append(records,
		[]string{"attribute_name", "attribute_value"},
		[]string{"Agent Version", i.AgentVersion},
		[]string{"Container Runtime Version", i.ContainerRuntimeVersion},
		[]string{"OS Image", i.OSImage},
		[]string{"Kernel Version", i.KernelVersion},
		[]string{"DCGM Version", i.DCGMVersion},
	)

	// CPU info
	if i.CPUInfo != nil {
		records = append(records,
			[]string{"CPU Type", i.CPUInfo.Type},
			[]string{"CPU Manufacturer", i.CPUInfo.Manufacturer},
			[]string{"CPU Architecture", i.CPUInfo.Architecture},
			[]string{"CPU Logical Cores", fmt.Sprintf("%d", i.CPUInfo.LogicalCores)},
		)
	}

	// Memory info
	if i.MemoryInfo != nil {
		records = append(records, []string{"Memory Total", fmt.Sprintf("%d", i.MemoryInfo.TotalBytes)})
	}

	// GPU info
	records = append(records, []string{"CUDA Version", i.CUDAVersion})
	if i.GPUInfo != nil {
		records = append(records,
			[]string{"GPU Driver Version", i.GPUDriverVersion},
			[]string{"GPU Product", i.GPUInfo.Product},
			[]string{"GPU Manufacturer", i.GPUInfo.Manufacturer},
			[]string{"GPU Architecture", i.GPUInfo.Architecture},
			[]string{"GPU Memory", i.GPUInfo.Memory},
		)
	}

	// Network info
	if i.NICInfo != nil {
		for idx, nic := range i.NICInfo.PrivateIPInterfaces {
			records = append(records, []string{
				fmt.Sprintf("Private IP Interface %d", idx+1),
				fmt.Sprintf("%s (%s, %s)", nic.Interface, nic.MAC, nic.IP),
			})
		}
	}

	// Disk info
	if i.DiskInfo != nil {
		records = append(records, []string{"Container Root Disk", i.DiskInfo.ContainerRootDisk})

		// Write block devices info
		for idx, device := range i.DiskInfo.BlockDevices {
			prefix := fmt.Sprintf("Block Device %d", idx+1)
			deviceRecords := [][]string{
				{prefix + " Name", device.Name},
				{prefix + " Type", device.Type},
				{prefix + " Size", fmt.Sprintf("%d", device.Size)},
				{prefix + " Used", fmt.Sprintf("%d", device.Used)},
				{prefix + " Mount Point", device.MountPoint},
				{prefix + " FS Type", device.FSType},
			}
			if device.Serial != "" {
				deviceRecords = append(deviceRecords, []string{prefix + " Serial", device.Serial})
			}
			if device.Model != "" {
				deviceRecords = append(deviceRecords, []string{prefix + " Model", device.Model})
			}
			records = append(records, deviceRecords...)
		}
	}

	// GPU instances (individual GPUs)
	if i.GPUInfo != nil && len(i.GPUInfo.GPUs) > 0 {
		for idx, gpu := range i.GPUInfo.GPUs {
			prefix := fmt.Sprintf("GPU %d", idx+1)
			records = append(records,
				[]string{prefix + " UUID", gpu.UUID},
				[]string{prefix + " Bus ID", gpu.BusID},
				[]string{prefix + " SN", gpu.SN},
				[]string{prefix + " Minor ID", gpu.MinorID},
				[]string{prefix + " Board ID", fmt.Sprintf("%d", gpu.BoardID)},
				[]string{prefix + " VBIOS Version", gpu.VBIOSVersion},
				[]string{prefix + " Chassis SN", gpu.ChassisSN},
			)
		}
	}

	// Write all records at once
	return writer.WriteAll(records)
}

// extractTimeFromInterface extracts time.Time from various time types including metav1.Time
func extractTimeFromInterface(timeValue any) *time.Time {
	// Handle metav1.Time specifically
	if metaTime, ok := timeValue.(metav1.Time); ok {
		return &metaTime.Time
	}

	// Use reflection to check if the value has a Time field (like metav1.Time)
	val := reflect.ValueOf(timeValue)
	if val.Kind() == reflect.Struct {
		timeField := val.FieldByName("Time")
		if timeField.IsValid() && timeField.Type() == reflect.TypeOf(time.Time{}) {
			timeVal := timeField.Interface().(time.Time)
			return &timeVal
		}
	}

	// If it's already a time.Time pointer
	if timePtr, ok := timeValue.(*time.Time); ok {
		return timePtr
	}

	return nil
}
