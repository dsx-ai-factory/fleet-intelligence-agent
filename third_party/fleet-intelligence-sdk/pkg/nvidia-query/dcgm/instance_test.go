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

package dcgm

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

func TestResolveInitFromEnv(t *testing.T) {
	// Default: no address override -> TCP localhost.
	t.Setenv("DCGM_URL", "")
	t.Setenv("DCGM_URL_IS_UNIX_SOCKET", "")
	p := resolveInitFromEnv()
	if p.isUnixSocket != "0" || p.address != "localhost" {
		t.Fatalf("expected default tcp localhost, got isUnixSocket=%q address=%q", p.isUnixSocket, p.address)
	}

	// TCP when DCGM_URL is set to host:port
	t.Setenv("DCGM_URL", "dcgm.svc:5555")
	t.Setenv("DCGM_URL_IS_UNIX_SOCKET", "0")
	p = resolveInitFromEnv()
	if p.isUnixSocket != "0" || p.address != "dcgm.svc:5555" {
		t.Fatalf("expected tcp dcgm.svc:5555, got isUnixSocket=%q address=%q", p.isUnixSocket, p.address)
	}

	// DCGM_URL unix socket path with truthy flag.
	t.Setenv("DCGM_URL", "/run/dcgm/dcgm.sock")
	t.Setenv("DCGM_URL_IS_UNIX_SOCKET", "true")
	p = resolveInitFromEnv()
	if p.isUnixSocket != "1" || p.address != "/run/dcgm/dcgm.sock" {
		t.Fatalf("expected unix /run/dcgm/dcgm.sock, got isUnixSocket=%q address=%q", p.isUnixSocket, p.address)
	}

	// Invalid bool values default to tcp.
	t.Setenv("DCGM_URL", "dcgm.svc:5555")
	t.Setenv("DCGM_URL_IS_UNIX_SOCKET", "maybe")
	p = resolveInitFromEnv()
	if p.isUnixSocket != "0" {
		t.Fatalf("expected invalid bool to default to tcp, got isUnixSocket=%q address=%q", p.isUnixSocket, p.address)
	}
}

func TestNewConnectedInstanceCleansUpWhenGroupCreationFails(t *testing.T) {
	originalDCGMInitFunc := dcgmInitFunc
	originalDCGMNewDefaultGroupFunc := dcgmNewDefaultGroupFunc
	originalGetSupportedDevices := getSupportedDevicesForInventory
	defer func() {
		dcgmInitFunc = originalDCGMInitFunc
		dcgmNewDefaultGroupFunc = originalDCGMNewDefaultGroupFunc
		getSupportedDevicesForInventory = originalGetSupportedDevices
	}()

	cleanupCalled := false
	dcgmInitFunc = func(_ dcgmInitParams) (func(), error) {
		return func() {
			cleanupCalled = true
		}, nil
	}
	getSupportedDevicesForInventory = func() ([]uint, error) {
		return nil, nil
	}

	expectedErr := errors.New("invalid group name")
	dcgmNewDefaultGroupFunc = func(_ string) (dcgm.GroupHandle, error) {
		return dcgm.GroupHandle{}, expectedErr
	}

	inst, err := newConnectedInstance("invalid group")
	if err == nil {
		t.Fatalf("expected newConnectedInstance() to fail")
	}
	if inst != nil {
		t.Fatalf("expected nil instance on group creation failure")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if !cleanupCalled {
		t.Fatalf("expected DCGM cleanup to be called when group creation fails")
	}
}

func TestNewConnectedInstanceFailsWhenDeviceEnumerationFails(t *testing.T) {
	originalDCGMInitFunc := dcgmInitFunc
	originalDCGMNewDefaultGroupFunc := dcgmNewDefaultGroupFunc
	originalGetSupportedDevices := getSupportedDevicesForInventory
	t.Cleanup(func() {
		dcgmInitFunc = originalDCGMInitFunc
		dcgmNewDefaultGroupFunc = originalDCGMNewDefaultGroupFunc
		getSupportedDevicesForInventory = originalGetSupportedDevices
	})

	cleanupCalled := false
	dcgmInitFunc = func(_ dcgmInitParams) (func(), error) {
		return func() {
			cleanupCalled = true
		}, nil
	}
	expectedErr := errors.New("enumeration failed")
	getSupportedDevicesForInventory = func() ([]uint, error) {
		return nil, expectedErr
	}
	dcgmNewDefaultGroupFunc = func(string) (dcgm.GroupHandle, error) {
		t.Fatal("DCGM group created before device enumeration succeeded")
		return dcgm.GroupHandle{}, nil
	}

	inst, err := newConnectedInstance("inventory-enumeration-failure")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("newConnectedInstance() error = %v, want %v", err, expectedErr)
	}
	if inst != nil {
		t.Fatalf("newConnectedInstance() = %v, want nil", inst)
	}
	if !cleanupCalled {
		t.Fatal("expected DCGM cleanup after device enumeration failure")
	}
}

