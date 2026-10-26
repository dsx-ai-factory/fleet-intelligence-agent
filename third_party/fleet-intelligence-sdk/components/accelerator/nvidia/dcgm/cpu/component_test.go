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

package cpu

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics/scraper"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dcgmInst, err := nvidiadcgm.New()
	if err != nil {
		t.Fatalf("failed to create DCGM instance: %v", err)
	}
	defer dcgmInst.Shutdown()

	// Create health cache and field value cache for testing
	dcgmHealthCache := nvidiadcgm.NewHealthCache(ctx, dcgmInst, time.Minute)
	dcgmFieldValueCache := nvidiadcgm.NewFieldValueCache(ctx, dcgmInst, time.Minute)

	gpudInst := &components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        dcgmInst,
		DCGMHealthCache:     dcgmHealthCache,
		DCGMFieldValueCache: dcgmFieldValueCache,
		HealthCheckInterval: time.Minute,
	}

	comp, err := New(gpudInst)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	if comp.Name() != Name {
		t.Errorf("expected name %q, got %q", Name, comp.Name())
	}
}

func TestCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dcgmInst, err := nvidiadcgm.New()
	if err != nil {
		t.Fatalf("failed to create DCGM instance: %v", err)
	}
	defer dcgmInst.Shutdown()

	if !dcgmInst.DCGMExists() {
		t.Skip("DCGM not available, skipping test")
	}

	devices := dcgmInst.GetDevices()
	if len(devices) == 0 {
		t.Skip("No GPU devices found, skipping test")
	}

	// Create health cache and field value cache for testing
	dcgmHealthCache := nvidiadcgm.NewHealthCache(ctx, dcgmInst, time.Minute)
	dcgmFieldValueCache := nvidiadcgm.NewFieldValueCache(ctx, dcgmInst, time.Minute)

	gpudInst := &components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        dcgmInst,
		DCGMHealthCache:     dcgmHealthCache,
		DCGMFieldValueCache: dcgmFieldValueCache,
		HealthCheckInterval: time.Minute,
	}

	comp, err := New(gpudInst)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Setup field watching for CPU fields after component creation
	if err := dcgmFieldValueCache.SetupFieldWatchingWithName("gpud-cpu-fields"); err != nil {
		t.Fatalf("failed to setup field watching: %v", err)
	}

	// Poll once to populate the cache
	if err := dcgmFieldValueCache.Poll(); err != nil {
		t.Logf("Poll() warning: %v", err)
	}

	// Perform check - this will query DCGM and update metrics
	result := comp.Check()
	if result == nil {
		t.Fatal("Check() returned nil")
	}

	if result.ComponentName() != Name {
		t.Errorf("ComponentName() = %q, want %q", result.ComponentName(), Name)
	}

	healthType := result.HealthStateType()
	t.Logf("Health state: %v, Summary: %s", healthType, result.Summary())

	// CPU component should always return healthy
	if healthType != apiv1.HealthStateTypeHealthy {
		t.Errorf("HealthStateType() = %v, want %v", healthType, apiv1.HealthStateTypeHealthy)
	}

	// Now verify that metrics were actually set in Prometheus by the Check() call
	t.Logf("\n=== Verifying Prometheus Metrics Were Set ===")

	// Use pkg/metrics scraper to gather metrics
	promScraper, err := scraper.NewPrometheusScraper(pkgmetrics.DefaultGatherer())
	if err != nil {
		t.Fatalf("Failed to create Prometheus scraper: %v", err)
	}

	metrics, err := promScraper.Scrape(ctx)
	if err != nil {
		t.Fatalf("Failed to scrape metrics: %v", err)
	}

	// Look for our CPU metrics
	cpuMetricsFound := map[string]int{
		"dcgm_fi_dev_cpu_temp_current":       0,
		"dcgm_fi_dev_cpu_power_limit":        0,
		"dcgm_fi_dev_cpu_temp_warning":       0,
		"dcgm_fi_dev_cpu_temp_critical":      0,
		"dcgm_fi_dev_cpu_power_util_current": 0,
	}

	totalFound := 0

	for _, metric := range metrics {
		if metric.Component != Name {
			continue
		}

		if _, exists := cpuMetricsFound[metric.Name]; !exists {
			t.Logf("  [SKIP] unexpected metric %s", metric.Name)
			continue
		}

		cpuID := metric.Labels["cpu_id"]
		t.Logf("  [OK] %s (cpu_id=%s): %.4f", metric.Name, cpuID, metric.Value)
		cpuMetricsFound[metric.Name]++
		totalFound++
	}

	if totalFound == 0 {
		t.Logf("No CPU metrics were recorded (likely unsupported on this hardware)")
	}

	for metricName, count := range cpuMetricsFound {
		if count == 0 {
			t.Logf("Metric %s was not found in Prometheus registry (likely unsupported on this hardware)", metricName)
			continue
		}
		t.Logf("Found %d instance(s) of metric %s", count, metricName)
	}
}

