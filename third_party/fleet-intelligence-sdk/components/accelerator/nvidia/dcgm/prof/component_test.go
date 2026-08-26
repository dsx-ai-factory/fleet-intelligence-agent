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
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics/scraper"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
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

	gpudInst := &components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        dcgmInst,
		DCGMHealthCache:     dcgmHealthCache,
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

	gpudInst := &components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        dcgmInst,
		DCGMHealthCache:     dcgmHealthCache,
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

// TestSetupFailureHandling verifies that when field group creation or watching
// setup fails during New(), the component returns Degraded state on Check().
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

	// Manually set setupDegradedReason to simulate a setup failure
	// (In real scenarios, this would be set by FieldGroupCreate or WatchFieldsWithGroupEx failures)
	c := comp.(*component)
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
