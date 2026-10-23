# Configuration

This page documents the runtime configuration for `fleetint run`:

- configurable environment variables
- configurable `fleetint run` flags
- how to set them on bare metal and in Kubernetes

## Where to configure

### Bare Metal

Package installs run `fleetint` through the `fleetintd` systemd service:

```ini
ExecStart=/usr/bin/fleetint run $FLEETINT_FLAGS
```

Configure runtime settings in `/etc/default/fleetint`, then restart the service:

```bash
sudo systemctl restart fleetintd
```

- Set environment variables directly in `/etc/default/fleetint`
- Set `fleetint run` flags through `FLEETINT_FLAGS="..."`

### Kubernetes

The Helm chart configures the container entrypoint as `fleetint run` and exposes:

- environment variables under `env.*`
- common `run` flags through dedicated chart values such as `logLevel`, `listenAddress`, and `components`

Apply changes by updating `values.yaml` or using `helm upgrade --set ...`.

## Configurable Environment Variables

These environment variables are read by `fleetint run` at startup.

| Environment variable            | Description                                                                                              | Default                                                                  | Bare metal              | Kubernetes                          |
| ------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ | ----------------------- | ----------------------------------- |
| `DCGM_URL`                      | Explicit DCGM HostEngine address. When set, it takes precedence over `DCGM_URLS`.                         | bare metal: `localhost`, Helm chart: unset                               | `/etc/default/fleetint` | `env.DCGM_URL`                      |
| `DCGM_URLS`                     | Ordered, comma-separated TCP addresses tried when `DCGM_URL` is unset.                                   | bare metal: unset, Helm chart: legacy and DRA GPU Operator services      | optional                | `env.DCGM_URLS`                     |
| `DCGM_URL_IS_UNIX_SOCKET`       | Treat `DCGM_URL` as a Unix socket path instead of a network address.                                     | `false`                                                                  | `/etc/default/fleetint` | `env.DCGM_URL_IS_UNIX_SOCKET`       |
| `MALLOC_ARENA_MAX`              | glibc arena cap to constrain RSS growth for DCGM/cgo-heavy workloads.                                    | `4`                                                                      | `/etc/default/fleetint` | `env.MALLOC_ARENA_MAX`              |
| `FLEETINT_COLLECT_INTERVAL`     | Export interval for health data. Valid range: `1s` to `24h`.                                             | `1m`                                                                     | `/etc/default/fleetint` | `env.FLEETINT_COLLECT_INTERVAL`     |
| `FLEETINT_INCLUDE_METRICS`      | Include metrics data in export payloads.                                                                 | `true`                                                                   | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_METRICS`      |
| `FLEETINT_INCLUDE_EVENTS`       | Include event data in export payloads.                                                                   | `true`                                                                   | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_EVENTS`       |
| `FLEETINT_INCLUDE_MACHINEINFO`  | Include machine information in export payloads.                                                          | `true`                                                                   | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_MACHINEINFO`  |
| `FLEETINT_INCLUDE_HEALTHCHECKS` | Include component data and health details in export payloads.                                            | `true`                                                                   | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_HEALTHCHECKS` |
| `FLEETINT_METRICS_LOOKBACK`     | Lookback window for metrics included in each export.                                                     | `1m`                                                                     | `/etc/default/fleetint` | `env.FLEETINT_METRICS_LOOKBACK`     |
| `FLEETINT_EVENTS_LOOKBACK`      | Lookback window for events included in each export.                                                      | `1m`                                                                     | `/etc/default/fleetint` | `env.FLEETINT_EVENTS_LOOKBACK`      |
| `FLEETINT_CHECK_INTERVAL`       | Standard interval for component checks, metric scrapes, and standard DCGM collection. Valid range: `1s` to `24h`.                              | `30s`                                                                    | `/etc/default/fleetint` | `env.FLEETINT_CHECK_INTERVAL`       |
| `FLEETINT_DCGM_EXTENDED_INTERVAL` | Minimum collection interval for extended DCGM fields. A longer standard interval still applies. Valid range: `1s` to `24h`. | `60s` | `/etc/default/fleetint` | `env.FLEETINT_DCGM_EXTENDED_INTERVAL` |
| `FLEETINT_DCGM_FIELD_PROFILES` | Field assignments, as comma-separated `ID=profile` or `FIRST-LAST=profile` entries. Assignments do not enable fields. | unset (built-in assignments below) | `/etc/default/fleetint` | `env.FLEETINT_DCGM_FIELD_PROFILES` |
| `FLEETINT_DCGM_HEALTH_CHECK_INTERVAL` | DCGM passive-health polling interval. Valid range: `1m` to `24h`.                                  | `1m`                                                                     | `/etc/default/fleetint` | `env.FLEETINT_DCGM_HEALTH_CHECK_INTERVAL` |
| `FLEETINT_RETRY_MAX_ATTEMPTS`   | Maximum retry attempts for failed exports. Minimum: `0`.                                                 | `3`                                                                      | `/etc/default/fleetint` | `env.FLEETINT_RETRY_MAX_ATTEMPTS`   |
| `FLEETINT_WORKLOAD_ATTRIBUTION_SOURCE` | Workload assignment source. Currently supported value: `hpc`. Unset disables workload attribution. | unset | `/etc/default/fleetint` | `workloadAttribution.source` |
| `FLEETINT_HPC_JOB_MAPPING_DIR`  | Absolute path to the scheduler-maintained GPU-to-job mapping directory. Required when the workload source is `hpc`. | unset                                                                    | `/etc/default/fleetint` | `workloadAttribution.hpc.jobMappingDir` |
| `FLEETINT_HPC_GPU_IDENTIFIER`   | Identifier used by HPC mapping filenames: `dcgm_index` or `uuid`. | `dcgm_index` | `/etc/default/fleetint` | `workloadAttribution.hpc.gpuIdentifier` |
| `FLEETINT_INVENTORY_ENABLED`    | Enable or disable the inventory loop.                                                                    | `true`                                                                   | `/etc/default/fleetint` | `env.FLEETINT_INVENTORY_ENABLED`    |
| `FLEETINT_INVENTORY_INTERVAL`   | Inventory loop interval override. Minimum: `5m`.                                                         | `1h`                                                                     | `/etc/default/fleetint` | `env.FLEETINT_INVENTORY_INTERVAL`   |
| `FLEETINT_ATTESTATION_ENABLED`  | Enable or disable the attestation loop.                                                                  | `true`                                                                   | `/etc/default/fleetint` | `env.FLEETINT_ATTESTATION_ENABLED`  |
| `FLEETINT_ATTESTATION_INTERVAL` | Attestation interval override. Minimum: `5m`.                                                            | `24h`                                                                    | `/etc/default/fleetint` | `env.FLEETINT_ATTESTATION_INTERVAL` |
| `HTTP_PROXY`                    | Proxy URL for outbound HTTP requests.                                                                    | empty                                                                    | `/etc/default/fleetint` | `env.HTTP_PROXY`                    |
| `HTTPS_PROXY`                   | Proxy URL for outbound HTTPS requests.                                                                   | empty                                                                    | `/etc/default/fleetint` | `env.HTTPS_PROXY`                   |

