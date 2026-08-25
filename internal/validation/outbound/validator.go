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

package outbound

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	"github.com/google/uuid"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/converter"
)

type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Issue struct {
	Severity Severity
	Category string
	Field    string
	Message  string
}

// LogIssues emits validation findings and does not block send/export workflows.
func LogIssues(component, payload string, issues []Issue, extraFields ...any) {
	for _, issue := range issues {
		fields := []any{
			"component", component,
			"payload", payload,
			"severity", issue.Severity,
			"category", issue.Category,
			"field", issue.Field,
			"message", issue.Message,
		}
		fields = append(fields, extraFields...)

		if issue.Severity == SeverityCritical {
			log.Logger.Errorw("outbound validation issue", fields...)
		} else {
			log.Logger.Warnw("outbound validation issue", fields...)
		}
	}
}

func ValidateNodeUpsertRequest(req *backendclient.NodeUpsertRequest) []Issue {
	issues := make([]Issue, 0)
	if req == nil {
		return append(issues, newIssue(SeverityCritical, "required", "payload", "node upsert request is nil"))
	}

	requireNonEmpty(&issues, "identity", "hostname", req.Hostname, SeverityCritical)
	requireNonEmpty(&issues, "identity", "machineId", req.MachineID, SeverityCritical)
	requireNonEmpty(&issues, "identity", "systemUUID", req.SystemUUID, SeverityCritical)

	validateTimeSanity(&issues, "time", "uptime", req.Uptime)
	validateTimeSanity(&issues, "time", "enrolledAt", req.EnrolledAt)

	validateLen(&issues, "length", "hostname", req.Hostname, 255)
	validateLen(&issues, "length", "machineId", req.MachineID, 255)
	validateLen(&issues, "length", "systemUUID", req.SystemUUID, 255)
	validateLen(&issues, "length", "bootID", req.BootID, 255)
	validateLen(&issues, "length", "kernelVersion", req.KernelVersion, 255)
	validateLen(&issues, "length", "osImage", req.OSImage, 1024)
	validateLen(&issues, "length", "agentVersion", req.AgentVersion, 255)
	validateIP(&issues, "format", "netPrivateIP", req.NetPrivateIP)
	validateLen(&issues, "length", "nodeGroup", req.NodeGroup, 255)
	validateLen(&issues, "length", "computeZone", req.ComputeZone, 255)

	validateNonNegative(&issues, "numeric", "agentConfig.totalComponents", req.AgentConfig.TotalComponents)
	validateNonNegative(&issues, "numeric", "agentConfig.retentionPeriodSeconds", req.AgentConfig.RetentionPeriodSeconds)
	validateNonNegative(&issues, "numeric", "agentConfig.metricScrapeIntervalSeconds", req.AgentConfig.MetricScrapeIntervalSeconds)
	validateNonNegative(&issues, "numeric", "agentConfig.inventoryIntervalSeconds", req.AgentConfig.InventoryIntervalSeconds)
	validateNonNegative(&issues, "numeric", "agentConfig.attestationIntervalSeconds", req.AgentConfig.AttestationIntervalSeconds)

	validateSliceLimit(&issues, "cardinality", "resources.gpuInfo.gpus", len(req.Resources.GPUInfo.GPUs), 64)
	validateSliceLimit(&issues, "cardinality", "resources.diskInfo.blockDevices", len(req.Resources.DiskInfo.BlockDevices), 512)
	validateSliceLimit(&issues, "cardinality", "resources.nicInfo.privateIPInterfaces", len(req.Resources.NICInfo.PrivateIPInterfaces), 128)

	seenGPUUUID := make(map[string]struct{}, len(req.Resources.GPUInfo.GPUs))
	for i, gpu := range req.Resources.GPUInfo.GPUs {
		prefix := fmt.Sprintf("resources.gpuInfo.gpus[%d]", i)
		validateLen(&issues, "length", prefix+".uuid", gpu.UUID, 255)
		validateLen(&issues, "length", prefix+".gpuIndex", gpu.GPUIndex, 64)
		validateLen(&issues, "length", prefix+".clusterUUID", gpu.ClusterUUID, 255)
		validateGPUUUID(&issues, "format", prefix+".uuid", gpu.UUID)
		if gpu.UUID != "" {
			if _, exists := seenGPUUUID[gpu.UUID]; exists {
				issues = append(issues, newIssue(SeverityWarning, "dedup", prefix+".uuid", "duplicate GPU UUID in payload"))
			}
			seenGPUUUID[gpu.UUID] = struct{}{}
		}
	}

	for i, disk := range req.Resources.DiskInfo.BlockDevices {
		prefix := fmt.Sprintf("resources.diskInfo.blockDevices[%d]", i)
		validateLen(&issues, "length", prefix+".name", disk.Name, 2048)
		validateLen(&issues, "length", prefix+".mountPoint", disk.MountPoint, 1024)
		validateLen(&issues, "length", prefix+".type", disk.Type, 64)
		validateLen(&issues, "length", prefix+".fsType", disk.FSType, 64)
		if disk.Size < 0 {
			issues = append(issues, newIssue(SeverityCritical, "numeric", prefix+".size", "disk size cannot be negative"))
		}
		validateSliceLimit(&issues, "cardinality", prefix+".parents", len(disk.Parents), 64)
	}

	for i, nic := range req.Resources.NICInfo.PrivateIPInterfaces {
		prefix := fmt.Sprintf("resources.nicInfo.privateIPInterfaces[%d]", i)
		validateLen(&issues, "length", prefix+".interface", nic.Interface, 255)
		validateLen(&issues, "length", prefix+".mac", nic.MAC, 64)
		validateIP(&issues, "format", prefix+".ip", nic.IP)
	}

	if req.Resources.CPUInfo.LogicalCores != "" {
		cores, err := strconv.ParseInt(req.Resources.CPUInfo.LogicalCores, 10, 64)
		if err != nil || cores < 0 {
			issues = append(issues, newIssue(SeverityCritical, "numeric", "resources.cpuInfo.logicalCores", "logicalCores must be a non-negative integer string"))
		}
	}

	if req.Resources.MemoryInfo.TotalBytes != "" {
		if _, err := strconv.ParseUint(req.Resources.MemoryInfo.TotalBytes, 10, 64); err != nil {
			issues = append(issues, newIssue(SeverityCritical, "numeric", "resources.memoryInfo.totalBytes", "totalBytes must be an unsigned integer string"))
		}
	}

	return issues
}

