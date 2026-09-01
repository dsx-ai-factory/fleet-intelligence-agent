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

package precheck

import (
	"context"
	"fmt"
	"testing"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateArchitecture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        Input
		wantPassed   bool
		wantMessages []string
	}{
		{
			name: "passes for hopper",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: true,
		},
		{
			name: "passes for blackwell",
			input: Input{
				GPUInfo:          gpuInfo("Blackwell"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: true,
		},
		{
			name: "passes for rubin",
			input: Input{
				GPUInfo:          gpuInfo("Rubin"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: true,
		},
		{
			name: "passes for ampere",
			input: Input{
				GPUInfo:          gpuInfo("Ampere"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: true,
		},
		{
			name: "passes for ada lovelace",
			input: Input{
				GPUInfo:          gpuInfo("Ada Lovelace"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: true,
		},
		{
			name: "passes for ada-lovelace",
			input: Input{
				GPUInfo:          gpuInfo("Ada-Lovelace"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: true,
		},
		{
			name: "fails for missing gpu",
			input: Input{
				GPUInfo: &apiv1.MachineGPUInfo{},
			},
			wantPassed: false,
			wantMessages: []string{
				"No NVIDIA GPU detected; verify the node has an NVIDIA GPU installed and visible to the OS",
			},
		},
		{
			name: "passes for hopper lowercase",
			input: Input{
				GPUInfo:          gpuInfo("hopper"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: true,
		},
		{
			name: "fails for unsupported architecture",
			input: Input{
				GPUInfo:          gpuInfo("Turing"),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: false,
			wantMessages: []string{
				"Unsupported GPU architecture: Turing; supported architectures are Hopper, Blackwell, Rubin, Ampere, and Ada Lovelace",
			},
		},
		{
			name: "fails for empty architecture",
			input: Input{
				GPUInfo:          gpuInfo(""),
				GPUDriverVersion: "575.57.08",
			},
			wantPassed: false,
			wantMessages: []string{
				"GPU detected, but its architecture could not be determined; verify the NVIDIA driver is installed and loaded",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := Evaluate(&tt.input)

			assert.Equal(t, tt.wantPassed, result.Passed())
			for _, wantMessage := range tt.wantMessages {
				assert.Contains(t, checkMessages(result.Checks), wantMessage)
			}
		})
	}
}

func TestSupportedArchitectures(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t, []string{"Hopper", "Blackwell", "Rubin", "Ampere", "Ada Lovelace"}, SupportedArchitectures())
}

func TestEvaluateDriverAndNVAT(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        Input
		wantPassed   bool
		wantMessages []string
	}{
		{
			name: "fails for missing driver",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "",
				NVAttestPresent:  boolPtr(true),
			},
			wantPassed: false,
			wantMessages: []string{
				"NVIDIA GPU hardware is present, but the NVIDIA driver was not detected; install or load the NVIDIA driver and retry",
			},
		},
		{
			name: "fails for malformed driver version",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "not-a-version",
				NVAttestPresent:  boolPtr(true),
			},
			wantPassed: false,
			wantMessages: []string{
				"failed to parse NVIDIA driver version: failed to parse driver version (expected at least 2 parts): not-a-version",
			},
		},
		{
			name: "fails for driver below minimum major version",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "509.12.01",
				NVAttestPresent:  boolPtr(true),
			},
			wantPassed: false,
			wantMessages: []string{
				"NVIDIA driver version 509.12.01 is below the required minimum 510; upgrade the driver and retry",
			},
		},
		{
			name: "fails for missing nvattest",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "575.57.08",
				NVAttestPresent:  boolPtr(false),
			},
			wantPassed: false,
			wantMessages: []string{
				"nvattest was not found in PATH; install nvattest and ensure it is available in PATH",
			},
		},
		{
			name: "passes when driver major is at minimum and nvattest is present",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "510.47.03",
				NVAttestPresent:  boolPtr(true),
			},
			wantPassed: true,
		},
		{
			name: "passes when newer driver and nvattest are present",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "575.57.08",
				NVAttestPresent:  boolPtr(true),
			},
			wantPassed: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := Evaluate(&tt.input)

			assert.Equal(t, tt.wantPassed, result.Passed())
			for _, wantMessage := range tt.wantMessages {
				assert.Contains(t, checkMessages(result.Checks), wantMessage)
			}
		})
	}
}

func TestEvaluateDetectsHardwareWithoutDriver(t *testing.T) {
	t.Parallel()

	result := Evaluate(&Input{
		GPUHardwarePresent: true,
		NVAttestPresent:    boolPtr(true),
	})

	assert.False(t, result.Passed())
	assert.Contains(t, checkMessages(result.Checks), "NVIDIA GPU detected")
	assert.Contains(t, checkMessages(result.Checks), "GPU architecture check skipped because the NVIDIA driver is not available")
	assert.Contains(t, checkMessages(result.Checks), "NVIDIA GPU hardware is present, but the NVIDIA driver was not detected; install or load the NVIDIA driver and retry")
}

func TestEvaluateNilInput(t *testing.T) {
	t.Parallel()

	result := Evaluate(nil)

	assert.False(t, result.Passed())
	assert.Contains(t, checkMessages(result.Checks), "No NVIDIA GPU detected; verify the node has an NVIDIA GPU installed and visible to the OS")
	assert.Contains(t, checkMessages(result.Checks), "nvattest check skipped")
	assert.Contains(t, checkMessages(result.Checks), "DCGM checks skipped")
}

func TestEvaluateSkipsArchitectureWhenGPUDetailsFail(t *testing.T) {
	t.Parallel()

	result := Evaluate(&Input{
		GPUHardwarePresent: true,
		GPUDriverVersion:   "575.57.08",
		GPUInfoErr:         fmt.Errorf("gpu info failed"),
		NVAttestPresent:    boolPtr(true),
	})

	assert.True(t, findCheck(t, result.Checks, "gpu-present").Passed)
	assert.True(t, findCheck(t, result.Checks, "gpu-driver").Passed)
	assert.Contains(t, checkMessages(result.Checks), "GPU architecture check skipped because GPU details could not be collected; check agent logs and retry")
}

func TestEvaluateReportsGPUProbeFailure(t *testing.T) {
	t.Parallel()

	result := Evaluate(&Input{
		GPUHardwareErr:  fmt.Errorf("lspci unavailable"),
		NVAttestPresent: boolPtr(true),
	})

	assert.False(t, result.Passed())
	assert.Contains(t, checkMessages(result.Checks), "Unable to determine NVIDIA GPU presence; verify lspci is installed and accessible, then retry")
	assert.Contains(t, checkMessages(result.Checks), "NVIDIA driver check skipped because no NVIDIA GPU was detected")
}

func TestEvaluateAggregatesFailures(t *testing.T) {
	t.Parallel()

	result := Evaluate(&Input{
		GPUInfo:          gpuInfo("Ampere"),
		GPUDriverVersion: "",
		NVAttestPresent:  boolPtr(false),
	})

	assert.False(t, result.Passed())
	assert.Contains(t, checkMessages(result.Checks), "GPU architecture check skipped because the NVIDIA driver is not available")
	assert.Contains(t, checkMessages(result.Checks), "NVIDIA GPU hardware is present, but the NVIDIA driver was not detected; install or load the NVIDIA driver and retry")
	assert.Contains(t, checkMessages(result.Checks), "nvattest was not found in PATH; install nvattest and ensure it is available in PATH")
}

func TestEvaluateDCGM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        Input
		wantPassed   bool
		wantMessages []string
	}{
		{
			name: "fails when dcgm is unreachable",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "575.57.08",
				NVAttestPresent:  boolPtr(true),
				DCGMReachable:    boolPtr(false),
			},
			wantPassed: false,
			wantMessages: []string{
				"DCGM HostEngine is not reachable; verify DCGM is running and DCGM_URL is configured correctly",
			},
		},
		{
			name: "fails when dcgm version is too old",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "575.57.08",
				NVAttestPresent:  boolPtr(true),
				DCGMReachable:    boolPtr(true),
				DCGMVersion:      "4.2.2",
			},
			wantPassed: false,
			wantMessages: []string{
				"DCGM HostEngine version 4.2.2 is below the required minimum 4.2.3; upgrade DCGM and retry",
			},
		},
		{
			name: "passes for minimum supported dcgm version",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "575.57.08",
				NVAttestPresent:  boolPtr(true),
				DCGMReachable:    boolPtr(true),
				DCGMVersion:      "4.2.3",
			},
			wantPassed: true,
		},
		{
			name: "passes for newer dcgm version",
			input: Input{
				GPUInfo:          gpuInfo("Hopper"),
				GPUDriverVersion: "575.57.08",
				NVAttestPresent:  boolPtr(true),
				DCGMReachable:    boolPtr(true),
				DCGMVersion:      "4.3.0",
			},
			wantPassed: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := Evaluate(&tt.input)

			assert.Equal(t, tt.wantPassed, result.Passed())
			for _, wantMessage := range tt.wantMessages {
				assert.Contains(t, checkMessages(result.Checks), wantMessage)
			}
		})
	}
}

