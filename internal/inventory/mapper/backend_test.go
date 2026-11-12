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

package mapper

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory"
)

func TestToNodeUpsertRequestNil(t *testing.T) {
	require.Nil(t, ToNodeUpsertRequest(nil))
}

func TestToNodeUpsertRequestAgentConfigJSONIncludesZeroValues(t *testing.T) {
	req := ToNodeUpsertRequest(&inventory.Snapshot{})
	require.NotNil(t, req)

	data, err := json.Marshal(req.AgentConfig)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"totalComponents": 0,
		"retentionPeriodSeconds": 0,
		"metricScrapeIntervalSeconds": 0,
		"enabledComponents": [],
		"disabledComponents": [],
		"inventoryEnabled": false,
		"inventoryIntervalSeconds": 0,
		"attestationEnabled": false,
		"attestationIntervalSeconds": 0
	}`, string(data))
}

func TestToNodeUpsertRequestOmitsUnavailableGPUBoardID(t *testing.T) {
	req := ToNodeUpsertRequest(&inventory.Snapshot{
		Resources: inventory.Resources{
			GPUInfo: inventory.GPUInfo{
				GPUs: []inventory.GPUDevice{{UUID: "GPU-1"}},
			},
		},
	})
	require.NotNil(t, req)
	require.Len(t, req.Resources.GPUInfo.GPUs, 1)

	payload, err := json.Marshal(req.Resources.GPUInfo.GPUs[0])
	require.NoError(t, err)
	require.NotContains(t, string(payload), `"boardID"`)
}

func TestToNodeUpsertRequest(t *testing.T) {
	bootTime := time.Date(2026, 4, 28, 12, 30, 0, 0, time.FixedZone("PDT", -7*60*60))
	cliqueID := uint32(0)
	req := ToNodeUpsertRequest(&inventory.Snapshot{
		Hostname:                "host-a",
		MachineID:               "machine-id",
		SystemUUID:              "uuid-1",
		BootID:                  "boot-1",
		Uptime:                  bootTime,
		OperatingSystem:         "linux",
		OSImage:                 "Ubuntu",
		KernelVersion:           "6.5.0",
		AgentVersion:            "1.2.3",
		GPUDriverVersion:        "550.54.15",
		CUDAVersion:             "12.4",
		DCGMVersion:             "4.2.3",
		ContainerRuntimeVersion: "containerd://1.7.13",
		NetPrivateIP:            "10.0.0.10",
		AgentConfig: inventory.AgentConfig{
			TotalComponents:             30,
			RetentionPeriodSeconds:      86400,
			MetricScrapeIntervalSeconds: 60,
			EnabledComponents:           []string{"cpu", "gpu"},
			DisabledComponents:          []string{"disk"},
			InventoryEnabled:            true,
			InventoryIntervalSeconds:    3600,
			AttestationEnabled:          true,
			AttestationIntervalSeconds:  86400,
		},
		Resources: inventory.Resources{
			CPUInfo: inventory.CPUInfo{
				Type:         "Xeon",
				Manufacturer: "Intel",
				Architecture: "x86_64",
				LogicalCores: 64,
			},
			MemoryInfo: inventory.MemoryInfo{
				TotalBytes: 1024,
			},
			GPUInfo: inventory.GPUInfo{
				Product:      "H100",
				Manufacturer: "NVIDIA",
				Architecture: "Hopper",
				Memory:       "80GB",
				GPUs: []inventory.GPUDevice{{
					UUID:         "GPU-1",
					BusID:        "0000:01:00.0",
					ClusterUUID:  "11111111-2222-3333-4444-555555555555",
					CliqueID:     &cliqueID,
					SN:           "serial",
					MinorID:      "1",
					BoardID:      7,
					VBIOSVersion: "vbios",
					ChassisSN:    "chassis",
					GPUIndex:     "0",
				}},
			},
			DiskInfo: inventory.DiskInfo{
				ContainerRootDisk: "/dev/nvme0n1",
				BlockDevices: []inventory.BlockDevice{{
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
			NICInfo: inventory.NICInfo{
				PrivateIPInterfaces: []inventory.NICInterface{{
					Interface: "eth0",
					MAC:       "00:11:22:33:44:55",
					IP:        "10.0.0.10",
				}},
			},
		},
	})

	require.NotNil(t, req)
	require.Equal(t, "host-a", req.Hostname)
	require.Equal(t, "machine-id", req.MachineID) //nolint:staticcheck // Preserve the deprecated OS machine ID contract.
	require.NotNil(t, req.Uptime)
	require.Equal(t, bootTime.UTC(), *req.Uptime)
	require.Equal(t, int64(30), req.AgentConfig.TotalComponents)
	require.Equal(t, int64(86400), req.AgentConfig.RetentionPeriodSeconds)
	require.Equal(t, int64(60), req.AgentConfig.MetricScrapeIntervalSeconds)
	require.Equal(t, []string{"cpu", "gpu"}, req.AgentConfig.EnabledComponents)
	require.Equal(t, []string{"disk"}, req.AgentConfig.DisabledComponents)
	require.True(t, req.AgentConfig.InventoryEnabled)
	require.Equal(t, int64(3600), req.AgentConfig.InventoryIntervalSeconds)
	require.True(t, req.AgentConfig.AttestationEnabled)
	require.Equal(t, int64(86400), req.AgentConfig.AttestationIntervalSeconds)
	require.Equal(t, "64", req.Resources.CPUInfo.LogicalCores)
	require.Equal(t, "1024", req.Resources.MemoryInfo.TotalBytes)
	require.Equal(t, "H100", req.Resources.GPUInfo.Product)
	require.Equal(t, "NVIDIA", req.Resources.GPUInfo.Manufacturer)
	require.Len(t, req.Resources.GPUInfo.GPUs, 1)
	require.Equal(t, 7, req.Resources.GPUInfo.GPUs[0].BoardID)
	require.Equal(t, "11111111-2222-3333-4444-555555555555", req.Resources.GPUInfo.GPUs[0].ClusterUUID)
	require.NotNil(t, req.Resources.GPUInfo.GPUs[0].CliqueID)
	require.Equal(t, uint32(0), *req.Resources.GPUInfo.GPUs[0].CliqueID)
	payload, err := json.Marshal(req.Resources.GPUInfo.GPUs[0])
	require.NoError(t, err)
	require.JSONEq(t, `{
		"uuid":"GPU-1",
		"busID":"0000:01:00.0",
		"clusterUUID":"11111111-2222-3333-4444-555555555555",
		"cliqueID":0,
		"sn":"serial",
		"minorID":"1",
		"boardID":7,
		"vbiosVersion":"vbios",
		"chassisSN":"chassis",
		"gpuIndex":"0"
	}`, string(payload))
	require.Equal(t, "/dev/nvme0n1", req.Resources.DiskInfo.ContainerRootDisk)
	require.Len(t, req.Resources.DiskInfo.BlockDevices, 1)
	require.Equal(t, "parent0", req.Resources.DiskInfo.BlockDevices[0].Parents[0])
	require.Len(t, req.Resources.NICInfo.PrivateIPInterfaces, 1)
	require.Equal(t, "eth0", req.Resources.NICInfo.PrivateIPInterfaces[0].Interface)
	require.Equal(t, "10.0.0.10", req.Resources.NICInfo.PrivateIPInterfaces[0].IP)
}

func TestToNodeUpsertRequestGPUPlatformInfoJSON(t *testing.T) {
	slot, tray, host, module, zero := uint32(26), uint32(16), uint32(1), uint32(3), uint32(0)
	peerType, unknownPeerType := "switchConnected", "PEER_TYPE_7"
	gpus := []inventory.GPUDevice{
		{
			UUID: "GPU-1", SlotNumber: &slot, TrayIndex: &tray,
			HostID: &host, PeerType: peerType, ModuleID: &module, FabricGUID: "0xb74a5c8b016b6418",
		},
		{
			UUID: "GPU-2", SlotNumber: &zero, TrayIndex: &zero,
			HostID: &zero, PeerType: "directConnected", ModuleID: &zero, FabricGUID: "0x0000000000000001",
		},
		{UUID: "GPU-3"},
		{UUID: "GPU-4", PeerType: unknownPeerType},
	}
	req := ToNodeUpsertRequest(&inventory.Snapshot{
		Resources: inventory.Resources{GPUInfo: inventory.GPUInfo{GPUs: gpus}},
	})
	for index, gpu := range req.Resources.GPUInfo.GPUs[:2] {
		require.NotSame(t, gpus[index].SlotNumber, gpu.SlotNumber)
		require.NotSame(t, gpus[index].TrayIndex, gpu.TrayIndex)
		require.NotSame(t, gpus[index].HostID, gpu.HostID)
		require.NotSame(t, gpus[index].ModuleID, gpu.ModuleID)
	}

	payload, err := json.Marshal(req.Resources.GPUInfo.GPUs)
	require.NoError(t, err)
	var decoded []backendclient.GPUDevice
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, req.Resources.GPUInfo.GPUs, decoded)
	require.Equal(t, uint32(26), *decoded[0].SlotNumber)
	require.Equal(t, uint32(16), *decoded[0].TrayIndex)
	require.Equal(t, uint32(1), *decoded[0].HostID)
	require.Equal(t, "switchConnected", decoded[0].PeerType)
	require.Equal(t, "PEER_TYPE_7", decoded[3].PeerType)
	require.Equal(t, uint32(3), *decoded[0].ModuleID)
	require.Equal(t, "0xb74a5c8b016b6418", decoded[0].FabricGUID)
	require.Equal(t, "0x0000000000000001", decoded[1].FabricGUID)

	var wire []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &wire))
	for _, field := range []string{"slotNumber", "trayIndex", "hostID", "moduleID"} {
		require.Equal(t, json.RawMessage("0"), wire[1][field], field)
		require.NotContains(t, wire[2], field)
	}
	require.Equal(t, json.RawMessage(`"switchConnected"`), wire[0]["peerType"])
	require.Equal(t, json.RawMessage(`"directConnected"`), wire[1]["peerType"])
	require.NotContains(t, wire[2], "peerType")
	require.Equal(t, json.RawMessage(`"PEER_TYPE_7"`), wire[3]["peerType"])
	require.NotContains(t, wire[2], "fabricGUID")
	module = 2
	zero = 4
	gpus[0].PeerType, gpus[1].PeerType, gpus[3].PeerType = "directConnected", "switchConnected", "PEER_TYPE_8"
	require.Equal(t, uint32(3), *req.Resources.GPUInfo.GPUs[0].ModuleID)
	require.Equal(t, uint32(0), *req.Resources.GPUInfo.GPUs[1].ModuleID)
	require.Equal(t, "switchConnected", req.Resources.GPUInfo.GPUs[0].PeerType)
	require.Equal(t, "directConnected", req.Resources.GPUInfo.GPUs[1].PeerType)
	require.Equal(t, "PEER_TYPE_7", req.Resources.GPUInfo.GPUs[3].PeerType)
}
