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

// legacyMetricsScraper removes metrics that the shared library now supplies.
// It otherwise preserves the existing Prometheus scrape unchanged.
type legacyMetricsScraper struct {
	scraper pkgmetrics.Scraper
}

// NewLegacyMetricsScraper wraps the existing FI metrics scraper.
func NewLegacyMetricsScraper(scraper pkgmetrics.Scraper) pkgmetrics.Scraper {
	return &legacyMetricsScraper{scraper: scraper}
}

func (scraper *legacyMetricsScraper) Scrape(ctx context.Context) (pkgmetrics.Metrics, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if scraper == nil || scraper.scraper == nil {
		return nil, nil
	}

	metrics, err := scraper.scraper.Scrape(ctx)
	if err != nil {
		return nil, err
	}
	return metricsWithoutSharedSignals(metrics), nil
}

// sharedMetricsScraper projects shared-library observations into FI metrics.
type sharedMetricsScraper struct {
	collect func(context.Context) (*observation.ObservationBatch, error)
}

// NewSharedMetricsScraper creates a scraper backed by shared collection.
func NewSharedMetricsScraper(
	collect func(context.Context) (*observation.ObservationBatch, error),
) pkgmetrics.Scraper {
	return &sharedMetricsScraper{collect: collect}
}

func (scraper *sharedMetricsScraper) Scrape(ctx context.Context) (pkgmetrics.Metrics, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if scraper == nil || scraper.collect == nil {
		return nil, nil
	}

	batch, err := scraper.collect(ctx)
	if err != nil {
		// A collection can return usable observations together with an error.
		pkglog.Logger.Errorw("shared metric collection failed", "error", err)
	}
	if batch == nil {
		return nil, nil
	}

	metrics, collectionErrors, projectionErrors := MetricsFromObservations(batch.GetObservations())
	logCollectionErrors("metrics", collectionErrors)
	for _, projectionErr := range projectionErrors {
		pkglog.Logger.Errorw("failed to project shared metric", "error", projectionErr)
	}
	return metrics, nil
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