func TestCompletedCPUSetupWithNoEntitiesReturnsHealthy(t *testing.T) {
	comp, err := New(&components.GPUdInstance{RootCtx: context.Background(), DCGMInstance: &mockDCGMInstance{dcgmExists: true}})
	require.NoError(t, err)
	c := comp.(*component)
	defer func() { require.NoError(t, c.Close()) }()
	// Empty discovery is a completed setup outcome. Checks must not try to read.
	c.fieldSetupComplete = true
	c.readFields = func(dcgm.Field_Entity_Group, uint, []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		t.Fatal("no CPU fields should be read after empty discovery")
		return nil, nil
	}
	for range 2 {
		result := c.Check()
		require.Equal(t, apiv1.HealthStateTypeHealthy, result.HealthStateType())
		require.Equal(t, "no CPU entities available", result.Summary())
	}
}

func TestCloseWaitsForActiveCheck(t *testing.T) {
	comp, err := New(&components.GPUdInstance{RootCtx: context.Background()})
	require.NoError(t, err)
	c := comp.(*component)
	previous := &checkResult{ts: time.Unix(100, 0).UTC(), health: apiv1.HealthStateTypeDegraded, reason: "previous check failed", err: context.DeadlineExceeded}
	c.lastCheckResult = previous
	states := previous.HealthStates()
	c.dcgmInstance = &mockDCGMInstance{dcgmExists: true}
	c.fieldSetupComplete = true
	c.cpuEntities = []uint{0}
	c.cpuGroupHandle.SetHandle(1)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	c.readFields = func(dcgm.Field_Entity_Group, uint, []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		close(entered)
		<-release
		// The synthetic watch has no native CPU group to destroy. Clear its
		// handle while Check still holds setupMu, before Close can run cleanup.
		c.cpuGroupHandle = dcgm.GroupHandle{}
		return nil, nil
	}
	checked := make(chan components.CheckResult, 1)
	go func() { checked <- c.Check() }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	<-c.ctx.Done()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before the active check finished: %v", err)
	default:
	}
	unblock()
	require.Equal(t, states, (<-checked).HealthStates(), "an interrupted check must preserve the prior assessment")
	require.NoError(t, <-closed)
	require.NoError(t, c.Close())
	require.Equal(t, states, c.Check().HealthStates())
	require.Equal(t, states, c.LastHealthStates())
}

