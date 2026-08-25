// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

package collector

import (
	"context"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/machineinfo"
)

func TestGenerateCollectionID(t *testing.T) {
	// Generate multiple collection IDs
	id1 := GenerateCollectionID()
	id2 := GenerateCollectionID()

	// Verify IDs are generated
	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)

	// Verify IDs are unique
	assert.NotEqual(t, id1, id2, "Collection IDs should be unique")

	// Verify IDs are valid hex strings
	_, err1 := hex.DecodeString(id1)
	_, err2 := hex.DecodeString(id2)
	assert.NoError(t, err1, "ID1 should be valid hex")
	assert.NoError(t, err2, "ID2 should be valid hex")

	// Verify IDs are 32 characters (16 bytes in hex)
	assert.Len(t, id1, 32, "Collection ID should be 32 characters")
	assert.Len(t, id2, 32, "Collection ID should be 32 characters")
}

func TestGenerateEventID(t *testing.T) {
	id1 := GenerateEventID()
	id2 := GenerateEventID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2, "Event IDs should be unique")

	_, err1 := uuid.Parse(id1)
	_, err2 := uuid.Parse(id2)
	assert.NoError(t, err1, "ID1 should be a valid UUID")
	assert.NoError(t, err2, "ID2 should be a valid UUID")
}

func TestNew(t *testing.T) {
	cfg := &config.HealthExporterConfig{
		IncludeMachineInfo:   true,
		IncludeMetrics:       true,
		IncludeEvents:        true,
		IncludeComponentData: true,
	}
	c := New(cfg, nil, nil, nil, "test-machine-id", nil)

	assert.NotNil(t, c, "Collector should be created")

	// Verify it implements Collector interface
	var _ = c
}

func TestCollector_Collect_BasicFlow(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeMachineInfo:   false,
		IncludeMetrics:       false,
		IncludeEvents:        false,
		IncludeComponentData: false,
	}

	collector := New(cfg, nil, nil, nil, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	require.NoError(t, err)
	require.NotNil(t, data)

	// Verify basic fields
	assert.NotEmpty(t, data.CollectionID, "CollectionID should be generated")
	// MachineID may be empty in test environment
	t.Logf("MachineID: %s", data.MachineID)
	assert.False(t, data.Timestamp.IsZero(), "Timestamp should be set")

	// Verify optional fields are nil/empty when disabled
	assert.Nil(t, data.MachineInfo, "MachineInfo should be nil when disabled")
	assert.Empty(t, data.Metrics, "Metrics should be empty when disabled")
	assert.Empty(t, data.Events, "Events should be empty when disabled")
	assert.Empty(t, data.ComponentData, "ComponentData should be empty when disabled")
}

func TestCollector_CollectMachineInfo_NoNVML(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeMachineInfo: true,
	}

	collector := New(cfg, nil, nil, nil, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	// Should still succeed but machine info will not be collected
	require.NoError(t, err)
	require.NotNil(t, data)

	// MachineInfo should be nil because NVML is not available
	assert.Nil(t, data.MachineInfo, "MachineInfo should be nil without NVML")
}

func TestCollector_CollectMachineInfo_UsesCachedProvider(t *testing.T) {
	info := &machineinfo.MachineInfo{
		GPUInfo: &apiv1.MachineGPUInfo{
			GPUs: []apiv1.MachineGPUInstance{{UUID: "GPU-123", GPUIndex: "0"}},
		},
	}
	provider := &fakeMachineInfoProvider{
		info: info,
		ok:   true,
	}
	data := &HealthData{}
	c := &collector{
		machineInfoProvider: provider,
	}

	c.collectMachineInfo(context.Background(), data)

	assert.Same(t, info, data.MachineInfo)
	assert.True(t, provider.refreshed)
	assert.False(t, provider.waited)
}

