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

// Package mem provides DCGM memory metrics collection and reporting.
package mem

import (
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/prometheus/client_golang/prometheus"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

// memFields defines the DCGM fields to monitor for memory metrics
var memFields = []dcgm.Short{
	dcgm.DCGM_FI_DEV_FB_FREE,                        // Free Frame Buffer in MB
	dcgm.DCGM_FI_DEV_FB_USED,                        // Used Frame Buffer in MB
	dcgm.DCGM_FI_DEV_FB_TOTAL,                       // Total Frame Buffer in MB
	dcgm.DCGM_FI_DEV_FB_USED_PERCENT,                // Percentage used of Frame Buffer: 'Used/(Total - Reserved)'
	dcgm.DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS,    // Number of remapped rows for uncorrectable errors
	dcgm.DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS,      // Number of remapped rows for correctable errors
	dcgm.DCGM_FI_DEV_ROW_REMAP_FAILURE,              // Whether remapping of rows has failed
	dcgm.DCGM_FI_DEV_ROW_REMAP_PENDING,              // Whether remapping of rows is pending
	dcgm.DCGM_FI_DEV_ECC_SBE_VOL_TOTAL,              // Total single bit volatile ECC errors
	dcgm.DCGM_FI_DEV_ECC_DBE_VOL_TOTAL,              // Total double bit volatile ECC errors
	dcgm.DCGM_FI_DEV_ECC_SBE_AGG_TOTAL,              // Total single bit aggregate (persistent) ECC errors
	dcgm.DCGM_FI_DEV_ECC_DBE_AGG_TOTAL,              // Total double bit aggregate (persistent) ECC errors
	dcgm.DCGM_FI_DEV_ECC_SBE_VOL_DEV,                // Single bit volatile ECC errors detected in device memory
	dcgm.DCGM_FI_DEV_ECC_DBE_VOL_DEV,                // Double bit volatile ECC errors detected in device memory
	dcgm.DCGM_FI_DEV_ECC_SBE_AGG_DEV,                // Aggregate single bit ECC errors detected in device memory
	dcgm.DCGM_FI_DEV_ECC_DBE_AGG_DEV,                // Aggregate double bit ECC errors detected in device memory
	dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_HIGH,    // Banks with high remap row availability
	dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_LOW,     // Banks with low remap row availability
	dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_MAX,     // Banks with max remap row availability
	dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_NONE,    // Banks with no remap row availability
	dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_PARTIAL, // Banks with partial remap row availability
}

var (
	componentLabel = prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: Name,
	}

	metricDCGMFIDevFBFree = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_fb_free",
			Help:      "Free Frame Buffer in MB.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevFBUsed = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_fb_used",
			Help:      "Used Frame Buffer in MB.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevFBTotal = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_fb_total",
			Help:      "Total Frame Buffer in MB.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevUncorrectableRemappedRows = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_uncorrectable_remapped_rows",
			Help:      "Number of remapped rows for uncorrectable errors.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevCorrectableRemappedRows = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_correctable_remapped_rows",
			Help:      "Number of remapped rows for correctable errors.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevRowRemapFailure = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_row_remap_failure",
			Help:      "Whether remapping of rows has failed (0=no failure, 1=failure).",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevFBUsedPercent = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_fb_used_percent",
			Help:      "Percentage used of Frame Buffer: 'Used/(Total - Reserved)'. Range 0.0-1.0",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevRowRemapPending = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_row_remap_pending",
			Help:      "Whether remapping of rows is pending (0=no pending, 1=pending).",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCSBEVolTotal = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_ecc_sbe_vol_total",
			Help:      "Total single bit volatile ECC errors.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCDBEVolTotal = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_ecc_dbe_vol_total",
			Help:      "Total double bit volatile ECC errors.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCSBEAggTotal = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_ecc_sbe_agg_total",
			Help:      "Total single bit aggregate (persistent) ECC errors. Note: monotonically increasing.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCDBAggTotal = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_ecc_dbe_agg_total",
			Help:      "Total double bit aggregate (persistent) ECC errors. Note: monotonically increasing.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCSBEVolDev = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Name: "dcgm_fi_dev_ecc_sbe_vol_dev",
			Help: "Device memory single bit volatile ECC errors.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCDBEVolDev = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Name: "dcgm_fi_dev_ecc_dbe_vol_dev",
			Help: "Device memory double bit volatile ECC errors.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCSBEAggDev = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Name: "dcgm_fi_dev_ecc_sbe_agg_dev",
			Help: "Device memory single bit aggregate (persistent) ECC errors. Note: monotonically increasing.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevECCDBEAggDev = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Name: "dcgm_fi_dev_ecc_dbe_agg_dev",
			Help: "Device memory double bit aggregate (persistent) ECC errors. Note: monotonically increasing.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevBanksRemapRowsAvailHigh = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_banks_remap_rows_avail_high", Help: "Historical high mark of available spare memory rows per memory bank"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevBanksRemapRowsAvailLow = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_banks_remap_rows_avail_low", Help: "Historical low mark of available spare memory rows per memory bank"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevBanksRemapRowsAvailMax = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_banks_remap_rows_avail_max", Help: "Historical max available spare memory rows per memory bank"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevBanksRemapRowsAvailNone = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_banks_remap_rows_avail_none", Help: "Historical marker of memory banks with no available spare memory rows"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevBanksRemapRowsAvailPartial = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_banks_remap_rows_avail_partial", Help: "Historical mark of partial available spare memory rows per memory bank"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
)

func init() {
	pkgmetrics.MustRegister(
		metricDCGMFIDevFBFree,
		metricDCGMFIDevFBUsed,
		metricDCGMFIDevFBTotal,
		metricDCGMFIDevFBUsedPercent,
		metricDCGMFIDevUncorrectableRemappedRows,
		metricDCGMFIDevCorrectableRemappedRows,
		metricDCGMFIDevRowRemapFailure,
		metricDCGMFIDevRowRemapPending,
		metricDCGMFIDevECCSBEVolTotal,
		metricDCGMFIDevECCDBEVolTotal,
		metricDCGMFIDevECCSBEAggTotal,
		metricDCGMFIDevECCDBAggTotal,
		metricDCGMFIDevECCSBEVolDev,
		metricDCGMFIDevECCDBEVolDev,
		metricDCGMFIDevECCSBEAggDev,
		metricDCGMFIDevECCDBEAggDev,
		metricDCGMFIDevBanksRemapRowsAvailHigh,
		metricDCGMFIDevBanksRemapRowsAvailLow,
		metricDCGMFIDevBanksRemapRowsAvailMax,
		metricDCGMFIDevBanksRemapRowsAvailNone,
		metricDCGMFIDevBanksRemapRowsAvailPartial,
	)
}
