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
	"context"
	"errors"
	"fmt"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

// DeviceInfo stores cached GPU identity and inventory information.
// A connected instance populates it with one batched live field query and
// refreshes it whenever the reconnecting instance establishes a new session.
type DeviceInfo struct {
	ID                     uint
	UUID                   string
	BusID                  string
	Brand                  string
	Model                  string
	Serial                 string
	VBIOSVersion           string
	DriverVersion          string
	CUDADriverVersion      int64
	MinorNumber            int64
	ComputeCapability      int64
	FabricClusterUUID      string
	FabricCliqueID         uint32
	FabricCliqueIDValid    bool
	ChassisSerial          string
	FramebufferMemoryBytes uint64
}

var deviceInventoryFields = []dcgm.Short{
	dcgm.DCGM_FI_DEV_UUID,
	dcgm.DCGM_FI_DEV_PCI_BUSID,
	dcgm.DCGM_FI_DEV_BRAND,
	dcgm.DCGM_FI_DEV_NAME,
	dcgm.DCGM_FI_DEV_SERIAL,
	dcgm.DCGM_FI_DEV_VBIOS_VERSION,
	dcgm.DCGM_FI_DRIVER_VERSION,
	dcgm.DCGM_FI_CUDA_DRIVER_VERSION,
	dcgm.DCGM_FI_DEV_MINOR_NUMBER,
	dcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY,
	dcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID,
	dcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID,
	dcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER,
	dcgm.DCGM_FI_DEV_FB_TOTAL,
}

var getSupportedDevicesForInventory = dcgm.GetSupportedDevices
var getLatestInventoryValues = dcgm.EntitiesGetLatestValues

var errDeviceEnumeration = errors.New("enumerate supported DCGM devices")

type deviceInventoryResult struct {
	devices []DeviceInfo
	err     error
}

