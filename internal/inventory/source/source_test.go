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

package source

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/inventory"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

type fakeMachineInfoCollector struct {
	info *machineinfo.MachineInfo
	err  error
}

func (f fakeMachineInfoCollector) Collect(context.Context) (*machineinfo.MachineInfo, error) {
	return f.info, f.err
}

func TestMachineInfoSourceCollect(t *testing.T) {
	bootTime := time.Date(2026, 4, 28, 12, 30, 0, 0, time.UTC)
	cliqueID := uint32(0)
	src := NewMachineInfoSource(fakeMachineInfoCollector{
		info: &machineinfo.MachineInfo{
			AgentVersion:            "1.2.3",
			GPUDriverVersion:        "550.54.15",
			CUDAVersion:             "12.4",
			DCGMVersion:             "4.2.3",
			ContainerRuntimeVersion: "containerd://1.7.13",
			KernelVersion:           "6.5.0",
			OSImage:                 "Ubuntu 22.04",
			OperatingSystem:         "linux",
			SystemUUID:              "system-uuid",
			MachineID:               "machine-id",
			BootID:                  "boot-id",
			Uptime:                  metav1.NewTime(bootTime),
			Hostname:                "host-a",
			CPUInfo: &apiv1.MachineCPUInfo{
				Type:         "Xeon",
				Manufacturer: "Intel",
				Architecture: "x86_64",
				LogicalCores: 64,
			},
			MemoryInfo: &apiv1.MachineMemoryInfo{
				TotalBytes: 1024,
			},
			GPUInfo: &apiv1.MachineGPUInfo{
				Product:      "H100",
				Manufacturer: "NVIDIA",
				Architecture: "Hopper",
				Memory:       "80GB",
				GPUs: []apiv1.MachineGPUInstance{{
					UUID:         "GPU-1",
					GPUIndex:     "0",
					BusID:        "0000:01:00.0",
					ClusterUUID:  "11111111-2222-3333-4444-555555555555",
					CliqueID:     &cliqueID,
					SN:           "serial",
					MinorID:      "1",
					BoardID:      7,
					VBIOSVersion: "vbios",
					ChassisSN:    "chassis",
				}},
			},
			DiskInfo: &apiv1.MachineDiskInfo{
				ContainerRootDisk: "/dev/nvme0n1",
				BlockDevices: []apiv1.MachineDiskDevice{{
					Name:       "nvme0n1",
					Type:       "disk",
					Size:       2048,
					WWN:        "wwn",
					MountPoint: "/",
					FSType:     "ext4",
					PartUUID:   "part-uuid",
					Parents:    []string{"parent0"},
				}},
			},
			NICInfo: &apiv1.MachineNICInfo{
				PrivateIPInterfaces: []apiv1.MachineNetworkInterface{{
					Interface: "eth0",
					MAC:       "00:11:22:33:44:55",
					IP:        "10.0.0.10",
				}},
			},
		},
	})

	snap, err := src.Collect(context.Background())
	require.NoError(t, err)
	require.NotNil(t, snap)
	require.Equal(t, "machine-id", snap.MachineID)
	require.Equal(t, "host-a", snap.Hostname)
	require.Equal(t, bootTime, snap.Uptime)
	require.Equal(t, "10.0.0.10", snap.NetPrivateIP)
	require.Equal(t, "Xeon", snap.Resources.CPUInfo.Type)
	require.Equal(t, uint64(1024), snap.Resources.MemoryInfo.TotalBytes)
	require.Equal(t, "H100", snap.Resources.GPUInfo.Product)
	require.Len(t, snap.Resources.GPUInfo.GPUs, 1)
	require.Equal(t, 7, snap.Resources.GPUInfo.GPUs[0].BoardID)
	require.Equal(t, "11111111-2222-3333-4444-555555555555", snap.Resources.GPUInfo.GPUs[0].ClusterUUID)
	require.NotNil(t, snap.Resources.GPUInfo.GPUs[0].CliqueID)
	require.Equal(t, uint32(0), *snap.Resources.GPUInfo.GPUs[0].CliqueID)
	require.Equal(t, "/dev/nvme0n1", snap.Resources.DiskInfo.ContainerRootDisk)
	require.Len(t, snap.Resources.DiskInfo.BlockDevices, 1)
	require.Equal(t, "eth0", snap.Resources.NICInfo.PrivateIPInterfaces[0].Interface)
}

func TestMachineInfoSourceCollectWithAgentConfig(t *testing.T) {
	src := NewMachineInfoSourceWithAgentConfig(
		fakeMachineInfoCollector{
			info: &machineinfo.MachineInfo{
				MachineID:  "machine-id",
				SystemUUID: "system-uuid",
				Hostname:   "host-a",
			},
		},
		&inventory.AgentConfig{
			TotalComponents:             42,
			RetentionPeriodSeconds:      86400,
			MetricScrapeIntervalSeconds: 60,
			EnabledComponents:           []string{"cpu", "gpu"},
			DisabledComponents:          []string{"disk"},
			InventoryEnabled:            true,
			InventoryIntervalSeconds:    3600,
			AttestationEnabled:          true,
			AttestationIntervalSeconds:  86400,
		},
	)

	snap, err := src.Collect(context.Background())
	require.NoError(t, err)
	require.NotNil(t, snap)
	require.Equal(t, "machine-id", snap.MachineID)
	require.Equal(t, int64(42), snap.AgentConfig.TotalComponents)
	require.Equal(t, int64(86400), snap.AgentConfig.RetentionPeriodSeconds)
	require.Equal(t, int64(60), snap.AgentConfig.MetricScrapeIntervalSeconds)
	require.Equal(t, []string{"cpu", "gpu"}, snap.AgentConfig.EnabledComponents)
	require.Equal(t, []string{"disk"}, snap.AgentConfig.DisabledComponents)
	require.True(t, snap.AgentConfig.InventoryEnabled)
	require.Equal(t, int64(3600), snap.AgentConfig.InventoryIntervalSeconds)
	require.True(t, snap.AgentConfig.AttestationEnabled)
	require.Equal(t, int64(86400), snap.AgentConfig.AttestationIntervalSeconds)
}

func TestMachineInfoSourceCollectIgnoresSystemUUIDForMachineID(t *testing.T) {
	src := NewMachineInfoSource(fakeMachineInfoCollector{
		info: &machineinfo.MachineInfo{
			MachineID:  "machine-id",
			SystemUUID: "system-uuid",
			Hostname:   "host-a",
		},
	})

	snap, err := src.Collect(context.Background())
	require.NoError(t, err)
	require.NotNil(t, snap)
	require.Equal(t, "machine-id", snap.MachineID)
}

func TestMachineInfoSourceCollectErrors(t *testing.T) {
	_, err := NewMachineInfoSource(nil).Collect(context.Background())
	require.ErrorContains(t, err, "collector is required")

	_, err = NewMachineInfoSource(fakeMachineInfoCollector{err: context.DeadlineExceeded}).Collect(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)

	_, err = NewMachineInfoSource(fakeMachineInfoCollector{}).Collect(context.Background())
	require.ErrorContains(t, err, "nil machine info")
}
