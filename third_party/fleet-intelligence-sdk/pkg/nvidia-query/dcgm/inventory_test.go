// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

func TestQueryDeviceInventoryUsesOneLiveBatch(t *testing.T) {
	originalGetSupportedDevices := getSupportedDevicesForInventory
	originalGetLatestValues := getLatestInventoryValues
	t.Cleanup(func() {
		getSupportedDevicesForInventory = originalGetSupportedDevices
		getLatestInventoryValues = originalGetLatestValues
	})

	getSupportedDevicesForInventory = func() ([]uint, error) {
		return []uint{3, 7}, nil
	}
	queryCount := 0
	getLatestInventoryValues = func(entities []dcgm.GroupEntityPair, fields []dcgm.Short, flags uint) ([]dcgm.FieldValue_v2, error) {
		queryCount++
		wantEntities := []dcgm.GroupEntityPair{
			{EntityGroupId: dcgm.FE_GPU, EntityId: 3},
			{EntityGroupId: dcgm.FE_GPU, EntityId: 7},
		}
		if !slices.Equal(entities, wantEntities) {
			t.Fatalf("entities = %v, want %v", entities, wantEntities)
		}
		if !slices.Equal(fields, deviceInventoryFields) {
			t.Fatalf("fields = %v, want %v", fields, deviceInventoryFields)
		}
		if flags != dcgm.DCGM_FV_FLAG_LIVE_DATA {
			t.Fatalf("flags = %v, want DCGM_FV_FLAG_LIVE_DATA", flags)
		}
		return []dcgm.FieldValue_v2{
			stringField(7, dcgm.DCGM_FI_DEV_UUID, "GPU-7"),
			stringField(3, dcgm.DCGM_FI_DEV_UUID, "GPU-3"),
			stringField(3, dcgm.DCGM_FI_DEV_PCI_BUSID, "0000:17:00.0"),
			stringField(3, dcgm.DCGM_FI_DEV_BRAND, "NVIDIA"),
			stringField(3, dcgm.DCGM_FI_DEV_NAME, "H100"),
			stringField(3, dcgm.DCGM_FI_DEV_SERIAL, "serial-3"),
			stringField(3, dcgm.DCGM_FI_DEV_VBIOS_VERSION, "96.00.5E.00.01"),
			stringField(3, dcgm.DCGM_FI_DRIVER_VERSION, "570.86.15"),
			int64Field(3, dcgm.DCGM_FI_CUDA_DRIVER_VERSION, 12080),
			int64Field(3, dcgm.DCGM_FI_DEV_MINOR_NUMBER, 5),
			int64Field(3, dcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY, 9<<16),
			stringField(3, dcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID, "cluster-3"),
			int64Field(3, dcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID, 11),
			stringField(3, dcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER, "chassis-3"),
			int64Field(3, dcgm.DCGM_FI_DEV_FB_TOTAL, 80*1024),
		}, nil
	}

	devices, complete, err := queryDeviceInventory()
	if err != nil {
		t.Fatalf("queryDeviceInventory() error = %v", err)
	}
	if !complete {
		t.Fatal("queryDeviceInventory() complete = false, want true")
	}
	if queryCount != 1 {
		t.Fatalf("live inventory query count = %d, want 1", queryCount)
	}
	want := []DeviceInfo{
		{
			ID:                     3,
			UUID:                   "GPU-3",
			BusID:                  "0000:17:00.0",
			Brand:                  "NVIDIA",
			Model:                  "H100",
			Serial:                 "serial-3",
			VBIOSVersion:           "96.00.5E.00.01",
			DriverVersion:          "570.86.15",
			CUDADriverVersion:      12080,
			MinorNumber:            5,
			ComputeCapability:      9 << 16,
			FabricClusterUUID:      "cluster-3",
			FabricCliqueID:         11,
			FabricCliqueIDValid:    true,
			ChassisSerial:          "chassis-3",
			FramebufferMemoryBytes: 80 * 1024 * 1024 * 1024,
		},
		{ID: 7, UUID: "GPU-7", MinorNumber: -1},
	}
	if !slices.Equal(devices, want) {
		t.Fatalf("devices = %+v, want %+v", devices, want)
	}
}

func TestQueryDeviceInventoryErrors(t *testing.T) {
	originalGetSupportedDevices := getSupportedDevicesForInventory
	originalGetLatestValues := getLatestInventoryValues
	t.Cleanup(func() {
		getSupportedDevicesForInventory = originalGetSupportedDevices
		getLatestInventoryValues = originalGetLatestValues
	})

	expected := errors.New("query failed")
	getSupportedDevicesForInventory = func() ([]uint, error) {
		return nil, expected
	}
	if _, _, err := queryDeviceInventory(); !errors.Is(err, expected) {
		t.Fatalf("queryDeviceInventory() error = %v, want %v", err, expected)
	}

	getSupportedDevicesForInventory = func() ([]uint, error) {
		return []uint{3, 7}, nil
	}
	getLatestInventoryValues = func([]dcgm.GroupEntityPair, []dcgm.Short, uint) ([]dcgm.FieldValue_v2, error) {
		return nil, expected
	}
	devices, complete, err := queryDeviceInventory()
	if !errors.Is(err, expected) {
		t.Fatalf("queryDeviceInventory() error = %v, want %v", err, expected)
	}
	if complete {
		t.Fatal("queryDeviceInventory() complete = true, want false")
	}
	want := []DeviceInfo{
		{ID: 3, MinorNumber: -1},
		{ID: 7, MinorNumber: -1},
	}
	if !slices.Equal(devices, want) {
		t.Fatalf("queryDeviceInventory() devices = %+v, want %+v", devices, want)
	}
}