func TestNewInitializedInstanceReturnsNoOpOnDeviceEnumerationFailure(t *testing.T) {
	originalNewConnectedInstanceFunc := newConnectedInstanceFunc
	t.Cleanup(func() {
		newConnectedInstanceFunc = originalNewConnectedInstanceFunc
	})

	expectedErr := errors.New("enumeration failed")
	newConnectedInstanceFunc = func() (Instance, error) {
		return nil, errors.Join(errDeviceEnumeration, expectedErr)
	}

	inst, err := newInitializedInstance()
	if err != nil {
		t.Fatalf("newInitializedInstance() error = %v, want nil", err)
	}
	if inst == nil || inst.DCGMExists() {
		t.Fatalf("newInitializedInstance() = %v, want no-op instance", inst)
	}
}

func TestNewInitializedInstanceWithGroupNameReturnsNoOpOnDeviceEnumerationFailure(t *testing.T) {
	originalNewConnectedInstanceWithGroupNameFunc := newConnectedInstanceWithGroupNameFunc
	t.Cleanup(func() {
		newConnectedInstanceWithGroupNameFunc = originalNewConnectedInstanceWithGroupNameFunc
	})

	expectedErr := errors.New("enumeration failed")
	newConnectedInstanceWithGroupNameFunc = func(string) (Instance, error) {
		return nil, errors.Join(errDeviceEnumeration, expectedErr)
	}

	inst, err := newInitializedInstanceWithGroupName("inventory-enumeration-failure")
	if err != nil {
		t.Fatalf("newInitializedInstanceWithGroupName() error = %v, want nil", err)
	}
	if inst == nil || inst.DCGMExists() {
		t.Fatalf("newInitializedInstanceWithGroupName() = %v, want no-op instance", inst)
	}
}

func TestInstanceRetriesOnlyIncompleteDeviceInventory(t *testing.T) {
	originalGetSupportedDevices := getSupportedDevicesForInventory
	originalGetLatestValues := getLatestInventoryValues
	t.Cleanup(func() {
		getSupportedDevicesForInventory = originalGetSupportedDevices
		getLatestInventoryValues = originalGetLatestValues
	})

	queryCount := 0
	getSupportedDevicesForInventory = func() ([]uint, error) {
		return []uint{3}, nil
	}
	getLatestInventoryValues = func([]dcgm.GroupEntityPair, []dcgm.Short, uint) ([]dcgm.FieldValue_v2, error) {
		queryCount++
		uuid := stringField(3, dcgm.DCGM_FI_DEV_UUID, "GPU-refreshed")
		if queryCount == 1 {
			uuid.Status = dcgm.DCGM_ST_NO_DATA
		}
		return []dcgm.FieldValue_v2{
			uuid,
		}, nil
	}

	devices, complete, err := queryDeviceInventory()
	if err != nil {
		t.Fatalf("queryDeviceInventory() error = %v", err)
	}
	if complete {
		t.Fatal("queryDeviceInventory() complete = true after DCGM_ST_NO_DATA, want false")
	}
	getSupportedDevicesForInventory = func() ([]uint, error) {
		t.Fatal("inventory enrichment retry re-enumerated devices")
		return nil, nil
	}

	inst := &instance{
		dcgmExists:        true,
		devices:           devices,
		inventoryEnriched: complete,
	}
	if err := inst.retryDeviceInventoryEnrichment(); err != nil {
		t.Fatalf("first retryDeviceInventoryEnrichment() error = %v", err)
	}
	if err := inst.retryDeviceInventoryEnrichment(); err != nil {
		t.Fatalf("second retryDeviceInventoryEnrichment() error = %v", err)
	}
	if queryCount != 2 {
		t.Fatalf("inventory query count = %d, want 2", queryCount)
	}

	want := []DeviceInfo{{ID: 3, UUID: "GPU-refreshed", MinorNumber: -1}}
	if got := inst.GetDevices(); !slices.Equal(got, want) {
		t.Fatalf("GetDevices() = %+v, want %+v", got, want)
	}
}