// This integration test exercises native CPU group setup and retry. It needs
// a connected DCGM session with CPU monitoring support.
func TestCPUDeferredSetupAndRecovery(t *testing.T) {
	inst, err := nvidiadcgm.New()
	require.NoError(t, err)
	defer inst.Shutdown()
	if !inst.DCGMExists() {
		t.Skip("DCGM is not available")
	}
	entities, err := dcgm.GetEntityGroupEntities(dcgm.FE_CPU)
	if err != nil || len(entities) == 0 {
		t.Skip("DCGM CPU monitoring is not available")
	}
	availability := &mockDCGMInstance{}
	comp, err := New(&components.GPUdInstance{
		RootCtx: context.Background(), DCGMInstance: availability,
		DCGMGroupNames: components.DCGMGroupNames{
			CPUGroup:      fmt.Sprintf("test-cpu-retry-%d", time.Now().UnixNano()),
			CPUFieldGroup: fmt.Sprintf("test-cpu-fields-%d", time.Now().UnixNano()),
		},
	})
	require.NoError(t, err)
	c := comp.(*component)
	defer func() { require.NoError(t, c.Close()) }()
	require.Equal(t, "DCGM library is not loaded", c.Check().Summary())
	require.False(t, c.fieldSetupComplete)
	availability.dcgmExists = true
	// DCGM permits at most 128 fields per group. Rejecting an oversized
	// fieldWatches exercises rollback after the native CPU group was populated.
	var oversized []dcgm.Short
	for id := 1; id <= 129; id++ {
		oversized = append(oversized, dcgm.Short(id))
	}
	c.fieldWatches = nvidiadcgm.NewFieldWatches(oversized, time.Minute, nvidiadcgm.CollectionIntervals{})
	reads := 0
	c.readFields = func(dcgm.Field_Entity_Group, uint, []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		reads++
		return nil, nil
	}
	result := c.Check()
	require.Equal(t, apiv1.HealthStateTypeDegraded, result.HealthStateType())
	require.Contains(t, result.Summary(), "failed to set up DCGM CPU field watching")
	require.False(t, c.fieldSetupComplete)
	require.Zero(t, reads)
	require.Zero(t, c.cpuGroupHandle.GetHandle())
	c.fieldWatches = nvidiadcgm.NewFieldWatches(cpuLevelFields, time.Minute, nvidiadcgm.CollectionIntervals{})

	require.Equal(t, apiv1.HealthStateTypeHealthy, c.Check().HealthStateType())
	require.True(t, c.fieldSetupComplete)
	require.Empty(t, c.setupDegradedReason)
	require.Equal(t, len(entities), reads)
	handle := c.cpuGroupHandle
	c.Check()
	require.Equal(t, handle, c.cpuGroupHandle, "successful setup must not recreate resources")
	require.Equal(t, 2*len(entities), reads, "every check reads all selected fields from DCGM cache")
}

func TestCheckReadsAllCachedFieldsAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		comp, err := New(&components.GPUdInstance{
			RootCtx: context.Background(), HealthCheckInterval: 30 * time.Second,
			DCGMIntervals: nvidiadcgm.CollectionIntervals{FieldProfiles: map[dcgm.Short]nvidiadcgm.FieldProfile{1110: nvidiadcgm.ExtendedProfile}},
		})
		require.NoError(t, err)
		c := comp.(*component)
		c.dcgmInstance = &mockDCGMInstance{dcgmExists: true}
		c.fieldSetupComplete = true
		c.cpuGroupHandle.SetHandle(1)
		c.cpuEntities = []uint{0}
		defer func() { c.cpuGroupHandle = dcgm.GroupHandle{}; require.NoError(t, c.Close()) }()
		var reads [][]dcgm.Short
		fail := true
		c.readFields = func(_ dcgm.Field_Entity_Group, _ uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
			reads = append(reads, append([]dcgm.Short(nil), fields...))
			if fail {
				return nil, fmt.Errorf("read failed")
			}
			return nil, nil
		}
		c.Check()
		time.Sleep(30 * time.Second)
		fail = false
		require.Equal(t, apiv1.HealthStateTypeHealthy, c.Check().HealthStateType())
		time.Sleep(30 * time.Second)
		c.Check()
		require.Equal(t, [][]dcgm.Short{cpuLevelFields, cpuLevelFields, cpuLevelFields}, reads)
		c.cpuGroupHandle = dcgm.GroupHandle{}
		require.NoError(t, c.Close())
		require.NoError(t, c.Close())
		time.Sleep(time.Hour)
		c.Check()
		require.Len(t, reads, 3)
	})
}

