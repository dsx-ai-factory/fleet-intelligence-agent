// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	nvmlmock "github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"github.com/stretchr/testify/require"

	nvmldevice "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml/device"
)

type fakeNVLinkSourceDevice struct {
	*nvmlmock.Device
	uuid              string
	fabricState       nvmldevice.FabricState
	fabricErr         error
	nvlinkStates      map[int]nvml.EnableState
	nvlinkReturns     map[int]nvml.Return
	capabilityValues  map[int]map[nvml.NvLinkCapability]uint32
	capabilityReturns map[int]map[nvml.NvLinkCapability]nvml.Return
	p2pStatuses       map[string]nvml.GpuP2PStatus
	p2pReturns        map[string]nvml.Return
	getFieldsReturn   nvml.Return
	linkCountReturn   nvml.Return
	linkCountType     nvml.ValueType
	linkCount         uint32
	speedReturn       nvml.Return
	speedType         nvml.ValueType
	speedMBytesPerS   uint32
	getFieldsCalls    int
}

func (f *fakeNVLinkSourceDevice) UUID() string {
	return f.uuid
}

func (f *fakeNVLinkSourceDevice) GetFabricState() (nvmldevice.FabricState, error) {
	if f.fabricErr != nil {
		return nvmldevice.FabricState{}, f.fabricErr
	}
	return f.fabricState, nil
}

func (f *fakeNVLinkSourceDevice) GetNvLinkState(link int) (nvml.EnableState, nvml.Return) {
	if ret, ok := f.nvlinkReturns[link]; ok {
		return f.nvlinkStates[link], ret
	}
	return nvml.FEATURE_ENABLED, nvml.ERROR_INVALID_ARGUMENT
}

func (f *fakeNVLinkSourceDevice) GetFieldValues(values []nvml.FieldValue) nvml.Return {
	f.getFieldsCalls++
	if f.getFieldsReturn != 0 && f.getFieldsReturn != nvml.SUCCESS {
		return f.getFieldsReturn
	}
	for i := range values {
		switch values[i].FieldId {
		case nvml.FI_DEV_NVLINK_LINK_COUNT:
			values[i] = fieldValue(values[i].FieldId, f.linkCount, f.linkCountType, f.linkCountReturn)
		case nvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
			values[i] = fieldValue(values[i].FieldId, f.speedMBytesPerS, f.speedType, f.speedReturn)
		}
	}
	return nvml.SUCCESS
}

func (f *fakeNVLinkSourceDevice) GetNvLinkCapability(link int, capability nvml.NvLinkCapability) (uint32, nvml.Return) {
	if returns, ok := f.capabilityReturns[link]; ok {
		if ret, ok := returns[capability]; ok {
			return f.capabilityValue(link, capability), ret
		}
	}
	return f.capabilityValue(link, capability), nvml.SUCCESS
}

func (f *fakeNVLinkSourceDevice) capabilityValue(link int, capability nvml.NvLinkCapability) uint32 {
	if values, ok := f.capabilityValues[link]; ok {
		if value, ok := values[capability]; ok {
			return value
		}
	}
	return 1
}

func (f *fakeNVLinkSourceDevice) GetP2PStatus(peer nvml.Device, p2pIndex nvml.GpuP2PCapsIndex) (nvml.GpuP2PStatus, nvml.Return) {
	peerUUID := ""
	if withUUID, ok := peer.(interface{ UUID() string }); ok {
		peerUUID = withUUID.UUID()
	}
	if ret, ok := f.p2pReturns[peerUUID]; ok {
		return f.p2pStatuses[peerUUID], ret
	}
	if status, ok := f.p2pStatuses[peerUUID]; ok {
		return status, nvml.SUCCESS
	}
	return nvml.P2P_STATUS_OK, nvml.SUCCESS
}

