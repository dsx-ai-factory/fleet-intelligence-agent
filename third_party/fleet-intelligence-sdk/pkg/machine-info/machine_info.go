// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

// Package machineinfo provides information about the machine.
package machineinfo

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	nvidiamemory "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/memory"
	componentnvml "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/nvml"
	componentcontainerd "github.com/NVIDIA/fleet-intelligence-sdk/components/containerd"
	componenttailscale "github.com/NVIDIA/fleet-intelligence-sdk/components/tailscale"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/asn"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/disk"
	pkghost "github.com/NVIDIA/fleet-intelligence-sdk/pkg/host"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/netutil"
	pkgnetutillatencyedge "github.com/NVIDIA/fleet-intelligence-sdk/pkg/netutil/latency/edge"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"
	nvidiapci "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia/pci"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/providers"
	pkgprovidersall "github.com/NVIDIA/fleet-intelligence-sdk/pkg/providers/all"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/providers/nebius"
	"github.com/NVIDIA/fleet-intelligence-sdk/version"
)

const diskPartitionsTimeout = 10 * time.Second

var listPCIGPUs = nvidiapci.ListPCIGPUs

func GetMachineInfo(nvmlInstance nvidianvml.Instance) (*apiv1.MachineInfo, error) {
	hostname, _ := os.Hostname()
	info := &apiv1.MachineInfo{
		GPUdVersion: version.Version,

		GPUDriverVersion:        nvmlInstance.DriverVersion(),
		CUDAVersion:             nvmlInstance.CUDAVersion(),
		ContainerRuntimeVersion: "",
		KernelVersion:           pkghost.KernelVersion(),
		OSImage:                 pkghost.OSName(),
		OperatingSystem:         runtime.GOOS,
		SystemUUID:              pkghost.SystemUUID(),
		MachineID:               pkghost.OSMachineID(),
		BootID:                  pkghost.BootID(),
		Hostname:                hostname,
		Uptime:                  metav1.NewTime(time.Unix(int64(pkghost.BootTimeUnixSeconds()), 0)),

		CPUInfo:    GetMachineCPUInfo(),
		MemoryInfo: GetMachineMemoryInfo(),
		NICInfo:    GetMachineNICInfo(),
	}

	var err error
	info.GPUInfo, err = GetMachineGPUInfo(nvmlInstance)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine gpu info: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if runtime.GOOS == "linux" {
		info.DiskInfo, err = GetMachineDiskInfo(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get machine disk info: %w", err)
		}

		if componentcontainerd.CheckContainerdInstalled() && componentcontainerd.CheckContainerdRunning(ctx) {
			containerdVersion, err := componentcontainerd.GetVersion(ctx, componentcontainerd.DefaultContainerRuntimeEndpoint)
			if err != nil {
				log.Logger.Warnw("failed to check containerd version", "error", err)
			} else {
				if !strings.HasPrefix(containerdVersion, "containerd://") {
					containerdVersion = "containerd://" + containerdVersion
				}
				info.ContainerRuntimeVersion = containerdVersion
			}
		}

		// Collect tailscale version if installed
		if componenttailscale.CheckTailscaleInstalled() {
			tailscaleVersion, err := componenttailscale.GetTailscaleVersion()
			if err != nil {
				log.Logger.Warnw("failed to get tailscale version", "error", err)
			} else {
				info.TailscaleVersion = strings.TrimSpace(tailscaleVersion)
			}
		}

	}

	return info, nil
}

// GetSystemResourceRootVolumeTotal returns the system root disk resource of the machine
// for the total disk size, using the type defined in "corev1.ResourceName"
// in https://pkg.go.dev/k8s.io/api/core/v1#ResourceName.
// It represents the Volume size, in bytes (e,g. 5Gi = 5GiB = 5 * 1024 * 1024 * 1024).
// Must be parsed using the "resource.ParseQuantity" function in https://pkg.go.dev/k8s.io/apimachinery/pkg/api/resource.
func GetSystemResourceRootVolumeTotal() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	usage, err := disk.GetUsage(ctx, "/")
	if err != nil {
		return "", fmt.Errorf("failed to get disk usage: %w", err)
	}

	qty := resource.NewQuantity(int64(usage.TotalBytes), resource.DecimalSI)
	return qty.String(), nil
}

