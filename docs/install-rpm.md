# RPM Installation

## Prerequisites

Before installing Fleet Intelligence Agent, ensure the following prerequisites are met:

- Configure an NVIDIA package repository for automatic dependency installation.
  The following dependencies are required and satisfied by the CUDA (network or local) repository.
  - `datacenter-gpu-manager-4-proprietary` (DCGM)
  - `nvattest` (NVIDIA Attestation SDK CLI, NVAT)
  - `corelib` (NVAT GPU evidence source dependency)
- DCGM HostEngine `4.2.3` or newer
- NVIDIA Datacenter Driver major version `510` or newer is installed
- Install/upgrade commands are run as `root` or with `sudo`
- `dnf-plugins-core` is installed (required for `dnf config-manager`)
- Attestation for the fleetint use case only supports Blackwell and newer GPUs, and applies to non-CC mode systems
  - Fleetint is supported without attestation for Hopper GPUs.
- For NVSwitch systems (driver branch must match installed datacenter driver):
  - Hopper (pre-4th gen NVSwitch): install `nvidia-driver:<driver-branch>/fm`
  - Blackwell (4th gen NVSwitch): install `nvidia-driver-<driver-branch>-open` and `nvlink-<driver-branch>`

Fleet Intelligence Agent package dependencies are available through NVIDIA's CUDA repository. Before installing Fleet Intelligence Agent, add the appropriate NVIDIA CUDA repository for your system:

Install `dnf-plugins-core` first if `dnf config-manager` is not available:

```bash
sudo dnf install -y dnf-plugins-core
```

### RHEL/Rocky/AlmaLinux Systems

```bash
# RHEL/Rocky/AlmaLinux 8 (x86_64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/rhel8/x86_64/cuda-rhel8.repo
sudo dnf clean all

# RHEL/Rocky/AlmaLinux 9 (x86_64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/rhel9/x86_64/cuda-rhel9.repo
sudo dnf clean all

# RHEL/Rocky/AlmaLinux 10 (x86_64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/rhel10/x86_64/cuda-rhel10.repo
sudo dnf clean all

# RHEL/Rocky/AlmaLinux 8 (ARM64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/rhel8/sbsa/cuda-rhel8.repo
sudo dnf clean all

# RHEL/Rocky/AlmaLinux 9 (ARM64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/rhel9/sbsa/cuda-rhel9.repo
sudo dnf clean all

# RHEL/Rocky/AlmaLinux 10 (ARM64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/rhel10/sbsa/cuda-rhel10.repo
sudo dnf clean all
```

### Amazon Linux 2023

```bash
# Amazon Linux 2023 (x86_64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/amzn2023/x86_64/cuda-amzn2023.repo
sudo dnf clean all

# Amazon Linux 2023 (ARM64)
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/amzn2023/sbsa/cuda-amzn2023.repo
sudo dnf clean all
```

After adding the CUDA repository, package dependencies (`datacenter-gpu-manager-4-proprietary`, `nvattest`, and `corelib`) are resolved automatically by `dnf` during Fleet Intelligence Agent installation.

## Install package

Download the package from [Latest stable release](https://github.com/NVIDIA/fleet-intelligence-agent/releases/latest), then install:

```bash
# RHEL/Rocky/AlmaLinux/Amazon Linux (x86_64)
sudo dnf install ./fleetint-VERSION-1.x86_64.rpm

# RHEL/Rocky/AlmaLinux/Amazon Linux (ARM64)
sudo dnf install ./fleetint-VERSION-1.aarch64.rpm
```

Verify:

```bash
sudo fleetint --version
systemctl status fleetintd
```

## Update

Install the newer package version:

```bash
# RHEL/Rocky/AlmaLinux/Amazon Linux (x86_64)
sudo dnf install ./fleetint-VERSION-1.x86_64.rpm

# RHEL/Rocky/AlmaLinux/Amazon Linux (ARM64)
sudo dnf install ./fleetint-VERSION-1.aarch64.rpm
```

The service will automatically restart with the new version.

## Uninstall

```bash
sudo dnf remove fleetint
```

References:
- DCGM: [Installation](https://docs.nvidia.com/datacenter/dcgm/latest/user-guide/getting-started.html#installation)
- Fabric Manager: [Installing Fabric Manager](https://docs.nvidia.com/datacenter/tesla/fabric-manager-user-guide/index.html#installing-fabric-manager)
- NVAT (`nvattest`/`corelib`): [NV Attestation SDK](https://docs.nvidia.com/attestation/nv-attestation-sdk-cpp/latest/overview.html)