func ValidateAttestationRequest(req *backendclient.AttestationRequest) []Issue {
	issues := make([]Issue, 0)
	if req == nil {
		return append(issues, newIssue(SeverityWarning, "required", "payload", "attestation request is nil"))
	}

	if req.AttestationData.NonceRefreshTimestamp.IsZero() {
		issues = append(issues, newIssue(SeverityWarning, "required", "attestationData.nonceRefreshTimestamp", "nonce refresh timestamp is required"))
	}
	validateLen(&issues, "length", "attestationData.errorMessage", req.AttestationData.ErrorMessage, 4096)
	validateLen(&issues, "length", "attestationData.sdkResponse.resultMessage", req.AttestationData.SDKResponse.ResultMessage, 2048)

	if req.AttestationData.Success && len(req.AttestationData.SDKResponse.Evidences) == 0 {
		issues = append(issues, newIssue(SeverityWarning, "required", "attestationData.sdkResponse.evidences", "evidence is required when success=true"))
	}

	validateSliceLimit(&issues, "cardinality", "attestationData.sdkResponse.evidences", len(req.AttestationData.SDKResponse.Evidences), 128)
	for i, e := range req.AttestationData.SDKResponse.Evidences {
		prefix := fmt.Sprintf("attestationData.sdkResponse.evidences[%d]", i)
		validateLen(&issues, "length", prefix+".arch", e.Arch, 64)
		validateLen(&issues, "length", prefix+".driverVersion", e.DriverVersion, 64)
		validateLen(&issues, "length", prefix+".nonce", e.Nonce, 512)
		validateLen(&issues, "length", prefix+".version", e.Version, 64)
		validateLen(&issues, "length", prefix+".certificate", e.Certificate, 4*1024*1024)
		validateLen(&issues, "length", prefix+".evidence", e.Evidence, 4*1024*1024)
	}

	return issues
}

func ValidateOTLPPayload(data *converter.OTLPData) []Issue {
	issues := make([]Issue, 0)
	if data == nil {
		return append(issues, newIssue(SeverityCritical, "required", "payload", "otlp payload is nil"))
	}

	if data.Metrics != nil {
		validateOTLPMetrics(&issues, data.Metrics)
	}
	if data.Logs != nil {
		validateOTLPLogs(&issues, data.Logs)
	}
	if data.Metrics == nil && data.Logs == nil {
		issues = append(issues, newIssue(SeverityWarning, "required", "payload", "otlp payload has neither metrics nor logs"))
	}
	return issues
}

func validateOTLPMetrics(issues *[]Issue, m *metricsv1.MetricsData) {
	for i, rm := range m.GetResourceMetrics() {
		base := fmt.Sprintf("metrics.resourceMetrics[%d]", i)
		validateOTLPResourceAttrs(issues, base+".resource", rm.GetResource().GetAttributes())
		for j, sm := range rm.GetScopeMetrics() {
			for k, metric := range sm.GetMetrics() {
				prefix := fmt.Sprintf("%s.scopeMetrics[%d].metrics[%d]", base, j, k)
				switch v := metric.GetData().(type) {
				case *metricsv1.Metric_Gauge:
					for dIdx, dp := range v.Gauge.GetDataPoints() {
						validateNumberDataPoint(issues, fmt.Sprintf("%s.gauge.dataPoints[%d]", prefix, dIdx), dp)
					}
				case *metricsv1.Metric_Sum:
					for dIdx, dp := range v.Sum.GetDataPoints() {
						validateNumberDataPoint(issues, fmt.Sprintf("%s.sum.dataPoints[%d]", prefix, dIdx), dp)
					}
				}
			}
		}
	}
}

