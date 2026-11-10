// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package v1

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestMachineGPUInstancePlatformJSON(t *testing.T) {
	zero := uint32(0)
	slot, tray, host := uint32(26), uint32(16), uint32(1)
	for _, guid := range []string{"0xb74a5c8b016b6418", "0x0000000000000001"} {
		t.Run(guid, func(t *testing.T) {
			gpu := MachineGPUInstance{
				UUID: "GPU-123", SlotNumber: &slot, TrayIndex: &tray,
				HostID: &host, ModuleID: &zero, FabricGUID: guid,
			}
			data, err := json.Marshal(gpu)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"slotNumber": "26", "trayIndex": "16", "hostID": "1",
				"moduleID": "0", "fabricGUID": `"` + guid + `"`,
			} {
				if string(fields[key]) != want {
					t.Errorf("JSON %s = %s, want %s", key, fields[key], want)
				}
			}
			var decoded MachineGPUInstance
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.ModuleID == nil || *decoded.ModuleID != 0 || decoded.FabricGUID != guid {
				t.Fatalf("zero module ID or GUID changed on round trip: %+v", decoded)
			}
		})
	}
	data, err := json.Marshal(MachineGPUInstance{UUID: "GPU-123"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"uuid":"GPU-123"}` {
		t.Fatalf("unavailable platform fields changed existing JSON: %s", data)
	}
}

func TestMachineGPUInfoPlatformTable(t *testing.T) {
	zero := uint32(0)
	info := MachineGPUInfo{GPUs: []MachineGPUInstance{
		{UUID: "GPU-123", ModuleID: &zero, FabricGUID: "0xb74a5c8b016b6418"},
		{UUID: "GPU-456"},
	}}
	var buf bytes.Buffer
	info.RenderTable(&buf)
	for _, value := range []string{"SLOT NUMBER", "TRAY INDEX", "HOST ID", "MODULE ID", "GPU FABRIC GUID", "0xb74a5c8b016b6418", "GPU-456"} {
		if !bytes.Contains(buf.Bytes(), []byte(value)) {
			t.Errorf("missing %q from table: %s", value, buf.String())
		}
	}
	if !bytes.Contains(buf.Bytes(), []byte(" 0 ")) {
		t.Fatalf("reported zero module ID missing from table: %s", buf.String())
	}
}

func TestHealthState_JSONIncludesIncidents(t *testing.T) {
	state := HealthState{
		Component: "accelerator-nvidia-dcgm-power",
		Health:    HealthStateTypeDegraded,
		Incidents: []HealthStateIncident{
			{
				EntityID: "GPU-0",
				Message:  "Clock throttled",
				Health:   HealthStateTypeDegraded,
				Error:    "DCGM_FR_CLOCK_THROTTLE_POWER",
			},
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	got := string(data)
	for _, want := range []string{
		`"incidents":[`,
		`"entity_id":"GPU-0"`,
		`"message":"Clock throttled"`,
		`"health":"Degraded"`,
		`"error":"DCGM_FR_CLOCK_THROTTLE_POWER"`,
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("Marshal() = %s, want substring %s", got, want)
		}
	}
}

func TestEventTypeFromString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected EventType
	}{
		{
			name:     "Info event type",
			input:    "Info",
			expected: EventTypeInfo,
		},
		{
			name:     "Warning event type",
			input:    "Warning",
			expected: EventTypeWarning,
		},
		{
			name:     "Critical event type",
			input:    "Critical",
			expected: EventTypeCritical,
		},
		{
			name:     "Fatal event type",
			input:    "Fatal",
			expected: EventTypeFatal,
		},
		{
			name:     "Unknown event type",
			input:    "NonExistent",
			expected: EventTypeUnknown,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: EventTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EventTypeFromString(tt.input)
			if got != tt.expected {
				t.Errorf("EventTypeFromString(%q) = %v, want %v",
					tt.input, got, tt.expected)
			}
		})
	}
}

func TestSuggestedActions_DescribeActions(t *testing.T) {
	tests := []struct {
		name             string
		suggestedActions SuggestedActions
		expected         string
	}{
		{
			name: "Single repair action",
			suggestedActions: SuggestedActions{
				Description:   "Test description",
				RepairActions: []RepairActionType{RepairActionTypeRebootSystem},
			},
			expected: "REBOOT_SYSTEM",
		},
		{
			name: "Multiple repair actions",
			suggestedActions: SuggestedActions{
				Description:   "Multiple actions needed",
				RepairActions: []RepairActionType{RepairActionTypeRebootSystem, RepairActionTypeHardwareInspection},
			},
			expected: "REBOOT_SYSTEM, HARDWARE_INSPECTION",
		},
		{
			name: "No repair actions",
			suggestedActions: SuggestedActions{
				Description:   "No actions needed",
				RepairActions: []RepairActionType{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.suggestedActions.DescribeActions()
			if got != tt.expected {
				t.Errorf("SuggestedActions.DescribeActions() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMachineInfo_RenderTable(t *testing.T) {
	tests := []struct {
		name         string
		machineInfo  MachineInfo
		wantContains []string
	}{
		{
			name: "Basic machine info",
			machineInfo: MachineInfo{
				GPUdVersion:             "1.0.0",
				CUDAVersion:             "11.7.0",
				ContainerRuntimeVersion: "containerd://1.4.2",
				KernelVersion:           "5.4.0-1024-aws",
				OSImage:                 "Ubuntu 20.04.3 LTS",
				GPUDriverVersion:        "470.82.01",
			},
			wantContains: []string{
				"GPUd Version", "1.0.0",
				"CUDA Version", "11.7.0",
				"Container Runtime Version", "containerd://1.4.2",
				"Kernel Version", "5.4.0-1024-aws",
				"OS Image", "Ubuntu 20.04.3 LTS",
			},
		},
		{
			name: "Machine info with GPU info",
			machineInfo: MachineInfo{
				GPUdVersion:      "1.0.0",
				GPUDriverVersion: "470.82.01",
				GPUInfo: &MachineGPUInfo{
					Product:      "NVIDIA A100-SXM4-40GB",
					Manufacturer: "NVIDIA",
					Architecture: "ampere",
					Memory:       "40GB",
				},
			},
			wantContains: []string{
				"GPUd Version", "1.0.0",
				"GPU Driver Version", "470.82.01",
				"GPU Product", "NVIDIA A100-SXM4-40GB",
				"GPU Manufacturer", "NVIDIA",
				"GPU Architecture", "ampere",
				"GPU Memory", "40GB",
			},
		},
		{
			name: "Machine info with Disk info",
			machineInfo: MachineInfo{
				GPUdVersion:   "1.0.0",
				KernelVersion: "5.4.0-1024-aws",
				OSImage:       "Ubuntu 20.04.3 LTS",
				DiskInfo: &MachineDiskInfo{
					ContainerRootDisk: "/dev/nvme0n1p1",
					BlockDevices: []MachineDiskDevice{
						{
							Name:       "/dev/nvme0n1p1",
							Type:       "part",
							FSType:     "ext4",
							Size:       500 * 1024 * 1024 * 1024,
							MountPoint: "/",
						},
					},
				},
			},
			wantContains: []string{
				"GPUd Version", "1.0.0",
				"Kernel Version", "5.4.0-1024-aws",
				"OS Image", "Ubuntu 20.04.3 LTS",
				"Container Root Disk", "/dev/nvme0n1p1",
				"NAME", "TYPE", "FSTYPE", "SIZE", "MOUNT POINT",
				"/dev/nvme0n1p1", "part", "ext4", "500 GiB", "/",
			},
		},
		{
			name: "Machine info with both GPU and Disk info",
			machineInfo: MachineInfo{
				GPUdVersion:      "1.0.0",
				GPUDriverVersion: "470.82.01",
				GPUInfo: &MachineGPUInfo{
					Product:      "NVIDIA A100-SXM4-40GB",
					Manufacturer: "NVIDIA",
					Architecture: "ampere",
					Memory:       "40GB",
					GPUs: []MachineGPUInstance{
						{
							UUID:    "GPU-abc123",
							SN:      "SN12345",
							MinorID: "0",
						},
					},
				},
				DiskInfo: &MachineDiskInfo{
					ContainerRootDisk: "/dev/nvme0n1p1",
					BlockDevices: []MachineDiskDevice{
						{
							Name:       "/dev/nvme0n1p1",
							Type:       "part",
							FSType:     "ext4",
							Size:       500 * 1024 * 1024 * 1024,
							MountPoint: "/",
						},
					},
				},
			},
			wantContains: []string{
				"GPUd Version", "1.0.0",
				"GPU Driver Version", "470.82.01",
				"GPU Product", "NVIDIA A100-SXM4-40GB",
				"Container Root Disk", "/dev/nvme0n1p1",
				"UUID", "SN", "MINOR ID",
				"GPU-abc123", "SN12345", "0",
				"/dev/nvme0n1p1", "part", "ext4", "500 GiB", "/",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.machineInfo.RenderTable(&buf)
			rendered := buf.String()

			for _, wantStr := range tt.wantContains {
				if !bytes.Contains(buf.Bytes(), []byte(wantStr)) {
					t.Errorf("MachineInfo.RenderTable() output does not contain %q\nGot: %q", wantStr, rendered)
				}
			}
		})
	}
}

func TestMachineGPUInfo_RenderTable(t *testing.T) {
	tests := []struct {
		name            string
		gpuInfo         MachineGPUInfo
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "No GPUs",
			gpuInfo: MachineGPUInfo{
				Product:      "NVIDIA A100-SXM4-40GB",
				Manufacturer: "NVIDIA",
				Architecture: "ampere",
				Memory:       "40GB",
				GPUs:         []MachineGPUInstance{},
			},
			wantContains: []string{},
		},
		{
			name: "Single GPU",
			gpuInfo: MachineGPUInfo{
				Product:      "NVIDIA A100-SXM4-40GB",
				Manufacturer: "NVIDIA",
				Architecture: "ampere",
				Memory:       "40GB",
				GPUs: []MachineGPUInstance{
					{
						UUID:    "GPU-abc123",
						SN:      "SN12345",
						MinorID: "0",
					},
				},
			},
			wantContains:    []string{"UUID", "GPU INDEX", "SN", "MINOR ID", "GPU-abc123", "SN12345", "0"},
			wantNotContains: []string{"BOARD ID", "SLOT NUMBER", "TRAY INDEX", "HOST ID", "MODULE ID", "GPU FABRIC GUID"},
		},
		{
			name: "Multiple GPUs",
			gpuInfo: MachineGPUInfo{
				Product:      "NVIDIA A100-SXM4-40GB",
				Manufacturer: "NVIDIA",
				Architecture: "ampere",
				Memory:       "40GB",
				GPUs: []MachineGPUInstance{
					{
						UUID:     "GPU-abc123",
						GPUIndex: "0",
						SN:       "SN12345",
						MinorID:  "0",
					},
					{
						UUID:     "GPU-def456",
						GPUIndex: "1",
						SN:       "SN67890",
						MinorID:  "1",
					},
				},
			},
			wantContains: []string{
				"UUID", "GPU INDEX", "SN", "MINOR ID",
				"GPU-abc123", "SN12345", "0",
				"GPU-def456", "SN67890", "1",
			},
			wantNotContains: []string{"BOARD ID"},
		},
		{
			name: "Board ID when available",
			gpuInfo: MachineGPUInfo{
				GPUs: []MachineGPUInstance{{
					UUID:    "GPU-abc123",
					BoardID: 7,
				}},
			},
			wantContains: []string{"BOARD ID", "7"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.gpuInfo.RenderTable(&buf)
			rendered := buf.String()

			// If we don't expect any output for cases like no GPUs
			if len(tt.wantContains) == 0 {
				if rendered != "" {
					t.Errorf("MachineGPUInfo.RenderTable() expected no output for empty GPUs list, got: %q", rendered)
				}
				return
			}

			for _, wantStr := range tt.wantContains {
				if !bytes.Contains(buf.Bytes(), []byte(wantStr)) {
					t.Errorf("MachineGPUInfo.RenderTable() output does not contain %q\nGot: %q", wantStr, rendered)
				}
			}
			for _, unwanted := range tt.wantNotContains {
				if bytes.Contains(buf.Bytes(), []byte(unwanted)) {
					t.Errorf("MachineGPUInfo.RenderTable() output unexpectedly contains %q\nGot: %q", unwanted, rendered)
				}
			}
		})
	}
}

func TestMachineDiskInfo_RenderTable(t *testing.T) {
	tests := []struct {
		name         string
		diskInfo     MachineDiskInfo
		wantContains []string
	}{
		{
			name: "No block devices",
			diskInfo: MachineDiskInfo{
				ContainerRootDisk: "/dev/nvme0n1p1",
				BlockDevices:      []MachineDiskDevice{},
			},
			wantContains: []string{},
		},
		{
			name: "Single block device",
			diskInfo: MachineDiskInfo{
				ContainerRootDisk: "/dev/nvme0n1p1",
				BlockDevices: []MachineDiskDevice{
					{
						Name:       "/dev/nvme0n1p1",
						Type:       "part",
						FSType:     "ext4",
						Size:       500 * 1024 * 1024 * 1024, // 500GB
						MountPoint: "/",
					},
				},
			},
			wantContains: []string{
				"NAME", "TYPE", "FSTYPE", "SIZE", "MOUNT POINT", "PARENTS", "CHILDREN",
				"/dev/nvme0n1p1", "part", "ext4", "500 GiB", "/",
			},
		},
		{
			name: "Multiple block devices with parent-child relationships",
			diskInfo: MachineDiskInfo{
				ContainerRootDisk: "/dev/nvme0n1p2",
				BlockDevices: []MachineDiskDevice{
					{
						Name:     "/dev/nvme0n1",
						Type:     "disk",
						Size:     1000 * 1024 * 1024 * 1024, // 1TB
						Children: []string{"/dev/nvme0n1p1", "/dev/nvme0n1p2"},
					},
					{
						Name:       "/dev/nvme0n1p1",
						Type:       "part",
						FSType:     "ext4",
						Size:       100 * 1024 * 1024 * 1024, // 100GB
						MountPoint: "/boot",
						Parents:    []string{"/dev/nvme0n1"},
					},
					{
						Name:       "/dev/nvme0n1p2",
						Type:       "part",
						FSType:     "ext4",
						Size:       900 * 1024 * 1024 * 1024, // 900GB
						MountPoint: "/",
						Parents:    []string{"/dev/nvme0n1"},
					},
				},
			},
			wantContains: []string{
				"NAME", "TYPE", "FSTYPE", "SIZE", "MOUNT POINT", "PARENTS", "CHILDREN",
				"/dev/nvme0n1", "disk", "1000 GiB", "/dev/nvme0n1p1", "/dev/nvme0n1p2",
				"/dev/nvme0n1p1", "part", "ext4", "100 GiB", "/boot", "/dev/nvme0n1",
				"/dev/nvme0n1p2", "part", "ext4", "900 GiB", "/", "/dev/nvme0n1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.diskInfo.RenderTable(&buf)
			rendered := buf.String()

			// If we don't expect any output for cases like no block devices
			if len(tt.wantContains) == 0 {
				if rendered != "" {
					t.Errorf("MachineDiskInfo.RenderTable() expected no output for empty block devices list, got: %q", rendered)
				}
				return
			}

			for _, wantStr := range tt.wantContains {
				if !bytes.Contains(buf.Bytes(), []byte(wantStr)) {
					t.Errorf("MachineDiskInfo.RenderTable() output does not contain %q\nGot: %q", wantStr, rendered)
				}
			}
		})
	}
}
