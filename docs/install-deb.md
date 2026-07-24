# DEB Installation

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
- Attestation for the fleetint use case only supports Blackwell and newer GPUs, and applies to non-CC mode systems
  - Fleetint is supported without attestation for Hopper GPUs.
- For NVSwitch systems (driver branch must match installed datacenter driver):
  - Hopper (pre-4th gen NVSwitch): install `cuda-drivers-fabricmanager-<driver-branch>`
  - Blackwell (4th gen NVSwitch): install `nvidia-open-<driver-branch>` and `nvlink5-<driver-branch>`

Fleet Intelligence Agent package dependencies are available through NVIDIA's CUDA repository. Before installing Fleet Intelligence Agent, add the appropriate NVIDIA CUDA repository for your system:

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

Download the package, `fleet-intelligence.pub.asc`, `checksums.txt`,
`checksums.txt.asc`, and `verify-linux-package-signature.sh` from the
[latest stable release](https://github.com/NVIDIA/fleet-intelligence-agent/releases/latest).
Confirm that the public key fingerprint is:

```text
FE0C 8B74 CA66 357C 13BE 197D CCE3 C963 0871 99B3
```

Verify the embedded package signature before installation:

```bash
gpg --import fleet-intelligence.pub.asc
gpg --verify checksums.txt.asc checksums.txt
sha256sum -c --ignore-missing checksums.txt
chmod 755 verify-linux-package-signature.sh
./verify-linux-package-signature.sh \
  fleetint_VERSION_amd64.deb \
  fleet-intelligence.pub.asc
```

Install the verified package:

```bash
# Ubuntu (x86_64)
sudo apt install ./fleetint_VERSION_amd64.deb

# Ubuntu (ARM64)
sudo apt install ./fleetint_VERSION_arm64.deb
```

Verify:

```bash
sudo fleetint --version
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

References:
- DCGM: <https://docs.nvidia.com/datacenter/dcgm/latest/user-guide/getting-started.html#installation>
- Fabric Manager: <https://docs.nvidia.com/datacenter/tesla/fabric-manager-user-guide/index.html#installing-fabric-manager>
- NVAT (`nvattest`/`corelib`): <https://docs.nvidia.com/attestation/nv-attestation-sdk-cpp/latest/overview.html>
