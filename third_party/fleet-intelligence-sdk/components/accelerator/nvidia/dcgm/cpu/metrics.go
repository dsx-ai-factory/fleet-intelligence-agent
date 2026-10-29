// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

// Package cpu provides DCGM CPU metrics collection and reporting.
package cpu

import (
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/prometheus/client_golang/prometheus"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

var cpuLevelFields = []dcgm.Short{
	dcgm.DCGM_FI_DEV_CPU_TEMP_CURRENT,       // 1110 - CPU
	dcgm.DCGM_FI_DEV_CPU_POWER_UTIL_CURRENT, // 1130 - CPU
	dcgm.DCGM_FI_DEV_CPU_POWER_LIMIT,        // 1131 - CPU
	dcgm.DCGM_FI_DEV_CPU_TEMP_WARNING,       // 1111 - CPU
	dcgm.DCGM_FI_DEV_CPU_TEMP_CRITICAL,      // 1112 - CPU
}

var (
	componentLabel = prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: Name,
	}

	// CPU-level metrics (use cpu_id label)
	metricDCGMFIDevCPUTempCurrent = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_cpu_temp_current",
			Help:      "CPU temperature.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "cpu_id"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevCPUPowerLimit = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_cpu_power_limit",
			Help:      "CPU power limit.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "cpu_id"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevCPUTempWarning = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_cpu_temp_warning", Help: "CPU Warning Temperature"},
		[]string{pkgmetrics.MetricComponentLabelKey, "cpu_id"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevCPUTempCritical = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_cpu_temp_critical", Help: "CPU Critical Temperature"},
		[]string{pkgmetrics.MetricComponentLabelKey, "cpu_id"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevCPUPowerUtilCurrent = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_cpu_power_util_current", Help: "CPU power utilization"},
		[]string{pkgmetrics.MetricComponentLabelKey, "cpu_id"},
	).MustCurryWith(componentLabel)
)

func init() {
	pkgmetrics.MustRegister(
		metricDCGMFIDevCPUTempCurrent,
		metricDCGMFIDevCPUPowerLimit,
		metricDCGMFIDevCPUTempWarning,
		metricDCGMFIDevCPUTempCritical,
		metricDCGMFIDevCPUPowerUtilCurrent,
	)
}
