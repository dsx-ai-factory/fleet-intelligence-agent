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

// Package cpu tracks NVIDIA GPU CPU metrics via DCGM.
package cpu

import (
	"context"
	"fmt"
	"sync"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

const Name = "accelerator-nvidia-dcgm-cpu"

const (
	defaultHealthCheckInterval = time.Minute
)

var _ components.Component = &component{}

type component struct {
	ctx    context.Context
	cancel context.CancelFunc

	healthCheckInterval time.Duration
	// readFields reads DCGM's cached values. Tests replace it to exercise read
	// failures and shutdown without GPU hardware.
	readFields func(dcgm.Field_Entity_Group, uint, []dcgm.Short) ([]dcgm.FieldValue_v1, error)

	dcgmInstance    nvidiadcgm.Instance
	dcgmHealthCache *nvidiadcgm.HealthCache

	cpuGroupHandle dcgm.GroupHandle // Dedicated group for CPUs
	// fieldWatches owns native sampling groups for the selected CPU fields.
	fieldWatches *nvidiadcgm.FieldWatches
	// setupMu serializes checks, setup, and cleanup. Close waits for an active
	// check before it releases the field and CPU entity groups.
	setupMu sync.Mutex
	// fieldSetupComplete prevents repeated setup after success or empty discovery.
	// Errors leave setup incomplete so a later Check can retry.
	fieldSetupComplete bool
	// Group names are retained because setup runs after construction.
	fieldGroupName string
	cpuGroupName   string

	// cpuEntities contains the entities committed by successful setup.
	cpuEntities []uint

	// setupDegradedReason records group or watch setup errors until setup succeeds.
	setupDegradedReason string

	lastMu          sync.RWMutex
	lastCheckResult *checkResult
}

// New configures CPU collection without allocating native DCGM resources.
// Check discovers CPU entities and sets up their sampling watches when DCGM is ready.
func New(gpudInstance *components.GPUdInstance) (components.Component, error) {
	cctx, ccancel := context.WithCancel(gpudInstance.RootCtx)
	dcgmGroupNames := gpudInstance.DCGMGroupNames.WithDefaults()

	healthCheckInterval := defaultHealthCheckInterval
	if gpudInstance.HealthCheckInterval > 0 {
		healthCheckInterval = gpudInstance.HealthCheckInterval
	}

	c := &component{
		ctx:        cctx,
		readFields: dcgm.EntityGetLatestValues,
		cancel:     ccancel,

		healthCheckInterval: healthCheckInterval,
		dcgmInstance:        gpudInstance.DCGMInstance,
		dcgmHealthCache:     gpudInstance.DCGMHealthCache,
		fieldWatches:        nvidiadcgm.NewFieldWatches(cpuLevelFields, healthCheckInterval, gpudInstance.DCGMIntervals),
		fieldGroupName:      dcgmGroupNames.CPUFieldGroup,
		cpuGroupName:        dcgmGroupNames.CPUGroup,
	}
	return c, nil
}

// setupFieldWatchingIfReady discovers CPUs and registers their watches.
// The caller holds setupMu. Failed attempts release partial resources before retry.
// Discovery errors leave setup incomplete and produce a Degraded assessment.
// Successful discovery with no CPUs completes setup without allocating groups.
func (c *component) setupFieldWatchingIfReady() {
	if c.ctx.Err() != nil || c.fieldSetupComplete || c.dcgmInstance == nil || !c.dcgmInstance.DCGMExists() {
		return
	}
	if err := c.fieldWatches.Close(); err != nil {
		c.setupDegradedReason = fmt.Sprintf("failed to clean up DCGM CPU field watching: %v", err)
		return
	}
	// A failed destroy retains its handle. Retry cleanup before allocating a group.
	if err := c.cleanupCPUGroupLocked(); err != nil {
		c.setupDegradedReason = fmt.Sprintf("failed to clean up DCGM CPU group: %v", err)
		return
	}

	cpuEntities, err := dcgm.GetEntityGroupEntities(dcgm.FE_CPU)
	if err != nil {
		// Leave setup incomplete so a later check retries discovery.
		c.setupDegradedReason = fmt.Sprintf("failed to discover DCGM CPU entities: %v", err)
		log.Logger.Warnw("failed to discover DCGM CPU entities", "error", err)
		return
	}
	if len(cpuEntities) == 0 {
		c.fieldSetupComplete = true
		c.setupDegradedReason = ""
		log.Logger.Infow("no CPU entities found, skipping CPU field setup")
		return
	}
	if c.ctx.Err() != nil {
		return
	}

	cpuGroupHandle, err := dcgm.CreateGroup(c.cpuGroupName)
	if err != nil {
		c.setupDegradedReason = fmt.Sprintf("failed to create DCGM CPU group: %v", err)
		return
	}
	c.cpuGroupHandle = cpuGroupHandle
	// On incomplete setup, release field watches and groups before the CPU
	// entity group. If field cleanup fails, retain the CPU group for a retry.
	defer func() {
		if !c.fieldSetupComplete {
			if err := c.fieldWatches.Close(); err != nil {
				return
			}
			_ = c.cleanupCPUGroupLocked()
		}
	}()
	for _, cpuID := range cpuEntities {
		if c.ctx.Err() != nil {
			return
		}
		if err := dcgm.AddEntityToGroup(cpuGroupHandle, dcgm.FE_CPU, cpuID); err != nil {
			c.setupDegradedReason = fmt.Sprintf("failed to add CPU to DCGM group: %v", err)
			return
		}
	}
	if c.ctx.Err() != nil {
		return
	}
	// Start sampling the CPU fields. On failure, the deferred cleanup above
	// releases partial resources.
	if err := c.fieldWatches.Setup(c.fieldGroupName, cpuGroupHandle); err != nil {
		c.setupDegradedReason = fmt.Sprintf("failed to set up DCGM CPU field watching: %v", err)
		return
	}
	c.cpuEntities = cpuEntities
	c.fieldSetupComplete = true
	c.setupDegradedReason = ""
}

// cleanupCPUGroupLocked retains the handle on failure so cleanup can be retried.
// The caller holds setupMu and must release field watches first.
func (c *component) cleanupCPUGroupLocked() error {
	if c.cpuGroupHandle.GetHandle() == 0 {
		return nil
	}
	if err := dcgm.DestroyGroup(c.cpuGroupHandle); err != nil {
		log.Logger.Warnw("failed to destroy DCGM CPU group", "error", err)
		return err
	}
	c.cpuGroupHandle = dcgm.GroupHandle{}
	return nil
}

func (c *component) Name() string { return Name }

func (c *component) Tags() []string {
	return []string{
		"accelerator",
		"gpu",
		"nvidia",
		"dcgm",
		Name,
	}
}

func (c *component) IsSupported() bool {
	if c.dcgmInstance == nil {
		return false
	}
	return c.dcgmInstance.DCGMExists()
}

// Start runs Check immediately, then at healthCheckInterval until cancellation.
// Native watches control sampling independently of these cached reads.
func (c *component) Start() error {
	go func() {
		ticker := time.NewTicker(c.healthCheckInterval)
		defer ticker.Stop()

		for {
			_ = c.Check()

			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (c *component) LastHealthStates() apiv1.HealthStates {
	c.lastMu.RLock()
	defer c.lastMu.RUnlock()
	if c.lastCheckResult == nil {
		return apiv1.HealthStates{{
			Time:      metav1.NewTime(time.Now().UTC()),
			Component: Name,
			Name:      Name,
			Health:    apiv1.HealthStateTypeHealthy,
			Reason:    "no data yet",
		}}
	}
	return c.lastCheckResult.HealthStates()
}

func (c *component) Events(ctx context.Context, since time.Time) (apiv1.Events, error) {
	return nil, nil
}

// Close cancels collection and waits for an active Check before releasing watches
// and then the CPU entity group. Cleanup failures can be retried with another Close.
func (c *component) Close() error {
	log.Logger.Debugw("closing component")

	c.cancel()
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	if c.fieldWatches != nil {
		if err := c.fieldWatches.Close(); err != nil {
			return err
		}
	}

	return c.cleanupCPUGroupLocked()
}

// Check retries incomplete setup, reads all selected CPU fields from DCGM's cache,
// and updates metrics and health. It holds setupMu through setup and native reads.
// Cancellation preserves the last completed assessment. If none exists, the
// returned result is Initializing and is not stored as a completed assessment.
func (c *component) Check() (result components.CheckResult) {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()

	cr := &checkResult{ts: time.Now().UTC()}
	defer func() {
		c.lastMu.Lock()
		defer c.lastMu.Unlock()
		// Cancellation is not a completed health assessment. Keep the prior
		// result, including its timestamp, reason, and error.
		if c.ctx.Err() != nil {
			if c.lastCheckResult != nil {
				result = c.lastCheckResult
			} else {
				cr.health = apiv1.HealthStateTypeInitializing
				cr.reason = "collector stopped before a check completed"
				cr.err = nil
				result = cr
			}
			return
		}
		c.lastCheckResult = cr
	}()
	if c.ctx.Err() != nil {
		return cr
	}
	log.Logger.Infow("checking nvidia gpu CPU metrics via DCGM")

	if c.dcgmInstance == nil {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "DCGM instance is nil"
		return cr
	}
	if !c.dcgmInstance.DCGMExists() {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "DCGM library is not loaded"
		return cr
	}

	c.setupFieldWatchingIfReady()
	if c.setupDegradedReason != "" {
		cr.health = apiv1.HealthStateTypeDegraded
		cr.reason = c.setupDegradedReason
		return cr
	}

	// Skip if no CPU entities or field group wasn't created
	if len(c.cpuEntities) == 0 {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "no CPU entities available"
		return cr
	}
	if c.cpuGroupHandle.GetHandle() == 0 {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "CPU group not created"
		return cr
	}

	// Read all CPU fields each check. Slower native watches can return the same
	// cached sample on consecutive checks without sampling the CPU again.
	for _, cpuID := range c.cpuEntities {
		if c.ctx.Err() != nil {
			break
		}
		fieldValues, err := c.readFields(dcgm.FE_CPU, cpuID, cpuLevelFields)
		if err != nil {
			log.Logger.Warnw("failed to get CPU-level fields", "cpuID", cpuID, "error", err)
			continue
		}

		cpuIDStr := fmt.Sprintf("cpu-%d", cpuID)

		for _, fieldValue := range fieldValues {
			if isSentinel := nvidiadcgm.CheckSentinel(fieldValue, "cpuID", cpuID); isSentinel {
				continue
			}

			// Use valid value
			switch fieldValue.FieldID {
			case dcgm.DCGM_FI_DEV_CPU_TEMP_CURRENT:
				metricDCGMFIDevCPUTempCurrent.With(prometheus.Labels{"cpu_id": cpuIDStr}).Set(fieldValue.Float64())
			case dcgm.DCGM_FI_DEV_CPU_POWER_UTIL_CURRENT:
				metricDCGMFIDevCPUPowerUtilCurrent.With(prometheus.Labels{"cpu_id": cpuIDStr}).Set(fieldValue.Float64())
			case dcgm.DCGM_FI_DEV_CPU_POWER_LIMIT:
				metricDCGMFIDevCPUPowerLimit.With(prometheus.Labels{"cpu_id": cpuIDStr}).Set(fieldValue.Float64())
			case dcgm.DCGM_FI_DEV_CPU_TEMP_WARNING:
				metricDCGMFIDevCPUTempWarning.With(prometheus.Labels{"cpu_id": cpuIDStr}).Set(fieldValue.Float64())
			case dcgm.DCGM_FI_DEV_CPU_TEMP_CRITICAL:
				metricDCGMFIDevCPUTempCritical.With(prometheus.Labels{"cpu_id": cpuIDStr}).Set(fieldValue.Float64())
			}
		}
	}

	// CPU metrics are informational only - always return healthy
	cr.health = apiv1.HealthStateTypeHealthy
	cr.reason = "CPU metrics collected successfully"

	return cr
}

var _ components.CheckResult = &checkResult{}

type checkResult struct {
	ts     time.Time
	err    error
	health apiv1.HealthStateType
	reason string
}

func (cr *checkResult) ComponentName() string {
	return Name
}

func (cr *checkResult) String() string {
	if cr == nil {
		return ""
	}
	return ""
}

func (cr *checkResult) Summary() string {
	if cr == nil {
		return ""
	}
	return cr.reason
}

func (cr *checkResult) HealthStateType() apiv1.HealthStateType {
	if cr == nil {
		return ""
	}
	return cr.health
}

func (cr *checkResult) getError() string {
	if cr == nil || cr.err == nil {
		return ""
	}
	return cr.err.Error()
}

func (cr *checkResult) HealthStates() apiv1.HealthStates {
	if cr == nil {
		return apiv1.HealthStates{
			{
				Time:      metav1.NewTime(time.Now().UTC()),
				Component: Name,
				Name:      Name,
				Health:    apiv1.HealthStateTypeHealthy,
				Reason:    "no data yet",
			},
		}
	}

	state := apiv1.HealthState{
		Time:      metav1.NewTime(cr.ts),
		Component: Name,
		Name:      Name,
		Reason:    cr.reason,
		Error:     cr.getError(),
		Health:    cr.health,
	}

	return apiv1.HealthStates{state}
}
