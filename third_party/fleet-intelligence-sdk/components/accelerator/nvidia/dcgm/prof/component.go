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

// Package prof tracks NVIDIA GPU profiling metrics via DCGM.
package prof

import (
	"context"
	"fmt"
	"os"
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

const Name = "accelerator-nvidia-dcgm-prof"

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

	// setupMu serializes checks, setup, and cleanup. DCGM handles cannot
	// change while a read is in flight.
	setupMu            sync.Mutex
	fieldSetupComplete bool
	fieldGroupName     string

	// Track fields we're actually watching (post-validation)
	watchedFields []dcgm.Short

	// fieldWatches owns native sampling groups for hardware-validated fields.
	fieldWatches *nvidiadcgm.FieldWatches
	// watchIntervals are retained because field selection happens during deferred
	// setup; configuration alone never selects a field.
	watchIntervals nvidiadcgm.CollectionIntervals

	// setupDegradedReason is non-empty when field group creation or watching setup
	// failed. Check() returns Degraded with this reason rather than querying fields
	// that were never successfully registered.
	setupDegradedReason string

	lastMu          sync.RWMutex
	lastCheckResult *checkResult
}

// New configures profiling collection without allocating native DCGM resources.
// Check validates hardware support and sets up sampling watches when DCGM is ready.
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
		fieldGroupName:      dcgmGroupNames.ProfilingFieldGroup,
		watchIntervals:      gpudInstance.DCGMIntervals,
	}

	return c, nil
}