func GetMachineCPUInfo() *apiv1.MachineCPUInfo {
	info := &apiv1.MachineCPUInfo{
		Type:         pkghost.CPUModelName(),
		Manufacturer: pkghost.CPUVendorID(),
		Architecture: runtime.GOARCH,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// counting the number of logical CPU cores available to the system
	// same as "nproc --all"
	cnt, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		log.Logger.Errorw("failed to get logical CPU cores count", "error", err)
	}
	info.LogicalCores = int64(cnt)

	return info
}

func GetMachineMemoryInfo() *apiv1.MachineMemoryInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		log.Logger.Errorw("failed to get memory info", "error", err)
		return &apiv1.MachineMemoryInfo{
			TotalBytes: 0,
		}
	}

	return &apiv1.MachineMemoryInfo{
		TotalBytes: vm.Total,
	}
}

func GetMachineNICInfo() *apiv1.MachineNICInfo {
	ifaces := []apiv1.MachineNetworkInterface{}
	privateIPs, err := netutil.GetPrivateIPs(
		netutil.WithPrefixesToSkip(
			"lo",
			"eni",
			"cali",
			"docker",
			"lepton",
			"tailscale",
			"ib", // e.g., "ibp24s0" infiniband links
		),
		netutil.WithSuffixesToSkip(".calico"),
	)
	if err != nil {
		log.Logger.Errorw("failed to get private ips", "error", err)
	}

	for _, ip := range privateIPs {
		addr := ip.Addr.String()
		if addr == "" {
			continue
		}
		ifaces = append(ifaces, apiv1.MachineNetworkInterface{
			Interface: ip.Iface.Name,
			MAC:       ip.Iface.HardwareAddr.String(),
			IP:        ip.Addr.String(),
			Addr:      ip.Addr,
		})
	}

	sort.Slice(ifaces, func(i, j int) bool {
		return ifaces[i].IP < ifaces[j].IP
	})
	return &apiv1.MachineNICInfo{
		PrivateIPInterfaces: ifaces,
	}
}

// GetProvider looks up the provider of the machine.
// If the metadata service or other provider detection fails, it falls back to ASN lookup
// using the public IP address.
func GetProvider(publicIP string) *providers.Info {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	providerInfo, err := pkgprovidersall.Detect(ctx)
	cancel()
	if err != nil {
		log.Logger.Warnw("failed to detect provider", "error", err)
	} else {
		log.Logger.Debugw("provider detection result", "provider", providerInfo)
	}

	if providerInfo == nil {
		log.Logger.Debugw("providerInfo is nil, creating default")
		providerInfo = &providers.Info{
			Provider: "unknown",
		}
	}
	if providerInfo.PublicIP == "" {
		providerInfo.PublicIP = publicIP
	}
	if providerInfo.Provider == "" {
		log.Logger.Debugw("providerInfo.Provider is empty, setting to unknown")
		providerInfo.Provider = "unknown"
	}

	log.Logger.Debugw("provider after initial detection", "provider", providerInfo.Provider, "publicIP", providerInfo.PublicIP)

	if providerInfo.Provider != "unknown" {
		log.Logger.Debugw("returning detected provider", "provider", providerInfo.Provider)
		return providerInfo
	}

	if publicIP != "" {
		// fallback to ASN lookup
		log.Logger.Debugw("fallback to ASN lookup for provider", "publicIP", publicIP)
		asnResult, err := asn.GetASLookup(publicIP)
		if err != nil {
			log.Logger.Warnw("ASN lookup failed", "error", err, "publicIP", publicIP)
			return providerInfo
		}

		normalizedProvider := asn.NormalizeASNName(asnResult.AsnName)
		log.Logger.Debugw("ASN lookup result", "asnResult", asnResult, "asnName", asnResult.AsnName, "normalizedProvider", normalizedProvider)

		// Ensure we don't set an empty provider
		if normalizedProvider != "" {
			providerInfo.Provider = normalizedProvider
		} else {
			// as lookup succeeded but normalized provider is empty
			providerInfo.Provider = asnResult.AsnName
			log.Logger.Warnw("normalized provider is empty -- fallback to raw asn name for provider name", "asnName", asnResult.AsnName)
		}
	} else {
		log.Logger.Warnw("no public IP provided for ASN lookup")
	}

	if providerInfo.Provider == "nebius" && providerInfo.InstanceID == "" {
		instanceID, err := nebius.GetInstanceID()
		if err != nil {
			log.Logger.Warnw("failed to get Nebius instance ID", "error", err)
		} else {
			providerInfo.InstanceID = instanceID
		}
	}

	log.Logger.Debugw("GetProvider returning",
		"provider", providerInfo.Provider,
		"publicIP", providerInfo.PublicIP,
		"privateIP", providerInfo.PrivateIP,
		"instanceID", providerInfo.InstanceID,
	)

	return providerInfo
}

