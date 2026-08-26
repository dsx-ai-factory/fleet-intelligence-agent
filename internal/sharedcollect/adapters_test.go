// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"context"
	"errors"
	"testing"
	"time"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect/observation"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/inventory"
)

type scraperFunc func(context.Context) (pkgmetrics.Metrics, error)

func (function scraperFunc) Scrape(ctx context.Context) (pkgmetrics.Metrics, error) {
	return function(ctx)
}

type inventorySourceFunc func(context.Context) (*inventory.Snapshot, error)

func (function inventorySourceFunc) Collect(ctx context.Context) (*inventory.Snapshot, error) {
	return function(ctx)
}

func TestMetricsScrapersSeparateLegacyAndSharedMetrics(t *testing.T) {
	timestamp := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	entity := &observation.Entity{Type: "gpu", Id: "GPU-test"}
	batch := &observation.ObservationBatch{
		CollectionId: "test-cycle",
		Observations: []*observation.Observation{
			testIntObservation(observation.SignalGPUInventoryIndex, sourceDCGM, entity, timestamp, 0),
			testDoubleObservation(observation.SignalPowerDraw, sourceDCGM, entity, timestamp, 125.5),
			observation.NewCollectionErrorObservation(
				observation.SignalGPUTemperatureCelsius,
				sourceDCGM,
				entity,
				timestamppb.New(timestamp),
				observation.UnitForSignal(observation.SignalGPUTemperatureCelsius),
				&observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable},
			),
		},
	}
	legacy := pkgmetrics.Metrics{
		{Component: componentPower, Name: "dcgm_fi_dev_power_usage", Value: 999},
		{Component: "another-component", Name: "dcgm_fi_dev_power_usage", Value: 10},
		{Component: "system-cpu", Name: "cpu_usage", Value: 20},
	}
	legacyScraper := NewLegacyMetricsScraper(scraperFunc(func(context.Context) (pkgmetrics.Metrics, error) {
		return legacy, nil
	}))
	sharedScraper := NewSharedMetricsScraper(func(context.Context) (*observation.ObservationBatch, error) {
		return batch, nil
	})

	legacyMetrics, err := legacyScraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Equal(t, pkgmetrics.Metrics{
		{Component: "another-component", Name: "dcgm_fi_dev_power_usage", Value: 10},
		{Component: "system-cpu", Name: "cpu_usage", Value: 20},
	}, legacyMetrics)

	sharedMetrics, err := sharedScraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Len(t, sharedMetrics, 1)
	require.Equal(t, componentPower, sharedMetrics[0].Component)
	require.Equal(t, 125.5, sharedMetrics[0].Value)
	require.Equal(t, map[string]string{"gpu": "0", "uuid": "GPU-test"}, sharedMetrics[0].Labels)
}

func TestMetricsScrapersRemainIndependentWhenSharedCollectionFails(t *testing.T) {
	legacyScraper := NewLegacyMetricsScraper(scraperFunc(func(context.Context) (pkgmetrics.Metrics, error) {
		return pkgmetrics.Metrics{
			{Component: componentPower, Name: "dcgm_fi_dev_power_usage", Value: 999},
			{Component: "system-cpu", Name: "cpu_usage", Value: 20},
		}, nil
	}))
	sharedScraper := NewSharedMetricsScraper(func(context.Context) (*observation.ObservationBatch, error) {
		return nil, errors.New("DCGM unavailable")
	})

	legacyMetrics, err := legacyScraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Equal(t, pkgmetrics.Metrics{{Component: "system-cpu", Name: "cpu_usage", Value: 20}}, legacyMetrics)

	sharedMetrics, err := sharedScraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Empty(t, sharedMetrics)
}

func TestInventoryAdapterReplacesOnlyGPUInventory(t *testing.T) {
	timestamp := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	entity := &observation.Entity{Type: "gpu", Id: "GPU-test"}
	batch := &observation.ObservationBatch{
		CollectionId: "test-cycle",
		Observations: []*observation.Observation{
			testIntObservation(observation.SignalGPUInventoryIndex, sourceDCGM, entity, timestamp, 3),
			testStringObservation(observation.SignalGPUInventoryModel, sourceNVML, entity, timestamp, "NVIDIA H100"),
		},
	}
	baseSnapshot := &inventory.Snapshot{
		Hostname: "node-1",
		Resources: inventory.Resources{
			CPUInfo: inventory.CPUInfo{Type: "CPU"},
			GPUInfo: inventory.GPUInfo{GPUs: []inventory.GPUDevice{{UUID: "legacy-GPU"}}},
		},
	}
	adapter := NewInventoryAdapter(inventorySourceFunc(func(context.Context) (*inventory.Snapshot, error) {
		return baseSnapshot, nil
	}), func(context.Context) (*observation.ObservationBatch, error) {
		return batch, nil
	})

	snapshot, err := adapter.Collect(context.Background())
	require.NoError(t, err)
	require.Equal(t, "node-1", snapshot.Hostname)
	require.Equal(t, "CPU", snapshot.Resources.CPUInfo.Type)
	require.Equal(t, "NVIDIA-H100", snapshot.Resources.GPUInfo.Product)
	require.Equal(t, []inventory.GPUDevice{{UUID: "GPU-test", GPUIndex: "3"}}, snapshot.Resources.GPUInfo.GPUs)
}

func TestInventoryAdapterRejectsUnverifiedEmptyInventory(t *testing.T) {
	timestamp := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	batch := &observation.ObservationBatch{
		CollectionId: "test-cycle",
		Observations: []*observation.Observation{
			observation.NewCollectionErrorObservation(
				observation.SignalGPUInventoryModel,
				sourceNVML,
				&observation.Entity{Type: "gpu", Id: "nvml-index-0"},
				timestamppb.New(timestamp),
				nil,
				&observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable},
			),
		},
	}
	adapter := NewInventoryAdapter(inventorySourceFunc(func(context.Context) (*inventory.Snapshot, error) {
		return &inventory.Snapshot{}, nil
	}), func(context.Context) (*observation.ObservationBatch, error) {
		return batch, nil
	})

	snapshot, err := adapter.Collect(context.Background())
	require.Nil(t, snapshot)
	require.ErrorContains(t, err, "shared GPU inventory produced no GPUs")
}
