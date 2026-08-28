// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

const (
	sourceDCGM = "dcgm"
)

const (
	componentClock       = "accelerator-nvidia-dcgm-clock"
	componentInforom     = "accelerator-nvidia-dcgm-inforom"
	componentMemory      = "accelerator-nvidia-dcgm-mem"
	componentNVLink      = "accelerator-nvidia-dcgm-nvlink"
	componentPCIe        = "accelerator-nvidia-dcgm-pcie"
	componentPower       = "accelerator-nvidia-dcgm-power"
	componentThermal     = "accelerator-nvidia-dcgm-thermal"
	componentUtilization = "accelerator-nvidia-dcgm-utilization"
)

// metricDefinition preserves the existing FI metric contract while the
// shared library owns the source-specific DCGM field mapping.
type metricDefinition struct {
	signalID   observation.SignalID
	component  string
	name       string
	metricType pkgmetrics.MetricType
}

var metricDefinitions = []metricDefinition{
	{observation.SignalPowerDraw, componentPower, "dcgm_fi_dev_power_usage", pkgmetrics.MetricTypeGauge},
	{observation.SignalPowerLimitEnforced, componentPower, "dcgm_fi_dev_enforced_power_limit", pkgmetrics.MetricTypeGauge},
	{observation.SignalPowerViolationDuration, componentPower, "dcgm_fi_dev_power_violation", pkgmetrics.MetricTypeGauge},
	{observation.SignalReliabilityViolation, componentPower, "dcgm_fi_dev_reliability_violation", pkgmetrics.MetricTypeGauge},
	{observation.SignalBoardLimitViolation, componentPower, "dcgm_fi_dev_board_limit_violation", pkgmetrics.MetricTypeGauge},

	{observation.SignalGPUTemperatureCelsius, componentThermal, "dcgm_fi_dev_gpu_temp", pkgmetrics.MetricTypeGauge},
	{observation.SignalGPUTemperatureHBMCelsius, componentThermal, "dcgm_fi_dev_memory_temp", pkgmetrics.MetricTypeGauge},
	{observation.SignalGPUTemperatureThresholdSlowdownCelsius, componentThermal, "dcgm_fi_dev_slowdown_temp", pkgmetrics.MetricTypeGauge},
	{observation.SignalThermalViolationDuration, componentThermal, "dcgm_fi_dev_thermal_violation", pkgmetrics.MetricTypeGauge},
	{observation.SignalGPUTemperatureSlowdownMarginCelsius, componentThermal, "dcgm_fi_dev_gpu_temp_limit", pkgmetrics.MetricTypeGauge},

	{observation.SignalSMClock, componentClock, "dcgm_fi_dev_sm_clock", pkgmetrics.MetricTypeGauge},
	{observation.SignalMemoryClock, componentClock, "dcgm_fi_dev_mem_clock", pkgmetrics.MetricTypeGauge},
	{observation.SignalClockEventReasons, componentClock, "dcgm_fi_dev_clocks_event_reasons", pkgmetrics.MetricTypeGauge},

	{observation.SignalComputeUtilization, componentUtilization, "dcgm_fi_dev_gpu_util", pkgmetrics.MetricTypeGauge},
	{observation.SignalMemoryCopyUtilization, componentUtilization, "dcgm_fi_dev_mem_copy_util", pkgmetrics.MetricTypeGauge},

	{observation.SignalFramebufferFree, componentMemory, "dcgm_fi_dev_fb_free", pkgmetrics.MetricTypeGauge},
	{observation.SignalFramebufferUsed, componentMemory, "dcgm_fi_dev_fb_used", pkgmetrics.MetricTypeGauge},
	{observation.SignalFramebufferTotal, componentMemory, "dcgm_fi_dev_fb_total", pkgmetrics.MetricTypeGauge},
	{observation.SignalFramebufferUsedRatio, componentMemory, "dcgm_fi_dev_fb_used_percent", pkgmetrics.MetricTypeGauge},
	{observation.SignalUncorrectableRemappedRows, componentMemory, "dcgm_fi_dev_uncorrectable_remapped_rows", pkgmetrics.MetricTypeGauge},
	{observation.SignalCorrectableRemappedRows, componentMemory, "dcgm_fi_dev_correctable_remapped_rows", pkgmetrics.MetricTypeGauge},
	{observation.SignalRowRemapFailure, componentMemory, "dcgm_fi_dev_row_remap_failure", pkgmetrics.MetricTypeGauge},
	{observation.SignalRowRemapPending, componentMemory, "dcgm_fi_dev_row_remap_pending", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCSingleBitVolatileTotal, componentMemory, "dcgm_fi_dev_ecc_sbe_vol_total", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCDoubleBitVolatileTotal, componentMemory, "dcgm_fi_dev_ecc_dbe_vol_total", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCSingleBitAggregateTotal, componentMemory, "dcgm_fi_dev_ecc_sbe_agg_total", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCDoubleBitAggregateTotal, componentMemory, "dcgm_fi_dev_ecc_dbe_agg_total", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCSingleBitVolatileDevice, componentMemory, "dcgm_fi_dev_ecc_sbe_vol_dev", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCDoubleBitVolatileDevice, componentMemory, "dcgm_fi_dev_ecc_dbe_vol_dev", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCSingleBitAggregateDevice, componentMemory, "dcgm_fi_dev_ecc_sbe_agg_dev", pkgmetrics.MetricTypeGauge},
	{observation.SignalECCDoubleBitAggregateDevice, componentMemory, "dcgm_fi_dev_ecc_dbe_agg_dev", pkgmetrics.MetricTypeGauge},
	{observation.SignalRemapRowsAvailableHigh, componentMemory, "dcgm_fi_dev_banks_remap_rows_avail_high", pkgmetrics.MetricTypeGauge},
	{observation.SignalRemapRowsAvailableLow, componentMemory, "dcgm_fi_dev_banks_remap_rows_avail_low", pkgmetrics.MetricTypeGauge},
	{observation.SignalRemapRowsAvailableMax, componentMemory, "dcgm_fi_dev_banks_remap_rows_avail_max", pkgmetrics.MetricTypeGauge},
	{observation.SignalRemapRowsAvailableNone, componentMemory, "dcgm_fi_dev_banks_remap_rows_avail_none", pkgmetrics.MetricTypeGauge},
	{observation.SignalRemapRowsAvailablePartial, componentMemory, "dcgm_fi_dev_banks_remap_rows_avail_partial", pkgmetrics.MetricTypeGauge},

	{observation.SignalPCIeReplayCount, componentPCIe, "dcgm_fi_dev_pcie_replay_counter", pkgmetrics.MetricTypeGauge},

	{observation.SignalNVLinkBandwidthTotal, componentNVLink, "dcgm_fi_dev_nvlink_bandwidth_total", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkDLCrcErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_error_dl_crc", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkDLRecoveryErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_error_dl_recovery", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkDLReplayErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_error_dl_replay", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRecoverySuccessfulCount, componentNVLink, "dcgm_fi_dev_nvlink_count_link_recovery_successful_events", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRecoveryFailedCount, componentNVLink, "dcgm_fi_dev_nvlink_count_link_recovery_failed_events", pkgmetrics.MetricTypeGauge},
	{observation.SignalFabricManagerStatus, componentNVLink, "dcgm_fi_dev_fabric_manager_status", pkgmetrics.MetricTypeGauge},
	{observation.SignalC2CLinkReplayErrorCount, componentNVLink, "dcgm_fi_dev_c2c_link_error_replay", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRXGeneralErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_rx_general_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRXErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_rx_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRXMalformedPacketErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_rx_malformed_packet_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRXRemoteErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_rx_remote_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRXSymbolErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_rx_symbol_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkRXBufferOverrunErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_rx_buffer_overrun_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkLocalIntegrityErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_local_link_integrity_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkEffectiveErrorCount, componentNVLink, "dcgm_fi_dev_nvlink_count_effective_errors", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkEffectiveBER, componentNVLink, "dcgm_fi_dev_nvlink_count_effective_ber_float", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkSymbolBER, componentNVLink, "dcgm_fi_dev_nvlink_count_symbol_ber_float", pkgmetrics.MetricTypeGauge},
	{observation.SignalNVLinkTXDiscardCount, componentNVLink, "dcgm_fi_dev_nvlink_count_tx_discards", pkgmetrics.MetricTypeGauge},

	{observation.SignalInforomConfigurationValid, componentInforom, "dcgm_fi_dev_inforom_config_valid", pkgmetrics.MetricTypeGauge},
}

var metricDefinitionBySignal = indexMetricDefinitionsBySignal(metricDefinitions)

// MetricsFromObservations converts successful DCGM observations into FI's
// current database metric model. FI metrics use the scrape time, matching the
// existing Prometheus scraper and dcgm-exporter. Canonical observations retain
// their source timestamps for consumers that need freshness information.
// Collection errors are returned separately and never become numeric samples.
func MetricsFromObservations(observations []*observation.Observation, scrapedAt time.Time) (pkgmetrics.Metrics, []*observation.Observation, []error) {
	indexes := gpuIndexes(observations)
	metrics := make(pkgmetrics.Metrics, 0, len(observations))
	collectionErrors := make([]*observation.Observation, 0)
	var projectionErrors []error

	for _, current := range observations {
		if current == nil || current.GetSource() != sourceDCGM {
			continue
		}
		definition, supported := metricDefinitionBySignal[current.GetSignalId()]
		if !supported {
			continue
		}
		if current.GetCollectionError() != nil {
			collectionErrors = append(collectionErrors, current)
			continue
		}

		entity := current.GetEntity()
		if entity == nil || entity.GetType() != "gpu" || entity.GetId() == "" {
			projectionErrors = append(projectionErrors, fmt.Errorf("project signal %q: GPU entity is required", current.GetSignalId()))
			continue
		}
		value, err := numericValue(current.GetValue())
		if err != nil {
			projectionErrors = append(projectionErrors, fmt.Errorf("project signal %q for GPU %q: %w", current.GetSignalId(), entity.GetId(), err))
			continue
		}
		if err := validateUnit(current); err != nil {
			projectionErrors = append(projectionErrors, err)
			continue
		}
		observedAt := current.GetObservedAt()
		if observedAt == nil || observedAt.CheckValid() != nil {
			projectionErrors = append(projectionErrors, fmt.Errorf("project signal %q for GPU %q: valid observation timestamp is required", current.GetSignalId(), entity.GetId()))
			continue
		}

		labels := map[string]string{"uuid": entity.GetId()}
		if index, exists := indexes[entity.GetId()]; exists {
			labels["gpu"] = index
		}
		metrics = append(metrics, pkgmetrics.Metric{
			UnixMilliseconds: scrapedAt.UnixMilli(),
			Component:        definition.component,
			Name:             definition.name,
			Type:             definition.metricType,
			Value:            value,
			Labels:           labels,
		})
	}

	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].Name != metrics[j].Name {
			return metrics[i].Name < metrics[j].Name
		}
		if metrics[i].Labels["gpu"] != metrics[j].Labels["gpu"] {
			return metrics[i].Labels["gpu"] < metrics[j].Labels["gpu"]
		}
		return metrics[i].Labels["uuid"] < metrics[j].Labels["uuid"]
	})
	return metrics, collectionErrors, projectionErrors
}