func GetMachineLocation() *apiv1.MachineLocation {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	latencies, err := pkgnetutillatencyedge.Measure(ctx)
	if err != nil || len(latencies) == 0 {
		return nil
	}

	closest := latencies.Closest()
	return &apiv1.MachineLocation{
		Region: closest.RegionCode,
	}
}

// GetSystemResourceGPUCount returns the system GPU resource of the machine
// with the GPU count, using the type defined in "corev1.ResourceName"
// in https://pkg.go.dev/k8s.io/api/core/v1#ResourceName.
// It represents the GPU count with the key "nvidia.com/gpu" or "nvidia.com/gpu.count".
// Must be parsed using the "resource.ParseQuantity" function in https://pkg.go.dev/k8s.io/apimachinery/pkg/api/resource.
//
// This is different from the device count in DCGM.
// ref. "CountDevEntry" in "nvvs/plugin_src/software/Software.cpp"
// ref. https://github.com/NVIDIA/DCGM/blob/903d745504f50153be8293f8566346f9de3b3c93/nvvs/plugin_src/software/Software.cpp#L220-L249
func GetSystemResourceGPUCount(nvmlInstance nvidianvml.Instance) (string, error) {
	deviceCount := len(nvmlInstance.Devices())
	if deviceCount == 0 {
		// fallback to pci in case nvml/nvidia driver has not been loaded
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		devs, err := listPCIGPUs(ctx)
		if err != nil {
			log.Logger.Errorw("failed to list nvidia pci devices", "error", err)
		}
		deviceCount = len(devs)
	}
	if deviceCount == 0 {
		return "0", nil
	}

	qty := resource.NewQuantity(int64(deviceCount), resource.DecimalSI)
	return qty.String(), nil
}

