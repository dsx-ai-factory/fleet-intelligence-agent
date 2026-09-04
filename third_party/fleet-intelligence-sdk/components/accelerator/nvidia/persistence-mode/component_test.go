// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package persistencemode

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

type testGPUProvider struct {
	devices []nvidiadcgm.DeviceInfo
}

func (p testGPUProvider) GPUDevices() []nvidiadcgm.DeviceInfo { return p.devices }

func newTestComponent(devices []nvidiadcgm.DeviceInfo, modes []PersistenceMode, err error) *component {
	return &component{
		ctx:            context.Background(),
		cancel:         func() {},
		getTimeNowFunc: func() time.Time { return time.Unix(1, 0) },
		gpuProvider:    testGPUProvider{devices: devices},
		getPersistenceModesFunc: func() ([]PersistenceMode, error) {
			return modes, err
		},
	}
}

func TestCheckWithoutGPU(t *testing.T) {
	result := newTestComponent(nil, nil, nil).Check().(*checkResult)
	assert.Equal(t, apiv1.HealthStateTypeHealthy, result.health)
	assert.Equal(t, "GPU is not detected by DCGM", result.reason)
}

func TestCheckPersistenceModeDisabled(t *testing.T) {
	devices := []nvidiadcgm.DeviceInfo{
		{ID: 0, UUID: "GPU-1"},
		{ID: 1, UUID: "GPU-2"},
		{ID: 2, UUID: "GPU-3"},
	}
	modes := []PersistenceMode{
		{UUID: "GPU-1", Supported: true, Enabled: false},
		{UUID: "GPU-2", Supported: true, Enabled: true},
		{UUID: "GPU-3", Supported: true, Enabled: false},
	}
	result := newTestComponent(devices, modes, nil).Check().(*checkResult)
	assert.Equal(t, apiv1.HealthStateTypeDegraded, result.health)
	assert.Equal(t, "GPU-1, GPU-3: persistence mode supported but not enabled", result.reason)
}

func TestCheckPersistenceModeEnabled(t *testing.T) {
	devices := []nvidiadcgm.DeviceInfo{{ID: 0, UUID: "GPU-1"}, {ID: 1, UUID: "GPU-2"}}
	modes := []PersistenceMode{
		{UUID: "GPU-1", Supported: true, Enabled: true},
		{UUID: "GPU-2", Supported: false},
	}
	result := newTestComponent(devices, modes, nil).Check().(*checkResult)
	assert.Equal(t, apiv1.HealthStateTypeHealthy, result.health)
	assert.Equal(t, "all 1 supported GPU(s) were checked, no persistence mode issue found", result.reason)
}

func TestCheckPersistenceModeUnsupported(t *testing.T) {
	devices := []nvidiadcgm.DeviceInfo{{ID: 0, UUID: "GPU-1"}}
	modes := []PersistenceMode{{UUID: "GPU-1", Supported: false}}
	result := newTestComponent(devices, modes, nil).Check().(*checkResult)
	assert.Equal(t, apiv1.HealthStateTypeHealthy, result.health)
	assert.Equal(t, "persistence mode is unsupported on all 1 GPU(s)", result.reason)
}

func TestCheckDCGMError(t *testing.T) {
	devices := []nvidiadcgm.DeviceInfo{{ID: 0, UUID: "GPU-1"}}
	result := newTestComponent(devices, nil, errors.New("query failed")).Check().(*checkResult)
	assert.Equal(t, apiv1.HealthStateTypeDegraded, result.health)
	assert.Error(t, result.err)
	assert.Equal(t, "failed to get DCGM persistence-mode field: query failed", result.reason)
	assert.Nil(t, result.suggestedActions)
}

func TestCheckDCGMUnhealthyErrors(t *testing.T) {
	devices := []nvidiadcgm.DeviceInfo{{ID: 0, UUID: "GPU-1"}}
	tests := []struct {
		name   string
		code   int32
		reason string
	}{
		{
			name:   "GPU lost",
			code:   dcgm.DCGM_ST_GPU_IS_LOST,
			reason: gpuLostReason,
		},
		{
			name:   "reset required",
			code:   dcgm.DCGM_ST_RESET_REQUIRED,
			reason: gpuRequiresResetReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("DCGM field query failed: error code %d", tt.code)
			result := newTestComponent(devices, nil, err).Check().(*checkResult)

			assert.Equal(t, apiv1.HealthStateTypeUnhealthy, result.health)
			assert.Equal(t, tt.reason, result.reason)
			assert.Equal(t, rebootSuggestedActions(tt.reason), result.suggestedActions)
		})
	}
}

func TestIsSupported(t *testing.T) {
	assert.False(t, (&component{}).IsSupported())
	c := &component{gpuProvider: testGPUProvider{devices: []nvidiadcgm.DeviceInfo{{ID: 0, UUID: "GPU-1"}}}}
	assert.True(t, c.IsSupported())
}

func TestInjectFault(t *testing.T) {
	c := newTestComponent([]nvidiadcgm.DeviceInfo{{ID: 0, UUID: "GPU-1"}}, nil, nil)
	c.defaultGetPersistenceModesFunc = c.getPersistenceModesFunc
	c.InjectFault("injected")
	_, err := c.getPersistenceModesFunc()
	require.EqualError(t, err, "injected")
	c.ClearFault()
	_, err = c.getPersistenceModesFunc()
	require.NoError(t, err)
}

var _ components.GPUDeviceProvider = testGPUProvider{}
