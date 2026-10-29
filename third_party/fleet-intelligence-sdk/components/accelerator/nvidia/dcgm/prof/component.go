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

	// checkMu serializes checks without making reset or Close wait for reads.
	checkMu sync.Mutex
	// setupMu protects watches and result publication, not cached-value reads.
	setupMu sync.Mutex
	// resetGeneration invalidates read batches when reconnect replaces watches.
	resetGeneration    uint64
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
		ctx:    cctx,
		cancel: ccancel,

		healthCheckInterval: healthCheckInterval,
		dcgmInstance:        gpudInstance.DCGMInstance,
		dcgmHealthCache:     gpudInstance.DCGMHealthCache,
		fieldGroupName:      dcgmGroupNames.ProfilingFieldGroup,
		watchIntervals:      gpudInstance.DCGMIntervals,
	}
	c.readFields = func(group dcgm.Field_Entity_Group, id uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		return nvidiadcgm.ReadEntityValues(c.dcgmInstance, group, id, fields)
	}
	if registrar, ok := c.dcgmInstance.(interface{ RegisterReconnectCallback(func()) }); ok {
		registrar.RegisterReconnectCallback(c.resetAfterReconnect)
	}

	return c, nil
}

// resetAfterReconnect forgets watches from the old session without native cleanup.
// The next Check validates fields and creates watches in the new session.
func (c *component) resetAfterReconnect() {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	c.resetGeneration++
	if c.fieldWatches != nil {
		c.fieldWatches.Reset()
	}
	c.fieldSetupComplete = false
	c.watchedFields = nil
	c.setupDegradedReason = ""
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

// cleanup releases profiling watches without waiting for cached-value reads.
func (c *component) cleanup() error {
	release := nvidiadcgm.AcquireSession(c.dcgmInstance)
	defer release()
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

// Close cancels collection and releases watches without waiting for cached reads.
// Cleanup failures can be retried by calling Close again.
func (c *component) Close() error {
	log.Logger.Debugw("closing profiling component")
	c.cancel()
	return c.cleanup()
}

// profilingReadInputs owns the selected fields and device inventory for one
// setup generation. These inputs remain valid after reconnect resets the owner.
type profilingReadInputs struct {
	fields     []dcgm.Short
	devices    []nvidiadcgm.DeviceInfo
	generation uint64
}

// Check separates setup, cached reads, and publication. Lifecycle locks belong
// to the phase methods and are never held during periodic cached-value reads.
func (c *component) Check() components.CheckResult {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()
	cr := &checkResult{}
	inputs := c.prepareCheck(cr)
	values, err := c.readCheck(inputs)
	return c.publishCheck(inputs, values, err, cr)
}

// prepareCheck releases both lifecycle locks before returning immutable read
// inputs. Setup failures and terminal empty selections retain their health policy.
func (c *component) prepareCheck(cr *checkResult) profilingReadInputs {
	release := nvidiadcgm.AcquireSession(c.dcgmInstance)
	defer release()
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	cr.ts = time.Now().UTC()
	inputs := profilingReadInputs{generation: c.resetGeneration}
	if c.ctx.Err() != nil {
		return inputs
	}
	log.Logger.Infow("checking profiling metrics via DCGM")

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

	// New() may have received a reconnecting instance that was still backed by
	// its no-op session. Keep setup here so the first check after DCGM becomes
	// available creates the profiling field group and watch.
	c.setupFieldWatchingIfReady()
	setupDegradedReason := c.setupDegradedReason
	watchedFields := append([]dcgm.Short(nil), c.watchedFields...)

	if setupDegradedReason != "" {
		cr.health = apiv1.HealthStateTypeDegraded
		cr.reason = setupDegradedReason
		return inputs
	}
	if len(watchedFields) == 0 {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "no profiling fields to monitor (hardware doesn't support)"
		return inputs
	}

	inputs.fields = watchedFields
	inputs.devices = c.dcgmInstance.GetDevices()
	return inputs
}

// readCheck holds no lifecycle locks. Active-check fatal errors retain their
// restart policy, while canceled checks discard late teardown errors.
func (c *component) readCheck(inputs profilingReadInputs) ([][]dcgm.FieldValue_v1, error) {
	values := make([][]dcgm.FieldValue_v1, len(inputs.devices))
	var readErr error
	for i, device := range inputs.devices {
		if c.ctx.Err() != nil {
			break
		}
		// Read all validated fields each check. Slower native watches can return
		// the same cached sample without sampling the GPU again.
		vals, err := c.readFields(
			dcgm.FE_GPU,   // Entity type: GPU
			device.ID,     // Entity ID
			inputs.fields, // Only query hardware-validated fields
		)

		// Close may unwatch fields while the cached read is still running. Ignore
		// its late result after cancellation, as the shared field cache does.
		if c.ctx.Err() != nil {
			break
		}
		if err != nil {
			// Fatal errors retain precedence over a reconnect generation change.
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
				log.Logger.Infow("DCGM transient error, will retry",
					"component", "prof",
					"deviceID", device.ID,
					"error", err)
				continue
			}

			readErr = err
			break
		}

		values[i] = vals
	}
	return values, readErr
}

// publishCheck validates the snapshot before updating gauges or health. Its
// short lock scope prevents reset or Close from interleaving with publication.
func (c *component) publishCheck(inputs profilingReadInputs, values [][]dcgm.FieldValue_v1, readErr error, cr *checkResult) components.CheckResult {
	release := nvidiadcgm.AcquireSession(c.dcgmInstance)
	defer release()
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	if c.ctx.Err() == nil && inputs.generation == c.resetGeneration && len(inputs.fields) > 0 {
		for i, device := range inputs.devices {
			// Only current-generation values may update public gauges.
			for _, val := range values[i] {
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
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_SM_ACTIVE:
					metricDCGMFIProfSmActive.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_SM_OCCUPANCY:
					metricDCGMFIProfSmOccupancy.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_TENSOR_ACTIVE:
					metricDCGMFIProfPipeTensorActive.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_TENSOR_IMMA_ACTIVE:
					metricDCGMFIProfPipeTensorImmaActive.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_TENSOR_HMMA_ACTIVE:
					metricDCGMFIProfPipeTensorHmmaActive.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_TENSOR_DFMA_ACTIVE:
					metricDCGMFIProfPipeTensorDfmaActive.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_INT_ACTIVE:
					metricDCGMFIProfPipeIntActive.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_DRAM_ACTIVE:
					metricDCGMFIProfDramActive.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_FP64_ACTIVE:
					metricDCGMFIProfPipeFp64Active.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_FP32_ACTIVE:
					metricDCGMFIProfPipeFp32Active.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PIPE_FP16_ACTIVE:
					metricDCGMFIProfPipeFp16Active.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(val.Float64(), val.TS)

				case dcgm.DCGM_FI_PROF_PCIE_TX_BYTES:
					metricDCGMFIProfPcieTxBytes.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(float64(val.Int64()), val.TS)

				case dcgm.DCGM_FI_PROF_PCIE_RX_BYTES:
					metricDCGMFIProfPcieRxBytes.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(float64(val.Int64()), val.TS)

				case dcgm.DCGM_FI_PROF_NVLINK_TX_BYTES:
					metricDCGMFIProfNvlinkTxBytes.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(float64(val.Int64()), val.TS)

				case dcgm.DCGM_FI_PROF_NVLINK_RX_BYTES:
					metricDCGMFIProfNvlinkRxBytes.With(prometheus.Labels{
						"uuid": device.UUID,
						"gpu":  fmt.Sprintf("%d", device.ID),
					}).SetWithTimestamp(float64(val.Int64()), val.TS)
				}
			}
		}

		cr.err = readErr
		if readErr != nil {
			if nvidiadcgm.IsUnhealthyAPIError(readErr) {
				cr.health = apiv1.HealthStateTypeUnhealthy
				cr.reason = nvidiadcgm.AppendDCGMErrorType("failed to get DCGM profiling fields", readErr)
			} else {
				cr.health = apiv1.HealthStateTypeDegraded
				cr.reason = fmt.Sprintf("failed to get DCGM profiling fields: %v", readErr)
			}
		} else {
			cr.health = apiv1.HealthStateTypeHealthy
			cr.reason = "profiling metrics collected successfully"
		}
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
