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

package inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestComputeHashIgnoresCollectedAtAndExistingHash(t *testing.T) {
	base := &Snapshot{
		CollectedAt:   time.Unix(100, 0).UTC(),
		InventoryHash: "old-hash",
		Hostname:      "host-a",
		MachineID:     "machine-id",
		Resources: Resources{
			CPUInfo: CPUInfo{Type: "Xeon", LogicalCores: 64},
		},
	}
	other := *base
	other.CollectedAt = time.Unix(200, 0).UTC()
	other.InventoryHash = "different-old-hash"

	hash1, err := ComputeHash(base)
	require.NoError(t, err)
	hash2, err := ComputeHash(&other)
	require.NoError(t, err)
	require.Equal(t, hash1, hash2)

	other.Hostname = "host-b"
	hash3, err := ComputeHash(&other)
	require.NoError(t, err)
	require.NotEqual(t, hash1, hash3)
}

func TestComputeHashIgnoresSetLikeInventoryListOrder(t *testing.T) {
	base := &Snapshot{
		Hostname: "host-a",
		AgentConfig: AgentConfig{
			EnabledComponents:  []string{"gpu", "cpu"},
			DisabledComponents: []string{"nic", "disk"},
		},
		Resources: Resources{
			GPUInfo: GPUInfo{GPUs: []GPUDevice{
				{UUID: "GPU-b", BusID: "2"},
				{UUID: "GPU-a", BusID: "1"},
			}},
			DiskInfo: DiskInfo{BlockDevices: []BlockDevice{
				{Name: "/dev/b", WWN: "wwn-b", Parents: []string{"parent-b-1", "parent-b-2"}},
				{Name: "/dev/a", WWN: "wwn-a", Parents: []string{"parent-a-1", "parent-a-2"}},
			}},
			NICInfo: NICInfo{PrivateIPInterfaces: []NICInterface{
				{Interface: "eth1", MAC: "00:00:00:00:00:02", IP: "10.0.0.2"},
				{Interface: "eth0", MAC: "00:00:00:00:00:01", IP: "10.0.0.1"},
			}},
		},
	}
	reordered := &Snapshot{
		Hostname: "host-a",
		AgentConfig: AgentConfig{
			EnabledComponents:  []string{"cpu", "gpu"},
			DisabledComponents: []string{"disk", "nic"},
		},
		Resources: Resources{
			GPUInfo: GPUInfo{GPUs: []GPUDevice{
				{UUID: "GPU-a", BusID: "1"},
				{UUID: "GPU-b", BusID: "2"},
			}},
			DiskInfo: DiskInfo{BlockDevices: []BlockDevice{
				{Name: "/dev/a", WWN: "wwn-a", Parents: []string{"parent-a-1", "parent-a-2"}},
				{Name: "/dev/b", WWN: "wwn-b", Parents: []string{"parent-b-1", "parent-b-2"}},
			}},
			NICInfo: NICInfo{PrivateIPInterfaces: []NICInterface{
				{Interface: "eth0", MAC: "00:00:00:00:00:01", IP: "10.0.0.1"},
				{Interface: "eth1", MAC: "00:00:00:00:00:02", IP: "10.0.0.2"},
			}},
		},
	}

	baseHash, err := ComputeHash(base)
	require.NoError(t, err)
	reorderedHash, err := ComputeHash(reordered)
	require.NoError(t, err)
	require.Equal(t, baseHash, reorderedHash)

	// Hashing must not reorder the snapshot that is later sent to the backend.
	require.Equal(t, []string{"gpu", "cpu"}, base.AgentConfig.EnabledComponents)
	require.Equal(t, "GPU-b", base.Resources.GPUInfo.GPUs[0].UUID)
	require.Equal(t, "/dev/b", base.Resources.DiskInfo.BlockDevices[0].Name)
	require.Equal(t, "eth1", base.Resources.NICInfo.PrivateIPInterfaces[0].Interface)
}

func TestComputeHashDetectsInventoryItemChanges(t *testing.T) {
	base := &Snapshot{
		Resources: Resources{
			GPUInfo: GPUInfo{GPUs: []GPUDevice{{UUID: "GPU-a", BusID: "1"}}},
			DiskInfo: DiskInfo{BlockDevices: []BlockDevice{{
				Name: "/dev/a", Parents: []string{"immediate-parent", "root-parent"},
			}}},
		},
	}

	baseHash, err := ComputeHash(base)
	require.NoError(t, err)

	changedGPU := *base
	changedGPU.Resources.GPUInfo.GPUs = append([]GPUDevice(nil), base.Resources.GPUInfo.GPUs...)
	changedGPU.Resources.GPUInfo.GPUs[0].BusID = "2"
	changedGPUHash, err := ComputeHash(&changedGPU)
	require.NoError(t, err)
	require.NotEqual(t, baseHash, changedGPUHash)

	changedParentOrder := *base
	changedParentOrder.Resources.DiskInfo.BlockDevices = append([]BlockDevice(nil), base.Resources.DiskInfo.BlockDevices...)
	changedParentOrder.Resources.DiskInfo.BlockDevices[0].Parents = []string{"root-parent", "immediate-parent"}
	changedParentHash, err := ComputeHash(&changedParentOrder)
	require.NoError(t, err)
	require.NotEqual(t, baseHash, changedParentHash)
}

func TestComputeHashRejectsNilSnapshot(t *testing.T) {
	_, err := ComputeHash(nil)
	require.ErrorContains(t, err, "inventory snapshot is nil")
}
