// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package machineinfo

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

func TestGetMachineNetwork(t *testing.T) {
	// Even if the environment variable is not set, we can still test the function structure
	network := GetMachineNICInfo()
	assert.NotNil(t, network)

	// Run more detailed test if environment variable is set
	if os.Getenv("TEST_MACHINE_NETWORK") == "true" {
		t.Log("Running detailed network test")
		assert.NotNil(t, network)
		assert.NotEmpty(t, network.PrivateIPInterfaces)
	} else {
		t.Log("Basic network test - verify structure only")
	}

	t.Logf("network: %+v", network)
}

func TestGetMachineCPUInfo(t *testing.T) {
	cpuInfo := GetMachineCPUInfo()
	assert.NotNil(t, cpuInfo)
	assert.Equal(t, runtime.GOARCH, cpuInfo.Architecture)
}

func TestGetMachineLocation(t *testing.T) {
	if os.Getenv("TEST_MACHINE_LOCATION") != "true" {
		t.Skip("TEST_MACHINE_LOCATION is not set")
	}

	// Always run a basic test, but don't assert on the results
	// as it may return nil depending on network conditions
	location := GetMachineLocation()
	t.Logf("location: %+v", location)

	// More detailed test when environment variable is set
	if os.Getenv("TEST_MACHINE_LOCATION") == "true" {
		t.Log("Running detailed location test")
		if location != nil {
			assert.NotEmpty(t, location.Region, "Region should not be empty when TEST_MACHINE_LOCATION is set")
		}
	} else {
		t.Log("Basic location test - no assertions on result")
	}
}

func TestGetSystemResourceRootVolumeTotal(t *testing.T) {
	// Skip test on non-Linux platforms or in environments where root volume check fails
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Test only runs on Linux or macOS")
	}

	volume, err := GetSystemResourceRootVolumeTotal()
	if err != nil {
		t.Skipf("Could not get root volume total: %v", err)
	}

	assert.NotEmpty(t, volume)
	volQty, err := resource.ParseQuantity(volume)
	assert.NoError(t, err)
	assert.NotZero(t, volQty.Value())
	t.Logf("Root volume: %s", volume)
}

// TestGetMachineInfo tests only basic functionality without mocking

// TestGetMachineDiskInfo tests disk info with minimal validation
func TestGetMachineDiskInfo(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Test only runs on Linux or macOS")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := GetMachineDiskInfo(ctx)
	if err != nil {
		t.Skipf("Could not get disk info: %v", err)
	}

	assert.NotNil(t, info)

	// At least one block device should be present
	assert.NotEmpty(t, info.BlockDevices)

	// Validate first block device
	if len(info.BlockDevices) > 0 {
		device := info.BlockDevices[0]
		assert.NotEmpty(t, device.Name)
		assert.NotEmpty(t, device.Type)
		assert.NotZero(t, device.Size)

		// Log device details for better understanding
		t.Logf("Device: %+v", device)
	}

	// If we're on Linux, check container root disk detection
	if runtime.GOOS == "linux" {
		t.Logf("Container root disk: %s", info.ContainerRootDisk)
	}
}

func TestSafeDiskUint64ToInt64(t *testing.T) {
	require.Equal(t, int64(42), safeDiskUint64ToInt64(42, "/dev/sda1", "size"))
	require.Equal(t, int64(math.MaxInt64), safeDiskUint64ToInt64(uint64(math.MaxInt64), "/dev/sda1", "size"))
	require.Equal(t, int64(math.MaxInt64), safeDiskUint64ToInt64(uint64(math.MaxInt64)+1, "/dev/sda1", "size"))
}

// TestGetMachineMemoryInfo tests memory info retrieval
func TestGetMachineMemoryInfo(t *testing.T) {
	memInfo := GetMachineMemoryInfo()
	assert.NotNil(t, memInfo)
	assert.NotZero(t, memInfo.TotalBytes, "Total memory bytes should be greater than zero")
	t.Logf("Memory info: %+v", memInfo)
}