Notes:

- Duration-valued environment variables use Go duration syntax such as `30s`, `1m`, `10m`, or `24h`.
- These environment variables modify the telemetry exporter configuration and runtime loop intervals used by `fleetint run`.
- `DCGM_URL` is the explicit endpoint override. When it is unset, `DCGM_URLS`
  supplies ordered TCP fallback endpoints. `DCGM_URL_IS_UNIX_SOCKET` applies
  only to `DCGM_URL`.
- `MALLOC_ARENA_MAX` is a Linux process-level tuning parameter (not a Fleet Intelligence setting) that caps the number of glibc memory arenas to constrain RSS growth in cgo-heavy workloads such as DCGM integration.

### DCGM field collection minimums

Most users need no configuration. The defaults are:

| Field class | Default interval | Setting |
| --- | --- | --- |
| Standard fields | `30s` | `FLEETINT_CHECK_INTERVAL` |
| Extended fields | At least `60s` | `FLEETINT_DCGM_EXTENDED_INTERVAL` |

The **standard interval** is `FLEETINT_CHECK_INTERVAL`. It controls component
checks, metric scrapes, and standard DCGM field collection. It also applies
to non-DCGM components.

The **extended interval setting** is `FLEETINT_DCGM_EXTENDED_INTERVAL`.
Extended fields use the longer of this setting and the standard interval.

For example, these settings use `10s` for standard fields and a `60s` minimum
for extended fields:

```bash
FLEETINT_CHECK_INTERVAL="10s"
FLEETINT_DCGM_EXTENDED_INTERVAL="60s"
```

Or use Helm values:

```yaml
env:
  FLEETINT_CHECK_INTERVAL: "10s"
  FLEETINT_DCGM_EXTENDED_INTERVAL: "60s"
```

| Standard interval | Extended interval setting | Effective extended minimum |
| --- | --- | --- |
| `30s` | `60s` | `60s` |
| `10s` | `60s` | `60s` |
| `90s` | `60s` | `90s` |
| `10s` | `10s` | `10s` |

