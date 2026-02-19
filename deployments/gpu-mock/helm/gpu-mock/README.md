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
kubectl get nodes -o 'custom-columns=NAME:.metadata.name,GPU_PRESENT:.metadata.labels.nvidia\.com/gpu\.present'
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
# DRA pods may take a few seconds to appear after helm install completes
sleep 5
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
| `gpu.profile` | `a100` | GPU profile: `a100`, `h100`, `b200`, or `gb200` |
| `gpu.count` | `8` | Number of mock GPUs per node |
| `gpu.customConfig` | `""` | Inline YAML to override profile config entirely |
| `image.repository` | `ghcr.io/nvidia/gpu-mock` | Container image repository |
| `image.tag` | `latest` | Container image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `driverVersion` | `"550.163.01"` | NVIDIA driver version string to mock |
| `nodeSelector` | `{}` | Node selector for DaemonSet |
| `tolerations` | `[{operator: Exists}]` | Pod tolerations (default: tolerate all) |

### GPU Profiles

Built-in profiles provide realistic hardware specs for common data center GPUs.
Select a profile with `--set gpu.profile=<name>`:

```bash
# Deploy as an 8-GPU H100 node
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock \
  --set image.tag=local \
  --set gpu.profile=h100

# Deploy as a 4-GPU B200 node
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock \
  --set image.tag=local \
  --set gpu.profile=b200 \
  --set gpu.count=4
```

#### Profile Comparison

| | A100 | H100 | B200 | GB200 |
|---|---|---|---|---|
| **Profile name** | `a100` | `h100` | `b200` | `gb200` |
| **Full name** | A100-SXM4-40GB | H100 80GB HBM3 | B200 | GB200 NVL |
| **Architecture** | Ampere | Hopper | Blackwell | Blackwell |
| **Compute capability** | 8.0 | 9.0 | 10.0 | 10.0 |
| **CUDA cores** | 6,912 | 16,896 | 18,432 | 18,432 |
| **Memory** | 40 GiB HBM2e | 80 GiB HBM3 | 192 GiB HBM3e | 192 GiB HBM3e |
| **NVLink** | v3, 12 links | v4, 18 links | v5, 18 links | v5, 18 links |
| **NVLink BW** | 600 GB/s | 900 GB/s | 1.8 TB/s | 1.8 TB/s |
| **TDP** | 400W | 700W | 1,000W | 1,000W |
| **PCIe** | Gen4 | Gen5 | Gen6 | Gen6 |
| **MIG instances** | 7 | 7 | 7 | 7 |
| **Grace CPU** | — | — | — | Yes (NVLink-C2C) |
| **FP8** | — | Yes | Yes | Yes |
| **FP4** | — | — | Yes | Yes |
| **Driver version** | 550.163.01 | 550.163.01 | 560.35.03 | 560.35.03 |

#### When to Use Each Profile

- **`a100`** (default) — broadest compatibility. Most NVIDIA software assumes A100 in docs and examples. Use this unless you need a specific architecture.
- **`h100`** — testing Hopper-specific features: FP8, Transformer Engine, PCIe Gen5, or NVLink v4 topology.
- **`b200`** — testing next-gen Blackwell features: FP4, NVLink v5, PCIe Gen6. Standalone GPU (no Grace CPU).
- **`gb200`** — testing Grace-Blackwell Superchip: NVLink-C2C to Grace CPU, unified memory, and Blackwell features.

### Custom Configuration

For GPU types not covered by built-in profiles, provide your own config YAML.

#### Option A: File-based (recommended)

Create a YAML file following the profile format, then pass it at install time:

```bash
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock \
  --set image.tag=local \
  --set-file gpu.customConfig=my-custom-gpus.yaml
```

#### Option B: Inline values

For small overrides, embed the config directly in a values file:

```yaml
# custom-values.yaml
gpu:
  count: 4
  customConfig: |
    version: "1.0"
    system:
      driver_version: "550.163.01"
      nvml_version: "12.550.163.01"
      cuda_version: "12.4"
      cuda_version_major: 12
      cuda_version_minor: 4
    device_defaults:
      name: "NVIDIA L40S"
      architecture: "ada_lovelace"
      compute_capability:
        major: 8
        minor: 9
      num_gpu_cores: 18176
      memory:
        total_bytes: 48318382080
        reserved_bytes: 536870912
        free_bytes: 47781511168
        used_bytes: 0
    devices:
      - index: 0
        uuid: "GPU-L40S-0000-0000-0000-000000000000"
        minor_number: 0
```

```bash
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock \
  --set image.tag=local \
  -f custom-values.yaml
```

#### Writing a Custom Profile

Use an existing profile as your starting point:

```bash
cp deployments/gpu-mock/helm/gpu-mock/profiles/a100.yaml my-custom-gpus.yaml
```

Key fields to change:

| Field | What to set |
|-------|-------------|
| `device_defaults.name` | GPU name shown in `nvidia-smi` |
| `device_defaults.architecture` | Architecture string (`ampere`, `hopper`, `blackwell`, etc.) |
| `device_defaults.compute_capability` | `major` / `minor` version |
| `device_defaults.num_gpu_cores` | CUDA core count |
| `device_defaults.memory.total_bytes` | Total GPU memory in bytes |
| `devices` | One entry per GPU (match `gpu.count`) with unique UUIDs |
| `nvlink` | NVLink version and links (or omit for PCIe-only GPUs) |

The full YAML schema matches the fields exposed by `nvidia-smi -x -q`. See the built-in
profiles in `deployments/gpu-mock/helm/gpu-mock/profiles/` for complete examples.

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
