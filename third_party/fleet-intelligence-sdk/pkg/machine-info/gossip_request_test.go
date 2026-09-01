// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package machineinfo

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

// TestCreateGossipRequestMocked tests the createGossipRequest function with mocked dependencies
func TestCreateGossipRequestMocked(t *testing.T) {
	// Setup
	machineID := "test-machine-id"
	gpuProvider := nvidiadcgm.NewNoOp()

	// Test cases for the private function
	tests := []struct {
		name               string
		getMachineInfoFunc func(gpuProvider nvidiadcgm.Instance, fieldCache *nvidiadcgm.FieldValueCache) (*apiv1.MachineInfo, error)
		wantError          bool
		expectedErrorMsg   string
	}{
		{
			name: "successful request creation",
			getMachineInfoFunc: func(gpuProvider nvidiadcgm.Instance, fieldCache *nvidiadcgm.FieldValueCache) (*apiv1.MachineInfo, error) {
				return &apiv1.MachineInfo{
					Hostname: "test-host",
					CPUInfo: &apiv1.MachineCPUInfo{
						Type: "test-cpu",
					},
				}, nil
			},
			wantError: false,
		},
		{
			name: "getMachineInfo returns error",
			getMachineInfoFunc: func(gpuProvider nvidiadcgm.Instance, fieldCache *nvidiadcgm.FieldValueCache) (*apiv1.MachineInfo, error) {
				return nil, errors.New("machine info error")
			},
			wantError:        true,
			expectedErrorMsg: "failed to get machine info: machine info error",
		},
	}

	// Run all test cases
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := createGossipRequest(machineID, gpuProvider, nil, tc.getMachineInfoFunc)

			if tc.wantError {
				assert.Error(t, err)
				assert.Nil(t, req)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, req)
				assert.Equal(t, machineID, req.MachineID)
				assert.NotNil(t, req.MachineInfo)
				assert.Equal(t, "test-host", req.MachineInfo.Hostname)
				assert.Equal(t, "test-cpu", req.MachineInfo.CPUInfo.Type)
			}
		})
	}
}