Both settings use Go duration syntax. The valid range is `1s` to `24h`.
Values can be raised or lowered. The `30s` standard default is an agent
setting, not a recommendation for every DCGM field. Shorter intervals
increase collection frequency and collection overhead.

Reads occur on standard-interval check cycles. They can occur after a deadline. For example,
a `40s` standard interval and a `60s` extended minimum can produce reads every `80s`.
Each DCGM read batch sets its next deadlines after the batch finishes. DCGM polling
waits one standard interval after each cycle. This also enforces the minimum when
sequential device reads have different durations.
Checks and scrapes between reads use the last published values. Missed cycles
do not cause a burst of reads. Failed reads also wait for the next deadline. A stored read error remains until
the affected interval group completes a successful read.
The shared GPU cache recreates watches after reconnect. It clears cached
values and retains read deadlines within the running process.

**Advanced: change field assignments**

By default, field IDs `1200–1219` and `1404–1419`, inclusive, are extended.
All other collected fields are standard. Most users should keep this mapping.

To change selected assignments, use `FLEETINT_DCGM_FIELD_PROFILES`:

```bash
FLEETINT_DCGM_FIELD_PROFILES="1110=extended"
```

In Helm, set `env.FLEETINT_DCGM_FIELD_PROFILES` to the same string. This example
assigns CPU field `1110` to the extended interval. Other fields keep their
default assignments. The assignment does not enable field `1110` or its component.

Assignments use `ID=standard`, `ID=extended`, or an inclusive range such as
`1200-1219=standard`. IDs must be decimal integers from `1` to `65535`.
Omitted IDs keep their built-in assignment. Profile names are case-sensitive.
Unknown names, overlapping entries, reversed ranges, and invalid IDs cause
startup to fail. An unset or empty mapping uses all built-in assignments.
Restart the agent to apply changes.

Only the `standard` and `extended` classes are supported. Customers cannot add
classes or configure individual field durations. Each collection path uses at
most two DCGM field groups. Equal effective intervals share one group. This
bounds group use even when many fields have explicit assignments. DCGM permits
64 field groups in one HostEngine and 128 field IDs per group. DCGM itself and
other clients also use this capacity. Setup rejects an oversized field group
and reports native resource-allocation failures. It does not use a faster
fallback interval. See the [DCGM field-group limits](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-data-structs.html).

These settings apply only to fields selected by enabled components and their
existing hardware checks. They never add a field to collection. They apply to
`fleetint run`, including offline mode. One-time scans use their existing standard
interval and the default extended interval and assignments. Export intervals,
metric names, labels, and payload formats remain unchanged. The shared GPU
cache keeps slower values between reads. Passive DCGM health checks and device
identity queries keep their existing behavior.

### Send Slurm workload identity to Fleet Intelligence

When this feature is enabled, FleetInt sends the current GPU-to-job
relationships through its normal metrics export using a stable metric and
label contract.

Before enabling it:

- Enroll the agent and keep metric export enabled (`FLEETINT_INCLUDE_METRICS=true`,
  the default).
- Configure administrator-owned Slurm Prolog/Epilog hooks to create and remove
  mapping files. FleetInt reads the files; it does not create them.