func TestInstanceRetainsPartialInventoryWhenRetryFails(t *testing.T) {
	originalGetSupportedDevices := getSupportedDevicesForInventory
	originalGetLatestValues := getLatestInventoryValues
	t.Cleanup(func() {
		getSupportedDevicesForInventory = originalGetSupportedDevices
		getLatestInventoryValues = originalGetLatestValues
	})

	expectedErr := errors.New("field query failed")
	getSupportedDevicesForInventory = func() ([]uint, error) {
		t.Fatal("inventory enrichment retry re-enumerated devices")
		return nil, nil
	}
	getLatestInventoryValues = func([]dcgm.GroupEntityPair, []dcgm.Short, uint) ([]dcgm.FieldValue_v2, error) {
		return nil, expectedErr
	}

	want := []DeviceInfo{{ID: 3, MinorNumber: -1}}
	inst := &instance{dcgmExists: true, devices: slices.Clone(want)}
	if err := inst.retryDeviceInventoryEnrichment(); !errors.Is(err, expectedErr) {
		t.Fatalf("retryDeviceInventoryEnrichment() error = %v, want %v", err, expectedErr)
	}
	if got := inst.GetDevices(); !slices.Equal(got, want) {
		t.Fatalf("GetDevices() = %+v, want retained inventory %+v", got, want)
	}
}

func TestInstance(t *testing.T) {
	inst, err := New()
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}
	defer inst.Shutdown()

	if !inst.DCGMExists() {
		t.Logf("DCGM not available, skipping detailed tests")
		return
	}

	// Test health check with a system
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Logf("failed to add health watch: %v", err)
	} else {
		health, incidents, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
		if err != nil {
			t.Logf("health check failed: %v", err)
		} else {
			t.Logf("health: %v, incidents: %d", health, len(incidents))
		}
	}
}

func TestNoOpInstance(t *testing.T) {
	inst := NewNoOp()

	// Verify no-op behavior
	if inst.DCGMExists() {
		t.Errorf("no-op instance should return false for DCGMExists()")
	}

	// Test HealthCheck returns PASS with no error (graceful degradation)
	health, incidents, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Errorf("no-op instance should not return error for HealthCheck(): %v", err)
	}
	if health != dcgm.DCGM_HEALTH_RESULT_PASS {
		t.Errorf("no-op instance should return PASS (assume healthy), got %v", health)
	}
	if incidents != nil {
		t.Errorf("no-op instance should return nil incidents, got %v", incidents)
	}

	if err := inst.Shutdown(); err != nil {
		t.Errorf("no-op instance should not return error for Shutdown(): %v", err)
	}
}

func TestInstanceWhenDCGMNotAvailable(t *testing.T) {
	// When DCGM is not available, New() should return a no-op instance
	// without error
	inst, err := New()
	if err != nil {
		t.Fatalf("New() should not return error even when DCGM is not available: %v", err)
	}

	// The instance should be valid (either real or no-op)
	if inst == nil {
		t.Fatal("instance should not be nil")
	}

	// Should be safe to call methods on the instance
	_ = inst.DCGMExists()
	_ = inst.Shutdown()
}