func TestEvaluateDCGMSkipsWhenReachabilityUnset(t *testing.T) {
	t.Parallel()

	result := Evaluate(&Input{
		GPUInfo:          gpuInfo("Hopper"),
		GPUDriverVersion: "575.57.08",
		NVAttestPresent:  boolPtr(true),
	})

	assert.True(t, result.Passed())
	assert.Contains(t, checkMessages(result.Checks), "DCGM checks skipped")
}

func TestCollectInputCallsDCGMDetection(t *testing.T) {
	originalCollectGPUInventory := collectGPUInventory
	originalLookPath := lookPath
	originalDetectDCGMVersion := detectDCGMVersion
	originalListPCIGPUs := listPCIGPUs
	t.Cleanup(func() {
		collectGPUInventory = originalCollectGPUInventory
		lookPath = originalLookPath
		detectDCGMVersion = originalDetectDCGMVersion
		listPCIGPUs = originalListPCIGPUs
	})

	collectGPUInventory = func() (*apiv1.MachineGPUInfo, string, error) {
		return nil, "", fmt.Errorf("skip DCGM inventory in test")
	}
	lookPath = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}

	detectDCGMCalled := false
	detectDCGMVersion = func() (string, error) {
		detectDCGMCalled = true
		return "4.2.3", nil
	}

	listPCIGPUs = func(_ context.Context) ([]string, error) {
		return []string{"0000:00:00.0 3D controller: NVIDIA Corporation Test GPU [10de:ffff]"}, nil
	}

	input, err := CollectInput()

	require.NoError(t, err)
	assert.True(t, input.GPUHardwarePresent)
	require.NotNil(t, input.DCGMReachable)
	assert.True(t, *input.DCGMReachable)
	assert.Equal(t, "4.2.3", input.DCGMVersion)
	assert.True(t, detectDCGMCalled)
}

