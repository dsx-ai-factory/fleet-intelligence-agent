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

// Package registry provides component registration and management
// for fleetint, allowing fine-grained control over which components are enabled.
package registry

import (
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	componentsdcgmclock "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/clock"
	componentsdcgmcpu "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/cpu"
	componentsdcgminforom "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/inforom"
	componentsdcgmmem "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/mem"
	componentsdcgmnvlink "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/nvlink"
	componentsdcgmnvswitch "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/nvswitch"
	componentsdcgmpcie "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/pcie"
	componentsdcgmpower "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/power"
	componentsdcgmprof "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/prof"
	componentsdcgmthermal "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/thermal"
	componentsdcgmutilization "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/utilization"
	componentsinfiniband "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/infiniband"
	componentsnccl "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/nccl"
	componentsnvlink "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/nvlink"
	componentsnvml "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/nvml"
	componentspeermem "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/peermem"
	componentspersistencemode "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/persistence-mode"
	componentssxid "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/sxid"
	componentsxid "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/xid"
	componentscpu "github.com/NVIDIA/fleet-intelligence-sdk/components/cpu"
	componentsdisk "github.com/NVIDIA/fleet-intelligence-sdk/components/disk"
	componentslibrary "github.com/NVIDIA/fleet-intelligence-sdk/components/library"
	componentsmemory "github.com/NVIDIA/fleet-intelligence-sdk/components/memory"
	componentsnetworkethernet "github.com/NVIDIA/fleet-intelligence-sdk/components/network/ethernet"
	componentsos "github.com/NVIDIA/fleet-intelligence-sdk/components/os"
)

// Component represents a health monitoring component with its name and initialization function
type Component struct {
	Name     string
	InitFunc components.InitFunc
	// EnabledByDefault indicates if this component should be enabled by default
	EnabledByDefault bool
}

// All returns all available components with their default enable/disable state
func All() []Component {
	return []Component{
		// NVIDIA GPU Components - all enabled by default
		{
			Name:             componentsinfiniband.Name,
			InitFunc:         componentsinfiniband.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsnccl.Name,
			InitFunc:         componentsnccl.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentspeermem.Name,
			InitFunc:         componentspeermem.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentspersistencemode.Name,
			InitFunc:         componentspersistencemode.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsnvml.Name,
			InitFunc:         componentsnvml.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsnvlink.Name,
			InitFunc:         componentsnvlink.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentssxid.Name,
			InitFunc:         componentssxid.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsxid.Name,
			InitFunc:         componentsxid.New,
			EnabledByDefault: true,
		},
		// DCGM Components
		{
			Name:             componentsdcgmclock.Name,
			InitFunc:         componentsdcgmclock.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmcpu.Name,
			InitFunc:         componentsdcgmcpu.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgminforom.Name,
			InitFunc:         componentsdcgminforom.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmmem.Name,
			InitFunc:         componentsdcgmmem.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmnvlink.Name,
			InitFunc:         componentsdcgmnvlink.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmnvswitch.Name,
			InitFunc:         componentsdcgmnvswitch.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmpcie.Name,
			InitFunc:         componentsdcgmpcie.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmpower.Name,
			InitFunc:         componentsdcgmpower.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmprof.Name,
			InitFunc:         componentsdcgmprof.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmthermal.Name,
			InitFunc:         componentsdcgmthermal.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdcgmutilization.Name,
			InitFunc:         componentsdcgmutilization.New,
			EnabledByDefault: true,
		},

		// System Components - all enabled by default
		{
			Name:             componentscpu.Name,
			InitFunc:         componentscpu.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsdisk.Name,
			InitFunc:         componentsdisk.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsmemory.Name,
			InitFunc:         componentsmemory.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsnetworkethernet.Name,
			InitFunc:         componentsnetworkethernet.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentsos.Name,
			InitFunc:         componentsos.New,
			EnabledByDefault: true,
		},
		{
			Name:             componentslibrary.Name,
			InitFunc:         componentslibrary.New,
			EnabledByDefault: true,
		},
	}
}

// GetEnabledComponents returns only the components that should be enabled by default
func GetEnabledComponents() []Component {
	var enabled []Component
	for _, c := range All() {
		if c.EnabledByDefault {
			enabled = append(enabled, c)
		}
	}
	return enabled
}

// GetComponent returns a component by name, or nil if not found
func GetComponent(name string) *Component {
	for _, c := range All() {
		if c.Name == name {
			return &c
		}
	}
	return nil
}

// AllComponentNames returns a list of all available component names
func AllComponentNames() []string {
	all := All()
	names := make([]string, len(all))
	for i, c := range all {
		names[i] = c.Name
	}
	return names
}