// CollectDeviceInventoryWithContext initializes a short-lived DCGM connection,
// collects one live device inventory snapshot, and releases the connection. It
// does not create a DCGM group, field group, or watch.
//
// A context deadline bounds how long the caller waits, but it cannot interrupt
// a native DCGM call already in progress. If the caller stops waiting, the
// goroutine still releases the DCGM connection when that call returns.
func CollectDeviceInventoryWithContext(ctx context.Context) ([]DeviceInfo, error) {
	resultCh := make(chan deviceInventoryResult, 1)
	go func() {
		cleanup, err := dcgmInitFunc(resolveInitFromEnv())
		if err != nil {
			resultCh <- deviceInventoryResult{err: fmt.Errorf("initialize DCGM for device inventory: %w", err)}
			return
		}

		devices, err := queryDeviceInventory()
		if cleanup != nil {
			cleanup()
		}
		resultCh <- deviceInventoryResult{devices: devices, err: err}
	}()

	select {
	case result := <-resultCh:
		return result.devices, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// queryDeviceInventory enumerates supported GPUs and reads all inventory
// fields in one request. No DCGM group, field group, or watch is required.
func queryDeviceInventory() ([]DeviceInfo, bool, error) {
	deviceIDs, err := getSupportedDevicesForInventory()
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", errDeviceEnumeration, err)
	}
	return queryDeviceInventoryFields(deviceIDs)
}

// queryDeviceInventoryFields enriches a fixed set of enumerated device IDs.
// It does not discover devices or change the established inventory membership.
func queryDeviceInventoryFields(deviceIDs []uint) ([]DeviceInfo, bool, error) {
	if len(deviceIDs) == 0 {
		return nil, true, nil
	}

	entities := make([]dcgm.GroupEntityPair, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		entities = append(entities, dcgm.GroupEntityPair{
			EntityGroupId: dcgm.FE_GPU,
			EntityId:      deviceID,
		})
	}

	values, err := getLatestInventoryValues(entities, deviceInventoryFields, dcgm.DCGM_FV_FLAG_LIVE_DATA)
	if err != nil {
		// GetSupportedDevices already established GPU presence. Preserve those
		// entities even when the batched query cannot enrich their inventory.
		devices, _ := deviceInventoryFromFieldValues(deviceIDs, nil)
		return devices, false, fmt.Errorf("get live DCGM inventory fields: %w", err)
	}
	devices, complete := deviceInventoryFromFieldValues(deviceIDs, values)
	return devices, complete, nil
}

func deviceInventoryFromFieldValues(deviceIDs []uint, values []dcgm.FieldValue_v2) ([]DeviceInfo, bool) {
	devices := make([]DeviceInfo, len(deviceIDs))
	deviceIndex := make(map[uint]int, len(deviceIDs))
	for index, deviceID := range deviceIDs {
		devices[index].ID = deviceID
		devices[index].MinorNumber = -1
		deviceIndex[deviceID] = index
	}

	complete := true
	for valueIndex := range values {
		value := &values[valueIndex]
		if value.EntityGroupId != dcgm.FE_GPU {
			continue
		}
		index, exists := deviceIndex[value.EntityID]
		if !exists {
			continue
		}
		if value.Status != dcgm.DCGM_ST_OK {
			log.Logger.Debugw("DCGM inventory field unavailable",
				"deviceID", value.EntityID,
				"fieldID", value.FieldID,
				"status", value.Status,
			)
			if value.Status == dcgm.DCGM_ST_NOT_SUPPORTED {
				// This field is permanently unavailable, so retrying cannot enrich it.
				continue
			}
			complete = false
			continue
		}
		if CheckSentinelV2(*value, "deviceID", value.EntityID) {
			continue
		}

		device := &devices[index]
		switch value.FieldID {
		case dcgm.DCGM_FI_DEV_UUID:
			device.UUID = inventoryString(value)
		case dcgm.DCGM_FI_DEV_PCI_BUSID:
			device.BusID = inventoryString(value)
		case dcgm.DCGM_FI_DEV_BRAND:
			device.Brand = inventoryString(value)
		case dcgm.DCGM_FI_DEV_NAME:
			device.Model = inventoryString(value)
		case dcgm.DCGM_FI_DEV_SERIAL:
			device.Serial = inventoryString(value)
		case dcgm.DCGM_FI_DEV_VBIOS_VERSION:
			device.VBIOSVersion = inventoryString(value)
		case dcgm.DCGM_FI_DRIVER_VERSION:
			device.DriverVersion = inventoryString(value)
		case dcgm.DCGM_FI_CUDA_DRIVER_VERSION:
			if cudaDriverVersion, ok := positiveInventoryInt64(value); ok {
				device.CUDADriverVersion = cudaDriverVersion
			}
		case dcgm.DCGM_FI_DEV_MINOR_NUMBER:
			if minorNumber, ok := inventoryInt64(value); ok && minorNumber >= 0 {
				device.MinorNumber = minorNumber
			}
		case dcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY:
			if computeCapability, ok := positiveInventoryInt64(value); ok {
				device.ComputeCapability = computeCapability
			}
		case dcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID:
			device.FabricClusterUUID = inventoryString(value)
		case dcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID:
			if cliqueID, ok := inventoryInt64(value); ok && cliqueID >= 0 && uint64(cliqueID) <= uint64(^uint32(0)) {
				device.FabricCliqueID = uint32(cliqueID)
				device.FabricCliqueIDValid = true
			}
		case dcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER:
			device.ChassisSerial = inventoryString(value)
		case dcgm.DCGM_FI_DEV_FB_TOTAL:
			if framebufferTotal, ok := inventoryInt64(value); ok {
				device.FramebufferMemoryBytes = framebufferMemoryBytes(framebufferTotal)
			}
		}
	}

	return devices, complete
}

func inventoryString(value *dcgm.FieldValue_v2) string {
	if value.FieldType != dcgm.DCGM_FT_STRING {
		return ""
	}
	return value.String()
}

func inventoryInt64(value *dcgm.FieldValue_v2) (int64, bool) {
	if value.FieldType != dcgm.DCGM_FT_INT64 {
		return 0, false
	}
	return value.Int64(), true
}

func positiveInventoryInt64(value *dcgm.FieldValue_v2) (int64, bool) {
	result, ok := inventoryInt64(value)
	if !ok || result <= 0 {
		return 0, false
	}
	return result, true
}

func framebufferMemoryBytes(totalMiB int64) uint64 {
	const bytesPerMiB uint64 = 1024 * 1024

	if totalMiB <= 0 {
		return 0
	}
	value := uint64(totalMiB)
	if value > ^uint64(0)/bytesPerMiB {
		return 0
	}
	return value * bytesPerMiB
}