func TestCachedMachineInfoProvider_DeduplicatesConcurrentRefresh(t *testing.T) {
	originalGetMachineInfo := getMachineInfo
	defer func() {
		getMachineInfo = originalGetMachineInfo
	}()

	var calls atomic.Int32
	blocker := make(chan struct{})
	getMachineInfo = func(nvmlInstance nvidianvml.Instance, opts ...machineinfo.MachineInfoOption) (*machineinfo.MachineInfo, error) {
		calls.Add(1)
		<-blocker
		return &machineinfo.MachineInfo{Hostname: "refreshed"}, nil
	}

	provider := newCachedMachineInfoProvider(nvidianvml.NewNoOp(), time.Minute).(*cachedMachineInfoProvider)
	ctx := context.Background()

	for range 3 {
		provider.RefreshAsync(ctx)
	}

	require.Eventually(t, func() bool {
		return calls.Load() == 1
	}, time.Second, 10*time.Millisecond)

	close(blocker)

	require.Eventually(t, func() bool {
		info, ok := provider.Get()
		return ok && info.Hostname == "refreshed"
	}, time.Second, 10*time.Millisecond)
}

func TestCachedMachineInfoProvider_WaitForInitialRefreshReturnsAfterCompletion(t *testing.T) {
	provider := newCachedMachineInfoProvider(nvidianvml.NewNoOp(), time.Minute).(*cachedMachineInfoProvider)

	go func() {
		time.Sleep(50 * time.Millisecond)
		provider.markInitialRefreshDone()
	}()

	start := time.Now()
	provider.WaitForInitialRefresh(context.Background(), time.Second)
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
	assert.Less(t, elapsed, 300*time.Millisecond)
}

func TestCachedMachineInfoProvider_WaitForInitialRefreshTimesOut(t *testing.T) {
	provider := newCachedMachineInfoProvider(nvidianvml.NewNoOp(), time.Minute).(*cachedMachineInfoProvider)

	start := time.Now()
	provider.WaitForInitialRefresh(context.Background(), 100*time.Millisecond)
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 300*time.Millisecond)
}

func TestCollector_CollectMetrics_NoStore(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeMetrics:  true,
		MetricsLookback: metav1.Duration{Duration: 5 * time.Minute},
	}

	collector := New(cfg, nil, nil, nil, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	// Should still succeed but metrics will not be collected
	require.NoError(t, err)
	require.NotNil(t, data)

	// Metrics should be empty because store is not available
	assert.Empty(t, data.Metrics, "Metrics should be empty without store")
}

func TestCollector_CollectMetrics_WithStore(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeMetrics:  true,
		MetricsLookback: metav1.Duration{Duration: 5 * time.Minute},
	}

	// Create mock metrics store
	mockStore := &mockMetricsStore{
		metrics: pkgmetrics.Metrics{
			{
				Component:        "gpu",
				Name:             "temperature",
				UnixMilliseconds: time.Now().UnixMilli(),
				Value:            65.5,
			},
			{
				Component:        "cpu",
				Name:             "usage",
				UnixMilliseconds: time.Now().UnixMilli(),
				Value:            75.0,
			},
		},
	}

	collector := New(cfg, mockStore, nil, nil, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	require.NoError(t, err)
	require.NotNil(t, data)

	// Verify metrics were collected
	assert.Len(t, data.Metrics, 2, "Should have 2 metrics")
	assert.Equal(t, "gpu", data.Metrics[0].Component)
	assert.Equal(t, "temperature", data.Metrics[0].Name)
}

func TestCollector_CollectMetrics_StoreError(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeMetrics:  true,
		MetricsLookback: metav1.Duration{Duration: 5 * time.Minute},
	}

	// Create mock metrics store that returns error
	mockStore := &mockMetricsStore{
		shouldError: true,
	}

	collector := New(cfg, mockStore, nil, nil, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	// Should still succeed (error is logged but doesn't fail collection)
	require.NoError(t, err)
	require.NotNil(t, data)

	// Metrics should be empty due to error
	assert.Empty(t, data.Metrics, "Metrics should be empty on error")
}

