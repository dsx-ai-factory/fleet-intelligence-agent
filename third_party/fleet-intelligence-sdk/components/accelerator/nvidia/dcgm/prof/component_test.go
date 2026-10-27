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

package prof

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

	// Create health cache for testing
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

	// Create health cache for testing
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

	// Note: Prof component creates its own field group and queries DCGM directly.
	// It does not use the shared field cache, so we don't need to set it up.

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

	// Profiling component should return healthy if setup succeeded
	// If there was a setup error (e.g., duplicate field group from previous test), it will be Degraded
	if healthType != apiv1.HealthStateTypeHealthy && healthType != apiv1.HealthStateTypeDegraded {
		t.Errorf("HealthStateType() = %v, want Healthy or Degraded", healthType)
	}

	// Only verify metrics if component is healthy (setup succeeded)
	if healthType != apiv1.HealthStateTypeHealthy {
		t.Logf("Skipping metrics verification - component is %v due to setup error", healthType)
		return
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

	// Look for our profiling metrics
	profilingMetricsFound := map[string]int{
		"dcgm_fi_prof_gr_engine_active":   0,
		"dcgm_fi_prof_sm_active":          0,
		"dcgm_fi_prof_sm_occupancy":       0,
		"dcgm_fi_prof_pipe_tensor_active": 0,
		"dcgm_fi_prof_dram_active":        0,
		"dcgm_fi_prof_pipe_fp64_active":   0,
		"dcgm_fi_prof_pipe_fp32_active":   0,
		"dcgm_fi_prof_pipe_fp16_active":   0,
		"dcgm_fi_prof_pcie_tx_bytes":      0,
		"dcgm_fi_prof_pcie_rx_bytes":      0,
		"dcgm_fi_prof_nvlink_tx_bytes":    0,
		"dcgm_fi_prof_nvlink_rx_bytes":    0,
	}

	totalFound := 0
	for _, metric := range metrics {
		if metric.Component != Name {
			continue
		}

		if _, exists := profilingMetricsFound[metric.Name]; !exists {
			t.Logf("  [SKIP] unexpected metric %s", metric.Name)
			continue
		}

		uuid := metric.Labels["uuid"]
		t.Logf("  [OK] %s (uuid=%s): %.4f", metric.Name, uuid, metric.Value)
		profilingMetricsFound[metric.Name]++
		totalFound++
	}

	if totalFound == 0 {
		// Check if profiling is unavailable
		summary := result.Summary()
		if summary == "profiling field group not created (module unavailable)" {
			t.Logf("No profiling metrics were recorded (profiling module unavailable)")
		} else {
			t.Fatalf("no profiling metrics were recorded")
		}
	}

	for metricName, count := range profilingMetricsFound {
		if count == 0 {
			t.Logf("Metric %s was not found in Prometheus registry (likely unsupported on this hardware)", metricName)
			continue
		}
		t.Logf("Found %d instance(s) of metric %s", count, metricName)
	}
}

// TestSetupFailureHandling verifies that a recorded field setup failure is
// returned as Degraded by Check().
func TestSetupFailureHandling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a mock instance that reports DCGM exists but will fail CGO calls
	// Since we can't actually trigger FieldGroupCreate failures without CGO,
	// this test documents the expected behavior when setupDegradedReason is set.
	mockInstance := &mockDCGMInstance{dcgmExists: false}

	gpudInst := &components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        mockInstance,
		HealthCheckInterval: time.Minute,
	}

	// Create component - with dcgmExists=false, it bypasses CGO setup
	comp, err := New(gpudInst)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Manually record the terminal state so this test can exercise reporting
	// without invoking DCGM's package-level CGO setup functions.
	c := comp.(*component)
	c.fieldSetupComplete = true
	c.setupDegradedReason = "failed to create DCGM profiling field group: mock error"

	// Now enable DCGMExists so Check() proceeds past the early guards
	mockInstance.dcgmExists = true

	// Check should return Degraded with the setup error reason
	result := comp.Check()
	if result == nil {
		t.Fatal("Check() returned nil")
	}

	healthType := result.HealthStateType()
	if healthType != apiv1.HealthStateTypeDegraded {
		t.Errorf("HealthStateType() = %v, want %v", healthType, apiv1.HealthStateTypeDegraded)
	}

	summary := result.Summary()
	if summary != "failed to create DCGM profiling field group: mock error" {
		t.Errorf("Summary() = %q, want setup error message", summary)
	}

	t.Logf("Setup failure correctly returned Degraded: %s", summary)
	states := c.LastHealthStates()
	require.NoError(t, c.Close())
	require.Same(t, result, c.Check())
	require.Equal(t, states, c.LastHealthStates())
}

func TestCheckAttemptsDeferredSetupAfterDCGMReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockInstance := &mockDCGMInstance{dcgmExists: false}
	comp, err := New(&components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        mockInstance,
		HealthCheckInterval: time.Minute,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	c := comp.(*component)
	if c.fieldSetupComplete {
		t.Fatal("profiling field setup was attempted while DCGM was unavailable")
	}

	c.setupDegradedReason = "previous field setup failure"
	mockInstance.dcgmExists = true
	result := comp.Check()
	if !c.fieldSetupComplete {
		t.Fatal("profiling field setup was not attempted after DCGM became available")
	}
	if result.HealthStateType() != apiv1.HealthStateTypeHealthy {
		t.Fatalf("HealthStateType() = %v, want Healthy", result.HealthStateType())
	}
}

