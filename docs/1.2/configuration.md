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

| Environment variable | Description | Default | Bare metal | Kubernetes |
| --- | --- | --- | --- | --- |
| `DCGM_URL` | DCGM HostEngine address used by the agent for DCGM-backed components. | bare metal: `localhost`, Helm chart: `nvidia-dcgm.gpu-operator.svc:5555` | `/etc/default/fleetint` | `env.DCGM_URL` |
| `DCGM_URL_IS_UNIX_SOCKET` | Treat `DCGM_URL` as a Unix socket path instead of a network address. | `false` | `/etc/default/fleetint` | `env.DCGM_URL_IS_UNIX_SOCKET` |
| `FLEETINT_COLLECT_INTERVAL` | Export interval for health data. Valid range: `1s` to `24h`. | `1m` | `/etc/default/fleetint` | `env.FLEETINT_COLLECT_INTERVAL` |
| `FLEETINT_INCLUDE_METRICS` | Include metrics data in export payloads. | `true` | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_METRICS` |
| `FLEETINT_INCLUDE_EVENTS` | Include event data in export payloads. | `true` | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_EVENTS` |
| `FLEETINT_INCLUDE_MACHINEINFO` | Include machine information in export payloads. | `true` | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_MACHINEINFO` |
| `FLEETINT_INCLUDE_HEALTHCHECKS` | Include component data and health details in export payloads. | `true` | `/etc/default/fleetint` | `env.FLEETINT_INCLUDE_HEALTHCHECKS` |
| `FLEETINT_METRICS_LOOKBACK` | Lookback window for metrics included in each export. | `1m` | `/etc/default/fleetint` | `env.FLEETINT_METRICS_LOOKBACK` |
| `FLEETINT_EVENTS_LOOKBACK` | Lookback window for events included in each export. | `1m` | `/etc/default/fleetint` | `env.FLEETINT_EVENTS_LOOKBACK` |
| `FLEETINT_CHECK_INTERVAL` | Health check interval for monitored components. Valid range: `1s` to `24h`. | `1m` | `/etc/default/fleetint` | `env.FLEETINT_CHECK_INTERVAL` |
| `FLEETINT_RETRY_MAX_ATTEMPTS` | Maximum retry attempts for failed exports. Minimum: `0`. | `3` | `/etc/default/fleetint` | `env.FLEETINT_RETRY_MAX_ATTEMPTS` |
| `FLEETINT_ATTESTATION_JITTER_ENABLED` | Enable random startup jitter for attestation scheduling. | `true` | `/etc/default/fleetint` | `env.FLEETINT_ATTESTATION_JITTER_ENABLED` |
| `FLEETINT_ATTESTATION_INTERVAL` | Attestation interval override. | `24h` | `/etc/default/fleetint` | `env.FLEETINT_ATTESTATION_INTERVAL` |
| `HTTP_PROXY` | Proxy URL for outbound HTTP requests. | empty | `/etc/default/fleetint` | `env.HTTP_PROXY` |
| `HTTPS_PROXY` | Proxy URL for outbound HTTPS requests. | empty | `/etc/default/fleetint` | `env.HTTPS_PROXY` |

Notes:

- Duration-valued environment variables use Go duration syntax such as `30s`, `1m`, `10m`, or `24h`.
- These environment variables modify the health exporter configuration used by `fleetint run`.
- `DCGM_URL` and `DCGM_URL_IS_UNIX_SOCKET` configure connectivity to DCGM HostEngine for DCGM-backed components.

### Bare Metal Example

```bash
sudoedit /etc/default/fleetint
```

```bash
FLEETINT_FLAGS="--log-level=info --components=all,-accelerator-nvidia-dcgm-prof"
DCGM_URL="localhost"
DCGM_URL_IS_UNIX_SOCKET="false"
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
  DCGM_URL: "nvidia-dcgm.gpu-operator.svc:5555"
  DCGM_URL_IS_UNIX_SOCKET: "false"
  FLEETINT_COLLECT_INTERVAL: "2m"
  FLEETINT_INCLUDE_EVENTS: "false"
  FLEETINT_CHECK_INTERVAL: "30s"
  HTTPS_PROXY: "http://proxy.example.com:3128"
```

Apply with:

```bash
helm upgrade --install fleet-intelligence-agent \
  oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  -n <namespace> \
  -f values.yaml
```

## Configurable `fleetint run` Flags

These are the `fleetint run` flags supported by the CLI.

| Flag | Description | Default | Bare metal | Kubernetes |
| --- | --- | --- | --- | --- |
| `--log-level` | Log level: `debug`, `info`, `warn`, `error`. | unset by CLI; packaged bare-metal default is `warn` via `FLEETINT_FLAGS` | `FLEETINT_FLAGS="--log-level=..."` | `logLevel` |
| `--log-file` | Log file path. Leave empty to log to stdout/stderr. | empty | `FLEETINT_FLAGS="--log-file=..."` | not exposed by chart by default |
| `--listen-address` | Listen address for the agent API server. An absolute path creates a Unix socket; a `host:port` value opens a TCP listener. | `/run/fleetint/fleetint.sock` | `FLEETINT_FLAGS="--listen-address=..."` | `listenAddress` |
| `--retention-period` | Retention period for stored metrics and events. Minimum `1m`. | `24h` | `FLEETINT_FLAGS="--retention-period=..."` | `retentionPeriod` |
| `--components` | Comma-separated component selection. Use `all`, `*`, explicit names, and `-name` exclusions. | empty flag value, which means enable all components by default | `FLEETINT_FLAGS="--components=..."` | `components` |
| `--offline-mode` | Disable the HTTP API server and write telemetry to files instead. | `false` | `FLEETINT_FLAGS="--offline-mode ..."` | not exposed by chart by default |
| `--path` | Absolute path to the output directory for offline mode. Must not point inside restricted system directories. Required with `--offline-mode`. | empty | `FLEETINT_FLAGS="--path=/path ..."` | not exposed by chart by default |
| `--duration` | Offline-mode collection duration in `HH:MM:SS` format. Required with `--offline-mode`. | empty | `FLEETINT_FLAGS="--duration=00:05:00 ..."` | not exposed by chart by default |
| `--format` | Offline-mode output format: `json` or `csv`. | `json` | `FLEETINT_FLAGS="--format=csv ..."` | not exposed by chart by default |
| `--enable-fault-injection` | Enable the local fault-injection endpoint for testing. | `false` | `FLEETINT_FLAGS="--enable-fault-injection"` | not exposed by chart by default |

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
- `accelerator-nvidia-processes`
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
