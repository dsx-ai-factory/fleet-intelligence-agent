# Helm Installation

## Prerequisites

- NVIDIA GPU Operator installed with standalone DCGM HostEngine enabled
  (`dcgm.enabled=true`). GPUCluster otherwise runs only the exporter's embedded
  HostEngine, which the agent cannot connect to.
- NVIDIA Datacenter Driver major version `510` or newer on the cluster nodes.
- DCGM HostEngine `4.2.3` or newer.
- A DCGM service endpoint reachable from the cluster. The chart automatically
  tries the ClusterPolicy service (`nvidia-dcgm`) and the DRA GPUCluster service
  (`nvidia-dcgm-dra`) in the `gpu-operator` namespace.
- Access to GitHub Container Registry (`ghcr.io`) from your cluster/network.

Set shared variables once for the examples below:

```bash
# Namespace (override if needed)
NS=fleet-intelligence

CHART_VERSION='<version>'  # e.g. 0.3.2 or 0.3.2-rc.1

# Optional explicit DCGM endpoint override
DCGM_URL='nvidia-dcgm.gpu-operator.svc:5555'

# Enrollment configuration - Go to the Fleet Intelligence UI to:
#   1. Generate an enrollment token (ENROLL_TOKEN)
#   2. Get the enrollment endpoint URL (ENROLL_ENDPOINT)
#      Note: Default is https://data.fleet-intelligence.nvidia.com
#            Check the 'Add New Node' page for your organization. 
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
printf '%s' "$ENROLL_TOKEN" | kubectl create secret generic "$ENROLL_TOKEN_SECRET_NAME" \
  --namespace "$NS" \
  --from-file=token=/dev/stdin
```

## Install or upgrade

Install:

```bash
helm install fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --set enroll.enabled=true \
  --set enroll.endpoint="$ENROLL_ENDPOINT" \
  --set enroll.tokenSecretName="$ENROLL_TOKEN_SECRET_NAME"
```

Install (no enrollment):

```bash
helm install fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS"
```

Upgrade:

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --set enroll.enabled=true \
  --set enroll.endpoint="$ENROLL_ENDPOINT" \
  --set enroll.tokenSecretName="$ENROLL_TOKEN_SECRET_NAME"
```

Optional: include node metadata during automatic enrollment:

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --set enroll.enabled=true \
  --set enroll.endpoint="$ENROLL_ENDPOINT" \
  --set enroll.tokenSecretName="$ENROLL_TOKEN_SECRET_NAME" \
  --set-string enroll.nodeGroup="prod-a" \
  --set-string enroll.computeZone="us-east-1c"
```

Notes:
- Omit `enroll.nodeGroup` / `enroll.computeZone` keys to omit the flags and preserve existing stored values.
- Set either value to an empty string to clear it (for example: `--set-string enroll.nodeGroup=""`).

Upgrade (no enrollment):

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS"
```

Upgrade and explicitly remove persisted enrollment metadata:

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
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

The default endpoint list supports both GPU Operator workflows:

- `ClusterPolicy`: `nvidia-dcgm.gpu-operator.svc:5555`
- DRA `GPUCluster`: `nvidia-dcgm-dra.gpu-operator.svc:5555`

If DCGM is exposed at a different namespace, service name, or port, set the
explicit `env.DCGM_URL` override:

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
- **Pending**: Check the node has either `nvidia.com/gpu.deploy.dcgm=true` or
  `nvidia.com/gpu.deploy.dcgm-dra=true`
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
# Verify the ClusterPolicy or GPUCluster DCGM service is present
kubectl get svc -n gpu-operator \
  -l 'app in (nvidia-dcgm,nvidia-dcgm-dra)'

# Test the ClusterPolicy endpoint from a pod; use nvidia-dcgm-dra for GPUCluster
kubectl exec -n "$NS" "$POD_NAME" -- curl -v telnet://nvidia-dcgm.gpu-operator.svc:5555

# Check DCGM endpoint environment variables
kubectl get pods -n "$NS" "$POD_NAME" -o jsonpath='{.spec.containers[0].env}'
```

If DCGM is at a different location, update the URL:

```bash
helm upgrade fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
  --version "$CHART_VERSION" \
  --namespace "$NS" \
  --reuse-values \
  --set env.DCGM_URL="<dcgm-service>:<port>"
```

## Node Scheduling

**By default**, the agent deploys to nodes where either GPU Operator workflow
runs standalone DCGM. The two node affinity terms are alternatives:

```yaml
nodeSelectorTerms:
  - matchExpressions:
      - key: nvidia.com/gpu.deploy.dcgm
        operator: In
        values: ["true"]
  - matchExpressions:
      - key: nvidia.com/gpu.deploy.dcgm-dra
        operator: In
        values: ["true"]
```

The agent requires a DCGM HostEngine to collect GPU metrics, so it must
co-locate with DCGM. GPU Operator applies the appropriate label when standalone
DCGM is enabled.

If you need custom scheduling, set either `nodeSelector` or `affinity`; either
one replaces the built-in compatibility affinity. You can also configure
tolerations for GPU taints.
The examples below use a generic label to illustrate the override syntax — replace it with the actual label used in your cluster.

Using `--set` (quote the tolerations for zsh, and escape dots in the label key):

```bash
helm upgrade --install fleet-intelligence-agent oci://ghcr.io/dsx-ai-factory/charts/fleet-intelligence-agent \
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
