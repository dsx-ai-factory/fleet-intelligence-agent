//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package sharedcollect adapts the shared collection library to Fleet
// Intelligence's existing metric and inventory models.
package sharedcollect

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	shared "github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
)

const defaultCollectionTimeout = 15 * time.Second

// Collector owns the shared sources and sensors used by the prototype. It is
// constructed once so repeated collection cycles reuse NVML initialization
// and the DCGM field watch.
type Collector struct {
	nvml       nvmlsource.Instance
	dcgm       *dcgmsource.Source
	nvmlSensor *nvmlsensor.Sensor
	dcgmSensor *dcgmsensor.Sensor
	timeout    time.Duration
}

// New initializes the source clients and configures DCGM's complete field
// watch before the first collection starts. Native DCGM initialization remains
// lazy inside the shared source.
func New(options Options) (*Collector, error) {
	if options.SourceTimeout <= 0 {
		options.SourceTimeout = defaultCollectionTimeout
	}
	nvmlSource, err := nvmlsource.New()
	if err != nil {
		return nil, fmt.Errorf("initialize shared NVML source: %w", err)
	}

	dcgmSource := dcgmsource.NewSource(dcgmsource.Options{
		Address:         options.DCGMAddress,
		IsSocket:        options.DCGMIsSocket,
		GroupNamePrefix: options.DCGMGroupNamePrefix,
	})
	dcgmSensor, err := dcgmsensor.New(dcgmSource, dcgmSignals()...)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("initialize shared DCGM sensor: %w", err),
			nvmlSource.Shutdown(),
			dcgmSource.Close(),
		)
	}

	return &Collector{
		nvml:       nvmlSource,
		dcgm:       dcgmSource,
		nvmlSensor: nvmlsensor.New(nvmlSource),
		dcgmSensor: dcgmSensor,
		timeout:    options.SourceTimeout,
	}, nil
}

// CollectMetrics reads the DCGM signals that map to FI's existing metric
// contract. Native and per-device failures remain collection-error
// observations.
func (collector *Collector) CollectMetrics(ctx context.Context) (*observation.ObservationBatch, error) {
	currentCycle, batch, err := collector.newCycle(ctx)
	if err != nil {
		return nil, err
	}

	measured, err := collector.dcgmSensor.Measure(ctx, shared.WithCycle(currentCycle))
	if measured != nil {
		batch.Observations = append(batch.Observations, measured.GetObservations()...)
	}
	if err != nil {
		return batch, fmt.Errorf("measure DCGM sensor: %w", err)
	}
	return batch, nil
}

// CollectGPUInventory combines NVML inventory with the DCGM GPU index used by
// FI metric labels. Both sensors run in the same collection cycle.
func (collector *Collector) CollectGPUInventory(ctx context.Context) (*observation.ObservationBatch, error) {
	currentCycle, batch, err := collector.newCycle(ctx)
	if err != nil {
		return nil, err
	}

	var measureErrors []error
	dcgmBatch, err := collector.dcgmSensor.Measure(
		ctx,
		shared.WithCycle(currentCycle),
		shared.WithSignalID(observation.SignalGPUInventoryIndex),
	)
	if dcgmBatch != nil {
		batch.Observations = append(batch.Observations, dcgmBatch.GetObservations()...)
	}
	if err != nil {
		measureErrors = append(measureErrors, fmt.Errorf("measure DCGM inventory: %w", err))
	}

	nvmlBatch, err := collector.nvmlSensor.Measure(
		ctx,
		shared.WithCycle(currentCycle),
		shared.WithSignalID(nvmlInventorySignals()...),
	)
	if nvmlBatch != nil {
		batch.Observations = append(batch.Observations, nvmlBatch.GetObservations()...)
	}
	if err != nil {
		measureErrors = append(measureErrors, fmt.Errorf("measure NVML inventory: %w", err))
	}
	return batch, errors.Join(measureErrors...)
}

func (collector *Collector) newCycle(ctx context.Context) (*shared.Cycle, *observation.ObservationBatch, error) {
	if collector == nil {
		return nil, nil, fmt.Errorf("shared collector is required")
	}
	if ctx == nil {
		return nil, nil, fmt.Errorf("collection context is required")
	}

	currentCycle, err := shared.NewCycleWithTimeout(uuid.NewString(), time.Now().UTC(), collector.timeout)
	if err != nil {
		return nil, nil, fmt.Errorf("create shared collection cycle: %w", err)
	}
	batch := &observation.ObservationBatch{CollectionId: currentCycle.CollectionID()}
	return currentCycle, batch, nil
}

// Close releases both source clients. It attempts both cleanups even when the
// first one reports an error.
func (collector *Collector) Close() error {
	if collector == nil {
		return nil
	}
	return errors.Join(collector.dcgm.Close(), collector.nvml.Shutdown())
}

func dcgmSignals() []string {
	signals := make([]string, 0, len(metricDefinitions)+1)
	signals = append(signals, observation.SignalGPUInventoryIndex)
	for _, definition := range metricDefinitions {
		signals = append(signals, definition.signalID)
	}
	return signals
}