func TestNewWithContextReturnsNoOpOnTimeout(t *testing.T) {
	originalNewInstanceFunc := newInstanceFunc
	originalNewConnectedInstanceFunc := newConnectedInstanceFunc
	defer func() {
		newInstanceFunc = originalNewInstanceFunc
		newConnectedInstanceFunc = originalNewConnectedInstanceFunc
	}()
	newConnectedInstanceFunc = func() (Instance, error) {
		return nil, errors.New("dcgm unavailable for reconnect test")
	}

	blocker := make(chan struct{})
	lateInstance := newShutdownTrackingInstance()
	newInstanceFunc = func() (Instance, error) {
		<-blocker
		return lateInstance, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	inst, err := NewWithContext(ctx)
	if err != nil {
		t.Fatalf("NewWithContext() returned error: %v", err)
	}
	defer inst.Shutdown()
	if inst == nil {
		t.Fatal("instance should not be nil")
	}
	if inst.DCGMExists() {
		t.Fatalf("expected no-op instance after timeout")
	}

	close(blocker)
	select {
	case <-lateInstance.shutdownCh:
	case <-time.After(time.Second):
		t.Fatal("late DCGM instance was not shut down")
	}
}

func TestNewOnceWithContextReturnsInitializedInstanceWithoutWrapping(t *testing.T) {
	originalNewInstanceFunc := newInstanceFunc
	defer func() {
		newInstanceFunc = originalNewInstanceFunc
	}()

	expected := newMockTrackingInstance()
	newInstanceFunc = func() (Instance, error) {
		return expected, nil
	}

	inst, err := NewOnceWithContextAndGroupName(context.Background(), defaultDCGMGroupName)
	if err != nil {
		t.Fatalf("NewOnceWithContextAndGroupName() returned error: %v", err)
	}
	if inst != expected {
		t.Fatalf("expected one-shot constructor to return the initialized instance directly, got %T", inst)
	}
}

func TestNewOnceWithContextReturnsNoOpAndCleansUpLateInstance(t *testing.T) {
	originalNewInstanceFunc := newInstanceFunc
	defer func() {
		newInstanceFunc = originalNewInstanceFunc
	}()

	blocker := make(chan struct{})
	lateInstance := newShutdownTrackingInstance()
	newInstanceFunc = func() (Instance, error) {
		<-blocker
		return lateInstance, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	inst, err := NewOnceWithContextAndGroupName(ctx, defaultDCGMGroupName)
	if err != nil {
		t.Fatalf("NewOnceWithContextAndGroupName() returned error: %v", err)
	}
	if inst == nil || inst.DCGMExists() {
		t.Fatalf("expected no-op instance after timeout")
	}

	close(blocker)
	select {
	case <-lateInstance.shutdownCh:
	case <-time.After(time.Second):
		t.Fatal("late one-shot DCGM instance was not shut down")
	}
}

func TestReconnectingInstanceReplaysDeferredState(t *testing.T) {
	originalNewConnectedInstanceFunc := newConnectedInstanceFunc
	defer func() {
		newConnectedInstanceFunc = originalNewConnectedInstanceFunc
	}()

	mock := newMockTrackingInstance()
	newConnectedInstanceFunc = func() (Instance, error) {
		return mock, nil
	}

	reconnectingInst := newReconnectingInstance(NewNoOp(), time.Hour)
	defer reconnectingInst.Shutdown()

	if err := reconnectingInst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE); err != nil {
		t.Fatalf("AddHealthWatch() failed: %v", err)
	}
	fields := []dcgm.Short{dcgm.DCGM_FI_DEV_FB_FREE, dcgm.DCGM_FI_DEV_FB_USED}
	if err := reconnectingInst.AddFieldsToWatch(fields); err != nil {
		t.Fatalf("AddFieldsToWatch() failed: %v", err)
	}
	if err := reconnectingInst.AddEntityToGroup(3); err != nil {
		t.Fatalf("AddEntityToGroup() failed: %v", err)
	}

	internalInst := reconnectingInst.(*reconnectingInstance)
	if err := internalInst.reconnectNow(); err != nil {
		t.Fatalf("reconnectNow() failed: %v", err)
	}

	if !reconnectingInst.DCGMExists() {
		t.Fatalf("expected reconnecting instance to report DCGM available")
	}
	if mock.watchedSystems != dcgm.DCGM_HEALTH_WATCH_PCIE {
		t.Fatalf("expected watched systems 0x%x, got 0x%x", dcgm.DCGM_HEALTH_WATCH_PCIE, mock.watchedSystems)
	}
	if _, ok := mock.entities[3]; !ok {
		t.Fatalf("expected entity 3 to be replayed to connected instance")
	}
	if len(mock.watchedFields) != len(fields) {
		t.Fatalf("expected %d watched fields, got %d", len(fields), len(mock.watchedFields))
	}
	for _, field := range fields {
		if _, ok := mock.watchedFields[field]; !ok {
			t.Fatalf("expected watched field %d to be replayed", field)
		}
	}
}

func TestReconnectingInstanceRetriesDeviceEnumerationFailure(t *testing.T) {
	originalNewConnectedInstanceFunc := newConnectedInstanceFunc
	t.Cleanup(func() {
		newConnectedInstanceFunc = originalNewConnectedInstanceFunc
	})

	expectedErr := errors.New("enumeration failed")
	wantDevices := []DeviceInfo{{ID: 3, UUID: "GPU-reconnected"}}
	connected := newMockTrackingInstance()
	connected.devices = wantDevices
	attempts := 0
	newConnectedInstanceFunc = func() (Instance, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.Join(errDeviceEnumeration, expectedErr)
		}
		return connected, nil
	}

	inst := &reconnectingInstance{
		current:           NewNoOp(),
		watchedFields:     make(map[dcgm.Short]struct{}),
		groupEntities:     make(map[uint]struct{}),
		reconnectInterval: time.Hour,
		stopCh:            make(chan struct{}),
	}
	defer inst.Shutdown()

	if err := inst.reconnectNow(); !errors.Is(err, expectedErr) {
		t.Fatalf("first reconnectNow() error = %v, want %v", err, expectedErr)
	}
	if inst.DCGMExists() {
		t.Fatal("failed reconnect replaced the no-op instance")
	}

	if err := inst.reconnectNow(); err != nil {
		t.Fatalf("second reconnectNow() error = %v, want nil", err)
	}
	if !inst.DCGMExists() {
		t.Fatal("successful reconnect did not install the connected instance")
	}
	if got := inst.GetDevices(); !slices.Equal(got, wantDevices) {
		t.Fatalf("GetDevices() = %+v, want %+v", got, wantDevices)
	}
}

