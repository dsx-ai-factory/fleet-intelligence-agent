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
	"strings"
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
	// DCGM errorString messages. The pinned go-dcgm discovery API returns
	// their text without preserving the status code.
	cpuModuleNotLoadedMessage = "This request is serviced by a module of DCGM that is not currently loaded"
	cpuNotSupportedMessage    = "Feature not supported"
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
	// checkMu serializes checks without making reset or Close wait for reads.
	checkMu sync.Mutex
	// setupMu protects watches and result publication, not cached-value reads.
	setupMu sync.Mutex
	// resetGeneration invalidates read batches when reconnect replaces watches.
	resetGeneration uint64
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
		ctx:    cctx,
		cancel: ccancel,

		healthCheckInterval: healthCheckInterval,
		dcgmInstance:        gpudInstance.DCGMInstance,
		dcgmHealthCache:     gpudInstance.DCGMHealthCache,
		fieldWatches:        nvidiadcgm.NewFieldWatches(cpuLevelFields, healthCheckInterval, gpudInstance.DCGMIntervals),
		fieldGroupName:      dcgmGroupNames.CPUFieldGroup,
		cpuGroupName:        dcgmGroupNames.CPUGroup,
	}
	c.readFields = func(group dcgm.Field_Entity_Group, id uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		return nvidiadcgm.ReadEntityValues(c.dcgmInstance, group, id, fields)
	}
	if registrar, ok := c.dcgmInstance.(interface{ RegisterReconnectCallback(func()) }); ok {
		registrar.RegisterReconnectCallback(c.resetAfterReconnect)
	}
	return c, nil
}

// resetAfterReconnect forgets resources from the old session without native
// cleanup. The next Check discovers CPUs and creates watches in the new session.
func (c *component) resetAfterReconnect() {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	c.resetGeneration++
	c.fieldWatches.Reset()
	c.cpuGroupHandle = dcgm.GroupHandle{}
	c.cpuEntities = nil
	c.fieldSetupComplete = false
	c.setupDegradedReason = ""
}

// setupFieldWatchingIfReady discovers CPUs and registers their watches.
// The caller holds setupMu. Failed attempts release partial resources before retry.
// Expected CPU-monitoring unavailability or no CPUs completes setup without
// groups. Unexpected discovery errors produce Degraded and remain retryable.
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
		c.handleCPUDiscoveryError(err)
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

