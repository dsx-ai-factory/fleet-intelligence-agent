# NVIDIA Fleet Intelligence Agent

NVIDIA Fleet Intelligence Agent - Host agent for GPU telemetry collection and attestation.

For complete documentation on NVIDIA Fleet Intelligence, including the service dashboard, enrollment, and user guide, see the [NVIDIA Fleet Intelligence Documentation](https://docs.nvidia.com/fleet-intelligence/latest/index.html).

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

## Supported Platforms

| OS Family | Supported Versions | Architecture | GPU |
|-----------|--------------------|--------------|-----|
| Ubuntu | 22.04, 24.04 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| RHEL | 8, 9, 10 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Rocky Linux | 8, 9, 10 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| AlmaLinux | 8, 9, 10 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Amazon Linux | 2023 | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |

## Documentation

- [Helm Installation](https://docs.nvidia.com/fleet-intelligence/latest/agent/install-helm.html) - Kubernetes (Helm) installation and troubleshooting
- [DEB Installation](https://docs.nvidia.com/fleet-intelligence/latest/agent/install-deb.html) - Ubuntu package install, update, and uninstall
- [RPM Installation](https://docs.nvidia.com/fleet-intelligence/latest/agent/install-rpm.html) - RHEL/Rocky/Alma/Amazon package install, update, and uninstall
- [Architecture](https://docs.nvidia.com/fleet-intelligence/latest/agent/architecture.html) - Bare metal and Kubernetes architecture, dependencies, and runtime flow
- [Usage](https://docs.nvidia.com/fleet-intelligence/latest/agent/usage.html) - Commands, HTTP API, integration, and troubleshooting
- [Configuration](https://docs.nvidia.com/fleet-intelligence/latest/agent/configuration.html) - Environment variables and service configuration
- [Development](https://docs.nvidia.com/fleet-intelligence/latest/agent/development.html) - Building from source and contributing

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

Related: [leptonai/gpud](https://github.com/leptonai/gpud) (upstream dependency)

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.