func TestReconnectingInstanceReturnsDeferredWatchedFields(t *testing.T) {
	reconnectingInst := newReconnectingInstance(NewNoOp(), time.Hour)
	defer reconnectingInst.Shutdown()

	fields := []dcgm.Short{dcgm.DCGM_FI_DEV_FB_FREE, dcgm.DCGM_FI_DEV_FB_USED}
	if err := reconnectingInst.AddFieldsToWatch(fields); err != nil {
		t.Fatalf("AddFieldsToWatch() failed: %v", err)
	}

	gotFields := reconnectingInst.GetWatchedFields()
	if len(gotFields) != len(fields) {
		t.Fatalf("expected %d watched fields, got %d", len(fields), len(gotFields))
	}

	gotSet := make(map[dcgm.Short]struct{}, len(gotFields))
	for _, field := range gotFields {
		gotSet[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := gotSet[field]; !ok {
			t.Fatalf("expected watched field %d in deferred state", field)
		}
	}
}

func TestReconnectingInstanceInvokesReconnectCallbacks(t *testing.T) {
	originalNewConnectedInstanceFunc := newConnectedInstanceFunc
	defer func() {
		newConnectedInstanceFunc = originalNewConnectedInstanceFunc
	}()

	mock := newMockTrackingInstance()
	newConnectedInstanceFunc = func() (Instance, error) {
		return mock, nil
	}

	reconnectingInst := newReconnectingInstance(NewNoOp(), time.Hour)
	defer reconnectingInst.Shutdown()

	internalInst := reconnectingInst.(*reconnectingInstance)
	callbackCount := 0
	internalInst.RegisterReconnectCallback(func() {
		callbackCount++
	})

	if err := internalInst.reconnectNow(); err != nil {
		t.Fatalf("reconnectNow() failed: %v", err)
	}

	if callbackCount != 1 {
		t.Fatalf("expected reconnect callback to run once, got %d", callbackCount)
	}
}

func TestReconnectingInstanceDoesNotDropRegistrationsDuringReplay(t *testing.T) {
	originalNewConnectedInstanceFunc := newConnectedInstanceFunc
	defer func() {
		newConnectedInstanceFunc = originalNewConnectedInstanceFunc
	}()

	mock := newBlockingReplayMockInstance()
	newConnectedInstanceFunc = func() (Instance, error) {
		return mock, nil
	}

	internalInst := &reconnectingInstance{
		current:           NewNoOp(),
		watchedFields:     make(map[dcgm.Short]struct{}),
		groupEntities:     make(map[uint]struct{}),
		reconnectInterval: time.Hour,
		stopCh:            make(chan struct{}),
	}
	defer internalInst.Shutdown()

	if err := internalInst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE); err != nil {
		t.Fatalf("AddHealthWatch(PCIE) failed: %v", err)
	}

	reconnectErrCh := make(chan error, 1)
	go func() {
		reconnectErrCh <- internalInst.reconnectNow()
	}()

	<-mock.firstAddHealthStarted

	addErrCh := make(chan error, 1)
	go func() {
		addErrCh <- internalInst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_THERMAL)
	}()

	select {
	case err := <-addErrCh:
		t.Fatalf("expected AddHealthWatch to wait for reconnect swap, got early return: %v", err)
	case <-time.After(50 * time.Millisecond):
		// expected: blocked until reconnect replay + swap complete
	}

	close(mock.unblockFirstAddHealth)

	if err := <-reconnectErrCh; err != nil {
		t.Fatalf("reconnectNow() failed: %v", err)
	}
	if err := <-addErrCh; err != nil {
		t.Fatalf("AddHealthWatch(THERMAL) failed: %v", err)
	}

	expectedSystems := dcgm.DCGM_HEALTH_WATCH_PCIE | dcgm.DCGM_HEALTH_WATCH_THERMAL
	if mock.watchedSystems != expectedSystems {
		t.Fatalf("expected watched systems 0x%x after replay window, got 0x%x", expectedSystems, mock.watchedSystems)
	}
}

