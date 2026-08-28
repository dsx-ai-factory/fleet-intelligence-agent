// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

package collector

import (
	"context"
	"sync"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/machineinfo"
)

const (
	defaultMachineInfoStaleAfter = 5 * time.Minute
)

type machineInfoProvider interface {
	Get() (*machineinfo.MachineInfo, bool)
	RefreshAsync(parent context.Context)
	WaitForInitialRefresh(ctx context.Context, maxWait time.Duration) bool
}

type cachedMachineInfoProvider struct {
	collect    func(context.Context) (*machineinfo.MachineInfo, error)
	staleAfter time.Duration

	mu                 sync.RWMutex
	cached             *machineinfo.MachineInfo
	lastUpdate         time.Time
	refreshing         bool
	initialWaited      bool
	initialRefreshDone chan struct{}
	initialRefreshOnce sync.Once
}

func newCachedMachineInfoProvider(
	collect func(context.Context) (*machineinfo.MachineInfo, error),
	staleAfter time.Duration,
) machineInfoProvider {
	if staleAfter <= 0 {
		staleAfter = defaultMachineInfoStaleAfter
	}

	return &cachedMachineInfoProvider{
		collect:            collect,
		staleAfter:         staleAfter,
		initialRefreshDone: make(chan struct{}),
	}
}

func (p *cachedMachineInfoProvider) Get() (*machineinfo.MachineInfo, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.cached == nil {
		return nil, false
	}

	return p.cached, true
}

func (p *cachedMachineInfoProvider) RefreshAsync(parent context.Context) {
	if p == nil || p.collect == nil {
		return
	}

	p.mu.Lock()
	if p.refreshing || !p.shouldRefreshLocked() {
		p.mu.Unlock()
		return
	}
	p.refreshing = true
	p.mu.Unlock()

	go func() {
		defer func() {
			p.mu.Lock()
			p.refreshing = false
			p.mu.Unlock()
			p.markInitialRefreshDone()
		}()

		info, err := p.collect(parent)
		if err != nil {
			log.Logger.Warnw("Machine info refresh failed", "error", err)
			return
		}

		p.mu.Lock()
		p.cached = info
		p.lastUpdate = time.Now().UTC()
		p.mu.Unlock()

		log.Logger.Debugw("Refreshed machine info cache")
	}()
}

func (p *cachedMachineInfoProvider) WaitForInitialRefresh(ctx context.Context, maxWait time.Duration) bool {
	if p == nil || maxWait <= 0 {
		return false
	}

	p.mu.Lock()
	if p.initialWaited {
		p.mu.Unlock()
		return false
	}
	p.initialWaited = true
	p.mu.Unlock()

	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	select {
	case <-p.initialRefreshDone:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (p *cachedMachineInfoProvider) shouldRefreshLocked() bool {
	if p.cached == nil {
		return true
	}
	if p.lastUpdate.IsZero() {
		return true
	}
	return time.Since(p.lastUpdate) >= p.staleAfter
}

func (p *cachedMachineInfoProvider) markInitialRefreshDone() {
	p.initialRefreshOnce.Do(func() {
		close(p.initialRefreshDone)
	})
}
