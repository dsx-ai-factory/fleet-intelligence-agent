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
	"slices"
	"strconv"
	"strings"
	"sync"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	dcgmendpoint "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm/endpoint"
)

// DeviceInfo stores cached GPU identity and inventory information.
// A connected instance populates it with a batched live field query. Failed
// enrichment is retried during field-cache polling for that session's fixed
// device membership.
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
	SlotNumber             uint32
	SlotNumberValid        bool
	TrayIndex              uint32
	TrayIndexValid         bool
	HostID                 uint32
	HostIDValid            bool
	ModuleID               uint32
	ModuleIDValid          bool
	PeerType               uint32
	PeerTypeValid          bool
	FabricGUID             string
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
	dcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SLOT_NUMBER,
	dcgm.DCGM_FI_DEV_PLATFORM_TRAY_INDEX,
	dcgm.DCGM_FI_DEV_PLATFORM_HOST_ID,
	dcgm.DCGM_FI_DEV_PLATFORM_MODULE_ID,
	dcgm.DCGM_FI_DEV_PLATFORM_PEER_TYPE,
	dcgm.DCGM_FI_DEV_PLATFORM_INFINIBAND_GUID,
	dcgm.DCGM_FI_DEV_FB_TOTAL,
}

var getSupportedDevicesForInventory = dcgm.GetSupportedDevices
var getLatestInventoryValues = dcgm.EntitiesGetLatestValues

type deviceInventoryEnricher interface {
	enrichDeviceInventoryIfIncomplete() error
}

// deviceInventory owns the fixed GPU membership and its best-known inventory
// data. Membership is established once when the DCGM session is created;
// incomplete identity fields can then be retried without re-enumerating GPUs.
type deviceInventory struct {
	refreshMu sync.Mutex
	mu        sync.RWMutex
	devices   []DeviceInfo
	complete  bool
}

func newDeviceInventory(devices []DeviceInfo, complete bool) *deviceInventory {
	return &deviceInventory{
		devices:  slices.Clone(devices),
		complete: complete,
	}
}

func (inventory *deviceInventory) snapshot() []DeviceInfo {
	if inventory == nil {
		return nil
	}

	inventory.mu.RLock()
	defer inventory.mu.RUnlock()
	return slices.Clone(inventory.devices)
}

// enrichIfIncomplete retries inventory fields for the membership established
// at session creation, retaining each recovered field even while others remain
// unavailable. A complete batch stops further live inventory queries.
func (inventory *deviceInventory) enrichIfIncomplete() error {
	if inventory == nil {
		return nil
	}

	// Poll can be called manually while its background loop is active. Allow
	// only one live inventory query at a time.
	inventory.refreshMu.Lock()
	defer inventory.refreshMu.Unlock()

	inventory.mu.RLock()
	if inventory.complete {
		inventory.mu.RUnlock()
		return nil
	}
	devices := slices.Clone(inventory.devices)
	deviceIDs := make([]uint, 0, len(devices))
	for _, device := range devices {
		deviceIDs = append(deviceIDs, device.ID)
	}
	inventory.mu.RUnlock()

	values, err := getDeviceInventoryFieldValues(deviceIDs)
	if err != nil {
		return err
	}
	complete := updateDeviceInventoryFromFieldValues(devices, values)

	inventory.mu.Lock()
	if !inventory.complete {
		inventory.devices = devices
		inventory.complete = complete
	}
	inventory.mu.Unlock()
	return nil
}

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
		var candidateErrors []error
		for _, candidate := range dcgmendpoint.ResolveFromEnv() {
			cleanup, err := dcgmInitFunc(candidate)
			if err != nil {
				candidateErrors = append(candidateErrors, fmt.Errorf("%s: %w", candidate.Address, err))
				continue
			}

			// One-shot callers retain partial inventory when individual fields are
			// unavailable; only long-lived instances use completeness to retry.
			devices, _, err := queryDeviceInventory()
			if cleanup != nil {
				cleanup()
			}
			if err != nil {
				candidateErrors = append(candidateErrors, fmt.Errorf("%s: %w", candidate.Address, err))
				continue
			}
			resultCh <- deviceInventoryResult{devices: devices}
			return
		}
		resultCh <- deviceInventoryResult{
			err: fmt.Errorf("collect device inventory from any DCGM HostEngine endpoint: %w", errors.Join(candidateErrors...)),
		}
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
	deviceIDs, err := enumerateDeviceInventory()
	if err != nil {
		return nil, false, err
	}
	return queryDeviceInventoryFields(deviceIDs)
}

func enumerateDeviceInventory() ([]uint, error) {
	deviceIDs, err := getSupportedDevicesForInventory()
	if err != nil {
		return nil, fmt.Errorf("enumerate supported DCGM devices: %w", err)
	}
	return deviceIDs, nil
}

// queryDeviceInventoryFields enriches a fixed set of enumerated device IDs.
// It does not discover devices or change the established inventory membership.
func queryDeviceInventoryFields(deviceIDs []uint) ([]DeviceInfo, bool, error) {
	if len(deviceIDs) == 0 {
		return nil, true, nil
	}

	values, err := getDeviceInventoryFieldValues(deviceIDs)
	devices, complete := deviceInventoryFromFieldValues(deviceIDs, values)
	if err != nil {
		// Enumeration established GPU presence even if enrichment failed.
		return devices, false, err
	}
	return devices, complete, nil
}

