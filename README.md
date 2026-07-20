# NVIDIA Fleet Intelligence Agent

NVIDIA Fleet Intelligence Agent - Host agent for GPU telemetry collection and attestation.

Built on top of [leptonai/gpud](https://github.com/leptonai/gpud)

## Overview

**What It Monitors:**
- GPU Metrics: Power, temperature, clocks, utilization, memory, Xid events
- System Metrics: CPU, memory, disk, network usage
- Infrastructure: NVIDIA drivers, CUDA runtime, InfiniBand, containers

**Export Formats:**
- HTTP API Server: Serves data via REST endpoints (JSON) and Prometheus metrics (`/metrics`)
- File Export (Offline Mode): Writes data to local files in CSV or JSON format
- Remote Export: Sends telemetry data to OpenTelemetry-compatible endpoints via OTLP over HTTP

**Key Features:**
- Lightweight: <500MB RAM, <1% CPU usage
- Non-intrusive: Read-only operations, no system modifications
- Production-ready: 24/7 datacenter operation

## Persistent Local State

The agent stores its node identity, enrollment metadata, and retained metrics and
events in `/var/lib/fleetint/fleetint.state` by default. The
`/var/lib/fleetint` directory must use persistent local storage that survives
agent and host restarts, reboots, upgrades, and reinstalls.

Deleting or replacing this directory removes the persisted node identity and
enrollment credentials. The agent may then generate a new node identity and
create a separate record for the same physical node in Fleet Intelligence. When
using custom images, installers, or container deployments, preserve or mount
this directory from persistent host storage and restrict access because it
contains enrollment credentials.

## Supported Platforms

| OS Family | Supported Versions | Architecture | GPU |
|-----------|--------------------|--------------|-----|
| Ubuntu | 22.04, 24.04 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| RHEL | 8, 9, 10 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Rocky Linux | 8, 9, 10 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| AlmaLinux | 8, 9, 10 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Amazon Linux | 2023 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |

## Documentation

**Important:** Documentation links are relative to the branch or tag you are viewing. The default GitHub view uses `main`, which may describe unreleased changes. When installing or upgrading a specific agent version, switch to that version's release tag first.

- [Helm Installation](docs/install-helm.md) - Kubernetes (Helm) installation and troubleshooting
- [DEB Installation](docs/install-deb.md) - Ubuntu package install, update, and uninstall
- [RPM Installation](docs/install-rpm.md) - RHEL/Rocky/Alma/Amazon package install, update, and uninstall
- [Architecture](docs/architecture.md) - Bare metal and Kubernetes architecture, dependencies, and runtime flow
- [Usage](docs/usage.md) - Commands, HTTP API, integration, and troubleshooting
- [Configuration](docs/configuration.md) - Environment variables and service configuration
- [Development](docs/development.md) - Building from source and contributing

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

Related: [leptonai/gpud](https://github.com/leptonai/gpud) (upstream dependency)

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.
