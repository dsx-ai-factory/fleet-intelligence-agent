# DEB Installation

## Prerequisites

The Fleet Intelligence Agent DEB package has the following runtime dependencies:

- `datacenter-gpu-manager-4-proprietary` (DCGM)
- `nvattest` (NVIDIA Attestation SDK CLI, NVAT)
- `corelib` (NVAT GPU evidence source dependency)

Before installing Fleet Intelligence Agent, ensure the following prerequisites are met:

- NVIDIA package repository is configured (network or local CUDA repository) so `datacenter-gpu-manager-4-proprietary`, `nvattest`, and `corelib` can be installed
- DCGM HostEngine `4.2.3` or newer
- NVIDIA Datacenter Driver major version `510` or newer is installed
- Install/upgrade commands are run as `root` or with `sudo`
- Attestation for the fleetint use case only supports Blackwell and newer GPUs, and applies to non-CC mode systems
- For NVSwitch systems (driver branch must match installed datacenter driver):
  - Hopper (pre-4th gen NVSwitch): install `cuda-drivers-fabricmanager-<driver-branch>`
  - Blackwell (4th gen NVSwitch): install `nvidia-open-<driver-branch>` and `nvlink5-<driver-branch>`

References:
- DCGM: [Installation](https://docs.nvidia.com/datacenter/dcgm/latest/user-guide/getting-started.html#installation)
- Fabric Manager: [Installing Fabric Manager](https://docs.nvidia.com/datacenter/tesla/fabric-manager-user-guide/index.html#installing-fabric-manager)
- NVAT (`nvattest`/`corelib`): [NV Attestation SDK](https://docs.nvidia.com/attestation/nv-attestation-sdk-cpp/latest/overview.html)

Fleet Intelligence Agent package dependencies (`datacenter-gpu-manager-4-proprietary`, `nvattest`, and `corelib`) are available through NVIDIA's CUDA repository. Before installing Fleet Intelligence Agent, add the appropriate NVIDIA CUDA repository for your system:

```bash
# Ubuntu 22.04 (x86_64)
wget https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/cuda-keyring_1.1-1_all.deb
sudo dpkg -i cuda-keyring_1.1-1_all.deb
sudo apt-get update

# Ubuntu 24.04 (x86_64)
wget https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/x86_64/cuda-keyring_1.1-1_all.deb
sudo dpkg -i cuda-keyring_1.1-1_all.deb
sudo apt-get update

# Ubuntu 22.04 (ARM64)
wget https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/sbsa/cuda-keyring_1.1-1_all.deb
sudo dpkg -i cuda-keyring_1.1-1_all.deb
sudo apt-get update

# Ubuntu 24.04 (ARM64)
wget https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/sbsa/cuda-keyring_1.1-1_all.deb
sudo dpkg -i cuda-keyring_1.1-1_all.deb
sudo apt-get update
```

After adding the CUDA repository, package dependencies (`datacenter-gpu-manager-4-proprietary`, `nvattest`, and `corelib`) are resolved automatically by `apt` during Fleet Intelligence Agent installation.

## Install package

Download the package from [Latest stable release](https://github.com/NVIDIA/fleet-intelligence-agent/releases/latest), then install:

```bash
# Ubuntu (x86_64)
sudo apt install ./fleetint_VERSION_amd64.deb

# Ubuntu (ARM64)
sudo apt install ./fleetint_VERSION_arm64.deb
```

Verify:

```bash
fleetint --version
systemctl status fleetintd
```

## Update

Install the newer package version:

```bash
# Ubuntu (x86_64)
sudo apt install ./fleetint_VERSION_amd64.deb

# Ubuntu (ARM64)
sudo apt install ./fleetint_VERSION_arm64.deb
```

The service will automatically restart with the new version.

## Uninstall

```bash
sudo apt remove fleetint
sudo apt purge fleetint  # Also removes configuration files
```
