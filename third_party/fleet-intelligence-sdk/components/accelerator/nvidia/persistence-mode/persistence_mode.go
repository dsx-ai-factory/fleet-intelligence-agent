// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package persistencemode

import (
	"errors"
	"fmt"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

// PersistenceMode is the persistence mode of the device.
// Implements "DCGM_FR_PERSISTENCE_MODE" in DCGM.
// ref. https://github.com/NVIDIA/DCGM/blob/903d745504f50153be8293f8566346f9de3b3c93/nvvs/plugin_src/software/Software.cpp#L526-L553
//
// Persistence mode controls whether the NVIDIA driver stays loaded when no active clients are connected to the GPU.
// ref. https://developer.nvidia.com/management-library-nvml
//
// Once all clients have closed the device file, the GPU state will be unloaded unless persistence mode is enabled.
// ref. https://docs.nvidia.com/deploy/driver-persistence/index.html
//
// NVIDIA Persistence Daemon provides a more robust implementation of persistence mode on Linux.
// ref. https://docs.nvidia.com/deploy/driver-persistence/index.html#usage
//
// To enable persistence mode, we need to check if "nvidia-persistenced" is running.
// Or run "nvidia-smi -pm 1" to enable persistence mode.
type PersistenceMode struct {
	UUID    string `json:"uuid"`
	BusID   string `json:"bus_id"`
	Enabled bool   `json:"enabled"`
	// Supported is true if the persistence mode is supported by the device.
	Supported bool `json:"supported"`
}

func getPersistenceModesFromDCGM(devices []nvidiadcgm.DeviceInfo, fieldCache *nvidiadcgm.FieldValueCache) ([]PersistenceMode, error) {
	if fieldCache == nil {
		return nil, errors.New("DCGM field cache is not configured")
	}
	// Unlike metric components, persistence mode historically attached recovery
	// guidance to per-device GPU-lost/reset-required query failures. Retain the
	// DCGM field statuses so we can preserve that behavior without reading their
	// potentially sentinel-valued payloads.
	results, err := fieldCache.GetResultPreservingFieldErrors([]dcgm.Short{dcgm.DCGM_FI_DEV_PERSISTENCE_MODE})
	if err != nil {
		return nil, err
	}
	return persistenceModesFromFieldValues(devices, results)
}

func persistenceModesFromFieldValues(devices []nvidiadcgm.DeviceInfo, results []nvidiadcgm.DeviceFieldValues) ([]PersistenceMode, error) {
	valuesByDevice := make(map[uint]dcgm.FieldValue_v1, len(results))
	for _, result := range results {
		for _, value := range result.Values {
			if value.FieldID == dcgm.DCGM_FI_DEV_PERSISTENCE_MODE {
				valuesByDevice[result.DeviceID] = value
			}
		}
	}

	modes := make([]PersistenceMode, 0, len(devices))
	for _, device := range devices {
		mode := PersistenceMode{UUID: device.UUID, BusID: device.BusID}
		value, ok := valuesByDevice[device.ID]
		if !ok {
			return nil, fmt.Errorf("DCGM persistence-mode field missing for device %d (%s)", device.ID, device.UUID)
		}
		switch value.Status {
		case dcgm.DCGM_ST_OK:
			mode.Supported = true
			mode.Enabled = value.Int64() != 0
		case dcgm.DCGM_ST_NOT_SUPPORTED:
			// The query succeeded, but this GPU does not support persistence mode.
		default:
			// Include the numeric code because the pinned go-dcgm API does not
			// expose a constructor for its typed error. The shared DCGM error
			// classifier supports this representation.
			return nil, fmt.Errorf("DCGM persistence-mode field failed for device %d (%s): error code %d", device.ID, device.UUID, value.Status)
		}
		modes = append(modes, mode)
	}
	return modes, nil
}
