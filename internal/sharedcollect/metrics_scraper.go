// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"context"
	"fmt"
	"time"

	pkglog "github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

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
	scrapedAt := time.Now().UTC()
	if err != nil {
		// A collection can return usable observations together with an error.
		pkglog.Logger.Errorw("shared metric collection failed", "error", err)
	}
	if batch == nil {
		return nil, nil
	}

	metrics, collectionErrors, projectionErrors := MetricsFromObservations(batch.GetObservations(), scrapedAt)
	logCollectionErrors("metrics", collectionErrors)
	for _, projectionErr := range projectionErrors {
		pkglog.Logger.Errorw("failed to project shared metric", "error", projectionErr)
	}
	return metrics, nil
}