// setupFieldWatchingIfReady validates and registers fields. Caller holds setupMu.
// Native setup failures remain retryable. An empty device inventory or no
// supported fields completes setup without watches. Failed cleanup must finish
// before a retry can replace the watch owner and its retained native handles.
func (c *component) setupFieldWatchingIfReady() {
	if c.ctx.Err() != nil || c.fieldSetupComplete || c.dcgmInstance == nil || !c.dcgmInstance.DCGMExists() {
		return
	}

	if c.fieldWatches != nil {
		if err := c.fieldWatches.Close(); err != nil {
			c.setupDegradedReason = fmt.Sprintf("failed to clean up DCGM profiling field watching: %v", err)
			return
		}
	}
	devices := c.dcgmInstance.GetDevices()
	if len(devices) == 0 {
		// Device membership is fixed when the connected DCGM session is built.
		// An empty inventory is therefore a terminal setup outcome for this
		// session, rather than something repeated checks can discover later.
		c.fieldSetupComplete = true
		c.setupDegradedReason = ""
		log.Logger.Warnw("no GPU devices found, skipping profiling field setup")
		return
	}

	// Use the first device for hardware validation. If field support can differ
	// between GPUs, profiling will need per-device validation.
	deviceID := devices[0].ID
	validFields := newFieldValidator(deviceID).validateFields(profFields)
	if len(validFields) == 0 {
		// No supported fields is a terminal outcome under the existing profiling
		// policy. Avoid repeating the same validation on every health check.
		c.fieldSetupComplete = true
		c.setupDegradedReason = ""
		log.Logger.Warnw("no valid profiling fields after hardware validation",
			"requestedFields", len(profFields),
			"deviceID", deviceID,
			"suggestion", "Check if GPU supports DCP metrics (datacenter GPUs only)")
		return
	}

	log.Logger.Infow("profiling field validation complete",
		"requestedFields", len(profFields),
		"validFields", len(validFields),
		"skippedFields", len(profFields)-len(validFields),
		"deviceID", deviceID)

	// Each retry uses the current hardware-validated fields after cleanup.
	c.fieldWatches = nvidiadcgm.NewFieldWatches(validFields, c.healthCheckInterval, c.watchIntervals)
	if err := c.fieldWatches.Setup(c.fieldGroupName, dcgm.GroupAllGPUs()); err != nil {
		c.setupDegradedReason = fmt.Sprintf("failed to set up DCGM profiling field watching: %v", err)
		return
	}
	c.fieldSetupComplete = true
	c.setupDegradedReason = ""
	c.watchedFields = validFields
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

// cleanup waits for an active Check before releasing profiling watches.
func (c *component) cleanup() error {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	return c.cleanupFieldGroupLocked()
}

// cleanupFieldGroupLocked releases sampling groups and retains failed handles
// for another cleanup attempt. The caller must hold setupMu.
func (c *component) cleanupFieldGroupLocked() error {
	if c.fieldWatches != nil {
		return c.fieldWatches.Close()
	}
	return nil
}

// Close cancels collection and releases watches after any active Check completes.
// Cleanup failures can be retried by calling Close again.
func (c *component) Close() error {
	log.Logger.Debugw("closing profiling component")
	c.cancel()
	return c.cleanup()
}

// Check retries incomplete setup, reads all validated fields from DCGM's cache,
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
	log.Logger.Infow("checking profiling metrics via DCGM")

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

	// New() may have received a reconnecting instance that was still backed by
	// its no-op session. Keep setup here so the first check after DCGM becomes
	// available creates the profiling field group and watch.
	c.setupFieldWatchingIfReady()
	setupDegradedReason := c.setupDegradedReason
	watchedFields := c.watchedFields

	if setupDegradedReason != "" {
		cr.health = apiv1.HealthStateTypeDegraded
		cr.reason = setupDegradedReason
		return cr
	}
	if len(watchedFields) == 0 {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "no profiling fields to monitor (hardware doesn't support)"
		return cr
	}

	devices := c.dcgmInstance.GetDevices()
	var readErr error
	readComplete := len(devices) > 0
	for _, device := range devices {
		if c.ctx.Err() != nil {
			break
		}
		// Read all validated fields each check. Slower native watches can return
		// the same cached sample without sampling the GPU again.
		vals, err := c.readFields(
			dcgm.FE_GPU,   // Entity type: GPU
			device.ID,     // Entity ID
			watchedFields, // Only query hardware-validated fields
		)

		if err != nil {
			// Check for fatal errors that require restart
			if nvidiadcgm.IsRestartRequired(err) {
				log.Logger.Errorw("DCGM fatal error, exiting for restart",
					"component", "prof",
					"deviceID", device.ID,
					"error", err,
					"action", "systemd/k8s will restart agent and recreate DCGM resources")
				os.Exit(1)
			}

			// Check if this is a transient error (benign, skip)
			if nvidiadcgm.IsTransientError(err) {
				readComplete = false
				log.Logger.Infow("DCGM transient error, will retry",
					"component", "prof",
					"deviceID", device.ID,
					"error", err)
				continue
			}

			readErr = err
			break
		}

		// Process each field value with type-aware sentinel checking
		for _, val := range vals {
			if isSentinel := nvidiadcgm.CheckSentinel(val,
				"deviceID", device.ID,
				"uuid", device.UUID,
			); isSentinel {
				continue
			}

			// Value is valid - export metric based on field type
			switch val.FieldID {
			case dcgm.DCGM_FI_PROF_GR_ENGINE_ACTIVE:
				metricDCGMFIProfGrEngineActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_SM_ACTIVE:
				metricDCGMFIProfSmActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_SM_OCCUPANCY:
				metricDCGMFIProfSmOccupancy.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_TENSOR_ACTIVE:
				metricDCGMFIProfPipeTensorActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_TENSOR_IMMA_ACTIVE:
				metricDCGMFIProfPipeTensorImmaActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_TENSOR_HMMA_ACTIVE:
				metricDCGMFIProfPipeTensorHmmaActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_TENSOR_DFMA_ACTIVE:
				metricDCGMFIProfPipeTensorDfmaActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_INT_ACTIVE:
				metricDCGMFIProfPipeIntActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_DRAM_ACTIVE:
				metricDCGMFIProfDramActive.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_FP64_ACTIVE:
				metricDCGMFIProfPipeFp64Active.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_FP32_ACTIVE:
				metricDCGMFIProfPipeFp32Active.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PIPE_FP16_ACTIVE:
				metricDCGMFIProfPipeFp16Active.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(val.Float64())

			case dcgm.DCGM_FI_PROF_PCIE_TX_BYTES:
				metricDCGMFIProfPcieTxBytes.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(float64(val.Int64()))

			case dcgm.DCGM_FI_PROF_PCIE_RX_BYTES:
				metricDCGMFIProfPcieRxBytes.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(float64(val.Int64()))

			case dcgm.DCGM_FI_PROF_NVLINK_TX_BYTES:
				metricDCGMFIProfNvlinkTxBytes.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(float64(val.Int64()))

			case dcgm.DCGM_FI_PROF_NVLINK_RX_BYTES:
				metricDCGMFIProfNvlinkRxBytes.With(prometheus.Labels{
					"uuid": device.UUID,
					"gpu":  fmt.Sprintf("%d", device.ID),
				}).Set(float64(val.Int64()))
			}
		}
	}

	if c.ctx.Err() != nil {
		return cr
	}
	if readErr == nil && !readComplete {
		// A transient or empty batch cannot verify recovery. Reuse the last
		// completed result until a complete cached read succeeds.
		c.lastMu.RLock()
		previous := c.lastCheckResult
		c.lastMu.RUnlock()
		if previous != nil {
			cr = previous
			return cr
		}
	}
	cr.err = readErr
	if cr.err != nil {
		if nvidiadcgm.IsUnhealthyAPIError(cr.err) {
			cr.health = apiv1.HealthStateTypeUnhealthy
			cr.reason = nvidiadcgm.AppendDCGMErrorType("failed to get DCGM profiling fields", cr.err)
		} else {
			cr.health = apiv1.HealthStateTypeDegraded
			cr.reason = fmt.Sprintf("failed to get DCGM profiling fields: %v", cr.err)
		}
		return cr
	}

	// The cached read completed without a non-transient API error.
	cr.health = apiv1.HealthStateTypeHealthy
	cr.reason = "profiling metrics collected successfully"

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
