// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

// Package machineinfo provides a shim layer over gpud's machine-info package
// to customize version information for Fleet Intelligence.
package machineinfo

import (
	"fmt"
	"io"
	"os"
	"strings"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	pkgmachineinfo "github.com/NVIDIA/fleet-intelligence-sdk/pkg/machine-info"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/providers"
	"github.com/dustin/go-humanize"
	"github.com/olekukonko/tablewriter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/dcgmversion"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/version"
)

var getDCGMVersion = dcgmversion.DetectHostengineVersion

// MachineInfo is a custom struct that replaces GPUdVersion with AgentVersion.
type MachineInfo struct {
	// AgentVersion represents the current version of the Fleet Intelligence agent.
	AgentVersion string `json:"agentVersion,omitempty"`
	// GPUDriverVersion represents the current version of GPU driver installed
	GPUDriverVersion string `json:"gpuDriverVersion,omitempty"`
	// CUDAVersion represents the current version of cuda library.
	CUDAVersion string `json:"cudaVersion,omitempty"`
	// DCGMVersion represents the current version of the DCGM HostEngine when reachable.
	DCGMVersion string `json:"dcgmVersion,omitempty"`
	// ContainerRuntime Version reported by the node through runtime remote API (e.g. containerd://1.4.2).
	ContainerRuntimeVersion string `json:"containerRuntimeVersion,omitempty"`
	// Kernel Version reported by the node from 'uname -r' (e.g. 3.16.0-0.bpo.4-amd64).
	KernelVersion string `json:"kernelVersion,omitempty"`
	// OS Image reported by the node from /etc/os-release (e.g. Debian GNU/Linux 7 (wheezy)).
	OSImage string `json:"osImage,omitempty"`
	// The Operating System reported by the node
	OperatingSystem string `json:"operatingSystem,omitempty"`
	// SystemUUID comes from https://github.com/google/cadvisor/blob/master/utils/sysfs/sysfs.go#L442
	SystemUUID string `json:"systemUUID,omitempty"`
	// MachineID is collected by GPUd. It comes from /etc/machine-id or /var/lib/dbus/machine-id
	MachineID string `json:"machineID,omitempty"`
	// BootID is collected by GPUd.
	BootID string `json:"bootID,omitempty"`
	// Hostname is the current host of machine
	Hostname string `json:"hostname,omitempty"`
	// Uptime represents when the machine started.
	Uptime metav1.Time `json:"uptime,omitempty"`

	// CPUInfo is the CPU info of the machine.
	CPUInfo *apiv1.MachineCPUInfo `json:"cpuInfo,omitempty"`
	// MemoryInfo is the memory info of the machine.
	MemoryInfo *apiv1.MachineMemoryInfo `json:"memoryInfo,omitempty"`
	// GPUInfo is the GPU info of the machine.
	GPUInfo *apiv1.MachineGPUInfo `json:"gpuInfo,omitempty"`
	// DiskInfo is the Disk info of the machine.
	DiskInfo *apiv1.MachineDiskInfo `json:"diskInfo,omitempty"`
	// NICInfo is the network info of the machine.
	NICInfo *apiv1.MachineNICInfo `json:"nicInfo,omitempty"`
}

// GetMachineInfo retrieves machine info and customizes it for Fleet Intelligence.
func GetMachineInfo(devices []nvidiadcgm.DeviceInfo) (*MachineInfo, error) {
	gpudInfo, err := pkgmachineinfo.GetMachineInfo(devices)
	if err != nil {
		return nil, err
	}

	// Override the hostname if it's set in the environment for containerized deployments
	if hostname := strings.TrimSpace(os.Getenv("HOSTNAME")); hostname != "" {
		gpudInfo.Hostname = hostname
	}

	dcgmVersion, _ := getDCGMVersion()

	// Convert to our custom MachineInfo struct with the agent version.
	return &MachineInfo{
		AgentVersion:            version.Version,
		GPUDriverVersion:        gpudInfo.GPUDriverVersion,
		CUDAVersion:             gpudInfo.CUDAVersion,
		DCGMVersion:             dcgmVersion,
		ContainerRuntimeVersion: gpudInfo.ContainerRuntimeVersion,
		KernelVersion:           gpudInfo.KernelVersion,
		OSImage:                 gpudInfo.OSImage,
		OperatingSystem:         gpudInfo.OperatingSystem,
		SystemUUID:              gpudInfo.SystemUUID,
		MachineID:               gpudInfo.MachineID,
		BootID:                  gpudInfo.BootID,
		Hostname:                gpudInfo.Hostname,
		Uptime:                  gpudInfo.Uptime,
		CPUInfo:                 gpudInfo.CPUInfo,
		MemoryInfo:              gpudInfo.MemoryInfo,
		GPUInfo:                 gpudInfo.GPUInfo,
		DiskInfo:                gpudInfo.DiskInfo,
		NICInfo:                 gpudInfo.NICInfo,
	}, nil
}