The file format is based on the
[dcgm-exporter HPC job-mapping convention](https://github.com/NVIDIA/dcgm-exporter#how-to-include-hpc-jobs-in-metric-labels):

- Each regular file represents one whole GPU or MIG GPU instance.
- Each line contains one active Slurm job ID, with at most 16 unique IDs per
  mapping file.
- Removing a job ID or file stops new samples for that relationship on the next
  metrics scrape.

Choose the filename format that the scheduler hook can provide:

| Value | Mapping filename | When to use it |
| --- | --- | --- |
| `dcgm_index` (default) | Numeric DCGM index, such as `0`, or index and GPU-instance ID, such as `2.1` | Use with an existing dcgm-exporter mapping directory, or when the hook's numeric value is verified to match FleetInt's `gpu` label. |
| `uuid` | Whole-GPU UUID, optionally followed by a GPU-instance ID, such as `GPU-2cf69c7e-0d83-51f3-6d41-d3f7a6b08cb7.1` | Use when the hook receives or resolves the allocated physical GPU UUID. This avoids ambiguity between numeric identifier namespaces. |

For a MIG filename, FleetInt validates the physical GPU against live DCGM
inventory and exports the instance suffix as `gpu_instance_id`. FleetInt trusts
the administrator-maintained instance ID; it does not yet query DCGM MIG
inventory to validate or enrich the instance.

Slurm GRES indexes, Linux device minors, and job-local indexes from
`CUDA_VISIBLE_DEVICES` are not guaranteed to equal the DCGM index. With the
GRES `env_uuid` flag, Slurm can provide whole-GPU UUIDs suitable for `uuid`
mode. FleetInt reads only the selected filename format.

Enable the feature on bare metal with environment variables:

```bash
FLEETINT_WORKLOAD_ATTRIBUTION_SOURCE="hpc"
FLEETINT_HPC_JOB_MAPPING_DIR="/path/to/gpu-job-mapping"
FLEETINT_HPC_GPU_IDENTIFIER="uuid"
```

Or configure the Helm chart:

```yaml
workloadAttribution:
  source: hpc
  hpc:
    jobMappingDir: /path/to/gpu-job-mapping
    gpuIdentifier: uuid
```

The mapping directory must be an absolute path and has no default. FleetInt
does not implicitly read dcgm-exporter environment variables or directories.

After the next metrics scrape, verify that FleetInt is reading the mapping:

```bash
sudo curl --unix-socket /run/fleetint/fleetint.sock \
  http://localhost/metrics | grep fleetint_gpu_workload_info
```

The local Prometheus output contains a series like:

```promql
fleetint_gpu_workload_info{gpud_component="workload-attribution",gpu="0",gpu_instance_id="",uuid="GPU-abc",workload_id="123456",workload_source="hpc"} 1
```

A MIG mapping such as `2.1` includes the instance identity:

```promql
fleetint_gpu_workload_info{gpud_component="workload-attribution",gpu="2",gpu_instance_id="1",uuid="GPU-def",workload_id="123456",workload_source="hpc"} 1
```

If the metric appears locally, it is included in the next normal metrics export
to Fleet Intelligence. If it does not appear, check that the mapping directory
contains active files, the configured filename mode matches those files, and
DCGM reports the referenced physical GPU.

FleetInt deliberately emits a separate identity metric instead of adding the
job ID to every GPU metric. Existing GPU telemetry remains unchanged, and job
turnover does not multiply series across every collected metric.

### Bare Metal Example

```bash
sudoedit /etc/default/fleetint
```

```bash
FLEETINT_FLAGS="--log-level=info --components=all,-accelerator-nvidia-dcgm-prof"
DCGM_URL="localhost"
DCGM_URL_IS_UNIX_SOCKET="false"
MALLOC_ARENA_MAX="4"
FLEETINT_COLLECT_INTERVAL="2m"
FLEETINT_INCLUDE_EVENTS="false"
FLEETINT_CHECK_INTERVAL="30s"
HTTPS_PROXY="http://proxy.example.com:3128"
```

```bash
sudo systemctl restart fleetintd
```

### Kubernetes Example

```yaml
logLevel: info
listenAddress: 0.0.0.0:15133
retentionPeriod: 24h
components: all,-accelerator-nvidia-dcgm-prof

env:
  DCGM_URL: ""
  DCGM_URLS: "nvidia-dcgm.gpu-operator.svc:5555,nvidia-dcgm-dra.gpu-operator.svc:5555"
  DCGM_URL_IS_UNIX_SOCKET: "false"
  MALLOC_ARENA_MAX: "4"
  FLEETINT_COLLECT_INTERVAL: "2m"
  FLEETINT_INCLUDE_EVENTS: "false"
  FLEETINT_CHECK_INTERVAL: "30s"
  FLEETINT_INVENTORY_INTERVAL: "5m"
  HTTPS_PROXY: "http://proxy.example.com:3128"
```

Apply with:

```bash
helm upgrade --install fleet-intelligence-agent \
  oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
  -n <namespace> \
  -f values.yaml
```

## Configurable `fleetint run` Flags

These are the `fleetint run` flags supported by the CLI.

| Flag                       | Description                                                                                                                                  | Default                                                        | Bare metal                                   | Kubernetes                      |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- | -------------------------------------------- | ------------------------------- |
| `--log-level`              | Log level: `debug`, `info`, `warn`, `error`.                                                                                                 | `warn`                                                         | `FLEETINT_FLAGS="--log-level=..."`           | `logLevel`                      |
| `--log-file`               | Log file path. Leave empty to log to stdout/stderr.                                                                                          | empty                                                          | `FLEETINT_FLAGS="--log-file=..."`            | not exposed by chart by default |
| `--listen-address`         | Listen address for the agent API server. An absolute path creates a Unix socket; a `host:port` value opens a TCP listener.                   | `/run/fleetint/fleetint.sock`                                  | `FLEETINT_FLAGS="--listen-address=..."`      | `listenAddress`                 |
| `--retention-period`       | Retention period for stored metrics and events. Minimum `1m`.                                                                                | `24h`                                                          | `FLEETINT_FLAGS="--retention-period=..."`    | `retentionPeriod`               |
| `--workload-attribution-source` | Workload assignment source. Currently supported value: `hpc`.                                                                          | unset (disabled)                                               | `FLEETINT_FLAGS="--workload-attribution-source=hpc ..."` | `workloadAttribution.source` |
| `--hpc-job-mapping-dir`    | Absolute path to the scheduler-maintained GPU-to-job mapping directory. Required when the source is `hpc`.                                  | unset                                                          | `FLEETINT_FLAGS="--hpc-job-mapping-dir=..."` | `workloadAttribution.hpc.jobMappingDir` |
| `--hpc-gpu-identifier`     | Identifier used by HPC mapping filenames: `dcgm_index` or `uuid`.                                                                          | `dcgm_index`                                                   | `FLEETINT_FLAGS="--hpc-gpu-identifier=uuid ..."` | `workloadAttribution.hpc.gpuIdentifier` |
| `--components`             | Comma-separated component selection. Use `all`, `*`, explicit names, and `-name` exclusions.                                                 | empty flag value, which means enable all components by default | `FLEETINT_FLAGS="--components=..."`          | `components`                    |
| `--offline-mode`           | Disable the HTTP API server and write telemetry to files instead.                                                                            | `false`                                                        | `FLEETINT_FLAGS="--offline-mode ..."`        | not exposed by chart by default |
| `--path`                   | Absolute path to the output directory for offline mode. Must not point inside restricted system directories. Required with `--offline-mode`. | empty                                                          | `FLEETINT_FLAGS="--path=/path ..."`          | not exposed by chart by default |
| `--duration`               | Offline-mode collection duration in `HH:MM:SS` format. Required with `--offline-mode`.                                                       | empty                                                          | `FLEETINT_FLAGS="--duration=00:05:00 ..."`   | not exposed by chart by default |
| `--format`                 | Offline-mode output format: `json` or `csv`.                                                                                                 | `json`                                                         | `FLEETINT_FLAGS="--format=csv ..."`          | not exposed by chart by default |
| `--enable-fault-injection` | Enable the local fault-injection endpoint for testing.                                                                                       | `false`                                                        | `FLEETINT_FLAGS="--enable-fault-injection"`  | `enableFaultInjection` |

## Component Selection

Use `--components` to control which monitoring components are enabled.

Examples:

```bash
# Enable all components
fleetint run --components=all

# Enable only specific components
fleetint run --components=accelerator-nvidia-dcgm-thermal,accelerator-nvidia-dcgm-utilization,cpu,memory

# Start from the default set and disable one component
fleetint run --components=all,-accelerator-nvidia-dcgm-prof
```

Rules:

- `all`, `*`, or an empty component list enables all components.
- `all,-<component-name>` starts with all components, then disables specific ones.
- An explicit comma-separated list enables only the named components.
- A non-matching explicit value effectively disables all components.

Available component names:

**NVIDIA GPU components**

- `accelerator-nvidia-infiniband`
- `accelerator-nvidia-nccl`
- `accelerator-nvidia-peermem`
- `accelerator-nvidia-persistence-mode`
- `accelerator-nvidia-error-sxid`
- `accelerator-nvidia-error-xid`

**NVIDIA GPU DCGM components**

- `accelerator-nvidia-dcgm-clock`
- `accelerator-nvidia-dcgm-cpu`
- `accelerator-nvidia-dcgm-inforom`
- `accelerator-nvidia-dcgm-mem`
- `accelerator-nvidia-dcgm-nvlink`
- `accelerator-nvidia-dcgm-nvswitch`
- `accelerator-nvidia-dcgm-pcie`
- `accelerator-nvidia-dcgm-power`
- `accelerator-nvidia-dcgm-prof`
- `accelerator-nvidia-dcgm-thermal`
- `accelerator-nvidia-dcgm-utilization`

**System components**

- `cpu`
- `disk`
- `memory`
- `network-ethernet`
- `os`
- `library`

## Verify Effective Configuration

### Bare Metal

```bash
sudo cat /etc/default/fleetint
sudo systemctl status fleetintd
sudo journalctl -u fleetintd -f
```

### Kubernetes

```bash
helm get values fleet-intelligence-agent -n <namespace>
kubectl get daemonset fleet-intelligence-agent -n <namespace> -o yaml
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-intelligence-agent --tail=100
```
