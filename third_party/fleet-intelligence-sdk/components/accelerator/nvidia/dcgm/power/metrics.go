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

// Package power provides DCGM power metrics collection and reporting.
package power

import (
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/prometheus/client_golang/prometheus"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

// powerFields defines the DCGM fields to monitor for power metrics
var powerFields = []dcgm.Short{
	dcgm.DCGM_FI_DEV_POWER_USAGE,          // Power usage for the device in Watts
	dcgm.DCGM_FI_DEV_ENFORCED_POWER_LIMIT, // Effective power limit that the driver enforces after taking into account all limiters
	dcgm.DCGM_FI_DEV_POWER_VIOLATION,
	dcgm.DCGM_FI_DEV_RELIABILITY_VIOLATION,
	dcgm.DCGM_FI_DEV_BOARD_LIMIT_VIOLATION,
}

var (
	componentLabel = prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: Name,
	}

	metricDCGMFIDevPowerUsage = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_power_usage",
			Help:      "Power usage for the device in Watts.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevEnforcedPowerLimit = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_enforced_power_limit",
			Help:      "Effective power limit that the driver enforces after taking into account all limiters.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevPowerViolation = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_power_violation", Help: "Power Violation time in ns"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevReliabilityViolation = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_reliability_violation", Help: "Reliability violation limit"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevBoardLimitViolation = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_board_limit_violation", Help: "Board violation limit"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
)

func init() {
	pkgmetrics.MustRegister(
		metricDCGMFIDevPowerUsage,
		metricDCGMFIDevEnforcedPowerLimit,
		metricDCGMFIDevPowerViolation,
		metricDCGMFIDevReliabilityViolation,
		metricDCGMFIDevBoardLimitViolation,
	)
}
