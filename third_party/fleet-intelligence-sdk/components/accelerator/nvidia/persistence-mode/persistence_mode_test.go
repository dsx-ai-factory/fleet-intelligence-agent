// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package persistencemode

import (
	"testing"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/stretchr/testify/assert"

	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

func TestPersistenceModesFromFieldValuesRequiresOKStatus(t *testing.T) {
	enabled := dcgm.FieldValue_v1{
		FieldID: dcgm.DCGM_FI_DEV_PERSISTENCE_MODE,
		Status:  dcgm.DCGM_ST_OK,
	}
	enabled.Value[0] = 1

	disabled := enabled
	disabled.Value[0] = 0

	failed := enabled
	failed.Status = dcgm.DCGM_ST_NVML_ERROR

	devices := []nvidiadcgm.DeviceInfo{
		{ID: 1, UUID: "GPU-enabled"},
		{ID: 2, UUID: "GPU-disabled"},
		{ID: 3, UUID: "GPU-failed"},
		{ID: 4, UUID: "GPU-missing"},
	}
	results := []nvidiadcgm.DeviceFieldValues{
		{DeviceID: 1, Values: []dcgm.FieldValue_v1{enabled}},
		{DeviceID: 2, Values: []dcgm.FieldValue_v1{disabled}},
		{DeviceID: 3, Values: []dcgm.FieldValue_v1{failed}},
	}

	modes, err := persistenceModesFromFieldValues(devices, results)
	assert.NoError(t, err)
	assert.Equal(t, []PersistenceMode{
		{UUID: "GPU-enabled", Supported: true, Enabled: true},
		{UUID: "GPU-disabled", Supported: true, Enabled: false},
		{UUID: "GPU-failed", Supported: false, Enabled: false},
		{UUID: "GPU-missing", Supported: false, Enabled: false},
	}, modes)
}

func TestPersistenceModesFromFieldValuesPreservesRecoveryStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		match  func(error) bool
	}{
		{name: "GPU lost", status: dcgm.DCGM_ST_GPU_IS_LOST, match: nvidiadcgm.IsGPULostError},
		{name: "reset required", status: dcgm.DCGM_ST_RESET_REQUIRED, match: nvidiadcgm.IsResetRequiredError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := dcgm.FieldValue_v1{
				FieldID: dcgm.DCGM_FI_DEV_PERSISTENCE_MODE,
				Status:  tt.status,
			}
			_, err := persistenceModesFromFieldValues(nil, []nvidiadcgm.DeviceFieldValues{{
				DeviceID: 3,
				UUID:     "GPU-3",
				Values:   []dcgm.FieldValue_v1{value},
			}})

			assert.Error(t, err)
			assert.True(t, tt.match(err))
			assert.Contains(t, err.Error(), "GPU-3")
		})
	}
}
