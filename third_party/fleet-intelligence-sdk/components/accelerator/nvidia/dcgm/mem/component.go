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

// Package mem tracks NVIDIA GPU memory metrics via DCGM.
package mem

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

const Name = "accelerator-nvidia-dcgm-mem"

const (
	defaultHealthCheckInterval = time.Minute

	EventNameIncident = "dcgm_mem_incident"
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
		ctx:                 cctx,
		cancel:              ccancel,
		healthCheckInterval: healthCheckInterval,
		dcgmInstance:        gpudInstance.DCGMInstance,
		dcgmHealthCache:     gpudInstance.DCGMHealthCache,
		dcgmFieldValueCache: gpudInstance.DCGMFieldValueCache,
	}

	// Only initialize if DCGM is available
	if c.dcgmInstance != nil {
		// Register this component's health watch system with DCGM
		if err := c.dcgmInstance.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_MEM); err != nil {
			log.Logger.Warnw("failed to add memory health watch", "error", err)
		} else {
			log.Logger.Infow("registered DCGM memory health watch")
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
	return []string{"accelerator", "gpu", "nvidia", "dcgm", Name}
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
	log.Logger.Infow("checking nvidia gpu memory via DCGM")

	c.lastMu.RLock()
	var prevIncidents []apiv1.HealthStateIncident
	if c.lastCheckResult != nil {
		prevIncidents = c.lastCheckResult.incidents
	}
	c.lastMu.RUnlock()

	cr := &checkResult{ts: time.Now().UTC()}
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

	// Get cached DCGM memory health check result from shared cache
	healthResult, incidents, err := c.dcgmHealthCache.GetResult(dcgm.DCGM_HEALTH_WATCH_MEM)
	if err != nil {
		if nvidiadcgm.IsUnhealthyAPIError(err) {
			cr.health = apiv1.HealthStateTypeUnhealthy
			cr.reason = nvidiadcgm.AppendDCGMErrorType("failed to get DCGM memory health check result", err)
		} else {
			// Unknown error - treat as degraded
			cr.health = apiv1.HealthStateTypeDegraded
			cr.reason = fmt.Sprintf("failed to get DCGM memory health check result: %v", err)
		}
		cr.err = err
		return cr
	}

	// Query and export DCGM memory field values for all devices
	deviceValues, err := c.dcgmFieldValueCache.GetResult(memFields)
	if err != nil {
		if nvidiadcgm.IsUnhealthyAPIError(err) {
			cr.health = apiv1.HealthStateTypeUnhealthy
			cr.reason = nvidiadcgm.AppendDCGMErrorType("failed to get DCGM memory fields", err)
		} else {
			// Unknown error - treat as degraded
			cr.health = apiv1.HealthStateTypeDegraded
			cr.reason = fmt.Sprintf("failed to get DCGM memory fields: %v", err)
		}
		cr.err = err
		return cr
	} else {
		for _, deviceData := range deviceValues {
			for _, fieldValue := range deviceData.Values {
				// Use valid value
				switch fieldValue.FieldID {
				case dcgm.DCGM_FI_DEV_FB_FREE:
					metricDCGMFIDevFBFree.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_FB_USED:
					metricDCGMFIDevFBUsed.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_FB_TOTAL:
					metricDCGMFIDevFBTotal.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_FB_USED_PERCENT:
					metricDCGMFIDevFBUsedPercent.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Float64()))
				case dcgm.DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS:
					metricDCGMFIDevUncorrectableRemappedRows.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS:
					metricDCGMFIDevCorrectableRemappedRows.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ROW_REMAP_FAILURE:
					metricDCGMFIDevRowRemapFailure.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ROW_REMAP_PENDING:
					metricDCGMFIDevRowRemapPending.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_SBE_VOL_TOTAL:
					metricDCGMFIDevECCSBEVolTotal.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_DBE_VOL_TOTAL:
					metricDCGMFIDevECCDBEVolTotal.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_SBE_AGG_TOTAL:
					metricDCGMFIDevECCSBEAggTotal.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_DBE_AGG_TOTAL:
					metricDCGMFIDevECCDBAggTotal.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_SBE_VOL_DEV:
					metricDCGMFIDevECCSBEVolDev.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_DBE_VOL_DEV:
					metricDCGMFIDevECCDBEVolDev.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_SBE_AGG_DEV:
					metricDCGMFIDevECCSBEAggDev.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_ECC_DBE_AGG_DEV:
					metricDCGMFIDevECCDBEAggDev.With(prometheus.Labels{
						"uuid": deviceData.UUID,
						"gpu":  fmt.Sprintf("%d", deviceData.DeviceID),
					}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_HIGH:
					metricDCGMFIDevBanksRemapRowsAvailHigh.With(prometheus.Labels{"uuid": deviceData.UUID, "gpu": fmt.Sprintf("%d", deviceData.DeviceID)}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_LOW:
					metricDCGMFIDevBanksRemapRowsAvailLow.With(prometheus.Labels{"uuid": deviceData.UUID, "gpu": fmt.Sprintf("%d", deviceData.DeviceID)}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_MAX:
					metricDCGMFIDevBanksRemapRowsAvailMax.With(prometheus.Labels{"uuid": deviceData.UUID, "gpu": fmt.Sprintf("%d", deviceData.DeviceID)}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_NONE:
					metricDCGMFIDevBanksRemapRowsAvailNone.With(prometheus.Labels{"uuid": deviceData.UUID, "gpu": fmt.Sprintf("%d", deviceData.DeviceID)}).Set(float64(fieldValue.Int64()))
				case dcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_PARTIAL:
					metricDCGMFIDevBanksRemapRowsAvailPartial.With(prometheus.Labels{"uuid": deviceData.UUID, "gpu": fmt.Sprintf("%d", deviceData.DeviceID)}).Set(float64(fieldValue.Int64()))
				}
			}
		}
	}

	// Map DCGM health result to GPUd health state
	switch healthResult {
	case dcgm.DCGM_HEALTH_RESULT_PASS:
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "all memory health checks passed"
	case dcgm.DCGM_HEALTH_RESULT_WARN:
		cr.health = apiv1.HealthStateTypeDegraded
		cr.enrichedIncidents = dcgmcommon.EnrichIncidents(incidents, deviceMapping)
		cr.incidents = dcgmcommon.ToHealthStateIncidents(cr.enrichedIncidents)
		cr.reason = dcgmcommon.FormatIncidents("memory health warning", cr.enrichedIncidents)
	case dcgm.DCGM_HEALTH_RESULT_FAIL:
		cr.health = apiv1.HealthStateTypeUnhealthy
		cr.enrichedIncidents = dcgmcommon.EnrichIncidents(incidents, deviceMapping)
		cr.incidents = dcgmcommon.ToHealthStateIncidents(cr.enrichedIncidents)
		cr.reason = dcgmcommon.FormatIncidents("memory health failure", cr.enrichedIncidents)
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
		return apiv1.HealthStates{{
			Time:      metav1.NewTime(time.Now().UTC()),
			Component: Name,
			Name:      Name,
			Health:    apiv1.HealthStateTypeHealthy,
			Reason:    "no data yet",
		}}
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
