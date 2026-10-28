// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

var errTransientGroupNotReady = errors.New("dcgm group handle not ready")

// HealthCache manages a shared cache of DCGM health check results.
// It polls DCGM health status in the background and provides cached results
// to multiple consumers without requiring each to poll independently.
type HealthCache struct {
	ctx    context.Context
	cancel context.CancelFunc

	instance Instance // The underlying DCGM instance

	mu         sync.RWMutex
	results    map[dcgm.HealthSystem]SystemHealthResult
	lastUpdate time.Time
	lastError  error

	// Configuration
	pollInterval time.Duration
	started      bool
}

// NewHealthCache creates a new health cache that will poll the given DCGM instance.
func NewHealthCache(ctx context.Context, instance Instance, pollInterval time.Duration) *HealthCache {
	cctx, ccancel := context.WithCancel(ctx)
	return &HealthCache{
		ctx:          cctx,
		cancel:       ccancel,
		instance:     instance,
		results:      make(map[dcgm.HealthSystem]SystemHealthResult),
		pollInterval: pollInterval,
	}
}

// Start begins background polling of DCGM health status.
// This should be called once after all DCGM components have registered their health watches.
func (hc *HealthCache) Start() error {
	hc.mu.Lock()
	if hc.started {
		hc.mu.Unlock()
		return fmt.Errorf("health cache already started")
	}
	hc.started = true
	hc.mu.Unlock()

	// Perform initial health check immediately
	if err := hc.Poll(); err != nil {
		log.Logger.Warnw("initial DCGM health check failed", "error", err)
	}

	// Start background polling
	go hc.pollLoop()

	log.Logger.Infow("DCGM health cache started", "pollInterval", hc.pollInterval)
	return nil
}

// Stop stops the background polling.
func (hc *HealthCache) Stop() {
	hc.cancel()
}

// pollLoop continuously polls DCGM health status at the configured interval.
func (hc *HealthCache) pollLoop() {
	ticker := time.NewTicker(hc.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hc.ctx.Done():
			log.Logger.Debugw("DCGM health cache poll loop stopped")
			return
		case <-ticker.C:
			if err := hc.Poll(); err != nil {
				log.Logger.Warnw("DCGM health check polling failed", "error", err)
			}
		}
	}
}

