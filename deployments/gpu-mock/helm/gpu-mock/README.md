# gpu-mock Helm Chart

Mock GPU infrastructure for Kubernetes testing. Turns any cluster into a
multi-GPU environment using a CGo-based mock NVML library — no physical
NVIDIA hardware required.

## What It Does

Deploys a DaemonSet that creates on every node:
- Mock `libnvidia-ml.so` shared library at `/var/lib/nvidia-mock/driver/usr/lib64/`
- Device nodes (`/dev/nvidia0`, `/dev/nvidia1`, ..., `/dev/nvidiactl`)
- GPU configuration at `/var/lib/nvidia-mock/driver/config/config.yaml`
- Node label `nvidia.com/gpu.present=true`

Consumers (DRA driver, device plugin) point at `/var/lib/nvidia-mock/driver`
as the NVIDIA driver root and discover GPUs through standard NVML APIs.

## Prerequisites

| Tool | Version | Required For |
|------|---------|-------------|
| [Docker](https://docs.docker.com/get-docker/) | 20.10+ | Building the image |
| [Kind](https://kind.sigs.k8s.io/) | 0.20+ | Local cluster (or use your own) |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.31+ | Cluster access |
| [Helm](https://helm.sh/docs/intro/install/) | 3.x | Chart installation |
| [Go](https://go.dev/dl/) | 1.23+ | Building from source |
| [jq](https://jqlang.github.io/jq/) | any | DRA verification only |

**Cluster requirements:**
- Privileged pods must be allowed (gpu-mock DaemonSet uses `privileged: true` for `mknod`)
- For DRA: Kubernetes 1.31+ with `DynamicResourceAllocation` feature gate enabled

## Quick Start: Device Plugin on KIND

This path uses the NVIDIA device plugin to expose mock GPUs as
`nvidia.com/gpu` allocatable resources. Tested in CI via
`.github/workflows/gpu-mock-e2e.yaml` → `e2e-device-plugin` job.

### 1. Create a KIND cluster

```bash
kind create cluster --name gpu-mock-test
```

### 2. Build and load the gpu-mock image

There is no published image yet. Build from source:

```bash
# From the repository root
docker build -t gpu-mock:local -f deployments/gpu-mock/Dockerfile .
kind load docker-image gpu-mock:local --name gpu-mock-test
```

### 3. Install gpu-mock

```bash
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock \
  --set image.tag=local \
  --wait --timeout 120s
```

### 4. Verify gpu-mock is running

```bash
kubectl rollout status daemonset/gpu-mock --timeout=60s
kubectl get nodes -o custom-columns=\
  NAME:.metadata.name,\
  GPU_PRESENT:.metadata.labels.nvidia\.com/gpu\.present
```

Expected: `GPU_PRESENT` shows `true`.

### 5. Deploy the device plugin

```bash
kubectl apply -f tests/e2e/device-plugin-mock.yaml
kubectl -n kube-system wait --for=condition=ready \
  pod -l name=nvidia-device-plugin-mock --timeout=120s
```

### 6. Verify allocatable GPUs

```bash
NODE=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
kubectl get node "$NODE" -o jsonpath='{.status.allocatable.nvidia\.com/gpu}'
```

Expected: `8` (default gpu.count).

### 7. Clean up

```bash
kind delete cluster --name gpu-mock-test
```

## Quick Start: DRA Driver on KIND

This path uses the NVIDIA DRA (Dynamic Resource Allocation) driver to expose
mock GPUs as ResourceSlices. DRA requires a cluster with specific feature
gates. Tested in CI via `.github/workflows/gpu-mock-e2e.yaml` → `e2e-dra` job.

### 1. Create a KIND cluster with DRA enabled

```bash
kind create cluster --name gpu-mock-dra --config tests/e2e/kind-dra-config.yaml
```

This config enables:
- `DynamicResourceAllocation` feature gate
- CDI (Container Device Interface) in containerd
- `resource.k8s.io/v1beta1` API

### 2. Build and load the gpu-mock image

```bash
docker build -t gpu-mock:local -f deployments/gpu-mock/Dockerfile .
kind load docker-image gpu-mock:local --name gpu-mock-dra
```

### 3. Install gpu-mock

```bash
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock \
  --set image.tag=local \
  --wait --timeout 120s
```

### 4. Verify gpu-mock is running

```bash
kubectl rollout status daemonset/gpu-mock --timeout=60s
```

### 5. Install the DRA driver

```bash
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia
helm repo update

helm install nvidia-dra-driver nvidia/nvidia-dra-driver-gpu \
  --namespace nvidia \
  --create-namespace \
  --set nvidiaDriverRoot=/var/lib/nvidia-mock/driver \
  --set gpuResourcesEnabledOverride=true \
  --set resources.computeDomains.enabled=false \
  --wait --timeout 180s
```

### 6. Verify ResourceSlices

```bash
kubectl -n nvidia wait --for=condition=ready pod --all --timeout=120s
kubectl get resourceslices -o json | \
  jq '[.items[].spec.devices // [] | length] | add // 0'
```

Expected: `8` (default gpu.count).

### 7. Clean up

```bash
kind delete cluster --name gpu-mock-dra
```

## Configuration

### Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `gpu.profile` | `a100` | GPU profile: `a100` or `gb200` |
| `gpu.count` | `8` | Number of mock GPUs per node |
| `gpu.customConfig` | `""` | Inline YAML to override profile config |
| `image.repository` | `ghcr.io/nvidia/gpu-mock` | Container image repository |
| `image.tag` | `latest` | Container image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `driverVersion` | `"550.163.01"` | NVIDIA driver version to mock |
| `nodeSelector` | `{}` | Node selector for DaemonSet |
| `tolerations` | `[{operator: Exists}]` | Pod tolerations (default: tolerate all) |

### GPU Profiles

**A100** (`gpu.profile: a100`): NVIDIA A100-SXM4-40GB, Ampere, 40 GiB HBM2e, 12 NVLink v3 links

**GB200** (`gpu.profile: gb200`): NVIDIA GB200 NVL, Blackwell, 192 GiB HBM3e, 18 NVLink v5 links

### Custom Configuration

Override the profile entirely with inline YAML:

```bash
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set-file gpu.customConfig=my-custom-gpus.yaml
```

See `deployments/gpu-mock/helm/gpu-mock/profiles/` for profile format.

## How It Works

The chart deploys:

1. **DaemonSet** — runs a privileged container on each node that:
   - Copies `libnvidia-ml.so.{version}` to the host at `/var/lib/nvidia-mock/driver/usr/lib64/`
   - Creates symlinks (`libnvidia-ml.so.1` → `libnvidia-ml.so.{version}`)
   - Creates device nodes (`/dev/nvidia0` ... `/dev/nvidiaX`, `/dev/nvidiactl`)
   - Writes GPU config YAML at `/var/lib/nvidia-mock/driver/config/config.yaml`
   - Labels the node `nvidia.com/gpu.present=true`
2. **ConfigMap** — GPU configuration from the selected profile
3. **RBAC** — ServiceAccount with permission to patch node labels

Consumer components (DRA driver, device plugin) mount `/var/lib/nvidia-mock`
and use `--nvidia-driver-root=/var/lib/nvidia-mock/driver` to discover GPUs
through standard NVML `tryResolveLibrary` paths.

## Troubleshooting

**ImagePullBackOff**: No published image exists yet. Build from source (see Quick Start).

**DaemonSet not ready**: Check pod logs: `kubectl logs -l app.kubernetes.io/name=gpu-mock`

**Device plugin shows 0 GPUs**: Verify mock files exist on the node:
```bash
NODE_CONTAINER=$(docker ps --filter name=control-plane -q)
docker exec "$NODE_CONTAINER" ls /var/lib/nvidia-mock/driver/usr/lib64/libnvidia-ml.so.*
docker exec "$NODE_CONTAINER" cat /var/lib/nvidia-mock/driver/config/config.yaml
```

**DRA driver pods not ready**: Check DRA logs:
```bash
kubectl -n nvidia logs -l app.kubernetes.io/name=nvidia-dra-driver-gpu --tail=100
```

**Privileged pods blocked**: Your cluster may have PodSecurity or OPA/Gatekeeper
policies blocking `privileged: true`. KIND allows this by default. For managed
clusters, you may need to create a PodSecurity exception for the gpu-mock namespace.

## Related Documentation

- [Mock NVML Library Documentation](../../../../docs/mocknvml/README.md)
- [E2E Test Workflow](../../../../.github/workflows/gpu-mock-e2e.yaml)
- [KIND DRA Config](../../../../tests/e2e/kind-dra-config.yaml)
- [Device Plugin Mock Manifest](../../../../tests/e2e/device-plugin-mock.yaml)
