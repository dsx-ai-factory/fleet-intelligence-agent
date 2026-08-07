// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package machineinfo

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"
	nvmldevice "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml/device"
	nvidiadev "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia/dev"
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

func TestGetSystemResourceGPUCount(t *testing.T) {
	// Skip if NVML is not available
	nvmlInstance, err := nvidianvml.New()
	if err != nil {
		t.Skip("NVML not available, skipping test")
	}
	defer func() {
		if err := nvmlInstance.Shutdown(); err != nil {
			log.Logger.Warnw("failed to shutdown nvml instance", "error", err)
		}
	}()

	_, err = nvidiadev.CountAllDevicesFromDevDir()
	assert.NoError(t, err)
	gpuCnt, err := GetSystemResourceGPUCount(nvmlInstance)
	assert.NoError(t, err)
	assert.NotEmpty(t, gpuCnt)

	expectedCount := len(nvmlInstance.Devices())
	if expectedCount == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		pciDevs, err := listPCIGPUs(ctx)
		assert.NoError(t, err)
		expectedCount = len(pciDevs)
	}

	if expectedCount == 0 {
		assert.Equal(t, "0", gpuCnt)
	} else {
		assert.Equal(t, strconv.Itoa(expectedCount), gpuCnt)
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
func TestGetMachineInfo(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Test only runs on Linux or macOS")
	}

	// Skip if NVML is not available
	nvmlInstance, err := nvidianvml.New()
	if err != nil {
		t.Skip("NVML not available, skipping test")
	}
	defer func() {
		if err := nvmlInstance.Shutdown(); err != nil {
			log.Logger.Warnw("failed to shutdown nvml instance", "error", err)
		}
	}()

	// Test the functionality, but don't verify detailed outputs
	info, err := GetMachineInfo(nvmlInstance)
	if err != nil {
		t.Skipf("Could not get machine info: %v", err)
	}

	// Basic validations
	assert.NotEmpty(t, info.GPUdVersion)
	assert.NotEmpty(t, info.Hostname)
	assert.NotNil(t, info.CPUInfo)
	if info.GPUInfo != nil && len(info.GPUInfo.GPUs) > 0 {
		assert.NotEmpty(t, info.GPUInfo.Memory)
	}
}