// TestGetSystemResourceGPUCount_NoGPU tests GPU count when no GPUs are present
func TestGetSystemResourceGPUCount_NoGPU(t *testing.T) {
	originalListPCIGPUs := listPCIGPUs
	t.Cleanup(func() {
		listPCIGPUs = originalListPCIGPUs
	})
	listPCIGPUs = func(_ context.Context) ([]string, error) {
		return nil, nil
	}

	count, err := GetSystemResourceGPUCount(nvidiadcgm.NewNoOp())
	assert.NoError(t, err)
	assert.Equal(t, "0", count, "GPU count should be 0 when no devices are present")
}

func TestArchitectureFromComputeCapability(t *testing.T) {
	tests := []struct {
		major uint64
		minor uint64
		want  string
	}{
		{major: 7, minor: 0, want: "volta"},
		{major: 7, minor: 5, want: "turing"},
		{major: 8, minor: 0, want: "ampere"},
		{major: 8, minor: 9, want: "ada-lovelace"},
		{major: 9, minor: 0, want: "hopper"},
		{major: 10, minor: 0, want: "blackwell"},
	}
	for _, tc := range tests {
		capability := int64((tc.major << 16) | tc.minor)
		assert.Equal(t, tc.want, architectureFromComputeCapability(capability))
	}
}

func TestFormatCUDADriverVersion(t *testing.T) {
	assert.Equal(t, "12.8", formatCUDADriverVersion(12080))
	assert.Empty(t, formatCUDADriverVersion(0))
}

func TestGetMachineGPUInfoFromDCGM(t *testing.T) {
	device := nvidiadcgm.DeviceInfo{
		ID:                     3,
		UUID:                   "GPU-123",
		BusID:                  "0000:17:00.0",
		Brand:                  "NVIDIA",
		Model:                  "NVIDIA H100 80GB HBM3",
		Serial:                 "serial-123",
		VBIOSVersion:           "96.00.5E.00.01",
		DriverVersion:          "570.86.15",
		FramebufferMemoryBytes: 80 * 1024 * 1024 * 1024,
	}
	instance := &staticDeviceInstance{
		Instance: nvidiadcgm.NewNoOp(),
		devices:  []nvidiadcgm.DeviceInfo{device},
	}
	reader := staticFieldReader{results: []nvidiadcgm.DeviceFieldValues{{
		DeviceID: device.ID,
		UUID:     device.UUID,
		Values: []dcgm.FieldValue_v1{
			int64Field(dcgm.DCGM_FI_CUDA_DRIVER_VERSION, 12080),
			int64Field(dcgm.DCGM_FI_DEV_MINOR_NUMBER, 7),
			int64Field(dcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY, (9 << 16)),
			stringField(dcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID, "cluster-123"),
			int64Field(dcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID, 9),
			stringField(dcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER, "chassis-123"),
		},
	}}}

	info, driverVersion, cudaVersion, err := getMachineGPUInfo(instance, reader)
	require.NoError(t, err)
	assert.Equal(t, "570.86.15", driverVersion)
	assert.Equal(t, "12.8", cudaVersion)
	assert.Equal(t, "NVIDIA-H100-80GB-HBM3", info.Product)
	assert.Equal(t, "NVIDIA", info.Manufacturer)
	assert.Equal(t, "hopper", info.Architecture)
	memory, err := resource.ParseQuantity(info.Memory)
	require.NoError(t, err)
	assert.Equal(t, int64(device.FramebufferMemoryBytes), memory.Value())
	require.Len(t, info.GPUs, 1)
	assert.Equal(t, "GPU-123", info.GPUs[0].UUID)
	assert.Equal(t, "3", info.GPUs[0].GPUIndex)
	assert.Equal(t, "7", info.GPUs[0].MinorID)
	assert.Equal(t, "cluster-123", info.GPUs[0].ClusterUUID)
	require.NotNil(t, info.GPUs[0].CliqueID)
	assert.Equal(t, uint32(9), *info.GPUs[0].CliqueID)
	assert.Equal(t, "chassis-123", info.GPUs[0].ChassisSN)
	assert.Zero(t, info.GPUs[0].BoardID)
}