func TestReconnectingInstanceAbortsInFlightReconnectOnShutdown(t *testing.T) {
	originalNewConnectedInstanceFunc := newConnectedInstanceFunc
	defer func() {
		newConnectedInstanceFunc = originalNewConnectedInstanceFunc
	}()

	started := make(chan struct{})
	release := make(chan struct{})
	mock := newMockTrackingInstance()
	newConnectedInstanceFunc = func() (Instance, error) {
		close(started)
		<-release
		return mock, nil
	}

	internalInst := &reconnectingInstance{
		current:           NewNoOp(),
		watchedFields:     make(map[dcgm.Short]struct{}),
		groupEntities:     make(map[uint]struct{}),
		reconnectInterval: time.Hour,
		stopCh:            make(chan struct{}),
	}

	reconnectErrCh := make(chan error, 1)
	go func() {
		reconnectErrCh <- internalInst.reconnectNow()
	}()

	<-started
	shutdownErr := internalInst.Shutdown()
	if shutdownErr != nil {
		t.Fatalf("Shutdown() failed: %v", shutdownErr)
	}
	close(release)

	reconnectErr := <-reconnectErrCh
	if !errors.Is(reconnectErr, errReconnectAborted) {
		t.Fatalf("expected reconnect to abort with %v, got %v", errReconnectAborted, reconnectErr)
	}

	internalInst.currentMu.RLock()
	defer internalInst.currentMu.RUnlock()
	if internalInst.current != nil {
		t.Fatalf("expected current instance to remain nil after shutdown")
	}
}

func TestNewWithContextReturnsUnderlyingError(t *testing.T) {
	originalNewInstanceFunc := newInstanceFunc
	defer func() {
		newInstanceFunc = originalNewInstanceFunc
	}()

	expectedErr := errors.New("boom")
	newInstanceFunc = func() (Instance, error) {
		return nil, expectedErr
	}

	inst, err := NewWithContext(context.Background())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if inst != nil {
		t.Fatalf("expected nil instance on error")
	}
}

func TestAddHealthWatch(t *testing.T) {
	inst, err := New()
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}
	defer inst.Shutdown()

	if !inst.DCGMExists() {
		t.Skip("DCGM not available, skipping test")
	}

	// Test adding a single health watch
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Errorf("AddHealthWatch(PCIE) failed: %v", err)
	}

	// Test adding another health watch (should OR together)
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_THERMAL)
	if err != nil {
		t.Errorf("AddHealthWatch(THERMAL) failed: %v", err)
	}

	// Verify the systems are tracked
	realInst := inst.(*instance)
	realInst.watchedSystemsMu.Lock()
	watchedSystems := realInst.watchedSystems
	realInst.watchedSystemsMu.Unlock()

	expectedSystems := dcgm.DCGM_HEALTH_WATCH_PCIE | dcgm.DCGM_HEALTH_WATCH_THERMAL
	if watchedSystems != expectedSystems {
		t.Errorf("expected watched systems to be 0x%x, got 0x%x", expectedSystems, watchedSystems)
	}
}

func TestRemoveHealthWatch(t *testing.T) {
	inst, err := New()
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}
	defer inst.Shutdown()

	if !inst.DCGMExists() {
		t.Skip("DCGM not available, skipping test")
	}

	// Add multiple health watches
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE | dcgm.DCGM_HEALTH_WATCH_THERMAL | dcgm.DCGM_HEALTH_WATCH_POWER)
	if err != nil {
		t.Fatalf("AddHealthWatch failed: %v", err)
	}

	// Remove one health watch
	err = inst.RemoveHealthWatch(dcgm.DCGM_HEALTH_WATCH_THERMAL)
	if err != nil {
		t.Errorf("RemoveHealthWatch(THERMAL) failed: %v", err)
	}

	// Verify the system was removed
	realInst := inst.(*instance)
	realInst.watchedSystemsMu.Lock()
	watchedSystems := realInst.watchedSystems
	realInst.watchedSystemsMu.Unlock()

	expectedSystems := dcgm.DCGM_HEALTH_WATCH_PCIE | dcgm.DCGM_HEALTH_WATCH_POWER
	if watchedSystems != expectedSystems {
		t.Errorf("expected watched systems to be 0x%x after removal, got 0x%x", expectedSystems, watchedSystems)
	}

	// Remove all remaining watches
	err = inst.RemoveHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE | dcgm.DCGM_HEALTH_WATCH_POWER)
	if err != nil {
		t.Errorf("RemoveHealthWatch failed: %v", err)
	}

	// Verify all systems removed
	realInst.watchedSystemsMu.Lock()
	watchedSystems = realInst.watchedSystems
	realInst.watchedSystemsMu.Unlock()

	if watchedSystems != 0 {
		t.Errorf("expected all systems to be removed (0), got 0x%x", watchedSystems)
	}
}

