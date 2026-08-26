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

package inforom

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	dcgmcommon "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/dcgm/common"
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

	// Create health cache for testing and trigger initial poll
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

	// Start the health cache polling (component New() already registered health watches)
	if err := dcgmHealthCache.Start(); err != nil {
		t.Fatalf("HealthCache.Start() failed: %v", err)
	}

	// Perform check
	result := comp.Check()
	if result == nil {
		t.Fatal("Check() returned nil")
	}

	if result.ComponentName() != Name {
		t.Errorf("ComponentName() = %q, want %q", result.ComponentName(), Name)
	}

	healthType := result.HealthStateType()
	t.Logf("Health state: %v, Summary: %s", healthType, result.Summary())

	validHealthTypes := map[apiv1.HealthStateType]bool{
		apiv1.HealthStateTypeHealthy:   true,
		apiv1.HealthStateTypeDegraded:  true,
		apiv1.HealthStateTypeUnhealthy: true,
	}

	if !validHealthTypes[healthType] {
		t.Errorf("HealthStateType() = %v, want one of Healthy/Degraded/Unhealthy", healthType)
	}

}

func TestCheckResultHealthStates_PreservesLegacyIncidentsAndAddsTypedIncidents(t *testing.T) {
	enriched := []dcgmcommon.EnrichedIncident{
		{
			UUID:      "GPU-1234",
			EntityID:  "GPU-0",
			Message:   "Inforom corruption detected",
			ErrorCode: dcgm.DCGM_FR_CORRUPT_INFOROM,
			System:    dcgm.DCGM_HEALTH_WATCH_INFOROM,
			Health:    dcgm.DCGM_HEALTH_RESULT_WARN,
		},
	}

	cr := &checkResult{
		ts:                time.Now().UTC(),
		health:            apiv1.HealthStateTypeDegraded,
		reason:            "InfoROM health warning: 1 incident(s) across 1 device(s)",
		incidents:         dcgmcommon.ToHealthStateIncidents(enriched),
		enrichedIncidents: enriched,
	}

	states := cr.HealthStates()
	if len(states) != 1 {
		t.Fatalf("len(HealthStates()) = %d, want 1", len(states))
	}

	state := states[0]
	if len(state.Incidents) != 1 {
		t.Fatalf("len(state.Incidents) = %d, want 1", len(state.Incidents))
	}
	if got := state.Incidents[0].EntityID; got != "GPU-0" {
		t.Fatalf("state.Incidents[0].EntityID = %q", got)
	}

	raw := state.ExtraInfo["dcgm_incidents"]
	if raw == "" {
		t.Fatal("state.ExtraInfo[dcgm_incidents] is empty")
	}

	var legacy []map[string]any
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		t.Fatalf("json.Unmarshal(dcgm_incidents) error = %v", err)
	}
	if got := legacy[0]["uuid"]; got != "GPU-1234" {
		t.Fatalf("legacy uuid = %v", got)
	}
}

func TestEvents_NilBucket(t *testing.T) {
	c := &component{}
	events, err := c.Events(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Events() returned unexpected error: %v", err)
	}
	if events != nil {
		t.Fatalf("Events() returned %v, want nil", events)
	}
}