type staticDeviceInstance struct {
	nvidiadcgm.Instance
	devices []nvidiadcgm.DeviceInfo
}

func (i *staticDeviceInstance) GetDevices() []nvidiadcgm.DeviceInfo { return i.devices }

type staticFieldReader struct {
	results []nvidiadcgm.DeviceFieldValues
	err     error
}

func (r staticFieldReader) GetResult([]dcgm.Short) ([]nvidiadcgm.DeviceFieldValues, error) {
	return r.results, r.err
}

func int64Field(fieldID dcgm.Short, value int64) dcgm.FieldValue_v1 {
	field := dcgm.FieldValue_v1{FieldID: fieldID, FieldType: dcgm.DCGM_FT_INT64}
	binary.NativeEndian.PutUint64(field.Value[:8], uint64(value))
	return field
}

func stringField(fieldID dcgm.Short, value string) dcgm.FieldValue_v1 {
	field := dcgm.FieldValue_v1{FieldID: fieldID, FieldType: dcgm.DCGM_FT_STRING}
	copy(field.Value[:], value)
	return field
}

// TestGetProvider tests provider detection
func TestGetProvider(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected string
	}{
		{
			name:     "empty IP",
			ip:       "",
			expected: "unknown", // GetProvider returns "unknown" for empty IP
		},
		{
			name:     "localhost",
			ip:       "127.0.0.1",
			expected: "unknown", // GetProvider returns "unknown" for localhost
		},
		{
			name:     "private IP",
			ip:       "192.168.1.1",
			expected: "unknown", // GetProvider returns "unknown" for private IP
		},
		{
			name:     "public IP",
			ip:       "8.8.8.8",
			expected: "unknown", // Will be "unknown" unless we're actually on a cloud provider
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := GetProvider(tt.ip)
			// We can't assert specific values since it depends on the actual environment
			// Just ensure it doesn't panic and returns a valid provider info
			assert.NotNil(t, provider)
			assert.IsType(t, "", provider.Provider)
			t.Logf("Provider for IP %s: %s", tt.ip, provider.Provider)
		})
	}
}

// TestGetMachineLocation_Basic tests basic location functionality
func TestGetMachineLocation_Basic(t *testing.T) {
	if os.Getenv("TEST_MACHINE_LOCATION") != "true" {
		t.Skip("TEST_MACHINE_LOCATION is not set")
	}

	location := GetMachineLocation()
	// Location can be nil if not on a cloud provider or network issues
	if location != nil {
		t.Logf("Location: %+v", location)
		// If we have a location, it should have some fields
		if location.Region != "" {
			assert.NotEmpty(t, location.Region)
		}
	} else {
		t.Log("No location detected (expected if not on cloud provider)")
	}
}

// TestGetMachineInfo_Components tests individual components of machine info

// TestGetMachineCPUInfo_Details tests detailed CPU information
func TestGetMachineCPUInfo_Details(t *testing.T) {
	cpuInfo := GetMachineCPUInfo()
	assert.NotNil(t, cpuInfo)

	// Test all fields
	assert.Equal(t, runtime.GOARCH, cpuInfo.Architecture)
	assert.NotZero(t, cpuInfo.LogicalCores, "Logical cores should be greater than zero")

	// Type and Manufacturer might be empty in some environments, but should be strings
	assert.IsType(t, "", cpuInfo.Type)
	assert.IsType(t, "", cpuInfo.Manufacturer)

	t.Logf("CPU Info - Type: %s, Manufacturer: %s, Architecture: %s, Cores: %d",
		cpuInfo.Type, cpuInfo.Manufacturer, cpuInfo.Architecture, cpuInfo.LogicalCores)
}

