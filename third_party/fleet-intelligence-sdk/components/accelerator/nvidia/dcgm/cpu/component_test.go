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

// TestSetupFailureHandling verifies that when CPU group creation, field group
// creation, or field watching setup fails during New(), the component returns
// Degraded state on Check().
func TestSetupFailureHandling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockInstance := &mockDCGMInstance{dcgmExists: false}

	gpudInst := &components.GPUdInstance{
		RootCtx:             ctx,
		DCGMInstance:        mockInstance,
		HealthCheckInterval: time.Minute,
	}

	comp, err := New(gpudInst)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Manually set setupDegradedReason to simulate a setup failure
	c := comp.(*component)
	c.setupDegradedReason = "failed to create DCGM CPU group: mock error"

	// Enable DCGMExists so Check() proceeds past early guards
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
	if summary != "failed to create DCGM CPU group: mock error" {
		t.Errorf("Summary() = %q, want setup error message", summary)
	}

	t.Logf("Setup failure correctly returned Degraded: %s", summary)
}

// TestNoCPUEntitiesReturnsHealthy verifies that when no CPU entities are
// available (hardware doesn't support CPU monitoring), the component returns
// Healthy (not Degraded).
func TestNoCPUEntitiesReturnsHealthy(t *testing.T) {
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

	// Component should have no CPU entities (GetEntityGroupEntities would fail in real scenario)
	c := comp.(*component)
	if len(c.cpuEntities) != 0 {
		t.Fatalf("Expected no CPU entities, got %d", len(c.cpuEntities))
	}

	// Check should return Healthy (not Degraded) because no CPUs is a valid state
	result := comp.Check()
	if result == nil {
		t.Fatal("Check() returned nil")
	}

	healthType := result.HealthStateType()
	if healthType != apiv1.HealthStateTypeHealthy {
		t.Errorf("HealthStateType() = %v, want %v", healthType, apiv1.HealthStateTypeHealthy)
	}

	t.Logf("No CPU entities correctly returned Healthy: %s", result.Summary())
}