func TestCollector_CollectEvents_NoStoreOrRegistry(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeEvents:  true,
		EventsLookback: metav1.Duration{Duration: 5 * time.Minute},
	}

	collector := New(cfg, nil, nil, nil, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	// Should still succeed but events will not be collected
	require.NoError(t, err)
	require.NotNil(t, data)

	// Events should be empty
	assert.Empty(t, data.Events, "Events should be empty without store/registry")
}

func TestCollector_CollectEvents_WithComponents(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeEvents:  true,
		EventsLookback: metav1.Duration{Duration: 5 * time.Minute},
	}

	// Create mock registry with component
	mockComp := &mockComponent{
		name: "test-component",
		events: []apiv1.Event{
			{
				EventID:   "123e4567-e89b-12d3-a456-426614174000",
				Time:      metav1.Time{Time: time.Now()},
				Component: "test-component",
				Name:      "test-event",
				Type:      apiv1.EventTypeWarning,
				Message:   "Test warning",
				ExtraInfo: map[string]string{
					"xid_code": "79",
				},
			},
		},
	}
	mockRegistry := &mockRegistry{
		components: []components.Component{mockComp},
	}
	mockEventStore := &mockEventStore{}

	collector := New(cfg, nil, mockEventStore, mockRegistry, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	require.NoError(t, err)
	require.NotNil(t, data)

	// Verify events were collected
	assert.Len(t, data.Events, 1, "Should have 1 event")
	assert.Equal(t, "test-component", data.Events[0].Component)
	assert.Equal(t, "test-event", data.Events[0].Name)
	assert.Equal(t, map[string]string{"xid_code": "79"}, data.Events[0].ExtraInfo)
	assert.Equal(t, "123e4567-e89b-12d3-a456-426614174000", data.Events[0].EventID)
	_, err = uuid.Parse(data.Events[0].EventID)
	assert.NoError(t, err)
}

func TestCollector_CollectEvents_NoComponents(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeEvents:  true,
		EventsLookback: metav1.Duration{Duration: 5 * time.Minute},
	}

	// Create mock registry with no components
	mockRegistry := &mockRegistry{
		components: []components.Component{},
	}
	mockEventStore := &mockEventStore{}

	collector := New(cfg, nil, mockEventStore, mockRegistry, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	// Should still succeed
	require.NoError(t, err)
	require.NotNil(t, data)

	// Events should be empty
	assert.Empty(t, data.Events, "Events should be empty with no components")
}

func TestCollector_CollectEvents_ComponentError(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeEvents:  true,
		EventsLookback: metav1.Duration{Duration: 5 * time.Minute},
	}

	// Create mock component that returns error
	mockComp := &mockComponent{
		name:        "error-component",
		shouldError: true,
	}
	mockRegistry := &mockRegistry{
		components: []components.Component{mockComp},
	}
	mockEventStore := &mockEventStore{}

	collector := New(cfg, nil, mockEventStore, mockRegistry, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	// Should still succeed (component errors are logged but don't fail collection)
	require.NoError(t, err)
	require.NotNil(t, data)

	// Events should be empty due to error
	assert.Empty(t, data.Events, "Events should be empty when component errors")
}

func TestCollector_CollectComponentData_NoRegistry(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeComponentData: true,
	}

	collector := New(cfg, nil, nil, nil, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	// Should still succeed
	require.NoError(t, err)
	require.NotNil(t, data)

	// ComponentData should be empty
	assert.Empty(t, data.ComponentData, "ComponentData should be empty without registry")
}

