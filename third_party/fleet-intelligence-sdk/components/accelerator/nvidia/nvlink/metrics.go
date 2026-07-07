// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink

import (
	"github.com/prometheus/client_golang/prometheus"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

const (
	MetricLinkProbeStatus     = "health_input_nvlink_link_probe_status"
	MetricLinkObservedMask    = "health_input_nvlink_link_observed_mask"
	MetricLinkPresentMask     = "health_input_nvlink_link_present_mask"
	MetricLinkEnabledMask     = "health_input_nvlink_link_enabled_mask"
	MetricLinkUnsupportedMask = "health_input_nvlink_link_unsupported_mask"
	MetricLinkErrorMask       = "health_input_nvlink_link_error_mask"
	MetricFabricHealthMask    = "health_input_nvlink_fabric_health_mask"
	MetricFabricHealthStatus  = "health_input_nvlink_fabric_health_status"
	MetricLinkCount           = "health_input_nvlink_link_count"
	MetricLinkCountStatus     = "health_input_nvlink_link_count_status"
	MetricCommonSpeed         = "health_input_nvlink_common_speed_mbytes_per_sec"
	MetricCommonSpeedStatus   = "health_input_nvlink_common_speed_status"
)

var (
	componentLabel = prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: Name,
	}

	metricNVLinkHealthInputs = map[string]*prometheus.GaugeVec{
		SignalLinkProbeStatus:     newNVLinkHealthInputMetric(MetricLinkProbeStatus, "Collection status for NVLink link observations."),
		SignalLinkObservedMask:    newNVLinkHealthInputMetric(MetricLinkObservedMask, "Bit mask of observed NVLink link indices."),
		SignalLinkPresentMask:     newNVLinkHealthInputMetric(MetricLinkPresentMask, "Bit mask of present NVLink links."),
		SignalLinkEnabledMask:     newNVLinkHealthInputMetric(MetricLinkEnabledMask, "Bit mask of enabled NVLink links."),
		SignalLinkUnsupportedMask: newNVLinkHealthInputMetric(MetricLinkUnsupportedMask, "Bit mask of NVLink links whose state is unsupported."),
		SignalLinkErrorMask:       newNVLinkHealthInputMetric(MetricLinkErrorMask, "Bit mask of NVLink links whose state collection failed."),
		SignalFabricHealthMask:    newNVLinkHealthInputMetric(MetricFabricHealthMask, "NVLink fabric health bit mask."),
		SignalFabricHealthStatus:  newNVLinkHealthInputMetric(MetricFabricHealthStatus, "Collection status for the NVLink fabric health mask."),
		SignalLinkCount:           newNVLinkHealthInputMetric(MetricLinkCount, "Number of NVLink links reported for the GPU."),
		SignalLinkCountStatus:     newNVLinkHealthInputMetric(MetricLinkCountStatus, "Collection status for the NVLink link count."),
		SignalCommonSpeed:         newNVLinkHealthInputMetric(MetricCommonSpeed, "Common NVLink speed in megabytes per second."),
		SignalCommonSpeedStatus:   newNVLinkHealthInputMetric(MetricCommonSpeedStatus, "Collection status for the common NVLink speed."),
	}
)

func init() {
	for _, metric := range metricNVLinkHealthInputs {
		pkgmetrics.MustRegister(metric)
	}
}

func recordNVLinkSourceMetrics(metrics []nvlinkSourceMetric) {
	for _, metric := range metrics {
		collector, ok := metricNVLinkHealthInputs[metric.signal]
		if !ok {
			continue
		}
		collector.With(prometheus.Labels{
			"uuid": metric.uuid,
			"gpu":  metric.gpu,
		}).Set(metric.value)
	}
}

func newNVLinkHealthInputMetric(name, help string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: name, Help: help},
		[]string{pkgmetrics.MetricComponentLabelKey, "uuid", "gpu"},
	).MustCurryWith(componentLabel)
}
