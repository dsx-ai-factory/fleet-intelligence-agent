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

// Package prof provides DCGM profiling metrics collection and reporting.
package prof

import (
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/prometheus/client_golang/prometheus"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

// profFields defines the DCGM fields to monitor for profiling metrics
var profFields = []dcgm.Short{
	dcgm.DCGM_FI_PROF_GR_ENGINE_ACTIVE, // Graphics engine active ratio
	dcgm.DCGM_FI_PROF_SM_ACTIVE,        // SM active cycles ratio
	dcgm.DCGM_FI_PROF_SM_OCCUPANCY,     // SM occupancy ratio
	dcgm.DCGM_FI_PROF_PIPE_TENSOR_ACTIVE,
	dcgm.DCGM_FI_PROF_PIPE_TENSOR_IMMA_ACTIVE, // Tensor (IMMA) pipe active
	dcgm.DCGM_FI_PROF_PIPE_TENSOR_HMMA_ACTIVE, // Tensor (HMMA) pipe active
	dcgm.DCGM_FI_PROF_PIPE_TENSOR_DFMA_ACTIVE, // Tensor (DFMA) pipe active
	dcgm.DCGM_FI_PROF_PIPE_INT_ACTIVE,         // Integer pipe active
	dcgm.DCGM_FI_PROF_DRAM_ACTIVE,
	dcgm.DCGM_FI_PROF_PIPE_FP64_ACTIVE,
	dcgm.DCGM_FI_PROF_PIPE_FP32_ACTIVE,
	dcgm.DCGM_FI_PROF_PIPE_FP16_ACTIVE,
	dcgm.DCGM_FI_PROF_PCIE_TX_BYTES,
	dcgm.DCGM_FI_PROF_PCIE_RX_BYTES,
	dcgm.DCGM_FI_PROF_NVLINK_TX_BYTES,
	dcgm.DCGM_FI_PROF_NVLINK_RX_BYTES,
}

var (
	componentLabel = prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: Name,
	}

	metricDCGMFIProfGrEngineActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_gr_engine_active",
			Help:      "Ratio of time the graphics engine is active.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfSmActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_sm_active",
			Help:      "Ratio of cycles an SM has at least 1 warp assigned.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfSmOccupancy = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_sm_occupancy",
			Help:      "Ratio of warps resident on an SM relative to the theoretical maximum.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeTensorActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_tensor_active",
			Help:      "Ratio of cycles any tensor pipe is active.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfDramActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_dram_active",
			Help:      "Ratio of cycles the device memory interface is active.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeFp64Active = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_fp64_active",
			Help:      "Ratio of cycles the FP64 pipe is active.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeFp32Active = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_fp32_active",
			Help:      "Ratio of cycles the FP32 pipe is active.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeFp16Active = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_fp16_active",
			Help:      "Ratio of cycles the FP16 pipe is active (excluding HMMA).",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPcieTxBytes = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pcie_tx_bytes",
			Help:      "Number of bytes of active PCIe transmit data from the GPU perspective.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPcieRxBytes = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pcie_rx_bytes",
			Help:      "Number of bytes of active PCIe receive data from the GPU perspective.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfNvlinkTxBytes = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_nvlink_tx_bytes",
			Help:      "Total bytes of active NVLink transmit data including header and payload.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfNvlinkRxBytes = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_nvlink_rx_bytes",
			Help:      "Total bytes of active NVLink receive data including header and payload.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeTensorImmaActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_tensor_imma_active",
			Help:      "The ratio of cycles the tensor (IMMA) pipe is active (off the peak sustained elapsed cycles).",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeTensorHmmaActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_tensor_hmma_active",
			Help:      "The ratio of cycles the tensor (HMMA) pipe is active (off the peak sustained elapsed cycles).",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeTensorDfmaActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_tensor_dfma_active",
			Help:      "The ratio of cycles the tensor (DFMA) pipe is active (off the peak sustained elapsed cycles).",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIProfPipeIntActive = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_prof_pipe_int_active",
			Help:      "Ratio of cycles the integer pipe is active.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
)

func init() {
	pkgmetrics.MustRegister(
		metricDCGMFIProfGrEngineActive,
		metricDCGMFIProfSmActive,
		metricDCGMFIProfSmOccupancy,
		metricDCGMFIProfPipeTensorActive,
		metricDCGMFIProfPipeTensorImmaActive,
		metricDCGMFIProfPipeTensorHmmaActive,
		metricDCGMFIProfPipeTensorDfmaActive,
		metricDCGMFIProfPipeIntActive,
		metricDCGMFIProfDramActive,
		metricDCGMFIProfPipeFp64Active,
		metricDCGMFIProfPipeFp32Active,
		metricDCGMFIProfPipeFp16Active,
		metricDCGMFIProfPcieTxBytes,
		metricDCGMFIProfPcieRxBytes,
		metricDCGMFIProfNvlinkTxBytes,
		metricDCGMFIProfNvlinkRxBytes,
	)
}
