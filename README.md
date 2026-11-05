# NVIDIA Fleet Intelligence Agent

![OSS Health: 71.5/100 Proficient](https://img.shields.io/badge/OSS%20Health-71.5%2F100%20Proficient-yellow)

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

- Lightweight: `<500 MB` RAM, `<1%` CPU usage
- Non-intrusive: Read-only operations, no system modifications
- Production-ready: 24/7 datacenter operation

## How It Works

The agent discovers GPU and system components, collects their telemetry and
health signals, and evaluates the resulting component state. It can optionally
expose that state through an HTTP API and Prometheus endpoint, send it to a
remote OpenTelemetry endpoint, or write it to local files. Bare-metal and
Kubernetes deployments use the same collection and evaluation pipeline, with
deployment-specific access to NVIDIA DCGM and host resources.

See [Architecture](docs/architecture.md) for component boundaries, deployment
models, data flow, and failure behavior.

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

## Attribute GPU health to Slurm jobs

Fleet Intelligence Agent can send GPU-to-job identity alongside its normal GPU
telemetry using a stable metric and label contract.

The feature uses scheduler-maintained mapping files inspired by the
[dcgm-exporter HPC job-mapping convention](https://github.com/NVIDIA/dcgm-exporter#how-to-include-hpc-jobs-in-metric-labels).
FleetInt converts each active GPU or MIG-instance/job relationship into a
normalized metric:

```promql
fleetint_gpu_workload_info{uuid="GPU-abc",gpu="0",workload_source="hpc",workload_id="123456"} 1
```

MIG mappings preserve the scheduler-provided GPU instance as the optional
`gpu_instance_id` label.

Workload attribution is opt-in. The cluster administrator configures Slurm
Prolog/Epilog hooks to maintain the files, then configures FleetInt with the
mapping directory and filename identifier. Existing GPU metrics are unchanged;
the identity metric is exported through the agent's normal telemetry path.

See [Send Slurm workload identity to Fleet Intelligence](docs/configuration.md#send-slurm-workload-identity-to-fleet-intelligence)
for setup and verification instructions.

## Supported Platforms

| OS Family    | Supported Versions | Architecture  | GPU                                            |
| ------------ | ------------------ | ------------- | ---------------------------------------------- |
| Ubuntu       | 22.04, 24.04       | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| RHEL         | 8, 9, 10           | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Rocky Linux  | 8, 9, 10           | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| AlmaLinux    | 8, 9, 10           | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Amazon Linux | 2023               | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |

## Quickstart

Install the agent using the guide for your environment:

- [Ubuntu package](docs/install-deb.md)
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

For questions, ideas, and community support, use
[GitHub Discussions](https://github.com/dsx-ai-factory/fleet-intelligence-agent/discussions).

Related: [leptonai/gpud](https://github.com/leptonai/gpud) (upstream dependency)

## License

Apache License 2.0 - see [LICENSE](https://github.com/dsx-ai-factory/fleet-intelligence-agent/blob/main/LICENSE) for details.