func TestHealthCheck(t *testing.T) {
	inst, err := New()
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}
	defer inst.Shutdown()

	if !inst.DCGMExists() {
		t.Skip("DCGM not available, skipping test")
	}

	// Add a health watch before checking
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Fatalf("AddHealthWatch failed: %v", err)
	}

	// Perform health check for PCIE system
	health, incidents, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Errorf("HealthCheck() failed: %v", err)
	}

	// Verify response is valid
	t.Logf("Health result: %v", health)
	t.Logf("Number of incidents: %d", len(incidents))
}

func TestHealthCheckCaching(t *testing.T) {
	// Note: Caching is now handled by DCGMHealthCache, not by the instance.
	// This test now verifies that direct HealthCheck calls work correctly.
	inst, err := New()
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}
	defer inst.Shutdown()

	if !inst.DCGMExists() {
		t.Skip("DCGM not available, skipping test")
	}

	// Add a health watch
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Fatalf("AddHealthWatch failed: %v", err)
	}

	// First call - should perform actual check and parse
	// Make multiple HealthCheck calls and verify they work correctly
	// Note: Each call now performs a fresh DCGM API call since caching is in DCGMHealthCache
	health1, incidents1, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Fatalf("first HealthCheck() failed: %v", err)
	}
	t.Logf("First call - Health: %v, incidents: %d", health1, len(incidents1))

	// Second call
	health2, incidents2, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Fatalf("second HealthCheck() failed: %v", err)
	}
	t.Logf("Second call - Health: %v, incidents: %d", health2, len(incidents2))

	// Third call
	health3, incidents3, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Fatalf("third HealthCheck() failed: %v", err)
	}
	t.Logf("Third call - Health: %v, incidents: %d", health3, len(incidents3))
}

func TestHealthCheckConcurrency(t *testing.T) {
	// Test concurrent HealthCheck calls
	inst, err := New()
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}
	defer inst.Shutdown()

	if !inst.DCGMExists() {
		t.Skip("DCGM not available, skipping test")
	}

	// Add a health watch
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Fatalf("AddHealthWatch failed: %v", err)
	}

	// Launch multiple goroutines calling HealthCheck simultaneously
	const numGoroutines = 10
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			_, _, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
			if err != nil {
				errChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		t.Errorf("concurrent HealthCheck() failed: %v", err)
	}

	t.Logf("Successfully performed %d concurrent health checks", numGoroutines)
}

func TestNoOpInstanceNewMethods(t *testing.T) {
	inst := NewNoOp()

	// Test AddHealthWatch is a no-op (returns nil, does nothing)
	err := inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Errorf("no-op instance AddHealthWatch should return nil (graceful no-op): %v", err)
	}

	// Test RemoveHealthWatch is a no-op (returns nil, does nothing)
	err = inst.RemoveHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Errorf("no-op instance RemoveHealthWatch should return nil (graceful no-op): %v", err)
	}

	// Test HealthCheck returns PASS with no error (DCGM unavailable = can't check = assume healthy)
	health, incidents, err := inst.HealthCheck(dcgm.DCGM_HEALTH_WATCH_PCIE)
	if err != nil {
		t.Errorf("no-op instance HealthCheck should not return error: %v", err)
	}
	if health != dcgm.DCGM_HEALTH_RESULT_PASS {
		t.Errorf("no-op instance should return PASS (assume healthy when can't check), got %v", health)
	}
	if incidents != nil {
		t.Errorf("no-op instance should return nil incidents, got %v", incidents)
	}
}

