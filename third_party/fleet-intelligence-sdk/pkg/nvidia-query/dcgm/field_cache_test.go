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
	"encoding/binary"
	"testing"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/stretchr/testify/assert"
)

// TestFieldValueCache_SetupFailureStoresError documents that setup errors
// are stored in lastError and can be retrieved via GetResult().
func TestFieldValueCache_SetupFailureStoresError(t *testing.T) {
	ctx := context.Background()

	// Create cache with nil instance (simulates DCGM unavailable)
	fc := NewFieldValueCache(ctx, nil, time.Second)

	// Attempt setup with no instance should return early (no error)
	err := fc.SetupFieldWatchingWithName("test-group")
	assert.NoError(t, err, "setup with nil instance should return nil")

	// GetResult should return an error because instance is nil
	result, err := fc.GetResult(nil)
	assert.Error(t, err, "GetResult with nil instance should return error")
	assert.Contains(t, err.Error(), "DCGM instance not available", "error should indicate DCGM unavailable")
	assert.Empty(t, result, "GetResult with nil instance should return empty result")
}

// TestFieldValueCache_GetResultReturnsLastError verifies that when lastError
// is set (e.g., from a setup failure), GetResult() returns that error.
func TestFieldValueCache_GetResultReturnsLastError(t *testing.T) {
	ctx := context.Background()
	fc := NewFieldValueCache(ctx, nil, time.Second)

	// Manually set lastError to simulate a setup failure
	fc.mu.Lock()
	fc.lastError = assert.AnError
	fc.mu.Unlock()

	// GetResult should return the stored error
	result, err := fc.GetResult(nil)
	assert.Error(t, err, "GetResult should return lastError")
	assert.Equal(t, assert.AnError, err, "GetResult should return the exact error stored")
	assert.Empty(t, result, "GetResult should return empty result when error is set")
}

// TestFieldValueCache_NoInstanceReturnsError documents behavior when
// instance is nil - it should return an error to components.
func TestFieldValueCache_NoInstanceReturnsError(t *testing.T) {
	ctx := context.Background()
	fc := NewFieldValueCache(ctx, nil, time.Second)

	result, err := fc.GetResult(nil)
	assert.Error(t, err, "GetResult with nil instance should return error")
	assert.Contains(t, err.Error(), "DCGM instance not available", "error should indicate DCGM unavailable")
	assert.Empty(t, result, "GetResult with nil instance should return empty result")
}

// TestFieldValueCache_SetupWithNoFields documents that setup with no
// registered fields returns early without error.
func TestFieldValueCache_SetupWithNoFields(t *testing.T) {
	ctx := context.Background()

	// Create a mock instance that returns DCGMExists() = true but no watched fields
	mockInstance := &mockDCGMInstance{dcgmExists: true}
	fc := NewFieldValueCache(ctx, mockInstance, time.Second)

	// Setup should return early because no fields are registered
	err := fc.SetupFieldWatchingWithName("test-group")
	assert.NoError(t, err, "setup with no fields should return nil")
}

func TestFieldValueCache_ResetAfterReconnectCallback(t *testing.T) {
	ctx := context.Background()
	mockInstance := &mockReconnectRegistrarInstance{
		mockDCGMInstance: mockDCGMInstance{dcgmExists: true},
	}

	fc := NewFieldValueCache(ctx, mockInstance, time.Second)
	assert.NotNil(t, mockInstance.callback, "reconnect callback should be registered")

	fc.mu.Lock()
	fc.lastError = assert.AnError
	fc.lastUpdate = time.Now()
	fc.values[1] = map[dcgm.Short]dcgm.FieldValue_v1{}
	fc.mu.Unlock()

	mockInstance.callback()

	fc.mu.RLock()
	defer fc.mu.RUnlock()
	assert.Nil(t, fc.lastError, "callback should clear cached errors")
	assert.True(t, fc.lastUpdate.IsZero(), "callback should reset last update timestamp")
	assert.Empty(t, fc.values, "callback should clear cached values")
}

func TestGetResultPreservingFieldErrorsRetainsSentinelPayload(t *testing.T) {
	const deviceID = uint(7)
	fieldValue := dcgm.FieldValue_v1{
		FieldID:   dcgm.DCGM_FI_DEV_PERSISTENCE_MODE,
		FieldType: dcgm.DCGM_FT_INT64,
		Status:    dcgm.DCGM_ST_GPU_IS_LOST,
	}
	// Failed DCGM fields commonly carry a sentinel payload. The regular result
	// path must keep filtering it, while the opt-in compatibility path retains
	// the status without requiring callers to decode this payload.
	sentinel := int64(dcgm.DCGM_FT_INT64_BLANK)
	binary.LittleEndian.PutUint64(fieldValue.Value[:8], uint64(sentinel))

	mockInstance := &mockDCGMInstance{
		dcgmExists: true,
		devices:    []DeviceInfo{{ID: deviceID, UUID: "GPU-7"}},
	}
	fc := NewFieldValueCache(context.Background(), mockInstance, time.Second)
	fc.values[deviceID] = map[dcgm.Short]dcgm.FieldValue_v1{
		dcgm.DCGM_FI_DEV_PERSISTENCE_MODE: fieldValue,
	}

	regular, err := fc.GetResult([]dcgm.Short{dcgm.DCGM_FI_DEV_PERSISTENCE_MODE})
	assert.NoError(t, err)
	assert.Empty(t, regular)

	preserved, err := fc.GetResultPreservingFieldErrors([]dcgm.Short{dcgm.DCGM_FI_DEV_PERSISTENCE_MODE})
	assert.NoError(t, err)
	if assert.Len(t, preserved, 1) && assert.Len(t, preserved[0].Values, 1) {
		assert.Equal(t, dcgm.DCGM_ST_GPU_IS_LOST, preserved[0].Values[0].Status)
	}
}

// mockDCGMInstance is a minimal mock for testing field cache behavior
type mockDCGMInstance struct {
	dcgmExists bool
	devices    []DeviceInfo
}

func (m *mockDCGMInstance) DCGMExists() bool {
	return m.dcgmExists
}

func (m *mockDCGMInstance) AddEntityToGroup(entityID uint) error {
	return nil
}

func (m *mockDCGMInstance) AddHealthWatch(system dcgm.HealthSystem) error {
	return nil
}

func (m *mockDCGMInstance) RemoveHealthWatch(system dcgm.HealthSystem) error {
	return nil
}

func (m *mockDCGMInstance) HealthCheck(system dcgm.HealthSystem) (dcgm.HealthResult, []dcgm.Incident, error) {
	var result dcgm.HealthResult
	return result, nil, nil
}

func (m *mockDCGMInstance) AddFieldsToWatch(fields []dcgm.Short) error {
	return nil
}

func (m *mockDCGMInstance) GetWatchedFields() []dcgm.Short {
	return nil // No fields registered
}

func (m *mockDCGMInstance) RemoveFieldsFromWatch(fields []dcgm.Short) error {
	return nil
}

func (m *mockDCGMInstance) GetLatestValuesForFields(deviceID uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
	return nil, nil
}

func (m *mockDCGMInstance) GetGroupHandle() dcgm.GroupHandle {
	return dcgm.GroupHandle{}
}

func (m *mockDCGMInstance) GetDevices() []DeviceInfo {
	return m.devices
}

func (m *mockDCGMInstance) Shutdown() error {
	return nil
}

type mockReconnectRegistrarInstance struct {
	mockDCGMInstance
	callback func()
}

func (m *mockReconnectRegistrarInstance) RegisterReconnectCallback(callback func()) {
	m.callback = callback
}