func TestCollectInputPreservesGPUProbeError(t *testing.T) {
	originalCollectGPUInventory := collectGPUInventory
	originalLookPath := lookPath
	originalDetectDCGMVersion := detectDCGMVersion
	originalListPCIGPUs := listPCIGPUs
	t.Cleanup(func() {
		collectGPUInventory = originalCollectGPUInventory
		lookPath = originalLookPath
		detectDCGMVersion = originalDetectDCGMVersion
		listPCIGPUs = originalListPCIGPUs
	})

	collectGPUInventory = func() (*apiv1.MachineGPUInfo, string, error) {
		return nil, "", fmt.Errorf("skip DCGM inventory in test")
	}
	lookPath = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	detectDCGMVersion = func() (string, error) {
		return "4.2.3", nil
	}
	listPCIGPUs = func(_ context.Context) ([]string, error) {
		return nil, fmt.Errorf("lspci unavailable")
	}

	input, err := CollectInput()

	require.NoError(t, err)
	assert.False(t, input.GPUHardwarePresent)
	require.Error(t, input.GPUHardwareErr)
	assert.Contains(t, input.GPUHardwareErr.Error(), "lspci unavailable")
}

func gpuInfo(architecture string) *apiv1.MachineGPUInfo {
	return &apiv1.MachineGPUInfo{
		Architecture: architecture,
		Product:      "test-gpu",
		GPUs: []apiv1.MachineGPUInstance{
			{UUID: "GPU-1"},
		},
	}
}

func findCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()

	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}

	t.Fatalf("check %q not found", name)
	return Check{}
}

func checkMessages(checks []Check) []string {
	messages := make([]string, 0, len(checks))
	for _, check := range checks {
		messages = append(messages, check.Message)
	}
	return messages
}

func boolPtr(v bool) *bool {
	return &v
}