func TestHealthCheckMultipleSystems(t *testing.T) {
	inst, err := New()
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}
	defer inst.Shutdown()

	if !inst.DCGMExists() {
		t.Skip("DCGM not available, skipping test")
	}

	// Add multiple health watches
	err = inst.AddHealthWatch(dcgm.DCGM_HEALTH_WATCH_PCIE | dcgm.DCGM_HEALTH_WATCH_THERMAL | dcgm.DCGM_HEALTH_WATCH_POWER)
	if err != nil {
		t.Fatalf("AddHealthWatch failed: %v", err)
	}

	// Check each system - they should all share the same cached DCGM call
	// but get parsed results for their specific system
	systems := []struct {
		name   string
		system dcgm.HealthSystem
	}{
		{"PCIE", dcgm.DCGM_HEALTH_WATCH_PCIE},
		{"THERMAL", dcgm.DCGM_HEALTH_WATCH_THERMAL},
		{"POWER", dcgm.DCGM_HEALTH_WATCH_POWER},
	}

	for _, sys := range systems {
		health, incidents, err := inst.HealthCheck(sys.system)
		if err != nil {
			t.Errorf("HealthCheck(%s) failed: %v", sys.name, err)
		}
		t.Logf("%s: health=%v, incidents=%d", sys.name, health, len(incidents))
	}
}

type mockTrackingInstance struct {
	watchedSystems dcgm.HealthSystem
	watchedFields  map[dcgm.Short]struct{}
	entities       map[uint]struct{}
	devices        []DeviceInfo
}

func newMockTrackingInstance() *mockTrackingInstance {
	return &mockTrackingInstance{
		watchedFields: make(map[dcgm.Short]struct{}),
		entities:      make(map[uint]struct{}),
	}
}

func (m *mockTrackingInstance) DCGMExists() bool { return true }
func (m *mockTrackingInstance) AddEntityToGroup(entityID uint) error {
	m.entities[entityID] = struct{}{}
	return nil
}
func (m *mockTrackingInstance) AddHealthWatch(system dcgm.HealthSystem) error {
	m.watchedSystems |= system
	return nil
}
func (m *mockTrackingInstance) RemoveHealthWatch(system dcgm.HealthSystem) error {
	m.watchedSystems &^= system
	return nil
}
func (m *mockTrackingInstance) HealthCheck(system dcgm.HealthSystem) (dcgm.HealthResult, []dcgm.Incident, error) {
	return dcgm.DCGM_HEALTH_RESULT_PASS, nil, nil
}
func (m *mockTrackingInstance) AddFieldsToWatch(fields []dcgm.Short) error {
	for _, field := range fields {
		m.watchedFields[field] = struct{}{}
	}
	return nil
}
func (m *mockTrackingInstance) GetWatchedFields() []dcgm.Short {
	fields := make([]dcgm.Short, 0, len(m.watchedFields))
	for field := range m.watchedFields {
		fields = append(fields, field)
	}
	return fields
}
func (m *mockTrackingInstance) RemoveFieldsFromWatch(fields []dcgm.Short) error {
	for _, field := range fields {
		delete(m.watchedFields, field)
	}
	return nil
}
func (m *mockTrackingInstance) GetLatestValuesForFields(deviceID uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
	return nil, nil
}
func (m *mockTrackingInstance) GetGroupHandle() dcgm.GroupHandle { return dcgm.GroupHandle{} }
func (m *mockTrackingInstance) GetDevices() []DeviceInfo         { return slices.Clone(m.devices) }
func (m *mockTrackingInstance) Shutdown() error                  { return nil }

type shutdownTrackingInstance struct {
	*mockTrackingInstance
	shutdownOnce sync.Once
	shutdownCh   chan struct{}
}

func newShutdownTrackingInstance() *shutdownTrackingInstance {
	return &shutdownTrackingInstance{
		mockTrackingInstance: newMockTrackingInstance(),
		shutdownCh:           make(chan struct{}),
	}
}

func (m *shutdownTrackingInstance) Shutdown() error {
	m.shutdownOnce.Do(func() {
		close(m.shutdownCh)
	})
	return nil
}

type blockingReplayMockInstance struct {
	*mockTrackingInstance
	firstAddHealthStarted chan struct{}
	unblockFirstAddHealth chan struct{}
	blockFirstAddHealth   sync.Once
}

func newBlockingReplayMockInstance() *blockingReplayMockInstance {
	return &blockingReplayMockInstance{
		mockTrackingInstance:  newMockTrackingInstance(),
		firstAddHealthStarted: make(chan struct{}),
		unblockFirstAddHealth: make(chan struct{}),
	}
}

func (m *blockingReplayMockInstance) AddHealthWatch(system dcgm.HealthSystem) error {
	m.blockFirstAddHealth.Do(func() {
		close(m.firstAddHealthStarted)
		<-m.unblockFirstAddHealth
	})
	return m.mockTrackingInstance.AddHealthWatch(system)
}
