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

package collector

import (
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

func TestNewEntityCatalog(t *testing.T) {
	cliqueID := uint32(0)
	bootTime := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	info := &machineinfo.MachineInfo{
		Hostname:         "gpu-node-01",
		GPUDriverVersion: "575.57.08",
		CUDAVersion:      "12.9",
		KernelVersion:    "6.14.0-27-generic",
		Uptime:           metav1.NewTime(bootTime),
		GPUInfo: &apiv1.MachineGPUInfo{
			Product:      "fallback-model",
			Architecture: "hopper",
			GPUs: []apiv1.MachineGPUInstance{
				{
					UUID:         "GPU-abc",
					GPUIndex:     "7",
					BusID:        "0000:01:00.0",
					MinorID:      "2",
					ModelName:    "NVIDIA H100",
					SN:           "GPU-SERIAL-123",
					VBIOSVersion: "97.00.82.00.5F",
					ClusterUUID:  "11111111-2222-3333-4444-555555555555",
					CliqueID:     &cliqueID,
				},
				{
					UUID:     "GPU-def",
					GPUIndex: "8",
					MinorID:  "-1",
				},
			},
		},
	}

	catalog := NewEntityCatalog(info, map[string]string{"GPU-abc": "0"})
	require.NotNil(t, catalog)
	assert.Equal(t, "gpu-node-01", catalog.Hostname)
	assert.Equal(t, "575.57.08", catalog.GPUDriverVersion)
	assert.Equal(t, "12.9", catalog.CUDADriverVersion)
	assert.Equal(t, "6.14.0-27-generic", catalog.KernelVersion)
	assert.Equal(t, bootTime, catalog.BootTime)
	assert.Equal(t, GPUIdentity{
		UUID:         "GPU-abc",
		GPU:          "0",
		PCIBusID:     "0000:01:00.0",
		Device:       "nvidia2",
		ModelName:    "NVIDIA H100",
		Architecture: "hopper",
		GPUSerial:    "GPU-SERIAL-123",
		VBIOSVersion: "97.00.82.00.5F",
		ClusterUUID:  "11111111-2222-3333-4444-555555555555",
		CliqueID:     "0",
	}, catalog.GPUsByUUID["GPU-abc"])
	assert.Equal(t, "fallback-model", catalog.GPUsByUUID["GPU-def"].ModelName)
	assert.Equal(t, "hopper", catalog.GPUsByUUID["GPU-def"].Architecture)
	assert.Empty(t, catalog.GPUsByUUID["GPU-def"].GPUSerial)
	assert.Empty(t, catalog.GPUsByUUID["GPU-def"].Device)
	assert.Equal(t, "GPU-abc", catalog.GPUUUIDByIndex["0"])
	assert.Equal(t, "GPU-def", catalog.GPUUUIDByIndex["8"])
}
