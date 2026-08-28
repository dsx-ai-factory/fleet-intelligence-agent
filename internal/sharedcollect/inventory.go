// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	nvidiaproduct "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia/product"

	"github.com/dsx-ai-factory/health-validation/collect/observation"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/inventory"
)

// GPUInventoryFromObservations reconstructs FI's existing GPU inventory model
// from canonical observations, grouped by GPU UUID.
func GPUInventoryFromObservations(observations []*observation.Observation) (inventory.GPUInfo, []*observation.Observation, []error) {
	selected := make(map[string]map[observation.SignalID]*observation.Observation)
	collectionErrors := make([]*observation.Observation, 0)
	var projectionErrors []error

	for _, current := range observations {
		if current == nil || !isInventorySignal(current.GetSignalId()) {
			continue
		}
		if current.GetCollectionError() != nil {
			collectionErrors = append(collectionErrors, current)
			continue
		}
		entity := current.GetEntity()
		if entity == nil || entity.GetType() != "gpu" || entity.GetId() == "" {
			projectionErrors = append(projectionErrors, fmt.Errorf(
				"project inventory signal %q: GPU entity is required",
				current.GetSignalId(),
			))
			continue
		}
		if current.GetValue() == nil {
			projectionErrors = append(projectionErrors, fmt.Errorf(
				"project inventory signal %q for GPU %q: value is required",
				current.GetSignalId(), entity.GetId(),
			))
			continue
		}
		bySignal := selected[entity.GetId()]
		if bySignal == nil {
			bySignal = make(map[observation.SignalID]*observation.Observation)
			selected[entity.GetId()] = bySignal
		}
		if _, exists := bySignal[current.GetSignalId()]; !exists {
			bySignal[current.GetSignalId()] = current
		}
	}

	uuids := make([]string, 0, len(selected))
	for uuid := range selected {
		uuids = append(uuids, uuid)
	}
	sort.Slice(uuids, func(i, j int) bool {
		left := selectedGPUIndex(selected[uuids[i]])
		right := selectedGPUIndex(selected[uuids[j]])
		leftIndex, leftErr := strconv.Atoi(left)
		rightIndex, rightErr := strconv.Atoi(right)
		if leftErr == nil && rightErr != nil {
			return true
		}
		if leftErr != nil && rightErr == nil {
			return false
		}
		if leftErr == nil && rightErr == nil && leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		if left != right {
			return left < right
		}
		return uuids[i] < uuids[j]
	})

	result := inventory.GPUInfo{GPUs: make([]inventory.GPUDevice, 0, len(uuids))}
	for _, uuid := range uuids {
		device := inventory.GPUDevice{UUID: uuid}
		for signalID, current := range selected[uuid] {
			if err := applyInventoryObservation(&result, &device, signalID, current); err != nil {
				projectionErrors = append(projectionErrors, fmt.Errorf("project inventory signal %q for GPU %q: %w", signalID, uuid, err))
			}
		}
		result.GPUs = append(result.GPUs, device)
	}
	return result, collectionErrors, projectionErrors
}

func selectedGPUIndex(bySignal map[observation.SignalID]*observation.Observation) string {
	current := bySignal[observation.SignalGPUInventoryIndex]
	if current == nil {
		return ""
	}
	value, err := integerValue(current.GetValue())
	if err != nil {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func applyInventoryObservation(gpuInfo *inventory.GPUInfo, device *inventory.GPUDevice, signalID observation.SignalID, current *observation.Observation) error {
	switch signalID {
	case observation.SignalGPUInventoryIndex:
		value, err := integerValue(current.GetValue())
		if err != nil {
			return err
		}
		device.GPUIndex = strconv.FormatInt(value, 10)
	case observation.SignalGPUInventoryModel:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		if gpuInfo.Product == "" {
			gpuInfo.Product = nvidiaproduct.SanitizeProductName(value)
		}
	case observation.SignalGPUInventoryManufacturer:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		if gpuInfo.Manufacturer == "" {
			gpuInfo.Manufacturer = value
		}
	case observation.SignalGPUInventoryArchitecture:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		if gpuInfo.Architecture == "" {
			gpuInfo.Architecture = value
		}
	case observation.SignalGPUInventoryPCIBusID:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		device.BusID = value
	case observation.SignalGPUInventorySerialNumber:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		device.SN = value
	case observation.SignalGPUInventoryMinorNumber:
		value, err := integerValue(current.GetValue())
		if err != nil {
			return err
		}
		device.MinorID = strconv.FormatInt(value, 10)
	case observation.SignalGPUInventoryBoardID:
		value, err := integerValue(current.GetValue())
		if err != nil {
			return err
		}
		if value < 0 {
			return fmt.Errorf("board ID must not be negative")
		}
		device.BoardID = int(value)
	case observation.SignalGPUInventoryVBIOSVersion:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		device.VBIOSVersion = value
	case observation.SignalGPUInventoryChassisSerialNumber:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		device.ChassisSN = value
	case observation.SignalGPUFabricClusterUUID:
		value, err := stringValue(current.GetValue())
		if err != nil {
			return err
		}
		device.ClusterUUID = value
	case observation.SignalGPUFabricCliqueID:
		value, err := integerValue(current.GetValue())
		if err != nil {
			return err
		}
		if value < 0 || value > math.MaxUint32 {
			return fmt.Errorf("fabric clique ID is outside uint32 range")
		}
		cliqueID := uint32(value)
		device.CliqueID = &cliqueID
	case observation.SignalFramebufferTotal:
		if err := validateUnit(current); err != nil {
			return err
		}
		value, err := integerValue(current.GetValue())
		if err != nil {
			return err
		}
		if value < 0 {
			return fmt.Errorf("framebuffer total must not be negative")
		}
		if value > math.MaxInt64/(1024*1024) {
			return fmt.Errorf("framebuffer total overflows bytes")
		}
		if gpuInfo.Memory == "" && value > 0 {
			gpuInfo.Memory = strconv.FormatInt(value*1024*1024, 10)
		}
	}
	return nil
}

func isInventorySignal(signalID observation.SignalID) bool {
	if signalID == observation.SignalFramebufferTotal {
		return true
	}
	for _, candidate := range observation.GPUInventorySignalIDs {
		if signalID == candidate {
			return true
		}
	}
	return false
}

func dcgmInventorySignals() []string {
	signals := make([]string, 0, len(observation.NodeInventorySignalIDs)+len(observation.GPUInventorySignalIDs))
	signals = append(signals, observation.NodeInventorySignalIDs...)
	for _, signalID := range observation.GPUInventorySignalIDs {
		if signalID != observation.SignalGPUInventoryBoardID {
			signals = append(signals, signalID)
		}
	}
	return append(signals, observation.SignalFramebufferTotal)
}

func integerValue(value *observation.Value) (int64, error) {
	if value == nil {
		return 0, fmt.Errorf("integer value is required")
	}
	current, ok := value.GetKind().(*observation.Value_IntValue)
	if !ok {
		return 0, fmt.Errorf("integer value is required")
	}
	return current.IntValue, nil
}

func stringValue(value *observation.Value) (string, error) {
	if value == nil {
		return "", fmt.Errorf("string value is required")
	}
	current, ok := value.GetKind().(*observation.Value_StringValue)
	if !ok {
		return "", fmt.Errorf("string value is required")
	}
	return current.StringValue, nil
}