func TestCollector_CollectComponentData_WithComponents(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeComponentData: true,
	}

	// Create mock component with health states
	mockComp := &mockComponent{
		name: "test-component",
		healthStates: []apiv1.HealthState{
			{
				Component: "test-component",
				Health:    "Healthy",
				Reason:    "All checks passed",
				Error:     "detailed error info",
				Time:      metav1.Time{Time: time.Now()},
				ExtraInfo: map[string]string{"key": "value"},
				SuggestedActions: &apiv1.SuggestedActions{
					Description:   "reboot the node",
					RepairActions: []apiv1.RepairActionType{apiv1.RepairActionTypeRebootSystem},
				},
				Incidents: []apiv1.HealthStateIncident{
					{
						EntityID: "GPU-1234",
						Message:  "Clock throttled",
						Health:   apiv1.HealthStateTypeDegraded,
						Error:    "DCGM_FR_CLOCK_THROTTLE_POWER",
					},
				},
			},
		},
	}
	mockRegistry := &mockRegistry{
		components: []components.Component{mockComp},
	}

	collector := New(cfg, nil, nil, mockRegistry, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	require.NoError(t, err)
	require.NotNil(t, data)

	// Verify component data was collected
	assert.Len(t, data.ComponentData, 1, "Should have 1 component data")
	compData, exists := data.ComponentData["test-component"]
	assert.True(t, exists, "Should have test-component data")

	dataMap := compData.(map[string]interface{})
	assert.Equal(t, "test-component", dataMap["component_name"])
	assert.Equal(t, "Healthy", dataMap["health"])
	assert.Equal(t, "All checks passed", dataMap["reason"])
	assert.Equal(t, "detailed error info", dataMap["error"])
	suggestedActions, ok := dataMap["suggested_actions"].(*apiv1.SuggestedActions)
	require.True(t, ok, "suggested_actions should preserve the typed SuggestedActions pointer")
	require.NotNil(t, suggestedActions)
	assert.Equal(t, "reboot the node", suggestedActions.Description)
	incidents, ok := dataMap["incidents"].([]apiv1.HealthStateIncident)
	require.True(t, ok, "incidents should preserve the typed health incidents slice")
	require.Len(t, incidents, 1)
	assert.Equal(t, "GPU-1234", incidents[0].EntityID)
}

func TestCollector_CollectComponentData_NoHealthStates(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeComponentData: true,
	}

	// Create mock component with no health states
	mockComp := &mockComponent{
		name:         "empty-component",
		healthStates: []apiv1.HealthState{},
	}
	mockRegistry := &mockRegistry{
		components: []components.Component{mockComp},
	}

	collector := New(cfg, nil, nil, mockRegistry, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	require.NoError(t, err)
	require.NotNil(t, data)

	// Verify component data with defaults
	assert.Len(t, data.ComponentData, 1, "Should have 1 component data")
	compData := data.ComponentData["empty-component"].(map[string]interface{})
	assert.Equal(t, "Unknown", compData["health"])
	assert.Equal(t, "No health data", compData["reason"])
	assert.Equal(t, "", compData["error"])
	assert.Nil(t, compData["suggested_actions"])
}

func TestCollector_AllFeaturesEnabled(t *testing.T) {
	ctx := context.Background()
	cfg := &config.HealthExporterConfig{
		IncludeMachineInfo:   true,
		IncludeMetrics:       true,
		IncludeEvents:        true,
		IncludeComponentData: true,
		MetricsLookback:      metav1.Duration{Duration: 5 * time.Minute},
		EventsLookback:       metav1.Duration{Duration: 5 * time.Minute},
	}

	// Create mocks for all features
	mockMetricsStore := &mockMetricsStore{
		metrics: pkgmetrics.Metrics{
			{Component: "gpu", Name: "temp", Value: 65.0, UnixMilliseconds: time.Now().UnixMilli()},
		},
	}

	mockComp := &mockComponent{
		name: "gpu",
		events: []apiv1.Event{
			{
				Time:    metav1.Time{Time: time.Now()},
				Name:    "test-event",
				Type:    apiv1.EventTypeInfo,
				Message: "Test",
			},
		},
		healthStates: []apiv1.HealthState{
			{Health: "Healthy", Reason: "OK", Time: metav1.Time{Time: time.Now()}},
		},
	}
	mockRegistry := &mockRegistry{
		components: []components.Component{mockComp},
	}

	mockEventStore := &mockEventStore{}

	collector := New(cfg, mockMetricsStore, mockEventStore, mockRegistry, "test-machine-id", nil)
	data, err := collector.Collect(ctx)

	require.NoError(t, err)
	require.NotNil(t, data)

	// Verify all data types are populated
	assert.NotEmpty(t, data.CollectionID)
	// MachineID may be empty in test environment
	t.Logf("MachineID: %s", data.MachineID)
	assert.False(t, data.Timestamp.IsZero())
	assert.Len(t, data.Metrics, 1)
	assert.Len(t, data.Events, 1)
	assert.Len(t, data.ComponentData, 1)
	// MachineInfo will be nil without NVML
}

