// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"context"
	"errors"
	"fmt"

	pkglog "github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"

	"github.com/dsx-ai-factory/health-validation/collect/observation"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/inventory"
)

type inventoryAdapter struct {
	legacy  inventory.Source
	collect func(context.Context) (*observation.ObservationBatch, error)
}

// NewInventoryAdapter preserves the existing host inventory source and replaces
// only its GPU section with inventory projected from shared observations.
func NewInventoryAdapter(
	legacy inventory.Source,
	collect func(context.Context) (*observation.ObservationBatch, error),
) inventory.Source {
	return &inventoryAdapter{legacy: legacy, collect: collect}
}

func (adapter *inventoryAdapter) Collect(ctx context.Context) (*inventory.Snapshot, error) {
	if adapter == nil || adapter.legacy == nil {
		return nil, fmt.Errorf("base inventory source is required")
	}
	snapshot, err := adapter.legacy.Collect(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("base inventory source returned nil snapshot")
	}
	if adapter.collect == nil {
		return nil, fmt.Errorf("shared inventory collector is required")
	}

	batch, collectionErr := adapter.collect(ctx)
	if batch == nil {
		return nil, errors.Join(collectionErr, fmt.Errorf("shared GPU inventory returned no observation batch"))
	}
	gpuInfo, collectionErrors, projectionErrors := GPUInventoryFromObservations(batch.GetObservations())
	logCollectionErrors("GPU inventory", collectionErrors)
	for _, projectionErr := range projectionErrors {
		pkglog.Logger.Errorw("failed to project shared GPU inventory", "error", projectionErr)
	}

	// An empty result is valid on a host without GPUs. When collection reported
	// errors, however, do not replace known inventory with an unverified empty
	// set.
	if len(gpuInfo.GPUs) == 0 && (collectionErr != nil || len(collectionErrors) > 0 || len(projectionErrors) > 0) {
		return nil, errors.Join(collectionErr, errors.Join(projectionErrors...), fmt.Errorf("shared GPU inventory produced no GPUs"))
	}
	if collectionErr != nil {
		pkglog.Logger.Warnw("shared GPU inventory completed with partial sensor errors", "error", collectionErr)
	}
	snapshot.Resources.GPUInfo = gpuInfo
	return snapshot, nil
}
