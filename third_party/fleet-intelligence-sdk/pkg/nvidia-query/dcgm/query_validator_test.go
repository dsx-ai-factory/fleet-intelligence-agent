// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

package dcgm

import (
	"fmt"
	"testing"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

// Note: Tests for CheckSentinel() and CheckSentinelV2() require actual DCGM library
// and GPU hardware to create proper FieldValue_v1/v2 instances. These are tested
// through integration tests and real usage in components.

func TestSentinelType_ShouldRetry(t *testing.T) {
	tests := []struct {
		name     string
		sentinel SentinelType
		want     bool
	}{
		{"BLANK should retry", SentinelBlank, true},
		{"NOT_FOUND should not retry", SentinelNotFound, false},
		{"NOT_SUPPORTED should not retry", SentinelNotSupported, false},
		{"NOT_PERMISSIONED should not retry", SentinelNotPermissioned, false},
		{"None should not retry", SentinelNone, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sentinel.ShouldRetry(); got != tt.want {
				t.Errorf("SentinelType.ShouldRetry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSentinelType_String(t *testing.T) {
	tests := []struct {
		name     string
		sentinel SentinelType
		want     string
	}{
		{"BLANK", SentinelBlank, "BLANK (no data yet)"},
		{"NOT_FOUND", SentinelNotFound, "NOT_FOUND (entity missing)"},
		{"NOT_SUPPORTED", SentinelNotSupported, "NOT_SUPPORTED (hardware doesn't support)"},
		{"NOT_PERMISSIONED", SentinelNotPermissioned, "NOT_PERMISSIONED (hostengine lacks CAP_SYS_ADMIN)"},
		{"None", SentinelNone, "OK"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sentinel.String(); got != tt.want {
				t.Errorf("SentinelType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsUnhealthyAPIError(t *testing.T) {
	tests := []struct {
		name string
		code int32
		want bool
	}{
		{"nvml error is unhealthy", dcgm.DCGM_ST_NVML_ERROR, true},
		{"gpu lost is unhealthy", dcgm.DCGM_ST_GPU_IS_LOST, true},
		{"reset required is unhealthy", dcgm.DCGM_ST_RESET_REQUIRED, true},
		{"gpu not supported is unhealthy", dcgm.DCGM_ST_GPU_NOT_SUPPORTED, true},
		{"dcgm timeout is degraded", dcgm.DCGM_ST_TIMEOUT, false},
		{"nvml driver timeout is degraded", dcgm.DCGM_ST_NVML_DRIVER_TIMEOUT, false},
		{"stale data is not unhealthy", dcgm.DCGM_ST_STALE_DATA, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("dcgm request failed with error code %d", tt.code)
			if got := IsUnhealthyAPIError(err); got != tt.want {
				t.Errorf("IsUnhealthyAPIError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecoveryErrorClassifiers(t *testing.T) {
	gpuLost := fmt.Errorf("field failed with error code %d", dcgm.DCGM_ST_GPU_IS_LOST)
	resetRequired := fmt.Errorf("field failed with error code %d", dcgm.DCGM_ST_RESET_REQUIRED)
	other := fmt.Errorf("field failed with error code %d", dcgm.DCGM_ST_TIMEOUT)

	if !IsGPULostError(gpuLost) || IsGPULostError(resetRequired) || IsGPULostError(other) {
		t.Fatal("IsGPULostError did not distinguish GPU-lost status")
	}
	if !IsResetRequiredError(resetRequired) || IsResetRequiredError(gpuLost) || IsResetRequiredError(other) {
		t.Fatal("IsResetRequiredError did not distinguish reset-required status")
	}
}