// =============================================================================
// Mock Implementations
// =============================================================================

type mockMetricsStore struct {
	metrics     pkgmetrics.Metrics
	shouldError bool
}

func (m *mockMetricsStore) Read(ctx context.Context, opts ...pkgmetrics.OpOption) (pkgmetrics.Metrics, error) {
	if m.shouldError {
		return nil, errors.New("mock metrics store error")
	}
	return m.metrics, nil
}

func (m *mockMetricsStore) Purge(ctx context.Context, since time.Time) (int, error) {
	return 0, nil
}

func (m *mockMetricsStore) Record(ctx context.Context, metrics ...pkgmetrics.Metric) error {
	return nil
}

type mockRegistry struct {
	components []components.Component
}

func (m *mockRegistry) All() []components.Component {
	return m.components
}

func (m *mockRegistry) Deregister(name string) components.Component {
	return nil
}

func (m *mockRegistry) Get(name string) components.Component {
	for _, comp := range m.components {
		if comp.Name() == name {
			return comp
		}
	}
	return nil
}

func (m *mockRegistry) MustRegister(initFunc components.InitFunc) {
	// No-op for testing
}

func (m *mockRegistry) Register(initFunc components.InitFunc) (components.Component, error) {
	return nil, nil
}

type mockEventStore struct{}

// Implement eventstore.Store interface
func (m *mockEventStore) Close(ctx context.Context) error {
	return nil
}

func (m *mockEventStore) Bucket(name string, opts ...eventstore.OpOption) (eventstore.Bucket, error) {
	return nil, nil
}

type fakeMachineInfoProvider struct {
	info      *machineinfo.MachineInfo
	ok        bool
	refreshed bool
	waited    bool
}

func (f *fakeMachineInfoProvider) Get() (*machineinfo.MachineInfo, bool) {
	return f.info, f.ok
}

func (f *fakeMachineInfoProvider) RefreshAsync(parent context.Context) {
	f.refreshed = true
}

func (f *fakeMachineInfoProvider) WaitForInitialRefresh(ctx context.Context, maxWait time.Duration) bool {
	f.waited = true
	return f.ok
}

type mockComponent struct {
	name         string
	events       []apiv1.Event
	healthStates []apiv1.HealthState
	shouldError  bool
}

func (m *mockComponent) Name() string {
	return m.name
}

func (m *mockComponent) Events(ctx context.Context, since time.Time) (apiv1.Events, error) {
	if m.shouldError {
		return nil, errors.New("mock component error")
	}
	return apiv1.Events(m.events), nil
}

func (m *mockComponent) LastHealthStates() apiv1.HealthStates {
	return apiv1.HealthStates(m.healthStates)
}

// Implement other required interface methods as no-ops
func (m *mockComponent) Start() error                   { return nil }
func (m *mockComponent) Stop(ctx context.Context) error { return nil }
func (m *mockComponent) States(ctx context.Context) ([]any, error) {
	return nil, nil
}
func (m *mockComponent) Metrics(ctx context.Context, since time.Time) ([]pkgmetrics.Metric, error) {
	return nil, nil
}
func (m *mockComponent) Check() components.CheckResult {
	return nil
}

func (m *mockComponent) Close() error {
	return nil
}

func (m *mockComponent) IsSupported() bool {
	return true
}

func (m *mockComponent) Tags() []string {
	return []string{}
}