// RenderTable renders the machine info table with Fleet Intelligence branding
func (i *MachineInfo) RenderTable(wr io.Writer) {
	if i == nil {
		return
	}

	table := tablewriter.NewWriter(wr)
	table.SetAlignment(tablewriter.ALIGN_CENTER)

	table.Append([]string{"Agent Version", i.AgentVersion})
	table.Append([]string{"Container Runtime Version", i.ContainerRuntimeVersion})
	table.Append([]string{"OS Image", i.OSImage})
	table.Append([]string{"Kernel Version", i.KernelVersion})

	if i.CPUInfo != nil {
		table.Append([]string{"CPU Type", i.CPUInfo.Type})
		table.Append([]string{"CPU Manufacturer", i.CPUInfo.Manufacturer})
		table.Append([]string{"CPU Architecture", i.CPUInfo.Architecture})
		table.Append([]string{"CPU Logical Cores", fmt.Sprintf("%d", i.CPUInfo.LogicalCores)})
	}
	if i.MemoryInfo != nil {
		table.Append([]string{"Memory Total", humanize.IBytes(i.MemoryInfo.TotalBytes)})
	}

	table.Append([]string{"CUDA Version", i.CUDAVersion})
	table.Append([]string{"DCGM Version", i.DCGMVersion})
	if i.GPUInfo != nil {
		table.Append([]string{"GPU Driver Version", i.GPUDriverVersion})
		table.Append([]string{"GPU Product", i.GPUInfo.Product})
		table.Append([]string{"GPU Manufacturer", i.GPUInfo.Manufacturer})
		table.Append([]string{"GPU Architecture", i.GPUInfo.Architecture})
		table.Append([]string{"GPU Memory", i.GPUInfo.Memory})
	}

	if i.NICInfo != nil {
		for idx, nic := range i.NICInfo.PrivateIPInterfaces {
			table.Append([]string{fmt.Sprintf("Private IP Interface %d", idx+1), fmt.Sprintf("%s (%s, %s)", nic.Interface, nic.MAC, nic.IP)})
		}
	}

	if i.DiskInfo != nil {
		table.Append([]string{"Container Root Disk", i.DiskInfo.ContainerRootDisk})
	}

	table.Render()
	fmt.Fprintf(wr, "\n")

	if i.DiskInfo != nil {
		i.DiskInfo.RenderTable(wr)
		fmt.Fprintf(wr, "\n")
	}

	if i.GPUInfo != nil {
		i.GPUInfo.RenderTable(wr)
		fmt.Fprintf(wr, "\n")
	}
}

// GetProvider is a passthrough to gpud's GetProvider function
func GetProvider(publicIP string) *providers.Info {
	return pkgmachineinfo.GetProvider(publicIP)
}

// PopulatePrivateIPFromMachineInfo populates the provider's private IP from machine info
// if it's not already set. It uses the first private IPv4 address found.
func PopulatePrivateIPFromMachineInfo(providerInfo *providers.Info, machineInfo *MachineInfo) {
	if providerInfo == nil || providerInfo.PrivateIP != "" {
		return
	}

	if machineInfo == nil || machineInfo.NICInfo == nil {
		return
	}

	for _, iface := range machineInfo.NICInfo.PrivateIPInterfaces {
		if iface.IP == "" {
			continue
		}
		if iface.Addr.IsPrivate() && iface.Addr.Is4() {
			providerInfo.PrivateIP = iface.IP
			return
		}
	}
}
