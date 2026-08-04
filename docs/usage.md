# Usage

## Basic Commands

### Quick Health Scan

```bash
sudo fleetint scan
```

Performs a quick health scan of GPUs and system components. Returns immediately with a summary of any detected issues.

**Aliases:** `sudo fleetint check`, `sudo fleetint s`

### Start Monitoring Server

```bash
sudo fleetint run
```

Starts the API server. By default it listens on a Unix socket at `/run/fleetint/fleetint.sock` (access controlled by file permissions). Pass `--listen-address` to switch to TCP.

**Options:**
- `--log-level`: Set logging level (debug, info, warn, error)
- `--listen-address`: Listen address. An absolute path (e.g. `/run/fleetint/fleetint.sock`) creates a Unix socket; a `host:port` value (e.g. `127.0.0.1:15133`) opens a TCP listener. Default: `/run/fleetint/fleetint.sock`. See [Exposing the Agent for External Monitoring](#exposing-the-agent-for-external-monitoring) for details on exposing to Prometheus and other tools.
- `--components`: Enable/disable specific components

### Check Status

```bash
sudo fleetint status
```

Displays the current status of the fleetint service and monitored components.

**Alias:** `sudo fleetint st`

### Machine Information

```bash
sudo fleetint machine-info
```

Shows detailed information about the machine:
- Hardware specifications (CPU, memory, disk)
- GPU configuration and driver version
- CUDA runtime version
- Operating system and kernel version
- Network interfaces
- System UUID and machine ID

### View/Update Metadata

```bash
# View current metadata
sudo fleetint metadata

# Set metadata key-value pair
sudo fleetint metadata --set-key="key" --set-value="value"
```

Used to view or update the agent's metadata store, including remote export configuration.

### Compact State Database

```bash
sudo fleetint compact
```

Compacts the local Fleet Intelligence state database to reduce disk usage.

Requirements:
- `fleetintd` must be stopped before running `compact`
- the agent must not be running (no active listener on the default socket or port)
- the command needs write access to the state database, so package installs typically require `sudo`

Typical workflow:

```bash
sudo systemctl stop fleetintd
sudo fleetint compact
sudo systemctl start fleetintd
```

### Validate Prerequisites

```bash
sudo fleetint precheck
```

Validates the local prerequisites required for installation and enrollment.

**What it checks:**
- NVIDIA GPU presence
- supported GPU architecture (`Ampere`, `Ada Lovelace`, `Hopper`, `Blackwell`, `Rubin`)
- NVIDIA driver major version (`510` or newer)
- DCGM HostEngine reachability
- DCGM HostEngine minimum version (`4.2.3`)
- `nvattest` availability

The command prints each check result and exits non-zero if any hard requirement fails.

**Environment Variables (DCGM connection):**
- `DCGM_URL`: Address of the DCGM HostEngine (default: `localhost`)
- `DCGM_URL_IS_UNIX_SOCKET`: Set to `true` if `DCGM_URL` is a Unix socket path (default: `false`)

### Enroll Agent

```bash
# Pass token directly (visible in process list)
sudo fleetint enroll --endpoint=https://data.fleet-intelligence.nvidia.com --token=<your-sak-token>

# Read token from a file (recommended)
sudo fleetint enroll --endpoint=https://data.fleet-intelligence.nvidia.com --token-file=/path/to/token

# Read token from stdin
echo "$TOKEN" | sudo fleetint enroll --endpoint=https://data.fleet-intelligence.nvidia.com --token-file=-
```

Enrolls the agent with the Fleet Intelligence backend by exchanging a Service Account Key (SAK) token for a JWT token. The JWT token and backend endpoints are stored locally for subsequent data exports.

**Required Options:**
- `--endpoint`: Base endpoint URL for the Fleet Intelligence backend (must use HTTPS)
- `--token`: Service Account Key (SAK) token for authentication (mutually exclusive with `--token-file`)
- `--token-file`: Path to a file containing the SAK token, or `-` to read from stdin (mutually exclusive with `--token`). Preferred over `--token` because it avoids exposing the token in `/proc/<pid>/cmdline`.

One of `--token` or `--token-file` is required.

**Optional Flags:**
- `--force`: Continue enrollment even if `fleetint precheck` fails
- `--node-group`: Optional node group metadata persisted in local agent metadata
- `--compute-zone`: Optional compute zone metadata persisted in local agent metadata

Metadata update behavior for `--node-group` and `--compute-zone`:
- If the flag is omitted, the existing stored value is preserved.
- If the flag is provided, the stored value is overwritten with the provided value.
- Providing an empty value (for example `--node-group=""`) clears the stored value.

**What it does:**
1. Runs the same prerequisite validation as `fleetint precheck`
2. Validates the endpoint URL (must be HTTPS)
3. Makes an enrollment request to exchange the SAK token for a JWT token
4. Stores the JWT token, backend endpoints (metrics, logs, nonce), and optional enrollment metadata (`node_group`, `compute_zone`) in the local metadata database
5. The stored credentials are used automatically by the agent for data export

**Example output:**
```text
Enrollment succeeded
```

**Error handling:**
- precheck failure: Enrollment is blocked unless `--force` is set
- 400: Token format is incorrect
- 401: Token is invalid
- 403: Token is expired or revoked
- 404: Endpoint not found
- 429: Server is rate limiting (retry later)
- 502/503/504: Temporary server issues (retry)

### Unenroll Agent

```bash
sudo fleetint unenroll
```

Removes all enrollment credentials and backend endpoints from the agent. After unenrolling, the agent will no longer export data to the backend until re-enrolled.

**What it does:**
1. Clears the JWT token from local storage
2. Clears the SAK token from local storage
3. Removes all stored backend endpoints (enroll, metrics, logs, nonce)

Use this command when:
- Decommissioning a machine
- Switching to a different backend
- Troubleshooting authentication issues

## Offline Data Collection

For environments without network access or when you need to collect data to files:

```bash
sudo fleetint run --offline-mode --path=/tmp/fleetint --duration=00:05:00 --format=csv
```

**Options:**
- `--offline-mode`: Disable HTTP API server and export to files
- `--path`: Absolute path to the output directory. Must not be inside restricted system directories (`/etc`, `/usr`, `/sys`, `/bin`, `/boot`, `/dev`, `/lib`, `/proc`, `/run`, `/sbin`).
- `--duration`: How long to collect data (format: HH:MM:SS)
- `--format`: Output format (`csv` or `json`)

## Running as a Service

After package installation, the agent runs as a systemd service:

```bash
# Check service status
sudo systemctl status fleetintd

# Start/stop/restart service
sudo systemctl start fleetintd
sudo systemctl stop fleetintd
sudo systemctl restart fleetintd

# View logs
sudo journalctl -u fleetintd -f
```

## HTTP API

The fleetint API server listens on a Unix socket (`/run/fleetint/fleetint.sock`) by default. When started with a TCP address (e.g. `--listen-address=127.0.0.1:15133`), the REST endpoints are also available over plain HTTP.

**Using curl with the default Unix socket** (requires sudo since the socket is owner-only):

```bash
sudo curl --unix-socket /run/fleetint/fleetint.sock http://localhost/healthz
```

**Using curl with TCP** (requires `--listen-address=127.0.0.1:15133`):

```bash
curl http://localhost:15133/healthz
```

The examples below use the TCP form for brevity. For the default socket, prefix with `sudo` and substitute `--unix-socket /run/fleetint/fleetint.sock http://localhost` for the hostname.

### Health Check

```bash
curl http://localhost:15133/healthz
```

Returns the health status of the API server

**Response:**
```json
{
  "status": "ok",
  "version": "v1"
}
```

### Machine Information

```bash
curl http://localhost:15133/machine-info
```

Returns basic machine info

Note: Detailed hardware and GPU information is available via the `fleetint machine-info` CLI command.

### Current Health States

```bash
curl http://localhost:15133/v1/states
```

Returns the current health states of all monitored components

### Component Metrics

```bash
curl http://localhost:15133/v1/metrics
```

Returns metrics data in JSON format from all monitored components

**Query Parameters:**
- `startTime`: Unix timestamp to retrieve metrics since a specific time
- `components`: Filter metrics by component name

**Example:**
```bash
# Get metrics from the last hour
curl "http://localhost:15133/v1/metrics?startTime=$(date -d '1 hour ago' +%s)"

# Get metrics for specific component
curl "http://localhost:15133/v1/metrics?components=accelerator-nvidia-dcgm-thermal"
```

### Component Events

```bash
curl http://localhost:15133/v1/events
```

Returns event data from all monitored components (errors, warnings, state changes)

**Query Parameters:**
- `since`: Unix timestamp to retrieve events since a specific time (default: last hour)
- `components`: Filter events by component name

**Example:**
```bash
# Get events from the last hour
curl "http://localhost:15133/v1/events?since=$(date -d '1 hour ago' +%s)"

# Get events for specific component
curl "http://localhost:15133/v1/events?components=accelerator-nvidia-error-xid"
```

### Prometheus Metrics

```bash
curl http://localhost:15133/metrics
```

Returns metrics in Prometheus exposition format for integration with monitoring systems

## Exposing the Agent for External Monitoring

By default, fleetint uses a Unix socket for security. To allow external monitoring tools like Prometheus to scrape metrics over the network, switch to a TCP listener with the `--listen-address` flag:

```bash
# Expose only on localhost
sudo fleetint run --listen-address=127.0.0.1:15133

# Expose on all interfaces
sudo fleetint run --listen-address=0.0.0.0:15133

# Or expose on a specific IP address
sudo fleetint run --listen-address=192.168.1.100:15133
```

### Prometheus Configuration Example

Configure Prometheus to scrape the exposed endpoint:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'fleetint'
    scrape_interval: 60s
    static_configs:
      - targets:
          - 'gpu-server-1:15133'
          - 'gpu-server-2:15133'
    metrics_path: /metrics
```

**For production deployments** with persistent configuration and security considerations, see the [Configuration Guide](configuration.md#where-to-configure).

## Troubleshooting

### Service won't start

1. Check service status:
   ```bash
   sudo systemctl status fleetintd
   ```

2. View recent logs:
   ```bash
   sudo journalctl -u fleetintd -n 50
   ```

3. Verify NVIDIA drivers are installed (if using GPUs):
   ```bash
   nvidia-smi
   ```

4. Check that the daemon is listening:
   ```bash
   # Default (unix socket)
   sudo ls -la /run/fleetint/fleetint.sock

   # TCP mode
   sudo netstat -tlnp | grep 15133
   ```

### Export issues

1. Check the logs:
   ```bash
   sudo journalctl -u fleetintd -f
   ```

2. Verify your configuration:
   ```bash
   sudo fleetint metadata
   ```

3. Test connectivity to the export endpoint manually with `curl`

4. Check proxy settings in `/etc/default/fleetint` if behind a firewall

### High resource usage

The agent should use &lt;500MB RAM and &lt;1% CPU. Higher usage might indicate:

- Very frequent collection intervals (check `FLEETINT_COLLECT_INTERVAL`)
- Large lookback windows (check `FLEETINT_METRICS_LOOKBACK` and `FLEETINT_EVENTS_LOOKBACK`)
- Many GPUs in the system (resource usage scales with GPU count)
- Debug logging enabled (use `--log-level=warn` or `error`)