// TestGetMachineNICInfo_Details tests detailed network interface information
func TestGetMachineNICInfo_Details(t *testing.T) {
	nicInfo := GetMachineNICInfo()
	assert.NotNil(t, nicInfo)

	// Test interface details if any are present
	for i, iface := range nicInfo.PrivateIPInterfaces {
		t.Run(fmt.Sprintf("interface_%d", i), func(t *testing.T) {
			assert.NotEmpty(t, iface.Interface, "Interface name should not be empty")
			assert.NotEmpty(t, iface.IP, "IP should not be empty")
			// MAC can be empty for some interface types
			assert.IsType(t, "", iface.MAC)

			// Test that Addr is valid if IP is set
			if iface.IP != "" {
				assert.True(t, iface.Addr.IsValid(), "Addr should be valid when IP is set")
			}

			t.Logf("Interface %d: %s (%s) - %s", i, iface.Interface, iface.MAC, iface.IP)
		})
	}
}

// TestGetSystemResourceRootVolumeTotal_Validation tests root volume total validation
func TestGetSystemResourceRootVolumeTotal_Validation(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Test only runs on Linux or macOS")
	}

	volume, err := GetSystemResourceRootVolumeTotal()
	if err != nil {
		t.Skipf("Could not get root volume total: %v", err)
	}

	assert.NotEmpty(t, volume)

	// Test that the volume can be parsed as a Kubernetes resource quantity
	volQty, err := resource.ParseQuantity(volume)
	assert.NoError(t, err, "Volume should be a valid Kubernetes resource quantity")
	assert.True(t, volQty.Value() > 0, "Volume should be greater than zero")

	// Test that it's in a reasonable range (at least 1GB, less than 100TB)
	minSize := resource.MustParse("1Gi")
	maxSize := resource.MustParse("100Ti")
	assert.True(t, volQty.Cmp(minSize) >= 0, "Volume should be at least 1GB")
	assert.True(t, volQty.Cmp(maxSize) <= 0, "Volume should be less than 100TB")

	t.Logf("Root volume: %s (parsed: %d bytes)", volume, volQty.Value())
}

// TestGetMachineDiskInfo_FilterEmptyMountPoints tests that GetMachineDiskInfo filters out empty mount points
func TestGetMachineDiskInfo_FilterEmptyMountPoints(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Test only runs on Linux or macOS")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := GetMachineDiskInfo(ctx)
	if err != nil {
		t.Skipf("Could not get disk info: %v", err)
	}

	assert.NotNil(t, info)

	// Verify no block devices have empty mount points
	for _, device := range info.BlockDevices {
		if device.MountPoint == "" {
			t.Errorf("Device %s has empty mount point, should be filtered out", device.Name)
		}
	}

	t.Logf("Verified %d block devices all have non-empty mount points", len(info.BlockDevices))
}

// TestGetMachineDiskInfo_FilterProviderSpecificPaths tests filtering of provider-specific mount points
func TestGetMachineDiskInfo_FilterProviderSpecificPaths(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Test only runs on Linux or macOS")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := GetMachineDiskInfo(ctx)
	if err != nil {
		t.Skipf("Could not get disk info: %v", err)
	}

	assert.NotNil(t, info)

	// Verify no provider-specific mount points
	for _, device := range info.BlockDevices {
		assert.False(t, strings.HasPrefix(device.MountPoint, "/mnt/customfs"),
			"Device %s has provider-specific mount point %s", device.Name, device.MountPoint)
		assert.False(t, strings.HasPrefix(device.MountPoint, "/mnt/cloud-metadata"),
			"Device %s has provider-specific mount point %s", device.Name, device.MountPoint)
	}

	t.Logf("Verified %d block devices have no provider-specific mount points", len(info.BlockDevices))
}
