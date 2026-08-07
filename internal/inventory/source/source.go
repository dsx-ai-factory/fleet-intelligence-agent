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

// Package source contains inventory collection adapters.
package source

import (
	"context"
	"fmt"
	"time"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/inventory"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

// MachineInfoCollector is the local machine inventory collector dependency.
type MachineInfoCollector interface {
	Collect(ctx context.Context) (*machineinfo.MachineInfo, error)
}

type machineInfoSource struct {
	collector   MachineInfoCollector
	agentConfig inventory.AgentConfig
}

// NewMachineInfoSource wraps the machine inventory collector as an inventory source.
func NewMachineInfoSource(collector MachineInfoCollector) inventory.Source {
	return &machineInfoSource{collector: collector}
}

// NewMachineInfoSourceWithAgentConfig wraps the machine inventory collector and attaches useful
// agent configuration that should travel with inventory rather than OTLP telemetry.
func NewMachineInfoSourceWithAgentConfig(collector MachineInfoCollector, agentConfig *inventory.AgentConfig) inventory.Source {
	var cfg inventory.AgentConfig
	if agentConfig != nil {
		cfg = *agentConfig
	}
	return &machineInfoSource{
		collector:   collector,
		agentConfig: cfg,
	}
}

func (s *machineInfoSource) Collect(ctx context.Context) (*inventory.Snapshot, error) {
	if s.collector == nil {
		return nil, fmt.Errorf("machine info collector is required")
	}
	info, err := s.collector.Collect(ctx)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, fmt.Errorf("machine info collector returned nil machine info")
	}

	snap := &inventory.Snapshot{
		CollectedAt:             time.Now().UTC(),
		Hostname:                info.Hostname,
		MachineID:               info.MachineID,
		SystemUUID:              info.SystemUUID,
		BootID:                  info.BootID,
		Uptime:                  info.Uptime.Time,
		OperatingSystem:         info.OperatingSystem,
		OSImage:                 info.OSImage,
		KernelVersion:           info.KernelVersion,
		AgentVersion:            info.AgentVersion,
		GPUDriverVersion:        info.GPUDriverVersion,
		CUDAVersion:             info.CUDAVersion,
		DCGMVersion:             info.DCGMVersion,
		ContainerRuntimeVersion: info.ContainerRuntimeVersion,
		AgentConfig:             s.agentConfig,
	}
	if info.CPUInfo != nil {
		snap.Resources.CPUInfo = inventory.CPUInfo{
			Type:         info.CPUInfo.Type,
			Manufacturer: info.CPUInfo.Manufacturer,
			Architecture: info.CPUInfo.Architecture,
			LogicalCores: info.CPUInfo.LogicalCores,
		}
	}
	if info.MemoryInfo != nil {
		snap.Resources.MemoryInfo = inventory.MemoryInfo{
			TotalBytes: info.MemoryInfo.TotalBytes,
		}
	}
	if info.GPUInfo != nil {
		snap.Resources.GPUInfo = inventory.GPUInfo{
			Product:      info.GPUInfo.Product,
			Manufacturer: info.GPUInfo.Manufacturer,
			Architecture: info.GPUInfo.Architecture,
			Memory:       info.GPUInfo.Memory,
		}
		if len(info.GPUInfo.GPUs) > 0 {
			snap.Resources.GPUInfo.GPUs = make([]inventory.GPUDevice, 0, len(info.GPUInfo.GPUs))
			for _, gpu := range info.GPUInfo.GPUs {
				var cliqueID *uint32
				if gpu.CliqueID != nil {
					id := *gpu.CliqueID
					cliqueID = &id
				}
				snap.Resources.GPUInfo.GPUs = append(snap.Resources.GPUInfo.GPUs, inventory.GPUDevice{
					UUID:         gpu.UUID,
					BusID:        gpu.BusID,
					ClusterUUID:  gpu.ClusterUUID,
					CliqueID:     cliqueID,
					SN:           gpu.SN,
					MinorID:      gpu.MinorID,
					BoardID:      int(gpu.BoardID),
					VBIOSVersion: gpu.VBIOSVersion,
					ChassisSN:    gpu.ChassisSN,
					GPUIndex:     gpu.GPUIndex,
				})
			}
		}
	}
	if info.DiskInfo != nil {
		snap.Resources.DiskInfo = inventory.DiskInfo{
			ContainerRootDisk: info.DiskInfo.ContainerRootDisk,
		}
		if len(info.DiskInfo.BlockDevices) > 0 {
			snap.Resources.DiskInfo.BlockDevices = make([]inventory.BlockDevice, 0, len(info.DiskInfo.BlockDevices))
			for _, disk := range info.DiskInfo.BlockDevices {
				snap.Resources.DiskInfo.BlockDevices = append(snap.Resources.DiskInfo.BlockDevices, inventory.BlockDevice{
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
		}
	}
	if info.NICInfo != nil && len(info.NICInfo.PrivateIPInterfaces) > 0 {
		snap.Resources.NICInfo.PrivateIPInterfaces = make([]inventory.NICInterface, 0, len(info.NICInfo.PrivateIPInterfaces))
		for _, nic := range info.NICInfo.PrivateIPInterfaces {
			snap.Resources.NICInfo.PrivateIPInterfaces = append(snap.Resources.NICInfo.PrivateIPInterfaces, inventory.NICInterface{
				Interface: nic.Interface,
				MAC:       nic.MAC,
				IP:        nic.IP,
			})
		}
		snap.NetPrivateIP = info.NICInfo.PrivateIPInterfaces[0].IP
	}

	return snap, nil
}
