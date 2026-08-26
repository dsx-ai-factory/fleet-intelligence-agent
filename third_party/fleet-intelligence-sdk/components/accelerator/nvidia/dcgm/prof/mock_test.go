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
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

// mockDCGMInstance is a minimal mock for testing prof component behavior
type mockDCGMInstance struct {
	dcgmExists bool
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

func (m *mockDCGMInstance) GetLatestValuesForFields(deviceID uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
	return nil, nil
}

func (m *mockDCGMInstance) GetGroupHandle() dcgm.GroupHandle {
	return dcgm.GroupHandle{}
}

func (m *mockDCGMInstance) GetDevices() []nvidiadcgm.DeviceInfo {
	// Return one device to trigger field setup logic
	return []nvidiadcgm.DeviceInfo{{ID: 0}}
}

func (m *mockDCGMInstance) Shutdown() error {
	return nil
}
