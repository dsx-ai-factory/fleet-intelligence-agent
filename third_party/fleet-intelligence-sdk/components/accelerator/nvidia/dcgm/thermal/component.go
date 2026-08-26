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

// Package thermal tracks NVIDIA GPU thermal/temperature metrics via DCGM.
package thermal

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	dcgmcommon "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/common"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

const Name = "accelerator-nvidia-dcgm-thermal"

const (
	defaultHealthCheckInterval = time.Minute

	EventNameIncident = "dcgm_thermal_incident"
)

var _ components.Component = &component{}

type component struct {
	ctx    context.Context
	cancel context.CancelFunc

	healthCheckInterval time.Duration

	dcgmInstance        nvidiadcgm.Instance
	dcgmHealthCache     *nvidiadcgm.HealthCache
	dcgmFieldValueCache *nvidiadcgm.FieldValueCache

	eventBucket eventstore.Bucket

	lastMu          sync.RWMutex
	lastCheckResult *checkResult
}

func New(gpudInstance *components.GPUdInstance) (components.Component, error) {
	cctx, ccancel := context.WithCancel(gpudInstance.RootCtx)

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
		dcgmFieldValueCache: gpudInstance.DCGMFieldValueCache,
	}

	// Only initialize if DCGM is available
	if c.dcgmInstance != nil {
		// Register this component's health watch system with DCGM
		if err := c.dcgmInstance.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_THERMAL); err != nil {
			log.Logger.Warnw("failed to add thermal health watch", "error", err)
		} else {
			log.Logger.Infow("registered DCGM thermal health watch")
		}
	}

	if gpudInstance.EventStore != nil {
		var err error
		c.eventBucket, err = gpudInstance.EventStore.Bucket(Name)
		if err != nil {
			ccancel()
			return nil, err
		}
	}

	return c, nil
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
	if c.eventBucket == nil {
		return nil, nil
	}
	events, err := c.eventBucket.Get(ctx, since)
	if err != nil {
		return nil, err
	}
	var ret apiv1.Events
	for _, ev := range events {
		ret = append(ret, ev.ToEvent())
	}
	return ret, nil
}

func (c *component) Close() error {
	log.Logger.Debugw("closing component")

	// Field watching is managed by centralized FieldValueCache, no cleanup needed here

	c.cancel()
	if c.eventBucket != nil {
		c.eventBucket.Close()
	}
	return nil
}