func fieldValue(fieldID uint32, value uint32, valueType nvml.ValueType, ret nvml.Return) nvml.FieldValue {
	if valueType == 0 {
		valueType = nvml.VALUE_TYPE_UNSIGNED_INT
	}
	var raw [8]byte
	binary.LittleEndian.PutUint32(raw[:4], value)
	return nvml.FieldValue{
		FieldId:    fieldID,
		ValueType:  uint32(valueType),
		NvmlReturn: uint32(ret),
		Value:      raw,
	}
}

func TestCollectNVLinkSourceMetricsRecordsExpectedSignals(t *testing.T) {
	dev := &fakeNVLinkSourceDevice{
		uuid:        "GPU-a",
		Device:      &nvmlmock.Device{},
		fabricState: nvmldevice.FabricState{HealthMask: 0x55, State: nvml.GPU_FABRIC_STATE_COMPLETED, Status: nvml.SUCCESS},
		nvlinkStates: map[int]nvml.EnableState{
			0: nvml.FEATURE_ENABLED,
			1: nvml.FEATURE_DISABLED,
			2: nvml.FEATURE_DISABLED,
		},
		nvlinkReturns: map[int]nvml.Return{
			0: nvml.SUCCESS,
			1: nvml.SUCCESS,
			2: nvml.SUCCESS,
			3: nvml.ERROR_INVALID_ARGUMENT,
		},
		linkCount:       18,
		speedMBytesPerS: 50000,
	}
	devices := map[string]*fakeNVLinkSourceDevice{"GPU-a": dev}

	metrics := collectNVLinkSourceMetrics(devices, map[string]string{"GPU-a": "3"})

	require.Len(t, metrics, 23)
	requireSourceMetric(t, metrics, SignalLinkProbeStatus, CollectionStatusCollected, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalLinkObservedMask, 0x3ffff, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalLinkPresentMask, 0x7, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalLinkEnabledMask, 0x1, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalLinkUnsupportedMask, 0, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalLinkErrorMask, 0, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalFabricHealthMask, 85, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalFabricHealthStatus, CollectionStatusCollected, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalFabricState, float64(nvml.GPU_FABRIC_STATE_COMPLETED), "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalFabricStatusCode, float64(nvml.SUCCESS), "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalLinkCount, 18, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalLinkCountStatus, CollectionStatusCollected, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalCommonSpeed, 50000, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalCommonSpeedStatus, CollectionStatusCollected, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalAllLinksSupportP2P, 1, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalAllLinksSupportSysmemAccess, 1, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalAllLinksSupportP2PAtomics, 1, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalAllLinksSupportSysmemAtomics, 1, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalAllLinksSupportSLI, 1, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalAllLinksSupportLink, 1, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalCapabilitiesStatus, CollectionStatusCollected, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalUnhealthyP2PPeerCount, 0, "GPU-a", "3")
	requireSourceMetric(t, metrics, SignalUnhealthyP2PPeerCountStatus, CollectionStatusCollected, "GPU-a", "3")
	require.Equal(t, 1, dev.getFieldsCalls)
}

func TestCollectNVLinkSourceMetricsRecordsUnsupportedAndErrorStatuses(t *testing.T) {
	devices := map[string]*fakeNVLinkSourceDevice{
		"GPU-b": {
			Device:          &nvmlmock.Device{},
			uuid:            "GPU-b",
			fabricErr:       errors.New("fabric state telemetry not supported"),
			nvlinkReturns:   map[int]nvml.Return{0: nvml.ERROR_NOT_SUPPORTED},
			getFieldsReturn: nvml.ERROR_UNKNOWN,
		},
	}

	metrics := collectNVLinkSourceMetrics(devices, nil)

	requireSourceMetric(t, metrics, SignalFabricHealthStatus, CollectionStatusUnsupported, "GPU-b", "")
	requireSourceMetric(t, metrics, SignalLinkProbeStatus, CollectionStatusUnsupported, "GPU-b", "")
	requireSourceMetric(t, metrics, SignalLinkUnsupportedMask, 1, "GPU-b", "")
	requireSourceMetric(t, metrics, SignalLinkCountStatus, CollectionStatusError, "GPU-b", "")
	requireSourceMetric(t, metrics, SignalCommonSpeedStatus, CollectionStatusError, "GPU-b", "")
	require.Equal(t, 2, countCollectionErrors(metrics))
}