func indexMetricDefinitionsBySignal(definitions []metricDefinition) map[observation.SignalID]metricDefinition {
	indexed := make(map[observation.SignalID]metricDefinition, len(definitions))
	for _, definition := range definitions {
		indexed[definition.signalID] = definition
	}
	return indexed
}

func gpuIndexes(observations []*observation.Observation) map[string]string {
	indexes := make(map[string]string)
	for _, current := range observations {
		if current == nil || current.GetSource() != sourceDCGM || current.GetSignalId() != observation.SignalGPUInventoryIndex || current.GetCollectionError() != nil {
			continue
		}
		entity := current.GetEntity()
		value := current.GetValue()
		if entity == nil || entity.GetId() == "" || value == nil {
			continue
		}
		if index, ok := value.GetKind().(*observation.Value_IntValue); ok {
			indexes[entity.GetId()] = strconv.FormatInt(index.IntValue, 10)
		}
	}
	return indexes
}

func numericValue(value *observation.Value) (float64, error) {
	if value == nil {
		return 0, fmt.Errorf("numeric value is required")
	}
	switch current := value.GetKind().(type) {
	case *observation.Value_IntValue:
		return float64(current.IntValue), nil
	case *observation.Value_DoubleValue:
		return current.DoubleValue, nil
	default:
		return 0, fmt.Errorf("numeric value is required")
	}
}

func validateUnit(current *observation.Observation) error {
	expected := observation.UnitForSignal(current.GetSignalId())
	if expected == nil {
		return nil
	}
	if current.Unit == nil || current.GetUnit() != *expected {
		return fmt.Errorf("project signal %q: unit %q does not match canonical unit %q", current.GetSignalId(), current.GetUnit(), *expected)
	}
	return nil
}
