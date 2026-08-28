// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"context"
	"errors"
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect/observation"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

func TestSharedMetricsScraperProjectsObservations(t *testing.T) {
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
	sharedScraper := NewSharedMetricsScraper(func(context.Context) (*observation.ObservationBatch, error) {
		return batch, nil
	})

	beforeScrape := time.Now().UTC()
	sharedMetrics, err := sharedScraper.Scrape(context.Background())
	afterScrape := time.Now().UTC()
	require.NoError(t, err)
	require.Len(t, sharedMetrics, 1)
	require.GreaterOrEqual(t, sharedMetrics[0].UnixMilliseconds, beforeScrape.UnixMilli())
	require.LessOrEqual(t, sharedMetrics[0].UnixMilliseconds, afterScrape.UnixMilli())
	require.NotEqual(t, timestamp.UnixMilli(), sharedMetrics[0].UnixMilliseconds)
	require.Equal(t, componentPower, sharedMetrics[0].Component)
	require.Equal(t, 125.5, sharedMetrics[0].Value)
	require.Equal(t, map[string]string{"gpu": "0", "uuid": "GPU-test"}, sharedMetrics[0].Labels)
}

func TestSharedMetricsScraperReturnsNoMetricsWhenCollectionFails(t *testing.T) {
	sharedScraper := NewSharedMetricsScraper(func(context.Context) (*observation.ObservationBatch, error) {
		return nil, errors.New("DCGM unavailable")
	})

	sharedMetrics, err := sharedScraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Empty(t, sharedMetrics)
}

func TestCollectMachineInfoReplacesNVIDIAInfo(t *testing.T) {
	timestamp := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	entity := &observation.Entity{Type: "gpu", Id: "GPU-test"}
	batch := &observation.ObservationBatch{
		CollectionId: "test-cycle",
		Observations: []*observation.Observation{
			testIntObservation(observation.SignalGPUInventoryIndex, sourceDCGM, entity, timestamp, 3),
			testStringObservation(observation.SignalGPUInventoryModel, sourceDCGM, entity, timestamp, "NVIDIA H100"),
			testStringObservation(observation.SignalNodeNVIDIADriverVersion, sourceDCGM, &observation.Entity{Type: "node"}, timestamp, "580.173.02"),
			testStringObservation(observation.SignalNodeNVIDIACUDADriverVersion, sourceDCGM, &observation.Entity{Type: "node"}, timestamp, "13.0"),
		},
	}
	baseInfo := &machineinfo.MachineInfo{
		Hostname: "node-1",
		CPUInfo:  &apiv1.MachineCPUInfo{Type: "CPU"},
		GPUInfo:  &apiv1.MachineGPUInfo{GPUs: []apiv1.MachineGPUInstance{{UUID: "legacy-GPU"}}},
	}

	info, err := collectMachineInfo(context.Background(), func() (*machineinfo.MachineInfo, error) {
		return baseInfo, nil
	}, func(context.Context) (*observation.ObservationBatch, error) {
		return batch, nil
	})
	require.NoError(t, err)
	require.Equal(t, "node-1", info.Hostname)
	require.Equal(t, "CPU", info.CPUInfo.Type)
	require.Equal(t, "580.173.02", info.GPUDriverVersion)
	require.Equal(t, "13.0", info.CUDAVersion)
	require.Equal(t, "NVIDIA-H100", info.GPUInfo.Product)
	require.Equal(t, []apiv1.MachineGPUInstance{{
		UUID: "GPU-test", GPUIndex: "3", ModelName: "NVIDIA-H100",
	}}, info.GPUInfo.GPUs)
}

func TestCollectMachineInfoRejectsUnverifiedEmptyInventory(t *testing.T) {
	timestamp := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	batch := &observation.ObservationBatch{
		CollectionId: "test-cycle",
		Observations: []*observation.Observation{
			observation.NewCollectionErrorObservation(
				observation.SignalGPUInventoryModel,
				sourceDCGM,
				&observation.Entity{Type: "gpu", Id: "dcgm-index-0"},
				timestamppb.New(timestamp),
				nil,
				&observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable},
			),
		},
	}

	info, err := collectMachineInfo(context.Background(), func() (*machineinfo.MachineInfo, error) {
		return &machineinfo.MachineInfo{}, nil
	}, func(context.Context) (*observation.ObservationBatch, error) {
		return batch, nil
	})
	require.Nil(t, info)
	require.ErrorContains(t, err, "shared GPU inventory produced no GPUs")
}

func TestSoftwareVersionsRequireNodeEntity(t *testing.T) {
	timestamp := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	driverVersion, cudaDriverVersion, collectionErrors, projectionErrors := softwareVersionsFromObservations(
		[]*observation.Observation{
			testStringObservation(
				observation.SignalNodeNVIDIADriverVersion,
				sourceDCGM,
				&observation.Entity{Type: "gpu", Id: "GPU-test"},
				timestamp,
				"580.173.02",
			),
		},
	)

	require.Empty(t, driverVersion)
	require.Empty(t, cudaDriverVersion)
	require.Empty(t, collectionErrors)
	require.Len(t, projectionErrors, 1)
	require.ErrorContains(t, projectionErrors[0], "node entity is required")
}
