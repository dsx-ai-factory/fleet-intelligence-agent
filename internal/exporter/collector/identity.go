// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

// GPUIdentity contains stable, non-workload identity for one physical GPU.
type GPUIdentity struct {
	UUID         string
	GPU          string
	PCIBusID     string
	Device       string
	ModelName    string
	GPUSerial    string
	VBIOSVersion string
	ClusterUUID  string
	CliqueID     string
}

// EntityCatalog is an immutable identity snapshot shared by metric and log conversion.
// MIG identities are intentionally outside this catalog.
type EntityCatalog struct {
	Hostname          string
	GPUDriverVersion  string
	CUDADriverVersion string
	BootTime          time.Time
	GPUsByUUID        map[string]GPUIdentity
	GPUUUIDByIndex    map[string]string
}

// NewEntityCatalog builds a physical-entity identity snapshot from cached inventory.
func NewEntityCatalog(info *machineinfo.MachineInfo, dcgmGPUIndexes map[string]string) *EntityCatalog {
	catalog := &EntityCatalog{
		GPUsByUUID:     make(map[string]GPUIdentity),
		GPUUUIDByIndex: make(map[string]string),
	}

	for uuid, gpu := range dcgmGPUIndexes {
		uuid = strings.TrimSpace(uuid)
		gpu = strings.TrimSpace(gpu)
		if uuid == "" {
			continue
		}
		identity := GPUIdentity{UUID: uuid, GPU: gpu}
		catalog.GPUsByUUID[uuid] = identity
		if gpu != "" {
			catalog.GPUUUIDByIndex[gpu] = uuid
		}
	}

	if info == nil {
		return catalog
	}
	catalog.Hostname = strings.TrimSpace(info.Hostname)
	catalog.GPUDriverVersion = strings.TrimSpace(info.GPUDriverVersion)
	catalog.CUDADriverVersion = strings.TrimSpace(info.CUDAVersion)
	if !info.Uptime.IsZero() {
		catalog.BootTime = info.Uptime.Time.UTC()
	}
	if info.GPUInfo == nil {
		return catalog
	}

	defaultModelName := strings.TrimSpace(info.GPUInfo.Product)
	for _, gpuInfo := range info.GPUInfo.GPUs {
		uuid := strings.TrimSpace(gpuInfo.UUID)
		if uuid == "" {
			continue
		}

		identity := catalog.GPUsByUUID[uuid]
		identity.UUID = uuid
		if identity.GPU == "" {
			identity.GPU = strings.TrimSpace(gpuInfo.GPUIndex)
		}
		identity.PCIBusID = strings.TrimSpace(gpuInfo.BusID)
		identity.GPUSerial = strings.TrimSpace(gpuInfo.SN)
		identity.VBIOSVersion = strings.TrimSpace(gpuInfo.VBIOSVersion)
		identity.ModelName = strings.TrimSpace(gpuInfo.ModelName)
		if identity.ModelName == "" {
			identity.ModelName = defaultModelName
		}
		identity.ClusterUUID = strings.TrimSpace(gpuInfo.ClusterUUID)
		if gpuInfo.CliqueID != nil {
			identity.CliqueID = strconv.FormatUint(uint64(*gpuInfo.CliqueID), 10)
		}

		minorID := strings.TrimSpace(gpuInfo.MinorID)
		if minor, err := strconv.Atoi(minorID); err == nil && minor >= 0 {
			identity.Device = "nvidia" + strconv.Itoa(minor)
		}

		catalog.GPUsByUUID[uuid] = identity
		if identity.GPU != "" {
			catalog.GPUUUIDByIndex[identity.GPU] = uuid
		}
	}

	return catalog
}