// getDeviceInventoryFieldValues reads one live batch for the fixed GPU membership.
func getDeviceInventoryFieldValues(deviceIDs []uint) ([]dcgm.FieldValue_v2, error) {
	if len(deviceIDs) == 0 {
		return nil, nil
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
		return nil, fmt.Errorf("get live DCGM inventory fields: %w", err)
	}
	return values, nil
}

func deviceInventoryFromFieldValues(deviceIDs []uint, values []dcgm.FieldValue_v2) ([]DeviceInfo, bool) {
	devices := make([]DeviceInfo, len(deviceIDs))
	for index, deviceID := range deviceIDs {
		devices[index].ID = deviceID
		devices[index].MinorNumber = -1
	}
	complete := updateDeviceInventoryFromFieldValues(devices, values)
	return devices, complete
}

// updateDeviceInventoryFromFieldValues applies valid observations to an owned
// snapshot. Missing, unsupported, and invalid fields retain their previous values.
func updateDeviceInventoryFromFieldValues(devices []DeviceInfo, values []dcgm.FieldValue_v2) bool {
	deviceIndex := make(map[uint]int, len(devices))
	for index, device := range devices {
		deviceIndex[device.ID] = index
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
		sentinelType, isSentinel := CheckSentinelV2Helper(*value)
		if isSentinel {
			CheckSentinelV2(*value, "deviceID", value.EntityID)
			if sentinelType.ShouldRetry() {
				complete = false
			}
			continue
		}

		device := &devices[index]
		switch value.FieldID {
		case dcgm.DCGM_FI_DEV_UUID:
			updateInventoryString(value, &device.UUID)
		case dcgm.DCGM_FI_DEV_PCI_BUSID:
			updateInventoryString(value, &device.BusID)
		case dcgm.DCGM_FI_DEV_BRAND:
			updateInventoryString(value, &device.Brand)
		case dcgm.DCGM_FI_DEV_NAME:
			updateInventoryString(value, &device.Model)
		case dcgm.DCGM_FI_DEV_SERIAL:
			updateInventoryString(value, &device.Serial)
		case dcgm.DCGM_FI_DEV_VBIOS_VERSION:
			updateInventoryString(value, &device.VBIOSVersion)
		case dcgm.DCGM_FI_DRIVER_VERSION:
			updateInventoryString(value, &device.DriverVersion)
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
			updateInventoryString(value, &device.FabricClusterUUID)
		case dcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID:
			if cliqueID, ok := inventoryInt64(value); ok && cliqueID >= 0 && uint64(cliqueID) <= uint64(^uint32(0)) {
				device.FabricCliqueID = uint32(cliqueID)
				device.FabricCliqueIDValid = true
			}
		case dcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER:
			updateInventoryString(value, &device.ChassisSerial)
		case dcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SLOT_NUMBER:
			if index, valid := inventoryPlatformValue(value); valid {
				device.SlotNumber, device.SlotNumberValid = index, true
			}
		case dcgm.DCGM_FI_DEV_PLATFORM_TRAY_INDEX:
			if index, valid := inventoryPlatformValue(value); valid {
				device.TrayIndex, device.TrayIndexValid = index, true
			}
		case dcgm.DCGM_FI_DEV_PLATFORM_HOST_ID:
			if index, valid := inventoryPlatformValue(value); valid {
				device.HostID, device.HostIDValid = index, true
			}
		case dcgm.DCGM_FI_DEV_PLATFORM_MODULE_ID:
			if index, valid := inventoryPlatformValue(value); valid {
				device.ModuleID, device.ModuleIDValid = index, true
			}
		case dcgm.DCGM_FI_DEV_PLATFORM_PEER_TYPE:
			if peerType, valid := inventoryPlatformValue(value); valid {
				device.PeerType, device.PeerTypeValid = peerType, true
			}
		case dcgm.DCGM_FI_DEV_PLATFORM_INFINIBAND_GUID:
			if guid := inventoryFabricGUID(value); guid != "" {
				device.FabricGUID = guid
			}
		case dcgm.DCGM_FI_DEV_FB_TOTAL:
			if framebufferTotal, ok := inventoryInt64(value); ok {
				if bytes := framebufferMemoryBytes(framebufferTotal); bytes > 0 {
					device.FramebufferMemoryBytes = bytes
				}
			}
		}
	}

	return complete
}

// updateInventoryString retains the last known value when a string is unavailable.
func updateInventoryString(value *dcgm.FieldValue_v2, target *string) {
	if text := inventoryString(value); text != "" {
		*target = text
	}
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

func inventoryPlatformValue(value *dcgm.FieldValue_v2) (uint32, bool) {
	index, ok := inventoryInt64(value)
	// NVML platform values are unsigned bytes; 0xff is an unavailable value.
	if !ok || index < 0 || index >= 0xff {
		return 0, false
	}
	return uint32(index), true
}

func inventoryFabricGUID(value *dcgm.FieldValue_v2) string {
	guid := strings.ToLower(strings.TrimSpace(inventoryString(value)))
	if !strings.HasPrefix(guid, "0x") || len(guid) < 3 || len(guid) > 18 {
		return ""
	}
	id, err := strconv.ParseUint(guid[2:], 16, 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("0x%016x", id)
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
