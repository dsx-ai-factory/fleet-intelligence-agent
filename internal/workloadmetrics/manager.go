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

// Package workloadmetrics owns runtime-configured workload attribution metrics.
package workloadmetrics

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/workloadmetrics/hpcjob"
)

// Manager owns workload metric collectors for one FleetInt server instance.
// Unlike package-global SDK metrics, these collectors depend on runtime
// configuration and GPU inventory and must be unregistered when the server
// stops.
type Manager struct {
	registerer prometheus.Registerer
	collectors []prometheus.Collector
	closeOnce  sync.Once
}

type dcgmDeviceProvider interface {
	GetDevices() []nvidiadcgm.DeviceInfo
}

// Start creates and registers the configured workload attribution collectors
// with the Fleet Intelligence SDK Prometheus registry.
func Start(
	workloadConfig *config.WorkloadAttributionConfig,
	dcgmDevices dcgmDeviceProvider,
) (*Manager, error) {
	var gpuUUIDByIndexProvider func() map[string]string
	if dcgmDevices != nil {
		gpuUUIDByIndexProvider = func() map[string]string {
			devices := dcgmDevices.GetDevices()
			gpuUUIDByIndex := make(map[string]string, len(devices))
			for _, device := range devices {
				gpuUUIDByIndex[strconv.FormatUint(uint64(device.ID), 10)] = device.UUID
			}
			return gpuUUIDByIndex
		}
	}
	return startWithRegisterer(pkgmetrics.DefaultRegisterer(), workloadConfig, gpuUUIDByIndexProvider)
}

func startWithRegisterer(
	registerer prometheus.Registerer,
	workloadConfig *config.WorkloadAttributionConfig,
	gpuUUIDByIndexProvider func() map[string]string,
) (*Manager, error) {
	m := &Manager{registerer: registerer}
	if err := workloadConfig.Validate(); err != nil {
		return nil, err
	}
	if workloadConfig == nil || workloadConfig.Source == "" {
		return m, nil
	}
	gpuIdentifier := workloadConfig.HPC.HPCGPUIdentifier()
	gpuIdentifierType := hpcjob.GPUIdentifierType(gpuIdentifier)

	collector := hpcjob.NewCollector(
		hpcjob.NewFileReader(
			workloadConfig.HPC.JobMappingDir,
			gpuIdentifierType,
		),
		gpuUUIDByIndexProvider,
	)
	if err := registerer.Register(collector); err != nil {
		return nil, fmt.Errorf("register HPC workload identity metric collector: %w", err)
	}
	m.collectors = append(m.collectors, collector)

	log.Logger.Infow(
		"enabled HPC workload identity metrics",
		"mappingDirectory", workloadConfig.HPC.JobMappingDir,
		"gpuIdentifier", gpuIdentifier,
	)
	return m, nil
}

// Close unregisters every workload collector owned by this manager. It is safe
// to call more than once.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() {
		for i := len(m.collectors) - 1; i >= 0; i-- {
			m.registerer.Unregister(m.collectors[i])
		}
		m.collectors = nil
	})
}