func TestCollectNVLinkSourceMetricsMapsPerFieldFailures(t *testing.T) {
	devices := map[string]*fakeNVLinkSourceDevice{
		"GPU-c": {
			Device:      &nvmlmock.Device{},
			uuid:        "GPU-c",
			fabricState: nvmldevice.FabricState{HealthMask: 0, State: nvml.GPU_FABRIC_STATE_COMPLETED, Status: nvml.SUCCESS},
			nvlinkReturns: map[int]nvml.Return{
				0: nvml.SUCCESS,
			},
			linkCountReturn: nvml.ERROR_NOT_SUPPORTED,
			speedReturn:     nvml.SUCCESS,
			speedType:       nvml.VALUE_TYPE_UNSIGNED_LONG_LONG,
			speedMBytesPerS: 50000,
		},
	}

	metrics := collectNVLinkSourceMetrics(devices, nil)

	requireSourceMetric(t, metrics, SignalLinkCountStatus, CollectionStatusUnsupported, "GPU-c", "")
	requireSourceMetric(t, metrics, SignalCommonSpeedStatus, CollectionStatusError, "GPU-c", "")
	require.Equal(t, 1, countCollectionErrors(metrics))
}

func TestCollectNVLinkSourceMetricsRecordsCapabilityAndP2PFailures(t *testing.T) {
	devices := map[string]*fakeNVLinkSourceDevice{
		"GPU-a": {
			Device:      &nvmlmock.Device{},
			uuid:        "GPU-a",
			fabricState: nvmldevice.FabricState{State: nvml.GPU_FABRIC_STATE_COMPLETED, Status: nvml.SUCCESS},
			nvlinkStates: map[int]nvml.EnableState{
				0: nvml.FEATURE_ENABLED,
			},
			nvlinkReturns: map[int]nvml.Return{
				0: nvml.SUCCESS,
			},
			capabilityValues: map[int]map[nvml.NvLinkCapability]uint32{
				0: {
					nvml.NVLINK_CAP_P2P_SUPPORTED: 0,
				},
			},
			p2pStatuses: map[string]nvml.GpuP2PStatus{
				"GPU-b": nvml.P2P_STATUS_NOT_SUPPORTED,
			},
		},
		"GPU-b": {
			Device:      &nvmlmock.Device{},
			uuid:        "GPU-b",
			fabricState: nvmldevice.FabricState{State: nvml.GPU_FABRIC_STATE_COMPLETED, Status: nvml.SUCCESS},
			nvlinkReturns: map[int]nvml.Return{
				0: nvml.ERROR_INVALID_ARGUMENT,
			},
		},
	}

	metrics := collectNVLinkSourceMetrics(devices, nil)

	requireSourceMetric(t, metrics, SignalAllLinksSupportP2P, 0, "GPU-a", "")
	requireSourceMetric(t, metrics, SignalCapabilitiesStatus, CollectionStatusCollected, "GPU-a", "")
	requireSourceMetric(t, metrics, SignalUnhealthyP2PPeerCount, 1, "GPU-a", "")
	requireSourceMetric(t, metrics, SignalUnhealthyP2PPeerCountStatus, CollectionStatusCollected, "GPU-a", "")
}

func requireSourceMetric(t *testing.T, metrics []nvlinkSourceMetric, signal string, value float64, uuid string, gpu string) {
	t.Helper()
	for _, metric := range metrics {
		if metric.signal != signal {
			continue
		}
		require.Equal(t, value, metric.value)
		require.Equal(t, uuid, metric.uuid)
		require.Equal(t, gpu, metric.gpu)
		return
	}
	t.Fatalf("signal %s not found", signal)
}
