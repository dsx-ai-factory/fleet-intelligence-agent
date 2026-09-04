// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package machineinfo

import (
	"fmt"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

func CreateGossipRequest(machineID string, devices []nvidiadcgm.DeviceInfo) (*apiv1.GossipRequest, error) {
	return createGossipRequest(machineID, devices, GetMachineInfo)
}

func createGossipRequest(
	machineID string,
	devices []nvidiadcgm.DeviceInfo,
	getMachineInfoFunc func([]nvidiadcgm.DeviceInfo) (*apiv1.MachineInfo, error),
) (*apiv1.GossipRequest, error) {
	req := &apiv1.GossipRequest{
		MachineID: machineID,
	}

	var err error
	req.MachineInfo, err = getMachineInfoFunc(devices)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine info: %w", err)
	}

	return req, nil
}