// Poll performs a single DCGM health check and updates the cache.
// This is useful for both initial polling (called by Start) and
// one-time checks in scan mode where background polling is not needed.
func (hc *HealthCache) Poll() error {
	if hc.instance == nil {
		return fmt.Errorf("DCGM instance is nil")
	}

	if !hc.instance.DCGMExists() {
		// DCGM may become available after startup (e.g., hostengine warmup).
		// Keep polling so the cache can recover automatically.
		log.Logger.Debugw("DCGM not available yet, skipping health check poll")
		return nil
	}

	// Perform health check on the group
	// This performs passive health checks for all registered systems
	healthResp, err := healthCheckDirect(hc.instance)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to perform DCGM health check: %w", err)

		exitForRestartIfRequired("health_cache", err)

		// Check if this is a transient error (benign, don't store)
		if errors.Is(err, errTransientGroupNotReady) || IsTransientError(err) {
			log.Logger.Infow("DCGM transient error, will retry",
				"component", "health_cache",
				"error", err)
			return nil
		}

		// Store all other errors (unhealthy or unknown)
		// Components will use IsUnhealthyAPIError() to determine severity
		hc.mu.Lock()
		hc.lastUpdate = time.Now()
		hc.lastError = wrappedErr
		hc.mu.Unlock()

		return wrappedErr
	}

	// Parse the response and cache per-system results
	newResults := make(map[dcgm.HealthSystem]SystemHealthResult)

	// Initialize all watched systems as PASS with no incidents
	for _, sys := range allHealthSystems {
		newResults[sys] = SystemHealthResult{
			Health:    dcgm.DCGM_HEALTH_RESULT_PASS,
			Incidents: nil,
		}
	}

	// Group incidents by system
	for _, incident := range healthResp.Incidents {
		// Check if this is a system-wide incident (DCGM_HEALTH_WATCH_ALL)
		// These should apply to all watched systems, not just be stored under the ALL key
		if incident.System == dcgm.DCGM_HEALTH_WATCH_ALL {
			// Apply this incident to all watched systems
			for sys := range newResults {
				result := newResults[sys]
				result.Incidents = append(result.Incidents, incident)

				// Update system health to worst severity found
				if incident.Health == dcgm.DCGM_HEALTH_RESULT_FAIL {
					result.Health = dcgm.DCGM_HEALTH_RESULT_FAIL
				} else if incident.Health == dcgm.DCGM_HEALTH_RESULT_WARN && result.Health != dcgm.DCGM_HEALTH_RESULT_FAIL {
					result.Health = dcgm.DCGM_HEALTH_RESULT_WARN
				}

				newResults[sys] = result
			}
		} else {
			// System-specific incident
			result, exists := newResults[incident.System]
			if !exists {
				// Incident for a system we're not watching (e.g., PMU, MCU, SM, DRIVER)
				// Initialize it so we can store the incident
				result = SystemHealthResult{
					Health:    dcgm.DCGM_HEALTH_RESULT_PASS,
					Incidents: nil,
				}
			}
			result.Incidents = append(result.Incidents, incident)

			// Update system health to worst severity found
			// Priority: FAIL > WARN > PASS
			if incident.Health == dcgm.DCGM_HEALTH_RESULT_FAIL {
				result.Health = dcgm.DCGM_HEALTH_RESULT_FAIL
			} else if incident.Health == dcgm.DCGM_HEALTH_RESULT_WARN && result.Health != dcgm.DCGM_HEALTH_RESULT_FAIL {
				result.Health = dcgm.DCGM_HEALTH_RESULT_WARN
			}

			newResults[incident.System] = result
		}
	}

	// Update cache atomically
	hc.mu.Lock()
	hc.results = newResults
	hc.lastUpdate = time.Now()
	hc.lastError = nil
	hc.mu.Unlock()

	log.Logger.Debugw("DCGM health check completed and cached",
		"systems", len(newResults),
		"age", time.Since(hc.lastUpdate))

	return nil
}

// GetResult returns the cached health result for the specified system.
// If no cached data exists, it returns PASS with no incidents.
// Returns a defensive copy of the incidents slice to prevent race conditions.
func (hc *HealthCache) GetResult(system dcgm.HealthSystem) (dcgm.HealthResult, []dcgm.Incident, error) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if hc.lastError != nil {
		return dcgm.DCGM_HEALTH_RESULT_FAIL, nil, hc.lastError
	}

	result, exists := hc.results[system]
	if !exists {
		// System not in cache (wasn't watched or no data yet)
		return dcgm.DCGM_HEALTH_RESULT_PASS, nil, nil
	}

	// Return a defensive copy of the incidents slice to prevent callers
	incidentsCopy := make([]dcgm.Incident, len(result.Incidents))
	copy(incidentsCopy, result.Incidents)

	return result.Health, incidentsCopy, nil
}

// GetLastUpdateTime returns the timestamp of the last successful poll.
func (hc *HealthCache) GetLastUpdateTime() time.Time {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.lastUpdate
}

// healthCheckDirect performs a direct health check on the DCGM instance.
// This is a helper to access the underlying DCGM API without going through
// the instance's own caching layer.
func healthCheckDirect(inst Instance) (*dcgm.HealthResponse, error) {
	release := AcquireSession(inst)
	defer release()
	groupHandle := inst.GetGroupHandle()
	if groupHandle.GetHandle() == 0 {
		return nil, fmt.Errorf("%w: DCGM group handle is not initialized", errTransientGroupNotReady)
	}

	// Call DCGM health check directly on the group
	healthResp, err := dcgm.HealthCheck(groupHandle)
	if err != nil {
		return nil, fmt.Errorf("failed to perform DCGM health check: %w", err)
	}

	return &healthResp, nil
}
