// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"context"
	"errors"
	"fmt"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	pkglog "github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"

	"github.com/dsx-ai-factory/health-validation/collect/observation"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/inventory"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

// collectMachineInfo combines host facts with NVIDIA inventory collected
// through the shared library. The host collector must omit its legacy NVIDIA
// fields so each field is read only once.
func collectMachineInfo(
	ctx context.Context,
	collectHost func() (*machineinfo.MachineInfo, error),
	collectNVIDIA func(context.Context) (*observation.ObservationBatch, error),
) (*machineinfo.MachineInfo, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if collectHost == nil {
		return nil, fmt.Errorf("host inventory collector is required")
	}
	if collectNVIDIA == nil {
		return nil, fmt.Errorf("shared NVIDIA inventory collector is required")
	}

	info, err := collectHost()
	if err != nil {
		return nil, fmt.Errorf("collect host inventory: %w", err)
	}
	if info == nil {
		return nil, fmt.Errorf("host inventory collector returned nil machine info")
	}

	batch, collectionErr := collectNVIDIA(ctx)
	if batch == nil {
		return nil, errors.Join(collectionErr, fmt.Errorf("shared NVIDIA inventory returned no observation batch"))
	}
	gpuInfo, collectionErrors, projectionErrors := GPUInventoryFromObservations(batch.GetObservations())
	logCollectionErrors("GPU inventory", collectionErrors)
	driverVersion, cudaDriverVersion, softwareErrors, softwareProjectionErrors := softwareVersionsFromObservations(batch.GetObservations())
	logCollectionErrors("NVIDIA software inventory", softwareErrors)
	projectionErrors = append(projectionErrors, softwareProjectionErrors...)
	for _, projectionErr := range projectionErrors {
		pkglog.Logger.Errorw("failed to project shared NVIDIA inventory", "error", projectionErr)
	}

	// An empty result is valid on a host without GPUs. If collection failed,
	// keep callers from replacing known inventory with an unverified empty set.
	if len(gpuInfo.GPUs) == 0 && (collectionErr != nil || len(collectionErrors) > 0 || len(projectionErrors) > 0) {
		return nil, errors.Join(
			collectionErr,
			errors.Join(projectionErrors...),
			fmt.Errorf("shared GPU inventory produced no GPUs"),
		)
	}
	if collectionErr != nil {
		pkglog.Logger.Warnw("shared NVIDIA inventory completed with partial sensor errors", "error", collectionErr)
	}

	info.GPUInfo = toMachineGPUInfo(gpuInfo)
	info.GPUDriverVersion = driverVersion
	info.CUDAVersion = cudaDriverVersion
	return info, nil
}

func softwareVersionsFromObservations(observations []*observation.Observation) (string, string, []*observation.Observation, []error) {
	var driverVersion string
	var cudaDriverVersion string
	var collectionErrors []*observation.Observation
	var projectionErrors []error

	for _, current := range observations {
		if current == nil {
			continue
		}
		switch current.GetSignalId() {
		case observation.SignalNodeNVIDIADriverVersion,
			observation.SignalNodeNVIDIACUDADriverVersion:
		default:
			continue
		}
		if current.GetCollectionError() != nil {
			collectionErrors = append(collectionErrors, current)
			continue
		}
		if entity := current.GetEntity(); entity == nil || entity.GetType() != "node" {
			projectionErrors = append(projectionErrors, fmt.Errorf(
				"project software signal %q: node entity is required",
				current.GetSignalId(),
			))
			continue
		}
		value, err := stringValue(current.GetValue())
		if err != nil {
			projectionErrors = append(projectionErrors, fmt.Errorf("project software signal %q: %w", current.GetSignalId(), err))
			continue
		}
		switch current.GetSignalId() {
		case observation.SignalNodeNVIDIADriverVersion:
			if driverVersion == "" {
				driverVersion = value
			}
		case observation.SignalNodeNVIDIACUDADriverVersion:
			if cudaDriverVersion == "" {
				cudaDriverVersion = value
			}
		}
	}

	return driverVersion, cudaDriverVersion, collectionErrors, projectionErrors
}

func toMachineGPUInfo(info inventory.GPUInfo) *apiv1.MachineGPUInfo {
	gpus := make([]apiv1.MachineGPUInstance, 0, len(info.GPUs))
	for _, current := range info.GPUs {
		var cliqueID *uint32
		if current.CliqueID != nil {
			value := *current.CliqueID
			cliqueID = &value
		}
		gpus = append(gpus, apiv1.MachineGPUInstance{
			UUID:         current.UUID,
			GPUIndex:     current.GPUIndex,
			BusID:        current.BusID,
			ModelName:    info.Product,
			ClusterUUID:  current.ClusterUUID,
			CliqueID:     cliqueID,
			SN:           current.SN,
			MinorID:      current.MinorID,
			BoardID:      uint32(current.BoardID),
			VBIOSVersion: current.VBIOSVersion,
			ChassisSN:    current.ChassisSN,
		})
	}
	return &apiv1.MachineGPUInfo{
		Product:      info.Product,
		Manufacturer: info.Manufacturer,
		Architecture: info.Architecture,
		Memory:       info.Memory,
		GPUs:         gpus,
	}
}
