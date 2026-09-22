# Getting Started

Fleet Intelligence Agent can run as a host package on supported Linux systems
or as a DaemonSet in Kubernetes. Choose one installation path, verify the
deployment, and then configure enrollment or another export mode.

## Prerequisites

Before installing the agent, confirm that the target has:

- A supported operating system and GPU architecture from the
  [supported-platform matrix](../README.md#supported-platforms).
- NVIDIA Datacenter Driver major version 510 or newer.
- DCGM HostEngine 4.2.3 or newer.
- Network access to the package or container registry used by the selected
  installation method.

The package and Helm guides list additional platform-specific prerequisites,
including the dependencies needed for attestation.

## Install the agent

Use the guide for your deployment environment:

- [Ubuntu: install the DEB package](install-deb.md)
- [RHEL, Rocky Linux, AlmaLinux, or Amazon Linux: install the RPM package](install-rpm.md)
- [Kubernetes: install the Helm chart](install-helm.md)

## Verify a host installation

Validate the host prerequisites, run a one-time health scan, and confirm that
the service is running:

```bash
sudo fleetint precheck
sudo fleetint scan
sudo fleetint status
```

`precheck` exits nonzero when a required dependency or supported GPU is
missing. `scan` prints a summary of detected health issues. `status` reports the
installed service and monitored component state; it reports `not enrolled`
until the optional backend enrollment step below is complete.

## Enroll a host with the Fleet Intelligence backend

Enrollment is required to export telemetry to the Fleet Intelligence backend.
It is optional when using only local health scans, the local API, or offline
file collection.

In the Fleet Intelligence UI, open the **Add New Node** page and generate an
enrollment token. Save the token in a file readable only by the account running
the enrollment command, then enroll the host:

```bash
sudo chmod 600 /path/to/enrollment-token
sudo fleetint enroll \
  --endpoint=https://data.fleet-intelligence.nvidia.com \
  --token-file=/path/to/enrollment-token
sudo fleetint status
```

Use `--token-file` instead of `--token` to keep the token out of the process
list. Enrollment runs the prerequisite checks again, exchanges the token for
backend credentials, and stores the credentials and export endpoints in the
agent's persistent state. A successful `status` response reports that the
agent is enrolled and lists its configured endpoints.

See [Enroll Agent](usage.md#enroll-agent) for optional node metadata, failure
handling, and unenrollment.

## Verify a Kubernetes installation

Confirm that the DaemonSet is available and that one agent pod is running on
each selected GPU node:

```bash
kubectl get daemonset fleet-intelligence-agent -n fleet-intelligence
kubectl get pods -n fleet-intelligence \
  -l app.kubernetes.io/name=fleet-intelligence-agent
kubectl rollout status daemonset/fleet-intelligence-agent \
  -n fleet-intelligence
```

If you selected another namespace during installation, replace
`fleet-intelligence` in these commands.

For Kubernetes, configure enrollment during Helm installation instead of
running the host enrollment command in a pod. The [Helm installation
guide](install-helm.md#create-enrollment-secret) shows how to store the token in
a Kubernetes Secret and enable the enrollment init container.

## Next steps

- Follow [Usage](usage.md) to run the monitoring server, inspect machine
  information, or collect data offline.
- Follow [Configuration](configuration.md) to select components, change the
  listener, or configure collection and export settings.
- Preserve `/var/lib/fleetint` across restarts and upgrades. It contains the
  node identity and enrollment credentials described in
  [Persistent Local State](../README.md#persistent-local-state).
