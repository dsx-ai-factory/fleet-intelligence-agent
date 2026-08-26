// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"context"
	"fmt"

	pkglog "github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// metricsAdapter combines metrics that have not migrated with metrics produced
// from shared-library observations. Legacy samples with migrated names are
// removed even when shared collection fails, so an old Prometheus gauge cannot
// refresh a stale value for a failed GPU.
type metricsAdapter struct {
	legacy  pkgmetrics.Scraper
	collect func(context.Context) (*observation.ObservationBatch, error)
}

// NewMetricsAdapter combines an existing FI scraper with shared collection.
func NewMetricsAdapter(
	legacy pkgmetrics.Scraper,
	collect func(context.Context) (*observation.ObservationBatch, error),
) pkgmetrics.Scraper {
	return &metricsAdapter{legacy: legacy, collect: collect}
}

// Scrape collects both paths independently so a failure in one does not hide
// successful metrics from the other.
func (adapter *metricsAdapter) Scrape(ctx context.Context) (pkgmetrics.Metrics, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}

	metrics := make(pkgmetrics.Metrics, 0)
	if adapter != nil && adapter.legacy != nil {
		legacy, err := adapter.legacy.Scrape(ctx)
		if err != nil {
			pkglog.Logger.Errorw("failed to scrape legacy metrics", "error", err)
		} else {
			metrics = append(metrics, metricsWithoutSharedSignals(legacy)...)
		}
	}

	if adapter == nil || adapter.collect == nil {
		return metrics, nil
	}
	batch, err := adapter.collect(ctx)
	if err != nil {
		pkglog.Logger.Errorw("shared metric collection failed", "error", err)
	}
	if batch == nil {
		return metrics, nil
	}

	sharedMetrics, collectionErrors, projectionErrors := MetricsFromObservations(batch.GetObservations())
	logCollectionErrors("metrics", collectionErrors)
	for _, projectionErr := range projectionErrors {
		pkglog.Logger.Errorw("failed to project shared metric", "error", projectionErr)
	}
	return append(metrics, sharedMetrics...), nil
}

func metricsWithoutSharedSignals(metrics pkgmetrics.Metrics) pkgmetrics.Metrics {
	filtered := make(pkgmetrics.Metrics, 0, len(metrics))
	for _, metric := range metrics {
		definition, migrated := metricDefinitionByName[metric.Name]
		if migrated && metric.Component == definition.component {
			continue
		}
		filtered = append(filtered, metric)
	}
	return filtered
}