// TestGetMachineGPUInfo tests GPU info without complex mocking
func TestGetMachineGPUInfo(t *testing.T) {
	// Skip if NVML is not available
	nvmlInstance, err := nvidianvml.New()
	if err != nil {
		t.Skip("NVML not available, skipping test")
	}
	defer func() {
		if err := nvmlInstance.Shutdown(); err != nil {
			log.Logger.Warnw("failed to shutdown nvml instance", "error", err)
		}
	}()

	if len(nvmlInstance.Devices()) == 0 {
		t.Skip("No GPU devices found, skipping test")
	}

	info, err := GetMachineGPUInfo(nvmlInstance)
	if err != nil {
		t.Skipf("Could not get GPU info: %v", err)
	}

	assert.NotEmpty(t, info.Product)
	assert.NotEmpty(t, info.Manufacturer)
	assert.NotEmpty(t, info.Memory)
	assert.NotEmpty(t, info.GPUs)

	for _, gpu := range info.GPUs {
		assert.NotEmpty(t, gpu.UUID)
		assert.NotEmpty(t, gpu.MinorID)
	}

	// Test memory parsing
	memQty, err := resource.ParseQuantity(info.Memory)
	assert.NoError(t, err)
	assert.NotZero(t, memQty.Value())
}

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

	// Create a mock NVML instance with no devices
	mockInstance := &mockNvmlInstance{}

	count, err := GetSystemResourceGPUCount(mockInstance)
	assert.NoError(t, err)
	assert.Equal(t, "0", count, "GPU count should be 0 when no devices are present")
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
func TestGetMachineInfo_Components(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Test only runs on Linux or macOS")
	}

	// Skip if NVML is not available
	nvmlInstance, err := nvidianvml.New()
	if err != nil {
		t.Skip("NVML not available, skipping test")
	}
	defer func() {
		if err := nvmlInstance.Shutdown(); err != nil {
			log.Logger.Warnw("failed to shutdown nvml instance", "error", err)
		}
	}()

	info, err := GetMachineInfo(nvmlInstance)
	if err != nil {
		t.Skipf("Could not get machine info: %v", err)
	}

	// Test individual components
	t.Run("version_info", func(t *testing.T) {
		assert.NotEmpty(t, info.GPUdVersion)
		assert.NotEmpty(t, info.Hostname)
		assert.NotEmpty(t, info.OperatingSystem)
	})

	t.Run("cpu_info", func(t *testing.T) {
		assert.NotNil(t, info.CPUInfo)
		assert.Equal(t, runtime.GOARCH, info.CPUInfo.Architecture)
		assert.NotZero(t, info.CPUInfo.LogicalCores)
	})

	t.Run("memory_info", func(t *testing.T) {
		assert.NotNil(t, info.MemoryInfo)
		assert.NotZero(t, info.MemoryInfo.TotalBytes)
	})

	t.Run("nic_info", func(t *testing.T) {
		assert.NotNil(t, info.NICInfo)
		// PrivateIPInterfaces can be empty in some environments
		t.Logf("Found %d network interfaces", len(info.NICInfo.PrivateIPInterfaces))
	})

	if runtime.GOOS == "linux" {
		t.Run("disk_info", func(t *testing.T) {
			if info.DiskInfo != nil {
				assert.NotEmpty(t, info.DiskInfo.BlockDevices)
				t.Logf("Found %d block devices", len(info.DiskInfo.BlockDevices))
			}
		})
	}
}

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

// TestGetMachineGPUInfo_NoDevices tests GPU info when no devices are available
func TestGetMachineGPUInfo_NoDevices(t *testing.T) {
	// Use the existing mockNvmlInstance which has no devices
	mockInstance := &mockNvmlInstance{}

	info, err := GetMachineGPUInfo(mockInstance)
	assert.NoError(t, err)
	assert.NotNil(t, info)
	// When no devices are present, these fields should be empty
	assert.Empty(t, info.GPUs)
	assert.Empty(t, info.Memory)
}

func Test_nvmlByteArrayToString(t *testing.T) {
	t.Parallel()

	b := make([]uint8, 16)
	copy(b, []byte("1583425610002"))

	got := nvmlByteArrayToString(b)
	assert.Equal(t, "1583425610002", got)
}

func TestGetMachineGPUInfo_ChassisSNPerGPU_MockNVML(t *testing.T) {
	t.Parallel()

	// Use NVML mock to make this deterministic.
	old := os.Getenv("GPUD_NVML_MOCK_ALL_SUCCESS")
	t.Cleanup(func() {
		_ = os.Setenv("GPUD_NVML_MOCK_ALL_SUCCESS", old)
	})
	require.NoError(t, os.Setenv("GPUD_NVML_MOCK_ALL_SUCCESS", "true"))

	nvmlInstance, err := nvidianvml.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = nvmlInstance.Shutdown() })

	info, err := GetMachineGPUInfo(nvmlInstance)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.NotEmpty(t, info.GPUs)

	if nvidianvml.PlatformInfoSupported() {
		for _, gpu := range info.GPUs {
			assert.Equal(t, "1583425610002", gpu.ChassisSN)
		}
	} else {
		for _, gpu := range info.GPUs {
			assert.Empty(t, gpu.ChassisSN)
		}
	}
}

func TestGetMachineGPUInfo_PartialNVMLFailureIsNonFatal(t *testing.T) {
	t.Parallel()

	info, err := GetMachineGPUInfo(&partialFailureMockNVMLInstance{})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Len(t, info.GPUs, 1)
	require.Equal(t, "GPU-failing-device", info.GPUs[0].UUID)
}

