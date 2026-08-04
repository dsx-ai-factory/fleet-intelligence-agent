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

// Package inventory owns inventory collection and sync state.
package inventory

import (
	"context"
	"errors"
	"time"
)

// ErrNotReady indicates inventory export cannot proceed because backend state is not ready yet.
var ErrNotReady = errors.New("inventory backend not ready")

// Snapshot is the agent-owned inventory state model.
type Snapshot struct {
	CollectedAt             time.Time
	InventoryHash           string
	Hostname                string
	MachineID               string
	SystemUUID              string
	BootID                  string
	Uptime                  time.Time
	OperatingSystem         string
	OSImage                 string
	KernelVersion           string
	AgentVersion            string
	GPUDriverVersion        string
	CUDAVersion             string
	DCGMVersion             string
	ContainerRuntimeVersion string
	NetPrivateIP            string
	AgentConfig             AgentConfig
	Resources               Resources
}

type AgentConfig struct {
	TotalComponents             int64
	RetentionPeriodSeconds      int64
	MetricScrapeIntervalSeconds int64
	EnabledComponents           []string
	DisabledComponents          []string
	InventoryEnabled            bool
	InventoryIntervalSeconds    int64
	AttestationEnabled          bool
	AttestationIntervalSeconds  int64
}

type Resources struct {
	CPUInfo    CPUInfo
	MemoryInfo MemoryInfo
	GPUInfo    GPUInfo
	DiskInfo   DiskInfo
	NICInfo    NICInfo
}

type CPUInfo struct {
	Type         string
	Manufacturer string
	Architecture string
	LogicalCores int64
}

type MemoryInfo struct {
	TotalBytes uint64
}

type GPUInfo struct {
	Product      string
	Manufacturer string
	Architecture string
	Memory       string
	GPUs         []GPUDevice
}

type GPUDevice struct {
	UUID         string
	BusID        string
	ClusterUUID  string
	CliqueID     *uint32
	SN           string
	MinorID      string
	BoardID      int
	VBIOSVersion string
	ChassisSN    string
	GPUIndex     string
}

type DiskInfo struct {
	ContainerRootDisk string
	BlockDevices      []BlockDevice
}

type BlockDevice struct {
	Name       string
	Type       string
	Size       int64
	WWN        string
	MountPoint string
	FSType     string
	PartUUID   string
	Parents    []string
}

type NICInfo struct {
	PrivateIPInterfaces []NICInterface
}

type NICInterface struct {
	Interface string
	MAC       string
	IP        string
}

// Source collects inventory from local providers.
type Source interface {
	Collect(ctx context.Context) (*Snapshot, error)
}

// Sink exports inventory snapshots to an external destination.
type Sink interface {
	Export(ctx context.Context, snap *Snapshot) error
}

// InventoryConfig controls periodic inventory scheduling.
type InventoryConfig struct {
	Interval      time.Duration
	RetryInterval time.Duration
	Timeout       time.Duration
	StartupJitter time.Duration
}
