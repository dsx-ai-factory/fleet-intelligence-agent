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

// Package mapper contains inventory payload mappers.
package mapper

import (
	"strconv"
	"time"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/backendclient"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/inventory"
)

// ToNodeUpsertRequest maps an inventory snapshot to the backend node-upsert contract.
func ToNodeUpsertRequest(s *inventory.Snapshot) *backendclient.NodeUpsertRequest {
	if s == nil {
		return nil
	}
	gpus := make([]backendclient.GPUDevice, 0, len(s.Resources.GPUInfo.GPUs))
	for _, gpu := range s.Resources.GPUInfo.GPUs {
		var cliqueID *uint32
		if gpu.CliqueID != nil {
			id := *gpu.CliqueID
			cliqueID = &id
		}
		gpus = append(gpus, backendclient.GPUDevice{
			UUID:         gpu.UUID,
			BusID:        gpu.BusID,
			ClusterUUID:  gpu.ClusterUUID,
			CliqueID:     cliqueID,
			SN:           gpu.SN,
			MinorID:      gpu.MinorID,
			BoardID:      gpu.BoardID,
			VBIOSVersion: gpu.VBIOSVersion,
			ChassisSN:    gpu.ChassisSN,
			GPUIndex:     gpu.GPUIndex,
		})
	}

	blockDevices := make([]backendclient.BlockDevice, 0, len(s.Resources.DiskInfo.BlockDevices))
	for _, disk := range s.Resources.DiskInfo.BlockDevices {
		blockDevices = append(blockDevices, backendclient.BlockDevice{
			Name:       disk.Name,
			Type:       disk.Type,
			Size:       disk.Size,
			WWN:        disk.WWN,
			MountPoint: disk.MountPoint,
			FSType:     disk.FSType,
			PartUUID:   disk.PartUUID,
			Parents:    append([]string(nil), disk.Parents...),
		})
	}

	interfaces := make([]backendclient.NICInterface, 0, len(s.Resources.NICInfo.PrivateIPInterfaces))
	for _, nic := range s.Resources.NICInfo.PrivateIPInterfaces {
		interfaces = append(interfaces, backendclient.NICInterface{
			Interface: nic.Interface,
			MAC:       nic.MAC,
			IP:        nic.IP,
		})
	}

	var uptime *time.Time
	if !s.Uptime.IsZero() {
		normalized := s.Uptime.UTC()
		uptime = &normalized
	}

	return &backendclient.NodeUpsertRequest{
		Hostname: s.Hostname,
		AgentConfig: backendclient.AgentConfig{
			TotalComponents:             s.AgentConfig.TotalComponents,
			RetentionPeriodSeconds:      s.AgentConfig.RetentionPeriodSeconds,
			MetricScrapeIntervalSeconds: s.AgentConfig.MetricScrapeIntervalSeconds,
			EnabledComponents:           cloneStringSlice(s.AgentConfig.EnabledComponents),
			DisabledComponents:          cloneStringSlice(s.AgentConfig.DisabledComponents),
			InventoryEnabled:            s.AgentConfig.InventoryEnabled,
			InventoryIntervalSeconds:    s.AgentConfig.InventoryIntervalSeconds,
			AttestationEnabled:          s.AgentConfig.AttestationEnabled,
			AttestationIntervalSeconds:  s.AgentConfig.AttestationIntervalSeconds,
		},
		MachineID:               s.MachineID,
		SystemUUID:              s.SystemUUID,
		BootID:                  s.BootID,
		Uptime:                  uptime,
		OperatingSystem:         s.OperatingSystem,
		OSImage:                 s.OSImage,
		KernelVersion:           s.KernelVersion,
		AgentVersion:            s.AgentVersion,
		GPUDriverVersion:        s.GPUDriverVersion,
		CUDAVersion:             s.CUDAVersion,
		DCGMVersion:             s.DCGMVersion,
		ContainerRuntimeVersion: s.ContainerRuntimeVersion,
		NetPrivateIP:            s.NetPrivateIP,
		Resources: backendclient.NodeResources{
			CPUInfo: backendclient.CPUInfo{
				Type:         s.Resources.CPUInfo.Type,
				Manufacturer: s.Resources.CPUInfo.Manufacturer,
				Architecture: s.Resources.CPUInfo.Architecture,
				LogicalCores: strconv.FormatInt(s.Resources.CPUInfo.LogicalCores, 10),
			},
			MemoryInfo: backendclient.MemoryInfo{
				TotalBytes: strconv.FormatUint(s.Resources.MemoryInfo.TotalBytes, 10),
			},
			GPUInfo: backendclient.GPUInfo{
				Product:      s.Resources.GPUInfo.Product,
				Manufacturer: s.Resources.GPUInfo.Manufacturer,
				Architecture: s.Resources.GPUInfo.Architecture,
				Memory:       s.Resources.GPUInfo.Memory,
				GPUs:         gpus,
			},
			DiskInfo: backendclient.DiskInfo{
				ContainerRootDisk: s.Resources.DiskInfo.ContainerRootDisk,
				BlockDevices:      blockDevices,
			},
			NICInfo: backendclient.NICInfo{
				PrivateIPInterfaces: interfaces,
			},
		},
	}
}

func cloneStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}
