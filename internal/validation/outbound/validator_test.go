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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/converter"
)

func TestValidateNodeUpsertRequest(t *testing.T) {
	now := time.Now().UTC()
	req := &backendclient.NodeUpsertRequest{
		Hostname:   "",
		MachineID:  "machine-1",
		SystemUUID: "not-a-uuid",
		Uptime:     &now,
		AgentConfig: backendclient.AgentConfig{
			MetricScrapeIntervalSeconds: -1,
		},
		Resources: backendclient.NodeResources{
			CPUInfo: backendclient.CPUInfo{LogicalCores: "-1"},
			MemoryInfo: backendclient.MemoryInfo{
				TotalBytes: "invalid",
			},
			DiskInfo: backendclient.DiskInfo{
				BlockDevices: []backendclient.BlockDevice{{
					Name: "disk0",
					Size: -1,
				}},
			},
			NICInfo: backendclient.NICInfo{
				PrivateIPInterfaces: []backendclient.NICInterface{{
					IP: "invalid-ip",
				}},
			},
		},
	}

	issues := ValidateNodeUpsertRequest(req)
	require.NotEmpty(t, issues)
	require.Contains(t, issues, Issue{
		Severity: SeverityCritical,
		Category: "identity",
		Field:    "hostname",
		Message:  "field is required",
	})
	require.Contains(t, issues, Issue{
		Severity: SeverityWarning,
		Category: "numeric",
		Field:    "agentConfig.metricScrapeIntervalSeconds",
		Message:  "value is negative",
	})
	require.Contains(t, issues, Issue{
		Severity: SeverityCritical,
		Category: "numeric",
		Field:    "resources.diskInfo.blockDevices[0].size",
		Message:  "disk size cannot be negative",
	})
}

func TestValidateNodeUpsertRequest_GPUUUID(t *testing.T) {
	t.Run("accepts NVIDIA GPU UUID format", func(t *testing.T) {
		req := &backendclient.NodeUpsertRequest{
			Hostname:   "host",
			MachineID:  "machine-1",
			SystemUUID: "4c4c4544-004a-3410-804c-c7c04f313434",
			Resources: backendclient.NodeResources{
				GPUInfo: backendclient.GPUInfo{
					GPUs: []backendclient.GPUDevice{{
						UUID: "GPU-b1631f34-4ee6-b7a8-5afc-00c88d0fcae1",
					}},
				},
			},
		}

		for _, issue := range ValidateNodeUpsertRequest(req) {
			require.NotEqual(t, "resources.gpuInfo.gpus[0].uuid", issue.Field,
				"unexpected GPU UUID validation issue: %+v", issue)
		}
	})

	t.Run("rejects malformed GPU UUID", func(t *testing.T) {
		req := &backendclient.NodeUpsertRequest{
			Hostname:   "host",
			MachineID:  "machine-1",
			SystemUUID: "4c4c4544-004a-3410-804c-c7c04f313434",
			Resources: backendclient.NodeResources{
				GPUInfo: backendclient.GPUInfo{
					GPUs: []backendclient.GPUDevice{{
						UUID: "GPU-not-a-valid-uuid",
					}},
				},
			},
		}

		issues := ValidateNodeUpsertRequest(req)
		require.Contains(t, issues, Issue{
			Severity: SeverityWarning,
			Category: "format",
			Field:    "resources.gpuInfo.gpus[0].uuid",
			Message:  "invalid GPU UUID format",
		})
	})
}

func TestValidateAttestationRequest(t *testing.T) {
	req := &backendclient.AttestationRequest{
		AttestationData: backendclient.AttestationData{
			Success: true,
			SDKResponse: backendclient.AttestationSDKResponse{
				ResultCode: 200,
			},
		},
	}

	issues := ValidateAttestationRequest(req)
	require.NotEmpty(t, issues)
	require.Contains(t, issues, Issue{
		Severity: SeverityWarning,
		Category: "required",
		Field:    "attestationData.nonceRefreshTimestamp",
		Message:  "nonce refresh timestamp is required",
	})
	require.Contains(t, issues, Issue{
		Severity: SeverityWarning,
		Category: "required",
		Field:    "attestationData.sdkResponse.evidences",
		Message:  "evidence is required when success=true",
	})
	for _, issue := range issues {
		require.Equal(t, SeverityWarning, issue.Severity)
	}
}

func TestValidateOTLPPayload(t *testing.T) {
	payload := &converter.OTLPData{
		Metrics: &metricsv1.MetricsData{
			ResourceMetrics: []*metricsv1.ResourceMetrics{{
				Resource: &resourcev1.Resource{
					Attributes: []*commonv1.KeyValue{
						{
							Key: "service.name",
							Value: &commonv1.AnyValue{
								Value: &commonv1.AnyValue_StringValue{StringValue: "fleet-intelligence-agent"},
							},
						},
					},
				},
				ScopeMetrics: []*metricsv1.ScopeMetrics{{
					Metrics: []*metricsv1.Metric{{
						Name: "bad_metric",
						Data: &metricsv1.Metric_Gauge{
							Gauge: &metricsv1.Gauge{
								DataPoints: []*metricsv1.NumberDataPoint{{
									TimeUnixNano: 0,
									Value: &metricsv1.NumberDataPoint_AsDouble{
										AsDouble: math.NaN(),
									},
								}},
							},
						},
					}},
				}},
			}},
		},
		Logs: &logsv1.LogsData{
			ResourceLogs: []*logsv1.ResourceLogs{{
				Resource: &resourcev1.Resource{},
			}},
		},
	}

	issues := ValidateOTLPPayload(payload)
	require.NotEmpty(t, issues)
	require.Contains(t, issues, Issue{
		Severity: SeverityCritical,
		Category: "required",
		Field:    "metrics.resourceMetrics[0].resource.machine.id",
		Message:  "machine.id is required",
	})
	require.Contains(t, issues, Issue{
		Severity: SeverityCritical,
		Category: "numeric",
		Field:    "metrics.resourceMetrics[0].scopeMetrics[0].metrics[0].gauge.dataPoints[0].value",
		Message:  "metric value is NaN or Inf",
	})
}
