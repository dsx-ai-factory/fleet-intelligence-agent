//go:build !linux || !cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"context"
	"fmt"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// Collector is unavailable when the native Linux sources cannot be linked.
type Collector struct{}

// New reports that hardware-backed collection requires Linux with cgo.
func New(Options) (*Collector, error) {
	return nil, fmt.Errorf("shared hardware collection requires Linux with cgo")
}

// CollectMetrics reports that hardware-backed collection requires Linux with cgo.
func (*Collector) CollectMetrics(context.Context) (*observation.ObservationBatch, error) {
	return nil, fmt.Errorf("shared hardware collection requires Linux with cgo")
}

// CollectGPUInventory reports that hardware-backed collection requires Linux with cgo.
func (*Collector) CollectGPUInventory(context.Context) (*observation.ObservationBatch, error) {
	return nil, fmt.Errorf("shared hardware collection requires Linux with cgo")
}

// Close has no native resources to release on unsupported platforms.
func (*Collector) Close() error { return nil }