func TestQueryDeviceInventoryDoesNotQueryFieldsWithoutDevices(t *testing.T) {
	originalGetSupportedDevices := getSupportedDevicesForInventory
	originalGetLatestValues := getLatestInventoryValues
	t.Cleanup(func() {
		getSupportedDevicesForInventory = originalGetSupportedDevices
		getLatestInventoryValues = originalGetLatestValues
	})

	getSupportedDevicesForInventory = func() ([]uint, error) {
		return nil, nil
	}
	getLatestInventoryValues = func([]dcgm.GroupEntityPair, []dcgm.Short, uint) ([]dcgm.FieldValue_v2, error) {
		t.Fatal("field query called without supported devices")
		return nil, nil
	}

	devices, complete, err := queryDeviceInventory()
	if err != nil {
		t.Fatalf("queryDeviceInventory() error = %v", err)
	}
	if !complete {
		t.Fatal("queryDeviceInventory() complete = false, want true")
	}
	if len(devices) != 0 {
		t.Fatalf("devices = %+v, want none", devices)
	}
}

func TestDeviceInventorySkipsUnavailableFieldsWithoutDroppingGPU(t *testing.T) {
	nonOK := stringField(1, dcgm.DCGM_FI_DEV_NAME, "invalid")
	nonOK.Status = dcgm.DCGM_ST_NOT_SUPPORTED
	blankMemory := int64Field(1, dcgm.DCGM_FI_DEV_FB_TOTAL, dcgm.DCGM_FT_INT64_BLANK)

	devices, complete := deviceInventoryFromFieldValues([]uint{1}, []dcgm.FieldValue_v2{
		stringField(1, dcgm.DCGM_FI_DEV_UUID, "GPU-1"),
		nonOK,
		blankMemory,
	})
	if !complete {
		t.Fatal("deviceInventoryFromFieldValues() complete = false for permanently unsupported field")
	}
	want := []DeviceInfo{{ID: 1, UUID: "GPU-1", MinorNumber: -1}}
	if !slices.Equal(devices, want) {
		t.Fatalf("devices = %+v, want %+v", devices, want)
	}
}

func TestDeviceInventoryDistinguishesZeroFromMismatchedNumericFields(t *testing.T) {
	devices, _ := deviceInventoryFromFieldValues([]uint{1, 2}, []dcgm.FieldValue_v2{
		stringField(1, dcgm.DCGM_FI_DEV_MINOR_NUMBER, "wrong type"),
		stringField(1, dcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID, "wrong type"),
		int64Field(2, dcgm.DCGM_FI_DEV_MINOR_NUMBER, 0),
		int64Field(2, dcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID, 0),
	})
	want := []DeviceInfo{
		{ID: 1, MinorNumber: -1},
		{ID: 2, MinorNumber: 0, FabricCliqueID: 0, FabricCliqueIDValid: true},
	}
	if !slices.Equal(devices, want) {
		t.Fatalf("devices = %+v, want %+v", devices, want)
	}
}

func TestFramebufferMemoryBytes(t *testing.T) {
	tests := []struct {
		name     string
		totalMiB int64
		want     uint64
	}{
		{name: "negative", totalMiB: -1, want: 0},
		{name: "zero", totalMiB: 0, want: 0},
		{name: "valid", totalMiB: 80 * 1024, want: 80 * 1024 * 1024 * 1024},
		{name: "conversion overflow", totalMiB: int64(^uint64(0)/(1024*1024) + 1), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := framebufferMemoryBytes(tt.totalMiB); got != tt.want {
				t.Fatalf("framebufferMemoryBytes(%d) = %d, want %d", tt.totalMiB, got, tt.want)
			}
		})
	}
}

func stringField(deviceID uint, fieldID dcgm.Short, value string) dcgm.FieldValue_v2 {
	field := dcgm.FieldValue_v2{
		EntityGroupId: dcgm.FE_GPU,
		EntityID:      deviceID,
		FieldID:       fieldID,
		FieldType:     dcgm.DCGM_FT_STRING,
		Status:        dcgm.DCGM_ST_OK,
	}
	copy(field.Value[:], value)
	return field
}

func int64Field(deviceID uint, fieldID dcgm.Short, value int64) dcgm.FieldValue_v2 {
	field := dcgm.FieldValue_v2{
		EntityGroupId: dcgm.FE_GPU,
		EntityID:      deviceID,
		FieldID:       fieldID,
		FieldType:     dcgm.DCGM_FT_INT64,
		Status:        dcgm.DCGM_ST_OK,
	}
	binary.NativeEndian.PutUint64(field.Value[:], uint64(value))
	return field
}
