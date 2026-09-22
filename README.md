# NVIDIA Fleet Intelligence Agent

NVIDIA Fleet Intelligence Agent gives GPU infrastructure operators a single,
lightweight agent for collecting hardware health, system telemetry, and
attestation data. It helps teams detect node-level problems and export
consistent diagnostics from bare-metal or Kubernetes environments without
assembling separate collectors for each signal source.

Built on top of [leptonai/gpud](https://github.com/leptonai/gpud).

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

## Quickstart

Install the agent using the guide for your environment:

- [Ubuntu or Debian package](docs/install-deb.md)
- [RHEL, Rocky Linux, AlmaLinux, or Amazon Linux package](docs/install-rpm.md)
- [Kubernetes with Helm](docs/install-helm.md)

After installing a host package, validate the machine and run a one-time health
scan:

```bash
sudo fleetint precheck
sudo fleetint scan
```

To export telemetry to the Fleet Intelligence backend, generate an enrollment
token from the Fleet Intelligence UI and enroll the host:

```bash
sudo fleetint enroll \
  --endpoint=https://data.fleet-intelligence.nvidia.com \
  --token-file=/path/to/enrollment-token
sudo fleetint status
```

Use `--token-file` instead of `--token` to avoid exposing the token in the
process list. Enrollment is optional for local scans and offline collection.
Kubernetes users configure enrollment during Helm installation.

See [Getting Started](docs/getting-started.md) for prerequisites, deployment
verification, and the next configuration steps.

## Documentation

Full documentation is available at **[docs.nvidia.com/fleet-intel/agent](https://docs.nvidia.com/fleet-intel/agent)**, including installation guides, configuration reference, architecture overview, and usage examples.

For the broader Fleet Intelligence platform documentation, see [docs.nvidia.com/fleet-intel](https://docs.nvidia.com/fleet-intel).

- [Getting Started](docs/getting-started.md)
- [Usage](docs/usage.md)
- [Configuration](docs/configuration.md)
- [Architecture](docs/architecture.md)
- [Development](docs/development.md)

## Contributing

See [CONTRIBUTING.md](https://github.com/dsx-ai-factory/fleet-intelligence-agent/blob/main/CONTRIBUTING.md) for development setup and guidelines.

Participation in this project is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

Related: [leptonai/gpud](https://github.com/leptonai/gpud) (upstream dependency)

## License

Apache License 2.0 - see [LICENSE](https://github.com/dsx-ai-factory/fleet-intelligence-agent/blob/main/LICENSE) for details.