func GetMachineGPUInfo(nvmlInstance nvidianvml.Instance) (*apiv1.MachineGPUInfo, error) {
	info := &apiv1.MachineGPUInfo{
		Product:      nvmlInstance.ProductName(),
		Manufacturer: nvmlInstance.Brand(),
		Architecture: nvmlInstance.Architecture(),
	}
	collectionErrors := make([]string, 0)
	appendCollectionError := func(uuid, stage, message string) {
		msg := strings.TrimSpace(message)
		if msg == "" {
			msg = "unknown error"
		}
		collectionErrors = append(collectionErrors, fmt.Sprintf("gpu %s: %s failed: %s", uuid, stage, msg))
	}

	platformInfoSupported := nvidianvml.PlatformInfoSupported()

	productName := nvmlInstance.ProductName()
	for uuid, dev := range nvmlInstance.Devices() {
		// Best-effort: NVML platform info (per GPU).
		// nvidia-smi surfaces this under "Platform Info" -> "Chassis Serial Number".
		chassisSN := ""
		if platformInfoSupported {
			pi, ret := dev.GetPlatformInfo()
			if ret == nvml.SUCCESS {
				chassisSN = nvmlByteArrayToString(pi.ChassisSerialNumber[:])
			} else if ret != nvml.ERROR_NOT_SUPPORTED {
				log.Logger.Debugw("failed to get NVML platform info", "uuid", uuid, "error", nvml.ErrorString(ret))
			}
		}

		if info.Memory == "" {
			gpuMemory, err := nvidiamemory.GetMemory(uuid, dev, productName, mem.VirtualMemoryWithContext)
			if err != nil {
				appendCollectionError(uuid, "get_memory", err.Error())
			} else {
				qty := resource.NewQuantity(int64(gpuMemory.TotalBytes), resource.DecimalSI)
				info.Memory = qty.String()
			}
		}

		serialID, ret := dev.GetSerial()
		if ret != nvml.SUCCESS {
			if ret != nvml.ERROR_NOT_SUPPORTED {
				appendCollectionError(uuid, "get_serial", nvml.ErrorString(ret))
			}
		}

		var minorID int
		minorID, ret = dev.GetMinorNumber()
		if ret != nvml.SUCCESS {
			if ret != nvml.ERROR_NOT_SUPPORTED {
				appendCollectionError(uuid, "get_minor_id", nvml.ErrorString(ret))
			}
			minorID = -1 // set to -1 when not supported
		}

		var boardID uint32
		boardID, ret = dev.GetBoardId()
		if ret != nvml.SUCCESS {
			if ret != nvml.ERROR_NOT_SUPPORTED {
				appendCollectionError(uuid, "get_board_id", nvml.ErrorString(ret))
			}
			boardID = 0 // set to 0 when not supported
		}

		busID, err := dev.GetPCIBusID()
		if err != nil {
			appendCollectionError(uuid, "get_pci_bus_id", err.Error())
		}

		modelName, ret := dev.GetName()
		if ret != nvml.SUCCESS {
			if ret != nvml.ERROR_NOT_SUPPORTED {
				appendCollectionError(uuid, "get_name", nvml.ErrorString(ret))
			}
			modelName = ""
		}

		var clusterUUID string
		var cliqueID *uint32
		if nvmlInstance.FabricStateSupported() {
			fabricState, fabricErr := dev.GetFabricState()
			if fabricErr != nil {
				log.Logger.Debugw("failed to get NVLink fabric identity", "uuid", uuid, "error", fabricErr)
			} else {
				clusterUUID = fabricState.ClusterUUID
				id := fabricState.CliqueID
				cliqueID = &id
			}
		}

		vbios, ret := dev.GetVbiosVersion()
		vbiosVersion := ""
		if ret != nvml.SUCCESS {
			appendCollectionError(uuid, "get_vbios_version", nvml.ErrorString(ret))
			log.Logger.Debugw("failed to get VBIOS version", "uuid", uuid, "error", nvml.ErrorString(ret))
		} else {
			vbiosVersion = vbios
		}

		gpuIndex := ""
		idx, ret := dev.GetIndex()
		if ret != nvml.SUCCESS {
			appendCollectionError(uuid, "get_index", nvml.ErrorString(ret))
			log.Logger.Debugw("failed to get GPU index", "uuid", uuid, "error", nvml.ErrorString(ret))
		} else {
			gpuIndex = strconv.Itoa(idx)
		}

		info.GPUs = append(info.GPUs, apiv1.MachineGPUInstance{
			UUID:         uuid,
			GPUIndex:     gpuIndex,
			ModelName:    modelName,
			ClusterUUID:  clusterUUID,
			CliqueID:     cliqueID,
			SN:           serialID,
			MinorID:      strconv.Itoa(minorID),
			BoardID:      boardID,
			BusID:        busID,
			VBIOSVersion: vbiosVersion,
			ChassisSN:    chassisSN,
		})
	}
	componentnvml.RunCheckWithErrors(collectionErrors)

	return info, nil
}