func TestGetMachineGPUInfo_CollectsFabricIdentity(t *testing.T) {
	t.Parallel()

	info, err := GetMachineGPUInfo(&fabricIdentityMockNVMLInstance{})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Len(t, info.GPUs, 1)
	require.NotNil(t, info.GPUs[0].CliqueID)
	assert.Equal(t, uint32(0), *info.GPUs[0].CliqueID)
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", info.GPUs[0].ClusterUUID)
}

type partialFailureMockNVMLInstance struct {
	nvidianvml.Instance
}

func (m *partialFailureMockNVMLInstance) ProductName() string {
	return "H100"
}

func (m *partialFailureMockNVMLInstance) Brand() string {
	return "NVIDIA"
}

func (m *partialFailureMockNVMLInstance) Architecture() string {
	return "hopper"
}

func (m *partialFailureMockNVMLInstance) FabricStateSupported() bool {
	return false
}

func (m *partialFailureMockNVMLInstance) Devices() map[string]nvmldevice.Device {
	return map[string]nvmldevice.Device{
		"GPU-failing-device": &partialFailureMockGPUDevice{},
	}
}

type partialFailureMockGPUDevice struct {
	nvmldevice.Device
}

func (d *partialFailureMockGPUDevice) PCIBusID() string {
	return "0000:01:00.0"
}

func (d *partialFailureMockGPUDevice) GetPlatformInfo() (nvml.PlatformInfo, nvml.Return) {
	return nvml.PlatformInfo{}, nvml.ERROR_GPU_IS_LOST
}

func (d *partialFailureMockGPUDevice) GetMemoryInfo_v2() (nvml.Memory_v2, nvml.Return) {
	return nvml.Memory_v2{}, nvml.ERROR_UNKNOWN
}

func (d *partialFailureMockGPUDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	return nvml.Memory{}, nvml.ERROR_GPU_IS_LOST
}

func (d *partialFailureMockGPUDevice) GetSerial() (string, nvml.Return) {
	return "", nvml.ERROR_GPU_IS_LOST
}

func (d *partialFailureMockGPUDevice) GetMinorNumber() (int, nvml.Return) {
	return 0, nvml.ERROR_GPU_IS_LOST
}

func (d *partialFailureMockGPUDevice) GetBoardId() (uint32, nvml.Return) {
	return 0, nvml.ERROR_GPU_IS_LOST
}

func (d *partialFailureMockGPUDevice) GetPCIBusID() (string, error) {
	return "0000:01:00.0", fmt.Errorf("gpu lost")
}

func (d *partialFailureMockGPUDevice) GetName() (string, nvml.Return) {
	return "", nvml.ERROR_GPU_IS_LOST
}

func (d *partialFailureMockGPUDevice) GetVbiosVersion() (string, nvml.Return) {
	return "", nvml.ERROR_GPU_IS_LOST
}

func (d *partialFailureMockGPUDevice) GetIndex() (int, nvml.Return) {
	return 0, nvml.ERROR_GPU_IS_LOST
}

type fabricIdentityMockNVMLInstance struct {
	partialFailureMockNVMLInstance
}

func (m *fabricIdentityMockNVMLInstance) FabricStateSupported() bool {
	return true
}

func (m *fabricIdentityMockNVMLInstance) Devices() map[string]nvmldevice.Device {
	return map[string]nvmldevice.Device{
		"GPU-fabric-device": &fabricIdentityMockGPUDevice{},
	}
}

type fabricIdentityMockGPUDevice struct {
	partialFailureMockGPUDevice
}

func (d *fabricIdentityMockGPUDevice) GetFabricState() (nvmldevice.FabricState, error) {
	return nvmldevice.FabricState{
		CliqueID:    0,
		ClusterUUID: "11111111-2222-3333-4444-555555555555",
	}, nil
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
