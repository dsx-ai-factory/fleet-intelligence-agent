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

## Attribute GPU health to workloads

Fleet Intelligence Agent can send GPU-to-workload identity alongside its normal
GPU telemetry using a stable metric and label contract. Workload attribution is
opt-in and supports two sources:

- Slurm/HPC mapping files maintained by scheduler hooks
- Kubernetes workload names selected from configured pod labels

The integration is based on
[dcgm-exporter's workload-label approaches](https://docs.nvidia.com/datacenter/dcgm/latest/installation/install-dcgm-exporter.html#add-workload-labels),
with cluster-specific identifiers normalized for the Fleet Intelligence backend.

Both sources produce the normalized workload identity metric without adding
workload labels to every GPU telemetry series:

```promql
fleetint_gpu_workload_info{uuid="GPU-abc",gpu="0",workload_source="hpc",workload_id="123456"} 1
```

Kubernetes attribution also reports the concrete pod/container allocation:

```promql
fleetint_gpu_pod_info{uuid="GPU-abc",gpu="0",pod_namespace="ml",pod_name="training-42-worker-0",container_name="trainer"} 1
```

See the [Kubernetes](docs/configuration.md#send-kubernetes-workload-identity-to-fleet-intelligence)
and [Slurm](docs/configuration.md#send-slurm-workload-identity-to-fleet-intelligence)
setup instructions.

## Supported Platforms

| OS Family    | Supported Versions | Architecture  | GPU                                            |
| ------------ | ------------------ | ------------- | ---------------------------------------------- |
| Ubuntu       | 22.04, 24.04       | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| RHEL         | 8, 9, 10           | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Rocky Linux  | 8, 9, 10           | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| AlmaLinux    | 8, 9, 10           | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |
| Amazon Linux | 2023               | x86_64, ARM64 | Ampere, Ada Lovelace, Hopper, Blackwell, Rubin |

## Documentation

Full documentation is available at **[docs.nvidia.com/fleet-intel/agent](https://docs.nvidia.com/fleet-intel/agent)**, including installation guides, configuration reference, architecture overview, and usage examples.

For the broader Fleet Intelligence platform documentation, see [docs.nvidia.com/fleet-intel](https://docs.nvidia.com/fleet-intel).

## Contributing

See [CONTRIBUTING.md](https://github.com/dsx-ai-factory/fleet-intelligence-agent/blob/main/CONTRIBUTING.md) for development setup and guidelines.

Related: [leptonai/gpud](https://github.com/leptonai/gpud) (upstream dependency)

## License

Apache License 2.0 - see [LICENSE](https://github.com/dsx-ai-factory/fleet-intelligence-agent/blob/main/LICENSE) for details.
