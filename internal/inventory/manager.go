// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package inventory

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

// Manager coordinates periodic inventory collection into a store.
type Manager interface {
	Run(ctx context.Context) error
	CollectOnce(ctx context.Context) (*Snapshot, error)
}

type manager struct {
	mu       sync.RWMutex
	exportMu sync.Mutex
	runMu    sync.Mutex
	source   Source
	sink     Sink
	config   InventoryConfig

	lastSnapshot *Snapshot
}

// NewManager creates an inventory manager.
func NewManager(source Source, sink Sink, cfg InventoryConfig) Manager {
	return &manager{
		source: source,
		sink:   sink,
		config: cfg,
	}
}

func (m *manager) Run(ctx context.Context) error {
	if m.config.StartupJitter > 0 {
		jitter := calculateJitter(m.config.StartupJitter)
		log.Logger.Infow("adding inventory startup jitter", "jitter_duration", jitter)
		if err := sleepWithContext(ctx, jitter); err != nil {
			return err
		}
	}

	_, err := m.runAttempt(ctx)
	if errors.Is(err, ErrNotReady) {
		log.Logger.Infow("initial inventory collection not ready", "error", err)
	} else if err != nil {
		log.Logger.Warnw("initial inventory collection failed", "error", err)
	}
	if m.config.Interval <= 0 {
		return nil
	}
	nextInterval := m.nextInterval(err)
	m.logNextRun(err, nextInterval)

	for {
		if err := sleepWithContext(ctx, nextInterval); err != nil {
			return err
		}
		_, err = m.runAttempt(ctx)
		nextInterval = m.nextInterval(err)
		m.logNextRun(err, nextInterval)
	}
}

func (m *manager) runAttempt(ctx context.Context) (*Snapshot, error) {
	if m.config.Timeout <= 0 {
		return m.CollectOnce(ctx)
	}
	if !m.runMu.TryLock() {
		return nil, fmt.Errorf("previous inventory collection is still running")
	}

	runCtx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	defer cancel()
	done := make(chan struct {
		snap *Snapshot
		err  error
	}, 1)

	go func() {
		defer m.runMu.Unlock()
		snap, err := m.CollectOnce(runCtx)
		done <- struct {
			snap *Snapshot
			err  error
		}{snap: snap, err: err}
	}()

	select {
	case result := <-done:
		return result.snap, result.err
	case <-runCtx.Done():
		return nil, runCtx.Err()
	}
}

func (m *manager) nextInterval(err error) time.Duration {
	if err != nil && m.config.RetryInterval > 0 {
		return m.config.RetryInterval
	}
	return m.config.Interval
}

func (m *manager) logNextRun(err error, nextInterval time.Duration) {
	if err == nil {
		log.Logger.Infow("inventory attempt succeeded", "next_run_in", nextInterval)
		return
	}
	if errors.Is(err, ErrNotReady) {
		log.Logger.Infow("inventory attempt not ready", "next_run_in", nextInterval, "error", err)
		return
	}
	log.Logger.Warnw("inventory attempt failed", "next_run_in", nextInterval, "error", err)
}

func (m *manager) CollectOnce(ctx context.Context) (*Snapshot, error) {
	if m.source == nil {
		return nil, fmt.Errorf("inventory source is required")
	}
	log.Logger.Infow("inventory collect started")
	snap, err := m.source.Collect(ctx)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, fmt.Errorf("inventory source returned nil snapshot")
	}
	hash, err := ComputeHash(snap)
	if err != nil {
		return nil, err
	}
	snap.InventoryHash = hash

	m.mu.Lock()
	cloned := *snap
	m.lastSnapshot = &cloned
	m.mu.Unlock()

	if m.sink != nil {
		m.exportMu.Lock()
		defer m.exportMu.Unlock()

		if err := m.sink.Export(ctx, snap); err != nil {
			if errors.Is(err, ErrNotReady) {
				return snap, err
			}
			return nil, err
		}
	}

	return snap, nil
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func calculateJitter(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}
	maxMs := int64(maxJitter / time.Millisecond)
	if maxMs <= 0 {
		return 0
	}
	randomMs, err := rand.Int(rand.Reader, big.NewInt(maxMs))
	if err != nil {
		log.Logger.Warnw("failed to generate secure inventory jitter, using fallback", "error", err)
		return time.Duration(time.Now().UnixNano()%maxMs) * time.Millisecond
	}
	return time.Duration(randomMs.Int64()) * time.Millisecond
}