// TestNoWatchedFieldsReturnsHealthy verifies that when no profiling fields
// are supported by the hardware, the component returns Healthy (not Degraded).
func TestNoWatchedFieldsReturnsHealthy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockInstance := &mockDCGMInstance{dcgmExists: true}

	gpudInst := &components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        mockInstance,
		HealthCheckInterval: time.Minute,
	}

	comp, err := New(gpudInst)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Component should have no watched fields (hardware doesn't support profiling)
	c := comp.(*component)
	if len(c.watchedFields) != 0 {
		t.Fatalf("Expected no watched fields, got %d", len(c.watchedFields))
	}

	// Check should return Healthy (not Degraded) because no fields is a valid state
	result := comp.Check()
	if result == nil {
		t.Fatal("Check() returned nil")
	}

	healthType := result.HealthStateType()
	if healthType != apiv1.HealthStateTypeHealthy {
		t.Errorf("HealthStateType() = %v, want %v", healthType, apiv1.HealthStateTypeHealthy)
	}

	t.Logf("No watched fields correctly returned Healthy: %s", result.Summary())
}

func TestCheckReadsAllCachedFieldsAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		comp, err := New(&components.GPUdInstance{
			RootCtx: context.Background(), HealthCheckInterval: 30 * time.Second,
			DCGMIntervals: nvidiadcgm.CollectionIntervals{FieldProfiles: map[dcgm.Short]nvidiadcgm.FieldProfile{1110: nvidiadcgm.ExtendedProfile}},
		})
		require.NoError(t, err)
		c := comp.(*component)
		c.dcgmInstance = &mockDCGMInstance{dcgmExists: true, devices: []nvidiadcgm.DeviceInfo{{ID: 0}}}
		c.fieldSetupComplete = true
		c.watchedFields = []dcgm.Short{100, 1200}
		defer func() { require.NoError(t, c.Close()) }()
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
		require.Equal(t, [][]dcgm.Short{c.watchedFields, c.watchedFields, c.watchedFields}, reads)

		require.NoError(t, c.Close())
		require.NoError(t, c.Close())
		time.Sleep(time.Hour)
		c.Check()
		require.Len(t, reads, 3)
	})
}

func TestCloseWaitsForActiveCheck(t *testing.T) {
	comp, err := New(&components.GPUdInstance{RootCtx: context.Background()})
	require.NoError(t, err)
	c := comp.(*component)
	previous := &checkResult{ts: time.Unix(100, 0).UTC(), health: apiv1.HealthStateTypeDegraded, reason: "previous check failed", err: context.DeadlineExceeded}
	c.lastCheckResult = previous
	states := previous.HealthStates()
	c.dcgmInstance = &mockDCGMInstance{dcgmExists: true, devices: []nvidiadcgm.DeviceInfo{{ID: 0}}}
	c.fieldSetupComplete = true
	c.watchedFields = []dcgm.Short{dcgm.DCGM_FI_PROF_SM_ACTIVE}
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	c.readFields = func(dcgm.Field_Entity_Group, uint, []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		close(entered)
		<-release
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

func TestProfilingTransientAndEmptyReadsReturnHealthy(t *testing.T) {
	comp, err := New(&components.GPUdInstance{RootCtx: context.Background()})
	require.NoError(t, err)
	c := comp.(*component)
	defer func() { require.NoError(t, c.Close()) }()
	c.dcgmInstance = &mockDCGMInstance{dcgmExists: true, devices: []nvidiadcgm.DeviceInfo{{ID: 0}}}
	c.fieldSetupComplete = true
	c.watchedFields = []dcgm.Short{100, 1200}
	readErr := fmt.Errorf("dcgm request failed with error code %d", dcgm.DCGM_ST_NVML_ERROR)
	c.readFields = func(_ dcgm.Field_Entity_Group, _ uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
		require.Equal(t, c.watchedFields, fields)
		return nil, readErr
	}
	require.Equal(t, apiv1.HealthStateTypeUnhealthy, c.Check().HealthStateType())
	readErr = fmt.Errorf("dcgm request failed with error code %d", dcgm.DCGM_ST_NO_DATA)
	require.Equal(t, apiv1.HealthStateTypeHealthy, c.Check().HealthStateType(), "transient errors are skipped")
	readErr = fmt.Errorf("dcgm request failed with error code %d", dcgm.DCGM_ST_NVML_ERROR)
	require.Equal(t, apiv1.HealthStateTypeUnhealthy, c.Check().HealthStateType())
	readErr = nil
	require.Equal(t, apiv1.HealthStateTypeHealthy, c.Check().HealthStateType(), "an empty value list is not an API error")
	readErr = fmt.Errorf("dcgm request failed with error code %d", dcgm.DCGM_ST_NVML_ERROR)
	require.Equal(t, apiv1.HealthStateTypeUnhealthy, c.Check().HealthStateType())
	c.dcgmInstance = &mockDCGMInstance{dcgmExists: true}
	require.Equal(t, apiv1.HealthStateTypeHealthy, c.Check().HealthStateType(), "no devices produces no read error")
}
