# nvml-mock Helm Chart

Mock GPU infrastructure for Kubernetes testing. Turns any cluster into a
multi-GPU environment using a CGo-based mock NVML library — no physical
NVIDIA hardware required.

## What It Does

Deploys a DaemonSet that creates on every node:
- Mock `libnvidia-ml.so` shared library at `/var/lib/nvml-mock/driver/usr/lib64/`
- Device nodes (`/dev/nvidia0`, `/dev/nvidia1`, ..., `/dev/nvidiactl`)
- GPU configuration at `/var/lib/nvml-mock/driver/config/config.yaml`
- Node label `nvidia.com/gpu.present=true`

Consumers (DRA driver, device plugin) point at `/var/lib/nvml-mock/driver`
as the NVIDIA driver root and discover GPUs through standard NVML APIs.

## Prerequisites

| Tool | Version | Required For |
|------|---------|-------------|
| [Docker](https://docs.docker.com/get-docker/) | 20.10+ | Building the image |
| [Kind](https://kind.sigs.k8s.io/) | 0.20+ | Local cluster (or use your own) |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.31+ | Cluster access |
| [Helm](https://helm.sh/docs/intro/install/) | 3.x | Chart installation |
| [Go](https://go.dev/dl/) | 1.25+ | Building from source |
| [jq](https://jqlang.github.io/jq/) | any | DRA verification only |

**Published image:** The nvml-mock container image is published at
`ghcr.io/nvidia/nvml-mock:latest` and is built automatically on pushes to
`main`. If the image is not yet available (e.g., before the first release),
use "Option B: Build from source" in the quick start sections below.

**Cluster requirements:**
- Privileged pods must be allowed (nvml-mock DaemonSet uses `privileged: true` for `mknod`)
- For DRA: Kubernetes 1.32+ with `DynamicResourceAllocation` feature gate enabled

## Quick Start: Device Plugin on KIND

This path uses the NVIDIA device plugin to expose mock GPUs as
`nvidia.com/gpu` allocatable resources. Tested in CI via
`.github/workflows/nvml-mock-e2e.yaml` → `e2e-device-plugin` job.

### 1. Create a KIND cluster

```bash
kind create cluster --name nvml-mock-test
```

### 2. Load the nvml-mock image

**Option A: Use the published image (recommended)**

```bash
docker pull ghcr.io/nvidia/nvml-mock:latest
kind load docker-image ghcr.io/nvidia/nvml-mock:latest --name nvml-mock-test
```

**Option B: Build from source**

```bash
# From the repository root
docker build -t nvml-mock:local -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:local --name nvml-mock-test
```

### 3. Install nvml-mock

**With published image:**

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --wait --timeout 120s
```

### 4. Verify nvml-mock is running

```bash
kubectl rollout status daemonset/nvml-mock --timeout=60s
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
kind delete cluster --name nvml-mock-test
```

## Quick Start: DRA Driver on KIND

This path uses the NVIDIA DRA (Dynamic Resource Allocation) driver to expose
mock GPUs as ResourceSlices. DRA requires a cluster with specific feature
gates. Tested in CI via `.github/workflows/nvml-mock-e2e.yaml` → `e2e-dra` job.

### 1. Create a KIND cluster with DRA enabled

```bash
kind create cluster --name nvml-mock-dra --config tests/e2e/kind-dra-config.yaml
```

This config enables:
- `DynamicResourceAllocation` feature gate
- CDI (Container Device Interface) in containerd
- `resource.k8s.io/v1beta1` API

### 2. Load the nvml-mock image

**Option A: Use the published image (recommended)**

```bash
docker pull ghcr.io/nvidia/nvml-mock:latest
kind load docker-image ghcr.io/nvidia/nvml-mock:latest --name nvml-mock-dra
```

**Option B: Build from source**

```bash
docker build -t nvml-mock:local -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:local --name nvml-mock-dra
```

### 3. Install nvml-mock

**With published image:**

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --wait --timeout 120s
```

### 4. Verify nvml-mock is running

```bash
kubectl rollout status daemonset/nvml-mock --timeout=60s
```

### 5. Install the DRA driver

```bash
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia
helm repo update

helm install nvidia-dra-driver nvidia/nvidia-dra-driver-gpu \
  --namespace nvidia \
  --create-namespace \
  --set nvidiaDriverRoot=/var/lib/nvml-mock/driver \
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
kind delete cluster --name nvml-mock-dra
```

## Quick Start: GPU Operator on KIND

This path validates the NVIDIA GPU Operator stack (device plugin, GFD, validator)
using CDI mode with mock GPUs. The CI `e2e-gpu-operator` job uses a more complete
setup — see `tests/e2e/kind-gpu-operator-config.yaml` and
`tests/e2e/gpu-operator-values.yaml` for the exact CI configuration.

### 1. Create a KIND cluster

```bash
kind create cluster --name nvml-mock-operator \
  --config tests/e2e/kind-gpu-operator-config.yaml
```

### 2. Install nvidia-container-toolkit in the Kind node

```bash
NODE_CONTAINER=nvml-mock-operator-control-plane
docker exec "$NODE_CONTAINER" bash -c '
  apt-get update -qq
  apt-get install -y -qq curl gpg
  curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
    | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
  curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
    | sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" \
    | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
  apt-get update -qq
  apt-get install -y -qq nvidia-container-toolkit
'
```

### 3. Configure CDI mode

```bash
docker exec "$NODE_CONTAINER" nvidia-ctk runtime configure \
  --runtime=containerd --cdi.enabled --set-as-default
docker exec "$NODE_CONTAINER" bash -c 'cat > /etc/nvidia-container-runtime/config.toml << EOF
[nvidia-container-runtime]
mode = "cdi"

[nvidia-container-runtime.modes.cdi]
default-kind = "nvidia.com/gpu"
spec-dirs = ["/var/run/cdi", "/etc/cdi"]
EOF'
```

### 4. Restart containerd

```bash
docker exec "$NODE_CONTAINER" systemctl restart containerd
sleep 5
```

### 5. Load the nvml-mock image

**Option A: Use the published image (recommended)**

```bash
docker pull ghcr.io/nvidia/nvml-mock:latest
kind load docker-image ghcr.io/nvidia/nvml-mock:latest --name nvml-mock-operator
```

**Option B: Build from source**

```bash
docker build -t nvml-mock:local -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:local --name nvml-mock-operator
```

### 6. Install nvml-mock

**With published image:**

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --wait --timeout 120s
```

### 7. Install the GPU Operator

```bash
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia
helm repo update

helm install gpu-operator nvidia/gpu-operator \
  --namespace gpu-operator \
  --create-namespace \
  -f tests/e2e/gpu-operator-values.yaml \
  --wait --timeout 300s
```

### 8. Verify

```bash
kubectl -n gpu-operator wait --for=condition=ready pod --all --timeout=180s
kubectl get nodes -o jsonpath='{.items[0].status.allocatable.nvidia\.com/gpu}'
```

Expected: `8` (default gpu.count).

### 9. Clean up

```bash
kind delete cluster --name nvml-mock-operator
```

## Quick Start: Multi-Node Heterogeneous GPU Fleet

Simulate a cluster with different GPU types on different nodes by installing
multiple Helm releases with `nodeSelector`. Each release creates its own
DaemonSet, ConfigMap, and RBAC resources. The device plugin (or DRA driver)
discovers different GPU types on each node, enabling heterogeneous scheduling
and topology-aware placement testing.

### 1. Create a Kind cluster with labeled workers

```bash
kind create cluster --name gpu-fleet --config tests/e2e/kind-multi-node-config.yaml
```

This creates 1 control-plane + 2 workers. The workers are pre-labeled
`nvml-mock/profile=a100` and `nvml-mock/profile=t4` respectively.

### 2. Build and load the nvml-mock image

**Option A: Use the published image (recommended)**

```bash
docker pull ghcr.io/nvidia/nvml-mock:latest
kind load docker-image ghcr.io/nvidia/nvml-mock:latest --name gpu-fleet
```

**Option B: Build from source**

```bash
docker build -t nvml-mock:local -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:local --name gpu-fleet
```

### 3. Install nvidia-container-toolkit on workers

```bash
for NODE in $(kind get nodes --name gpu-fleet | grep worker); do
  echo "Installing nvidia-container-toolkit on $NODE..."
  docker exec "$NODE" bash -c '
    apt-get update -qq &&
    apt-get install -y -qq curl gpg > /dev/null &&
    curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey |
      gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg &&
    curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list |
      sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" |
      tee /etc/apt/sources.list.d/nvidia-container-toolkit.list &&
    apt-get update -qq &&
    apt-get install -y -qq nvidia-container-toolkit > /dev/null
  '
  docker exec "$NODE" systemctl restart containerd
done
sleep 5
```

### 4. Install nvml-mock on each node

**With published image:**

```bash
helm install nvml-mock-a100 deployments/nvml-mock/helm/nvml-mock \
  --set gpu.profile=a100 \
  --set gpu.count=4 \
  --set "nodeSelector.nvml-mock/profile=a100" \
  --wait --timeout 120s

helm install nvml-mock-t4 deployments/nvml-mock/helm/nvml-mock \
  --set gpu.profile=t4 \
  --set gpu.count=2 \
  --set "nodeSelector.nvml-mock/profile=t4" \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock-a100 deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=a100 \
  --set gpu.count=4 \
  --set "nodeSelector.nvml-mock/profile=a100" \
  --wait --timeout 120s

helm install nvml-mock-t4 deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=t4 \
  --set gpu.count=2 \
  --set "nodeSelector.nvml-mock/profile=t4" \
  --wait --timeout 120s
```

### 5. Deploy the device plugin

```bash
kubectl apply -f tests/e2e/device-plugin-mock.yaml
kubectl -n kube-system wait --for=condition=ready \
  pod -l name=nvidia-device-plugin-mock --timeout=120s
```

### 6. Verify GPUs on both nodes

```bash
for NODE in $(kubectl get nodes -l nvml-mock/profile -o jsonpath='{.items[*].metadata.name}'); do
  echo -n "$NODE: "
  for i in $(seq 1 12); do
    COUNT=$(kubectl get node "$NODE" -o jsonpath='{.status.allocatable.nvidia\.com/gpu}' 2>/dev/null)
    if [ -n "$COUNT" ] && [ "$COUNT" != "0" ]; then
      echo "${COUNT} GPUs"
      break
    fi
    sleep 5
  done
done
```

Expected: worker with `a100` profile shows `4` GPUs, worker with `t4` profile shows `2` GPUs.

### 7. Clean up

```bash
kind delete cluster --name gpu-fleet
```

## Integration: fake-gpu-operator

[fake-gpu-operator](https://github.com/run-ai/fake-gpu-operator) by Run:ai
simulates GPUs at the Kubernetes API level for scale testing. nvml-mock
can provide driver-level fidelity (real NVML API) on real nodes while
fake-gpu-operator handles KWOK virtual nodes.

### Enable Profile Discovery

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true
```

This creates per-profile ConfigMaps discoverable by fake-gpu-operator:

```bash
kubectl get cm -l run.ai/gpu-profile=true
```

```
NAME                              DATA   AGE
nvml-mock-profile-a100            1      10s
nvml-mock-profile-h100            1      10s
nvml-mock-profile-b200            1      10s
nvml-mock-profile-gb200           1      10s
nvml-mock-profile-l40s            1      10s
nvml-mock-profile-t4              1      10s
```

### Custom Labels

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true \
  --set 'integrations.fakeGpuOperator.profileLabels.my-org/gpu-profile=true'
```

## Configuration

### Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `gpu.profile` | `a100` | GPU profile: `a100`, `h100`, `b200`, `gb200`, `l40s`, or `t4` |
| `gpu.count` | `8` | Number of mock GPUs per node |
| `gpu.customConfig` | `""` | Inline YAML to override profile config entirely |
| `image.repository` | `ghcr.io/nvidia/nvml-mock` | Container image repository |
| `image.tag` | `latest` | Container image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `driverVersion` | `""` (auto) | NVIDIA driver version to mock. When empty, auto-derived from `gpu.profile` (even if `gpu.customConfig` is set): A100/H100/L40S/T4 → `550.163.01`, B200/GB200 → `560.35.03`. For non-standard GPUs configured via `gpu.customConfig`, explicitly set `driverVersion`. |
| `nodeSelector` | `{}` | Node selector for DaemonSet |
| `tolerations` | `[{operator: Exists}]` | Pod tolerations (default: tolerate all) |
| `integrations.fakeGpuOperator.enabled` | `false` | Create per-profile ConfigMaps for fake-gpu-operator discovery |
| `integrations.fakeGpuOperator.profileLabels` | `{"run.ai/gpu-profile": "true"}` | Labels on profile ConfigMaps for discovery |

### GPU Profiles

Built-in profiles provide realistic hardware specs for common data center GPUs.
Select a profile with `--set gpu.profile=<name>`:

```bash
# Deploy as an 8-GPU H100 node
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=h100

# Deploy as a 4-GPU B200 node
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=b200 \
  --set gpu.count=4
```

#### Profile Comparison

| | A100 | H100 | B200 | GB200 | L40S | T4 |
|---|---|---|---|---|---|---|
| **Profile name** | `a100` | `h100` | `b200` | `gb200` | `l40s` | `t4` |
| **Full name** | A100-SXM4-40GB | H100 80GB HBM3 | B200 | GB200 NVL | L40S | Tesla T4 |
| **Architecture** | Ampere | Hopper | Blackwell | Blackwell | Ada Lovelace | Turing |
| **Compute capability** | 8.0 | 9.0 | 10.0 | 10.0 | 8.9 | 7.5 |
| **CUDA cores** | 6,912 | 16,896 | 18,432 | 18,432 | 18,176 | 2,560 |
| **Memory** | 40 GiB HBM2e | 80 GiB HBM3 | 192 GiB HBM3e | 192 GiB HBM3e | 48 GiB GDDR6 | 16 GiB GDDR6 |
| **NVLink** | v3, 12 links | v4, 18 links | v5, 18 links | v5, 18 links | — | — |
| **NVLink BW** | 600 GB/s | 900 GB/s | 1.8 TB/s | 1.8 TB/s | — | — |
| **TDP** | 400W | 700W | 1,000W | 1,000W | 350W | 70W |
| **PCIe** | Gen4 | Gen5 | Gen6 | Gen6 | Gen4 | Gen3 |
| **MIG instances** | 7 | 7 | 7 | 7 | 0 | 0 |
| **Grace CPU** | — | — | — | Yes (NVLink-C2C) | — | — |
| **FP8** | — | Yes | Yes | Yes | Yes | — |
| **FP4** | — | — | Yes | Yes | — | — |
| **Driver version** | 550.163.01 | 550.163.01 | 560.35.03 | 560.35.03 | 550.163.01 | 550.163.01 |

#### When to Use Each Profile

- **`a100`** (default) — broadest compatibility. Most NVIDIA software assumes A100 in docs and examples. Use this unless you need a specific architecture.
- **`h100`** — testing Hopper-specific features: FP8, Transformer Engine, PCIe Gen5, or NVLink v4 topology.
- **`b200`** — testing next-gen Blackwell features: FP4, NVLink v5, PCIe Gen6. Standalone GPU (no Grace CPU).
- **`gb200`** — testing Grace-Blackwell Superchip: NVLink-C2C to Grace CPU, unified memory, and Blackwell features.
- **`l40s`** — testing Ada Lovelace inference workloads: FP8, PCIe Gen4, no NVLink (PCIe-only topology).
- **`t4`** — testing Turing inference GPUs: low power (70W), small memory (16 GiB), 4 GPUs per node.

### Custom Configuration

For GPU types not covered by built-in profiles, provide your own config YAML.

#### Option A: File-based (recommended)

Create a YAML file following the profile format, then pass it at install time:

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
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
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  -f custom-values.yaml
```

#### Writing a Custom Profile

Use an existing profile as your starting point:

```bash
cp deployments/nvml-mock/helm/nvml-mock/profiles/a100.yaml my-custom-gpus.yaml
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
profiles in `deployments/nvml-mock/helm/nvml-mock/profiles/` for complete examples.

## How It Works

The chart deploys:

1. **DaemonSet** — runs a privileged container on each node that:
   - Copies `libnvidia-ml.so.{version}` to the host at `/var/lib/nvml-mock/driver/usr/lib64/`
   - Creates symlinks (`libnvidia-ml.so.1` → `libnvidia-ml.so.{version}`)
   - Creates device nodes (`/dev/nvidia0` ... `/dev/nvidiaX`, `/dev/nvidiactl`)
   - Writes GPU config YAML at `/var/lib/nvml-mock/driver/config/config.yaml`
   - Labels the node `nvidia.com/gpu.present=true`
2. **ConfigMap** — GPU configuration from the selected profile
3. **RBAC** — ServiceAccount with permission to patch node labels

Consumer components (DRA driver, device plugin) mount `/var/lib/nvml-mock`
and use `--nvidia-driver-root=/var/lib/nvml-mock/driver` to discover GPUs
through standard NVML `tryResolveLibrary` paths.

## Known Limitations

The mock NVML library covers the NVML C API surface used by consumers for GPU
discovery and monitoring. Some host-level subsystems are not mocked:

| What's Missing | Affected Consumer | Impact |
|----------------|-------------------|--------|
| `/sys/bus/pci/devices/{busID}` sysfs entries | DRA driver | `dra.k8s.io/pcieRoot` attribute absent from ResourceSlices — **blocks topology-aware scheduling demos** (e.g., GPU + SR-IOV VF alignment) |
| `/sys/bus/pci/devices/{busID}/numa_node` | Device plugin | NUMA-aware topology hints unavailable; scheduling works but NUMA affinity not enforced |
| `/sys/bus/pci/devices/*/vendor,device,class` | NFD (Node Feature Discovery) | PCI feature labels not auto-detected (nvml-mock sets `nvidia.com/gpu.present` and `pci-10de.present` directly) |

### PCIe Root Complex (DRA driver)

When using the DRA driver with nvml-mock, you will see warnings like:

```
W0319 11:41:21.314205       1 nvlib.go:491] error getting PCIe root for device 0,
  continuing without attribute: failed to resolve PCIe Root Complex for PCI Bus ID
  0000:07:00.0: failed to read symlink for PCI Bus ID /sys/bus/pci/devices/0000:07:00.0:
  readlink /sys/bus/pci/devices/0000:07:00.0: no such file or directory
```

**This warning is expected** but has real impact. The DRA driver resolves PCIe
root complex topology by reading sysfs symlinks. Since nvml-mock provides a mock
NVML library (not a full kernel driver), these sysfs entries don't exist. GPUs
appear in ResourceSlices and are fully allocatable, but the
`dra.k8s.io/pcieRoot` topology attribute is absent.

**What this blocks:** DRA topology-aware scheduling that uses `pcieRoot` to
align devices on the same PCIe root complex — for example, co-scheduling a GPU
with an SR-IOV virtual function (VF) from the same root for optimal data path
locality. Without `pcieRoot`, ResourceClaims that express cross-device topology
constraints cannot be validated.

We are actively working on PCIe sysfs simulation to address this gap — see
[#265](https://github.com/NVIDIA/k8s-test-infra/issues/265) for progress.

## Troubleshooting

**ImagePullBackOff**: Verify the image is accessible. The published image is at `ghcr.io/nvidia/nvml-mock:latest`. For local builds, ensure the image is loaded into your cluster (see Quick Start).

**DaemonSet not ready**: Check pod logs: `kubectl logs -l app.kubernetes.io/name=nvml-mock`

**Device plugin shows 0 GPUs**: Verify mock files exist on the node:
```bash
NODE_CONTAINER=$(docker ps --filter name=control-plane -q)
docker exec "$NODE_CONTAINER" ls /var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so.*
docker exec "$NODE_CONTAINER" cat /var/lib/nvml-mock/driver/config/config.yaml
```

**DRA driver pods not ready**: Check DRA logs:
```bash
kubectl -n nvidia logs -l app.kubernetes.io/name=nvidia-dra-driver-gpu --tail=100
```

**PCIe root warnings from DRA driver**: See [Known Limitations](#known-limitations).

**Privileged pods blocked**: Your cluster may have PodSecurity or OPA/Gatekeeper
policies blocking `privileged: true`. KIND allows this by default. For managed
clusters, you may need to create a PodSecurity exception for the nvml-mock namespace.

## Related Documentation

- [Mock NVML Library Documentation](../../../../docs/README.md)
- [E2E Test Workflow](../../../../.github/workflows/nvml-mock-e2e.yaml)
- [KIND DRA Config](../../../../tests/e2e/kind-dra-config.yaml)
- [Device Plugin Mock Manifest](../../../../tests/e2e/device-plugin-mock.yaml)
