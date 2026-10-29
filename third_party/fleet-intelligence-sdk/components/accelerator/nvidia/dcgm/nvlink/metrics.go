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

// Package nvlink provides DCGM NVLink metrics collection and reporting.
package nvlink

import (
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/prometheus/client_golang/prometheus"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

// nvlinkFields defines the DCGM fields to monitor for NVLink metrics
var nvlinkFields = []dcgm.Short{
	dcgm.DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL,                       // Total NVLink bandwidth
	dcgm.DCGM_FI_DEV_NVLINK_ERROR_DL_CRC,                          // NVLink DL CRC errors
	dcgm.DCGM_FI_DEV_NVLINK_ERROR_DL_RECOVERY,                     // NVLink DL recovery errors
	dcgm.DCGM_FI_DEV_NVLINK_ERROR_DL_REPLAY,                       // NVLink DL replay errors
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_LINK_RECOVERY_SUCCESSFUL_EVENTS, // Successful link recovery events
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS,     // Failed link recovery events
	dcgm.DCGM_FI_DEV_FABRIC_MANAGER_STATUS,
	dcgm.DCGM_FI_DEV_C2C_LINK_ERROR_REPLAY,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_GENERAL_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_MALFORMED_PACKET_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_REMOTE_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_SYMBOL_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_BUFFER_OVERRUN_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_LOCAL_LINK_INTEGRITY_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_EFFECTIVE_ERRORS,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_EFFECTIVE_BER_FLOAT,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_SYMBOL_BER_FLOAT,
	dcgm.DCGM_FI_DEV_NVLINK_COUNT_TX_DISCARDS,
}

var (
	componentLabel = prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: Name,
	}

	metricDCGMFIDevNvlinkBandwidthTotal = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_nvlink_bandwidth_total",
			Help:      "Total bidirectional NVLink bandwidth across all lanes.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevNvlinkErrorDLCrc = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_nvlink_error_dl_crc",
			Help:      "NVLink CRC Error Counter",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevNvlinkErrorDLRecovery = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_nvlink_error_dl_recovery",
			Help:      "NVLink Recovery Error Counter",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevNvlinkErrorDLReplay = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_nvlink_error_dl_replay",
			Help:      "NVLink Replay Error Counter",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevNvlinkCountLinkRecoverySuccessfulEvents = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_nvlink_count_link_recovery_successful_events",
			Help:      "Number of times link went from Up to recovery, succeeded and link came back up.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevNvlinkCountLinkRecoveryFailedEvents = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{
			Namespace: "",
			Subsystem: "",
			Name:      "dcgm_fi_dev_nvlink_count_link_recovery_failed_events",
			Help:      "Number of times link went from Up to recovery, failed and link was declared down.",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)

	metricDCGMFIDevFabricManagerStatus = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_fabric_manager_status", Help: "The status of the fabric manager - a value from dcgmFabricManagerStatus_t"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevC2CLinkErrorReplay = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_c2c_link_error_replay", Help: "C2C Link Replay Error Counter"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountRxGeneralErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_rx_general_errors", Help: "Total number of packets Rx with header mismatch"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountRxErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_rx_errors", Help: "Total number of packets with errors Rx on a link"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountRxMalformedPacketErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_rx_malformed_packet_errors", Help: "Number of packets Rx on a link where packets are malformed"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountRxRemoteErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_rx_remote_errors", Help: "Total number of packets Rx - stomp/EBP marker"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountRxSymbolErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_rx_symbol_errors", Help: "Number of errors in rx symbols"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountRxBufferOverrunErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_rx_buffer_overrun_errors", Help: "Number of packets that were discarded on Rx due to buffer overrun"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountLocalLinkIntegrityErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_local_link_integrity_errors", Help: "Total number of times that the count of local errors exceeded a threshold"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountEffectiveErrors = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_effective_errors", Help: "Sum of the number of errors in each Nvlink packet"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountEffectiveBERFloat = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_nvlink_count_effective_ber_float", Help: "Effective BER for effective errors - decoded float value"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountSymbolBERFloat = pkgmetrics.NewSampledGaugeVec(
		prometheus.GaugeOpts{Name: "dcgm_fi_dev_nvlink_count_symbol_ber_float", Help: "BER for symbol errors - decoded float value"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
	metricDCGMFIDevNvlinkCountTxDiscards = pkgmetrics.NewSettableCounterVec(
		prometheus.CounterOpts{Name: "dcgm_fi_dev_nvlink_count_tx_discards", Help: "Total number of tx error packets that were discarded"},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
)

func init() {
	pkgmetrics.MustRegister(
		metricDCGMFIDevNvlinkBandwidthTotal,
		metricDCGMFIDevNvlinkErrorDLCrc,
		metricDCGMFIDevNvlinkErrorDLRecovery,
		metricDCGMFIDevNvlinkErrorDLReplay,
		metricDCGMFIDevNvlinkCountLinkRecoverySuccessfulEvents,
		metricDCGMFIDevNvlinkCountLinkRecoveryFailedEvents,
		metricDCGMFIDevFabricManagerStatus,
		metricDCGMFIDevC2CLinkErrorReplay,
		metricDCGMFIDevNvlinkCountRxGeneralErrors,
		metricDCGMFIDevNvlinkCountRxErrors,
		metricDCGMFIDevNvlinkCountRxMalformedPacketErrors,
		metricDCGMFIDevNvlinkCountRxRemoteErrors,
		metricDCGMFIDevNvlinkCountRxSymbolErrors,
		metricDCGMFIDevNvlinkCountRxBufferOverrunErrors,
		metricDCGMFIDevNvlinkCountLocalLinkIntegrityErrors,
		metricDCGMFIDevNvlinkCountEffectiveErrors,
		metricDCGMFIDevNvlinkCountEffectiveBERFloat,
		metricDCGMFIDevNvlinkCountSymbolBERFloat,
		metricDCGMFIDevNvlinkCountTxDiscards,
	)
}