func nvmlByteArrayToString(b []uint8) string {
	// NVML returns fixed-size byte arrays; treat as null-terminated string.
	n := 0
	for ; n < len(b); n++ {
		if b[n] == 0 {
			break
		}
	}
	return strings.TrimSpace(string(b[:n]))
}

func GetMachineDiskInfo(ctx context.Context) (*apiv1.MachineDiskInfo, error) {
	blks, err := disk.GetBlockDevicesWithLsblk(
		ctx,
		disk.WithFstype(disk.DefaultFsTypeFunc),
		disk.WithDeviceType(disk.DefaultDeviceTypeFunc),
	)
	if err != nil {
		return nil, err
	}
	flattened := blks.Flatten()

	rs := make([]apiv1.MachineDiskDevice, 0, len(flattened))
	for _, bd := range flattened {
		if bd.MountPoint == "" {
			continue
		}

		rs = append(rs, apiv1.MachineDiskDevice{
			Name:       bd.Name,
			Type:       bd.Type,
			Size:       safeDiskUint64ToInt64(bd.Size, bd.Name, "size"),
			Used:       safeDiskUint64ToInt64(bd.FSUsed, bd.Name, "used"),
			Rota:       bd.Rota,
			Serial:     bd.Serial,
			WWN:        bd.WWN,
			Vendor:     bd.Vendor,
			Model:      bd.Model,
			Rev:        bd.Rev,
			MountPoint: bd.MountPoint,
			FSType:     bd.FSType,
			PartUUID:   bd.PartUUID,
			Parents:    bd.Parents,
			Children:   bd.Children,
		})
	}

	// track nfs partitions only with available fields
	if runtime.GOOS == "linux" {
		timeoutCtx, cancel := context.WithTimeout(ctx, diskPartitionsTimeout)
		nfsParts, err := disk.GetPartitions(
			timeoutCtx,
			disk.WithFstype(disk.DefaultNFSFsTypeFunc),
			disk.WithMountPoint(disk.DefaultMountPointFunc),
		)
		cancel()
		if err != nil {
			return nil, err
		}
		for _, part := range nfsParts {
			dev := apiv1.MachineDiskDevice{
				Name:       part.Device,
				Type:       "nfs",
				MountPoint: part.MountPoint,
				FSType:     part.Fstype,
			}
			if part.Usage != nil {
				dev.Size = safeDiskUint64ToInt64(part.Usage.TotalBytes, part.Device, "size")
				dev.Used = safeDiskUint64ToInt64(part.Usage.UsedBytes, part.Device, "used")
			}
			rs = append(rs, dev)
		}
	}

	info := &apiv1.MachineDiskInfo{
		BlockDevices: rs,
	}

	if runtime.GOOS == "linux" {
		_, serr := os.Stat("/var/lib/kubelet")
		if serr != nil && !os.IsNotExist(serr) {
			return nil, serr
		}
		if serr == nil {
			out, err := disk.FindMnt(ctx, "/var/lib/kubelet")
			if err != nil {
				return nil, err
			}
			if len(out.Filesystems) > 0 && len(out.Filesystems[0].Sources) > 0 {
				info.ContainerRootDisk = out.Filesystems[0].Sources[0]
			}
		}
	}

	return info, nil
}

func safeDiskUint64ToInt64(value uint64, deviceName, field string) int64 {
	if value <= math.MaxInt64 {
		return int64(value)
	}
	log.Logger.Errorw("disk value overflow detected during machine info collection",
		"device", deviceName,
		"field", field,
		"value", value,
		"max_int64", math.MaxInt64)
	return math.MaxInt64
}