func TestCPUSetupDefersUntilChecksAndStopsAfterClose(t *testing.T) {
	inst := &mockDCGMInstance{dcgmExists: true}
	comp, err := New(&components.GPUdInstance{
		RootCtx: context.Background(), DCGMInstance: inst,
		DCGMGroupNames: components.DCGMGroupNames{CPUGroup: "custom-cpu", CPUFieldGroup: "custom-fields"},
	})
	require.NoError(t, err)
	c := comp.(*component)
	require.False(t, c.fieldSetupComplete, "New must defer native setup")
	require.Empty(t, c.cpuEntities)
	require.Zero(t, c.cpuGroupHandle.GetHandle())
	require.Equal(t, "custom-cpu", c.cpuGroupName)
	require.Equal(t, "custom-fields", c.fieldGroupName)
	inst.dcgmExists = false
	previous := c.Check()
	require.Equal(t, "DCGM library is not loaded", previous.Summary())
	require.False(t, c.fieldSetupComplete)
	require.NoError(t, c.Close())
	inst.dcgmExists = true
	require.Same(t, previous, c.Check())
	require.False(t, c.fieldSetupComplete)
	require.Empty(t, c.cpuEntities)
	require.Zero(t, c.cpuGroupHandle.GetHandle())
	require.NoError(t, c.Close())
}

func TestCollectionProfilesDoNotEnableCPUFields(t *testing.T) {
	comp, err := New(&components.GPUdInstance{
		RootCtx: context.Background(), HealthCheckInterval: 5 * time.Second,
		DCGMIntervals: nvidiadcgm.CollectionIntervals{
			Extended: 10 * time.Second,
			FieldProfiles: map[dcgm.Short]nvidiadcgm.FieldProfile{
				dcgm.DCGM_FI_DEV_CPU_TEMP_CURRENT: nvidiadcgm.ExtendedProfile,
				999:                               nvidiadcgm.ExtendedProfile,
			},
		},
	})
	require.NoError(t, err)
	c := comp.(*component)
	defer func() { require.NoError(t, c.Close()) }()
	require.ElementsMatch(t, cpuLevelFields, c.fieldWatches.Fields())
	require.NotContains(t, c.fieldWatches.Fields(), dcgm.Short(999), "assignments must not enable fields")
}

func TestCPUDiscoveryFailureIsDegradedAndRetryable(t *testing.T) {
	// Exercise the native failure path without adding production test hooks.
	// A live session with successful CPU discovery cannot exercise this case.
	_, discoveryErr := dcgm.GetEntityGroupEntities(dcgm.FE_CPU)
	if discoveryErr == nil {
		t.Skip("CPU discovery succeeds; this test requires a discovery failure")
	}
	comp, err := New(&components.GPUdInstance{
		RootCtx: context.Background(), DCGMInstance: &mockDCGMInstance{dcgmExists: true},
	})
	require.NoError(t, err)
	c := comp.(*component)
	defer func() { require.NoError(t, c.Close()) }()
	c.readFields = func(dcgm.Field_Entity_Group, uint, []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		t.Fatal("fields must not be read after failed CPU discovery")
		return nil, nil
	}
	var previous components.CheckResult
	for range 2 {
		// A new attempt must replace the prior setup error, rather than simply
		// returning a stale health result without retrying discovery.
		c.setupDegradedReason = "previous setup failure"
		result := c.Check()
		previous = result
		require.Equal(t, apiv1.HealthStateTypeDegraded, result.HealthStateType())
		require.Contains(t, result.Summary(), "failed to discover DCGM CPU entities:")
		require.False(t, c.fieldSetupComplete)
		require.Empty(t, c.cpuEntities)
		require.Zero(t, c.cpuGroupHandle.GetHandle())
	}
	states := c.LastHealthStates()
	require.NoError(t, c.Close())
	require.Same(t, previous, c.Check())
	require.Equal(t, states, c.LastHealthStates())
}

func TestStoppedBeforeFirstCheckReturnsInitializing(t *testing.T) {
	comp, err := New(&components.GPUdInstance{RootCtx: context.Background()})
	require.NoError(t, err)
	c := comp.(*component)
	require.NoError(t, c.Close())
	for range 2 {
		result := c.Check()
		require.Equal(t, apiv1.HealthStateTypeInitializing, result.HealthStateType())
		require.Equal(t, "collector stopped before a check completed", result.Summary())
		require.Nil(t, c.lastCheckResult, "a stopped call must not publish a health assessment")
	}
}
