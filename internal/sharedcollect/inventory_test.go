// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestGPUInventoryFromObservationsMergesSourcesByUUID(t *testing.T) {
	timestamp := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	entity := &observation.Entity{Type: "gpu", Id: "GPU-test"}
	failedEntity := &observation.Entity{Type: "gpu", Id: "GPU-failed"}
	observations := []*observation.Observation{
		testIntObservation(observation.SignalGPUInventoryIndex, sourceNVML, entity, timestamp, 7),
		testIntObservation(observation.SignalGPUInventoryIndex, sourceDCGM, entity, timestamp, 3),
		testStringObservation(observation.SignalGPUInventoryModel, sourceNVML, entity, timestamp, "NVIDIA H100 80GB HBM3"),
		testStringObservation(observation.SignalGPUInventoryManufacturer, sourceNVML, entity, timestamp, "NVIDIA"),
		testStringObservation(observation.SignalGPUInventoryArchitecture, sourceNVML, entity, timestamp, "hopper"),
		testStringObservation(observation.SignalGPUInventoryPCIBusID, sourceNVML, entity, timestamp, "00000000:53:00.0"),
		testStringObservation(observation.SignalGPUInventorySerialNumber, sourceNVML, entity, timestamp, "serial"),
		testIntObservation(observation.SignalGPUInventoryMinorNumber, sourceNVML, entity, timestamp, 0),
		testIntObservation(observation.SignalGPUInventoryBoardID, sourceNVML, entity, timestamp, 21248),
		testStringObservation(observation.SignalGPUInventoryVBIOSVersion, sourceNVML, entity, timestamp, "96.00.D0.00.02"),
		testStringObservation(observation.SignalGPUInventoryChassisSerialNumber, sourceNVML, entity, timestamp, "chassis"),
		testStringObservation(observation.SignalGPUFabricClusterUUID, sourceNVML, entity, timestamp, "cluster"),
		testIntObservation(observation.SignalGPUFabricCliqueID, sourceNVML, entity, timestamp, 4),
		testIntObservation(observation.SignalFramebufferTotal, sourceDCGM, entity, timestamp, 100),
		testIntObservation(observation.SignalFramebufferTotal, sourceNVML, entity, timestamp, 80),
		observation.NewCollectionErrorObservation(
			observation.SignalGPUInventorySerialNumber,
			sourceNVML,
			failedEntity,
			timestamppb.New(timestamp),
			nil,
			&observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable, Detail: "GPU is lost"},
		),
	}

	result, collectionErrors, projectionErrors := GPUInventoryFromObservations(observations)
	require.Empty(t, projectionErrors)
	require.Len(t, collectionErrors, 1)
	require.Equal(t, "NVIDIA-H100-80GB-HBM3", result.Product)
	require.Equal(t, "NVIDIA", result.Manufacturer)
	require.Equal(t, "hopper", result.Architecture)
	require.Equal(t, "83886080", result.Memory)
	require.Len(t, result.GPUs, 1)

	gpu := result.GPUs[0]
	require.Equal(t, "GPU-test", gpu.UUID)
	require.Equal(t, "3", gpu.GPUIndex)
	require.Equal(t, "00000000:53:00.0", gpu.BusID)
	require.Equal(t, "serial", gpu.SN)
	require.Equal(t, "0", gpu.MinorID)
	require.Equal(t, 21248, gpu.BoardID)
	require.Equal(t, "96.00.D0.00.02", gpu.VBIOSVersion)
	require.Equal(t, "chassis", gpu.ChassisSN)
	require.Equal(t, "cluster", gpu.ClusterUUID)
	require.NotNil(t, gpu.CliqueID)
	require.Equal(t, uint32(4), *gpu.CliqueID)
}

func TestGPUInventoryFromObservationsReportsMalformedValues(t *testing.T) {
	timestamp := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	observations := []*observation.Observation{
		{
			SignalId:   observation.SignalGPUInventoryModel,
			Source:     sourceNVML,
			ObservedAt: timestamppb.New(timestamp),
			Outcome: &observation.Observation_Value{Value: &observation.Value{
				Kind: &observation.Value_StringValue{StringValue: "H100"},
			}},
		},
		{
			SignalId:   observation.SignalGPUInventoryModel,
			Source:     sourceNVML,
			Entity:     &observation.Entity{Type: "gpu", Id: "GPU-test"},
			ObservedAt: timestamppb.New(timestamp),
		},
	}

	result, collectionErrors, projectionErrors := GPUInventoryFromObservations(observations)
	require.Empty(t, result.GPUs)
	require.Empty(t, collectionErrors)
	require.Len(t, projectionErrors, 2)
	require.ErrorContains(t, projectionErrors[0], "GPU entity is required")
	require.ErrorContains(t, projectionErrors[1], "value is required")
}

func TestNVMLInventorySignalsCoverFIInventory(t *testing.T) {
	require.ElementsMatch(t, []string{
		observation.SignalGPUInventoryIndex,
		observation.SignalGPUInventoryModel,
		observation.SignalGPUInventoryManufacturer,
		observation.SignalGPUInventoryArchitecture,
		observation.SignalGPUInventoryPCIBusID,
		observation.SignalGPUInventorySerialNumber,
		observation.SignalGPUInventoryMinorNumber,
		observation.SignalGPUInventoryBoardID,
		observation.SignalGPUInventoryVBIOSVersion,
		observation.SignalGPUInventoryChassisSerialNumber,
		observation.SignalFramebufferTotal,
		observation.SignalGPUFabricClusterUUID,
		observation.SignalGPUFabricCliqueID,
	}, nvmlInventorySignals())
}