// handleCPUDiscoveryError distinguishes optional CPU-monitoring support from a
// discovery failure. The caller holds setupMu and supplies a non-nil error.
func (c *component) handleCPUDiscoveryError(err error) {
	// GetEntityGroupEntities formats DCGM's error text without preserving its
	// status code. Match only known unavailability messages; other errors retry.
	message := strings.TrimPrefix(err.Error(), "error getting entity count: ")
	switch message {
	case cpuModuleNotLoadedMessage, cpuNotSupportedMessage:
		c.fieldSetupComplete = true
		c.setupDegradedReason = ""
		log.Logger.Infow("CPU monitoring not available", "reason", err.Error())
	default:
		c.setupDegradedReason = fmt.Sprintf("failed to discover DCGM CPU entities: %v", err)
		log.Logger.Warnw("failed to discover DCGM CPU entities", "error", err)
	}
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

// Close cancels collection and releases watches and then the CPU entity group.
// It does not wait for cached reads. Failed cleanup can be retried with another Close.
func (c *component) Close() error {
	log.Logger.Debugw("closing component")

	c.cancel()
	release := nvidiadcgm.AcquireSession(c.dcgmInstance)
	defer release()
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	if c.fieldWatches != nil {
		if err := c.fieldWatches.Close(); err != nil {
			return err
		}
	}

	return c.cleanupCPUGroupLocked()
}

// cpuReadInputs is an immutable snapshot of one setup generation.
type cpuReadInputs struct {
	entities   []uint
	generation uint64
}

// Check prepares a snapshot, performs periodic reads without lifecycle locks,
// and publishes results only for the current generation. Close and reconnect
// never take checkMu.
func (c *component) Check() components.CheckResult {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()
	cr := &checkResult{}
	inputs := c.prepareCheck(cr)
	values := c.readCheck(inputs)
	return c.publishCheck(inputs, values, cr)
}

// prepareCheck owns both lifecycle locks only while setting up and taking a
// snapshot. Every return releases them in this method.
func (c *component) prepareCheck(cr *checkResult) cpuReadInputs {
	release := nvidiadcgm.AcquireSession(c.dcgmInstance)
	defer release()
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	cr.ts = time.Now().UTC()
	inputs := cpuReadInputs{generation: c.resetGeneration}
	if c.ctx.Err() != nil {
		return inputs
	}
	log.Logger.Infow("checking nvidia gpu CPU metrics via DCGM")

	if c.dcgmInstance == nil {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "DCGM instance is nil"
		return inputs
	}
	if !c.dcgmInstance.DCGMExists() {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "DCGM library is not loaded"
		return inputs
	}

	c.setupFieldWatchingIfReady()
	if c.setupDegradedReason != "" {
		cr.health = apiv1.HealthStateTypeDegraded
		cr.reason = c.setupDegradedReason
		return inputs
	}

	// Skip if no CPU entities or field group wasn't created
	if len(c.cpuEntities) == 0 {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "no CPU entities available"
		return inputs
	}
	if c.cpuGroupHandle.GetHandle() == 0 {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "CPU group not created"
		return inputs
	}

	inputs.entities = append([]uint(nil), c.cpuEntities...)
	return inputs
}

// readCheck uses entity IDs and field IDs, not native group handles. The shared
// read layer keeps the library loaded for each call without pinning a session.
func (c *component) readCheck(inputs cpuReadInputs) [][]dcgm.FieldValue_v1 {
	values := make([][]dcgm.FieldValue_v1, len(inputs.entities))
	for i, id := range inputs.entities {
		if c.ctx.Err() != nil {
			break
		}
		fields, err := c.readFields(dcgm.FE_CPU, id, cpuLevelFields)
		if err != nil {
			log.Logger.Warnw("failed to get CPU-level fields", "cpuID", id, "error", err)
			continue
		}
		values[i] = fields
	}
	return values
}

// publishCheck protects the generation check, gauge updates, and assessment
// publication as one operation. Cancellation retains the last full assessment.
func (c *component) publishCheck(inputs cpuReadInputs, values [][]dcgm.FieldValue_v1, cr *checkResult) components.CheckResult {
	release := nvidiadcgm.AcquireSession(c.dcgmInstance)
	defer release()
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	if c.ctx.Err() == nil && inputs.generation == c.resetGeneration && len(inputs.entities) > 0 {
		for i, cpuID := range inputs.entities {
			cpuIDStr := fmt.Sprintf("cpu-%d", cpuID)
			for _, fieldValue := range values[i] {
				if isSentinel := nvidiadcgm.CheckSentinel(fieldValue, "cpuID", cpuID); isSentinel {
					continue
				}

				// Use valid value
				switch fieldValue.FieldID {
				case dcgm.DCGM_FI_DEV_CPU_TEMP_CURRENT:
					metricDCGMFIDevCPUTempCurrent.With(prometheus.Labels{"cpu_id": cpuIDStr}).SetWithTimestamp(fieldValue.Float64(), fieldValue.TS)
				case dcgm.DCGM_FI_DEV_CPU_POWER_UTIL_CURRENT:
					metricDCGMFIDevCPUPowerUtilCurrent.With(prometheus.Labels{"cpu_id": cpuIDStr}).SetWithTimestamp(fieldValue.Float64(), fieldValue.TS)
				case dcgm.DCGM_FI_DEV_CPU_POWER_LIMIT:
					metricDCGMFIDevCPUPowerLimit.With(prometheus.Labels{"cpu_id": cpuIDStr}).SetWithTimestamp(fieldValue.Float64(), fieldValue.TS)
				case dcgm.DCGM_FI_DEV_CPU_TEMP_WARNING:
					metricDCGMFIDevCPUTempWarning.With(prometheus.Labels{"cpu_id": cpuIDStr}).SetWithTimestamp(fieldValue.Float64(), fieldValue.TS)
				case dcgm.DCGM_FI_DEV_CPU_TEMP_CRITICAL:
					metricDCGMFIDevCPUTempCritical.With(prometheus.Labels{"cpu_id": cpuIDStr}).SetWithTimestamp(fieldValue.Float64(), fieldValue.TS)
				}
			}
		}

		// CPU read errors retain the existing informational Healthy policy.
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "CPU metrics collected successfully"
	}

	c.lastMu.Lock()
	defer c.lastMu.Unlock()
	// Close can cancel during gauge publication, so check cancellation again.
	if c.ctx.Err() != nil || inputs.generation != c.resetGeneration {
		if c.lastCheckResult != nil {
			return c.lastCheckResult
		}
		cr.health = apiv1.HealthStateTypeInitializing
		cr.reason = "DCGM session changed before a check completed"
		if c.ctx.Err() != nil {
			cr.reason = "collector stopped before a check completed"
		}
		cr.err = nil
		return cr
	}
	c.lastCheckResult = cr
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