func validateOTLPLogs(issues *[]Issue, logsData *logsv1.LogsData) {
	for i, rl := range logsData.GetResourceLogs() {
		base := fmt.Sprintf("logs.resourceLogs[%d]", i)
		validateOTLPResourceAttrs(issues, base+".resource", rl.GetResource().GetAttributes())
		for j, sl := range rl.GetScopeLogs() {
			for k, rec := range sl.GetLogRecords() {
				prefix := fmt.Sprintf("%s.scopeLogs[%d].logRecords[%d]", base, j, k)
				if rec.GetTimeUnixNano() == 0 {
					*issues = append(*issues, newIssue(SeverityWarning, "time", prefix+".timeUnixNano", "log timestamp is zero"))
				}
				if rec.GetBody() == nil {
					*issues = append(*issues, newIssue(SeverityWarning, "required", prefix+".body", "log body is empty"))
				}
			}
		}
	}
}

func validateNumberDataPoint(issues *[]Issue, prefix string, dp *metricsv1.NumberDataPoint) {
	if dp.GetTimeUnixNano() == 0 {
		*issues = append(*issues, newIssue(SeverityWarning, "time", prefix+".timeUnixNano", "metric timestamp is zero"))
	}
	if v, ok := dp.GetValue().(*metricsv1.NumberDataPoint_AsDouble); ok {
		if math.IsNaN(v.AsDouble) || math.IsInf(v.AsDouble, 0) {
			*issues = append(*issues, newIssue(SeverityCritical, "numeric", prefix+".value", "metric value is NaN or Inf"))
		}
	}
}

func validateOTLPResourceAttrs(issues *[]Issue, field string, attrs []*commonv1.KeyValue) {
	serviceName := lookupAttr(attrs, "service.name")
	if strings.TrimSpace(serviceName) == "" {
		*issues = append(*issues, newIssue(SeverityCritical, "required", field+".service.name", "service.name is required"))
	}
	machineID := lookupAttr(attrs, "machine.id")
	if strings.TrimSpace(machineID) == "" {
		*issues = append(*issues, newIssue(SeverityCritical, "required", field+".machine.id", "machine.id is required"))
	}
}

func lookupAttr(attrs []*commonv1.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.GetKey() == key {
			return kv.GetValue().GetStringValue()
		}
	}
	return ""
}

func newIssue(severity Severity, category, field, message string) Issue {
	return Issue{
		Severity: severity,
		Category: category,
		Field:    field,
		Message:  message,
	}
}

func requireNonEmpty(issues *[]Issue, category, field, value string, severity Severity) {
	if strings.TrimSpace(value) == "" {
		*issues = append(*issues, newIssue(severity, category, field, "field is required"))
	}
}

func validateNonNegative(issues *[]Issue, category, field string, value int64) {
	if value < 0 {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, "value is negative"))
	}
}

func validateLen(issues *[]Issue, category, field, value string, max int) {
	if max <= 0 {
		return
	}
	if len(value) > max {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, fmt.Sprintf("length %d exceeds max %d", len(value), max)))
	}
}

func validateSliceLimit(issues *[]Issue, category, field string, size, max int) {
	if max <= 0 {
		return
	}
	if size > max {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, fmt.Sprintf("size %d exceeds max %d", size, max)))
	}
}

func validateIP(issues *[]Issue, category, field, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if net.ParseIP(strings.TrimSpace(value)) == nil {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, "invalid IP format"))
	}
}

// validateGPUUUID checks NVIDIA GPU UUIDs as returned by NVML/nvidia-smi (e.g.
// "GPU-b1631f34-4ee6-b7a8-5afc-00c88d0fcae1"). The "GPU-" prefix is optional;
// the remainder must be a valid RFC 4122 UUID.
func validateGPUUUID(issues *[]Issue, category, field, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	uuidPart := value
	if strings.HasPrefix(value, "GPU-") {
		uuidPart = strings.TrimPrefix(value, "GPU-")
	}
	if _, err := uuid.Parse(uuidPart); err != nil {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, "invalid GPU UUID format"))
	}
}

func validateTimeSanity(issues *[]Issue, category, field string, t *time.Time) {
	if t == nil || t.IsZero() {
		return
	}
	if t.Location() != time.UTC {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, "timestamp is not UTC"))
	}
	now := time.Now().UTC()
	if t.After(now.Add(24 * time.Hour)) {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, "timestamp is too far in future"))
	}
	if t.Before(now.Add(-100 * 365 * 24 * time.Hour)) {
		*issues = append(*issues, newIssue(SeverityWarning, category, field, "timestamp is too far in past"))
	}
}