func (c *component) Check() components.CheckResult {
	log.Logger.Infow("checking nvidia gpu thermal via DCGM")

	c.lastMu.RLock()
	var prevIncidents []apiv1.HealthStateIncident
	if c.lastCheckResult != nil {
		prevIncidents = c.lastCheckResult.incidents
	}
	c.lastMu.RUnlock()

	cr := &checkResult{
		ts: time.Now().UTC(),
	}
	defer func() {
		c.lastMu.Lock()
		c.lastCheckResult = cr
		c.lastMu.Unlock()
	}()

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
	if c.dcgmHealthCache == nil {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "DCGM health cache is not configured"
		return cr
	}

	// Build entity ID to UUID mapping from DCGM devices
	// This provides the mapping from entity ID (0, 1, 2, etc.) to entity UUID
	deviceMapping := make(map[uint]string)
	for _, device := range c.dcgmInstance.GetDevices() {
		deviceMapping[device.ID] = device.UUID
	}

	// Get cached DCGM thermal health check result from shared cache
	healthResult, incidents, err := c.dcgmHealthCache.GetResult(dcgm.DCGM_HEALTH_WATCH_THERMAL)
	if err != nil {
		if nvidiadcgm.IsUnhealthyAPIError(err) {
			cr.health = apiv1.HealthStateTypeUnhealthy
			cr.reason = nvidiadcgm.AppendDCGMErrorType("failed to get DCGM thermal health check result", err)
		} else {
			// Unknown error - treat as degraded
			cr.health = apiv1.HealthStateTypeDegraded
			cr.reason = fmt.Sprintf("failed to get DCGM thermal health check result: %v", err)
		}
		cr.err = err
		return cr
	}

	// Query and export DCGM temperature field values for all devices
	deviceValues, err := c.dcgmFieldValueCache.GetResult(temperatureFields)
	if err != nil {
		if nvidiadcgm.IsUnhealthyAPIError(err) {
			cr.health = apiv1.HealthStateTypeUnhealthy
			cr.reason = nvidiadcgm.AppendDCGMErrorType("failed to get DCGM thermal fields", err)
		} else {
			// Unknown error - treat as degraded
			cr.health = apiv1.HealthStateTypeDegraded
			cr.reason = fmt.Sprintf("failed to get DCGM thermal fields: %v", err)
		}
		cr.err = err
		return cr
	} else {
		for _, deviceData := range deviceValues {
			for _, fieldValue := range deviceData.Values {
				// Use valid value
				switch fieldValue.FieldID {
				case dcgm.DCGM_FI_DEV_GPU_TEMP:
					metricDCGMFIDevGPUTemp.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_MEMORY_TEMP:
					metricDCGMFIDevMemoryTemp.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_SLOWDOWN_TEMP:
					metricDCGMFIDevSlowdownTemp.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_THERMAL_VIOLATION:
					metricDCGMFIDevThermalViolation.With(prometheus.Labels{"uuid": deviceData.UUID, "gpu": fmt.Sprintf("%d", deviceData.DeviceID)}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_GPU_TEMP_LIMIT:
					metricDCGMFIDevGPUTempLimit.With(prometheus.Labels{"uuid": deviceData.UUID, "gpu": fmt.Sprintf("%d", deviceData.DeviceID)}).Set(float64(fieldValue.Int64()))
				}
			}
		}
	}

	// Map DCGM health result to GPUd health state
	switch healthResult {
	case dcgm.DCGM_HEALTH_RESULT_PASS:
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "all thermal health checks passed"
	case dcgm.DCGM_HEALTH_RESULT_WARN:
		cr.health = apiv1.HealthStateTypeDegraded
		cr.enrichedIncidents = dcgmcommon.EnrichIncidents(incidents, deviceMapping)
		cr.incidents = dcgmcommon.ToHealthStateIncidents(cr.enrichedIncidents)
		cr.reason = dcgmcommon.FormatIncidents("thermal health warning", cr.enrichedIncidents)
	case dcgm.DCGM_HEALTH_RESULT_FAIL:
		cr.health = apiv1.HealthStateTypeUnhealthy
		cr.enrichedIncidents = dcgmcommon.EnrichIncidents(incidents, deviceMapping)
		cr.incidents = dcgmcommon.ToHealthStateIncidents(cr.enrichedIncidents)
		cr.reason = dcgmcommon.FormatIncidents("thermal health failure", cr.enrichedIncidents)
	default:
		cr.health = apiv1.HealthStateTypeDegraded
		cr.reason = "unknown health status"
	}

	dcgmcommon.EmitNewIncidentEvents(c.ctx, cr.ts, Name, EventNameIncident, c.eventBucket, prevIncidents, cr.incidents)

	return cr
}

var _ components.CheckResult = &checkResult{}

type checkResult struct {
	ts                time.Time
	err               error
	health            apiv1.HealthStateType
	reason            string
	incidents         []apiv1.HealthStateIncident
	enrichedIncidents []dcgmcommon.EnrichedIncident
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
		Incidents: cr.incidents,
	}

	// Add enriched DCGM incidents to ExtraInfo if available
	if len(cr.enrichedIncidents) > 0 {
		if enrichedIncidentsJSON, err := json.Marshal(cr.enrichedIncidents); err == nil {
			state.ExtraInfo = map[string]string{"dcgm_incidents": string(enrichedIncidentsJSON)}
		}
	}

	return apiv1.HealthStates{state}
}
