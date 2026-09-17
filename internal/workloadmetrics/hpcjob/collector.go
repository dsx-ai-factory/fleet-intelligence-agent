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

package hpcjob

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

const (
	metricName    = "fleetint_gpu_workload_info"
	componentName = "workload-attribution"

	workloadSource = "hpc"
)

// Collector emits one normalized workload identity metric for each active
// GPU-to-job mapping. It deliberately leaves the existing GPU metric series
// unchanged so job churn is not multiplied by the number of GPU metrics.
type Collector struct {
	reader                 Reader
	gpuIdentifierType      GPUIdentifierType
	gpuUUIDByIndexProvider func() map[string]string
	desc                   *prometheus.Desc
}

// NewCollector creates a normalized HPC workload identity metric collector.
func NewCollector(
	reader Reader,
	gpuUUIDByIndexProvider func() map[string]string,
) *Collector {
	var gpuIdentifierType GPUIdentifierType
	if reader != nil {
		gpuIdentifierType = reader.GPUIdentifierType()
	}
	return &Collector{
		reader:                 reader,
		gpuIdentifierType:      gpuIdentifierType,
		gpuUUIDByIndexProvider: gpuUUIDByIndexProvider,
		desc: prometheus.NewDesc(
			metricName,
			"Current workload assignment for a GPU.",
			[]string{
				"uuid",
				"gpu",
				"workload_source",
				"workload_id",
			},
			prometheus.Labels{pkgmetrics.MetricComponentLabelKey: componentName},
		),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	if c.reader == nil {
		return
	}

	mapping, err := c.reader.Read()
	if err != nil {
		log.Logger.Warnw("failed to read HPC job mapping; omitting workload identity metrics", "error", err)
		return
	}
	if c.gpuUUIDByIndexProvider == nil {
		return
	}
	gpuUUIDByIndex := c.gpuUUIDByIndexProvider()
	var gpuIndexByUUID map[string]string
	if c.gpuIdentifierType == GPUIdentifierUUID {
		gpuIndexByUUID = make(map[string]string, len(gpuUUIDByIndex))
		for gpuIndex, uuid := range gpuUUIDByIndex {
			if uuid != "" {
				gpuIndexByUUID[uuid] = gpuIndex
			}
		}
	}

	for gpuIdentifier, jobIDs := range mapping {
		gpuIndex, uuid, found := resolveGPU(
			gpuIdentifier,
			c.gpuIdentifierType,
			gpuUUIDByIndex,
			gpuIndexByUUID,
		)
		if !found || uuid == "" {
			log.Logger.Infow("HPC job mapping references an unknown GPU; omitting workload identity metrics", "gpuIdentifier", gpuIdentifier)
			continue
		}
		for _, jobID := range jobIDs {
			ch <- prometheus.MustNewConstMetric(
				c.desc,
				prometheus.GaugeValue,
				1,
				uuid,
				gpuIndex,
				workloadSource,
				jobID,
			)
		}
	}
}

func resolveGPU(
	gpuIdentifier string,
	gpuIdentifierType GPUIdentifierType,
	gpuUUIDByIndex map[string]string,
	gpuIndexByUUID map[string]string,
) (gpuIndex string, uuid string, found bool) {
	switch gpuIdentifierType {
	case GPUIdentifierDCGMIndex:
		uuid, found = gpuUUIDByIndex[gpuIdentifier]
		if found {
			return gpuIdentifier, uuid, true
		}
	case GPUIdentifierUUID:
		gpuIndex, found = gpuIndexByUUID[gpuIdentifier]
		if found {
			return gpuIndex, gpuIdentifier, true
		}
	}
	return "", "", false
}
