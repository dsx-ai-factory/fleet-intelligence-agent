# Helm Installation

## Prerequisites

- NVIDIA GPU Operator installed with DCGM HostEngine enabled.
- NVIDIA Datacenter Driver major version `510` or newer on the cluster nodes.
- DCGM HostEngine `4.2.3` or newer.
- A DCGM service endpoint reachable from the cluster (defaults to `nvidia-dcgm.gpu-operator.svc:5555`).
- Access to GitHub Container Registry (`ghcr.io`) from your cluster/network.

Set shared variables once for the examples below:

```bash
# Namespace (override if needed)
NS=fleet-intelligence

CHART_VERSION='<version>'  # e.g. 0.3.2 or 0.3.2-rc.1

# DCGM endpoint (usually the default is correct)
DCGM_URL='nvidia-dcgm.gpu-operator.svc:5555'

# Enrollment configuration - Go to the Fleet Intelligence UI to:
#   1. Generate an enrollment token (ENROLL_TOKEN)
#   2. Get the enrollment endpoint URL (ENROLL_ENDPOINT)
ENROLL_ENDPOINT='<enroll-endpoint>'
ENROLL_TOKEN='<enroll-token>'
ENROLL_TOKEN_SECRET_NAME='fleet-intelligence-enroll-token'  # Recommended secret name
```

## Create namespace

```bash
kubectl create namespace "$NS" || true
```

## Create enrollment secret

If you need to enroll nodes, create the token Secret. The secret name should match the `ENROLL_TOKEN_SECRET_NAME` variable set above:

```bash
kubectl create secret generic "$ENROLL_TOKEN_SECRET_NAME" \
  --namespace "$NS" \
  --from-literal=token="$ENROLL_TOKEN"
```

## Install or upgrade

Install:

```bash
helm install fleet-intelligence-agent oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --set enroll.enabled=true \
  --set enroll.endpoint="$ENROLL_ENDPOINT" \
  --set enroll.tokenSecretName="$ENROLL_TOKEN_SECRET_NAME"
```

Install (no enrollment):

```bash
helm install fleet-intelligence-agent oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS"
```

Upgrade:

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --set enroll.enabled=true \
  --set enroll.endpoint="$ENROLL_ENDPOINT" \
  --set enroll.tokenSecretName="$ENROLL_TOKEN_SECRET_NAME"
```

Upgrade (no enrollment):

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS"
```

Upgrade and explicitly remove persisted enrollment metadata:

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --set enroll.enabled=false \
  --set enroll.unenroll=true
```

`enroll.enabled` and `enroll.unenroll` are mutually exclusive. Setting both to `true` causes Helm template rendering to fail.

To use a different image registry/repository, add:

```bash
--set image.repository="<custom-image-repo>"
```

If DCGM is exposed at a different service name or port, set `env.DCGM_URL`:

```bash
--set env.DCGM_URL="$DCGM_URL"
```

## Verifying deployment

After installation, verify the agent is running correctly:

```bash
# Check DaemonSet status
kubectl get daemonset fleet-intelligence-agent -n "$NS"

# Check pods (should be one per GPU node)
kubectl get pods -n "$NS" -l app.kubernetes.io/name=fleet-intelligence-agent

# View pod logs
kubectl logs -n "$NS" -l app.kubernetes.io/name=fleet-intelligence-agent --tail=50

# Watch rollout status
kubectl rollout status daemonset/fleet-intelligence-agent -n "$NS"
```

Check a specific pod in detail:

```bash
# Get a pod name
POD_NAME=$(kubectl get pods -n "$NS" -l app.kubernetes.io/name=fleet-intelligence-agent -o jsonpath='{.items[0].metadata.name}')

# Describe the pod
kubectl describe pod -n "$NS" "$POD_NAME"

# View full logs
kubectl logs -n "$NS" "$POD_NAME" --follow
```

## Troubleshooting

**Pods not starting:**

```bash
# Check pod events
kubectl describe pod -n "$NS" -l app.kubernetes.io/name=fleet-intelligence-agent
```

Common issues:
- **ImagePullBackOff**: Verify nodes can reach `ghcr.io` and the image tag exists
- **Pending**: Check node labels match `nodeSelector` (default: `nvidia.com/gpu.deploy.dcgm=true`)
- **CrashLoopBackOff**: Check logs for errors

**Enrollment failures:**

```bash
# Check init container logs
kubectl logs -n "$NS" "$POD_NAME" -c enroll

# Verify enrollment secret exists
kubectl get secret "$ENROLL_TOKEN_SECRET_NAME" -n "$NS"

# Check secret content (verify token is not empty)
kubectl get secret "$ENROLL_TOKEN_SECRET_NAME" -n "$NS" -o jsonpath='{.data.token}' | base64 -d | wc -c
```

**DCGM connection issues:**

```bash
# Verify DCGM service is accessible
kubectl get svc -n gpu-operator nvidia-dcgm

# Test DCGM connectivity from a pod
kubectl exec -n "$NS" "$POD_NAME" -- curl -v telnet://nvidia-dcgm.gpu-operator.svc:5555

# Check DCGM URL environment variable
kubectl get pods -n "$NS" "$POD_NAME" -o jsonpath='{.spec.containers[0].env[?(@.name=="DCGM_URL")].value}'
```

If DCGM is at a different location, update the URL:

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --reuse-values \
  --set env.DCGM_URL="<dcgm-service>:<port>"
```

## Node Scheduling

**By default**, the agent only deploys to nodes where DCGM is running, using the nodeSelector:

```yaml
nodeSelector:
  nvidia.com/gpu.deploy.dcgm: "true"
```

The agent requires a DCGM HostEngine to collect GPU metrics, so it must co-locate with DCGM. This label is
automatically set by the NVIDIA GPU Operator when DCGM is enabled — no manual labeling is required.

If you need a different node selector or tolerations for GPU taints, you can override them.
The examples below use a generic label to illustrate the override syntax — replace it with the actual label used in your cluster.

Using `--set` (quote the tolerations for zsh, and escape dots in the label key):

```bash
helm upgrade --install fleet-intelligence-agent oci://ghcr.io/nvidia/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --set-string nodeSelector.my-org\\.com/gpu-node=true \
  --set 'tolerations[0].key=nvidia.com/gpu' \
  --set 'tolerations[0].operator=Exists' \
  --set 'tolerations[0].effect=NoSchedule'
```

Using a values file:

```yaml
nodeSelector:
  my-org.com/gpu-node: "true"

tolerations:
  - key: "nvidia.com/gpu"
    operator: "Exists"
    effect: "NoSchedule"
```
