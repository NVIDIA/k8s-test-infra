# nvml-mock Helm Chart

Mock GPU infrastructure for Kubernetes testing. Turns any cluster into a
multi-GPU environment using a CGo-based mock NVML library — no physical
NVIDIA hardware required.

## What It Does

Deploys a DaemonSet that creates on every node:
- Mock `libnvidia-ml.so` shared library at `/var/lib/nvml-mock/driver/usr/lib64/`
- Mock device nodes at `/var/lib/nvml-mock/driver/dev/nvidia{N,ctl,-uvm,-uvm-tools}` (consumers see them at `/dev/nvidia*` via CDI bind-mount)
- GPU configuration at `/var/lib/nvml-mock/driver/config/config.yaml`
- Node label `nvidia.com/gpu.present=true`
- A fake InfiniBand sysfs tree at `/var/lib/nvml-mock/ib/sys/class/infiniband/...`
  paired with `libibmocksys.so` (`LD_PRELOAD`) so real `ibstat`, `ibstatus`,
  `iblinkinfo`, ... read mock HCAs
- A fake PCI sysfs tree at `/var/lib/nvml-mock/sys/bus/pci/devices/...` (symlinks
  into `/var/lib/nvml-mock/sys/devices/pciDDDD:BB/...`) so topology-aware
  consumers (NVIDIA DRA driver `dra.k8s.io/pcieRoot`, NUMA-aware schedulers)
  resolve PCIe root complex via a standard `readlink()`

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
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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
helm install nvml-mock-a100 oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set gpu.profile=a100 \
  --set gpu.count=4 \
  --set "nodeSelector.nvml-mock/profile=a100" \
  --wait --timeout 120s

helm install nvml-mock-t4 oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set gpu.profile=t4 \
  --set gpu.count=2 \
  --set "nodeSelector.nvml-mock/profile=t4" \
  --wait --timeout 120s
```

**With locally built image:**

```bash
helm install nvml-mock-a100 oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=a100 \
  --set gpu.count=4 \
  --set "nodeSelector.nvml-mock/profile=a100" \
  --wait --timeout 120s

helm install nvml-mock-t4 oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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
nvml-mock-profile-gb300           1      10s
nvml-mock-profile-l40s            1      10s
nvml-mock-profile-t4              1      10s
```

### Custom Labels

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true \
  --set 'integrations.fakeGpuOperator.profileLabels.my-org/gpu-profile=true'
```

## InfiniBand mocking

Each profile carries an `infiniband:` block alongside the GPU config. When the
DaemonSet starts, `mock-ib` reads it and writes a fake sysfs tree at
`/var/lib/nvml-mock/ib/sys/class/infiniband/...`. Inside the container, three
LD_PRELOAD shims cooperate (preload order
`libibmockumad.so:libibmockverbs.so:libibmocksys.so`):

- `libibmocksys.so` rewrites every access to `/sys/class/infiniband*`,
  `/sys/class/infiniband_mad/`, `/sys/class/infiniband_verbs/` and
  `/dev/infiniband` so sysfs-driven tools read from the rendered tree.
- `libibmockumad.so` proxies `libibumad`'s `umad_send` / `umad_recv` to the
  in-pod `mock-ib` daemon (Unix socket) which handles SA path queries,
  ibping echoes, and SMP synthesis for `iblinkinfo`.
- `libibmockverbs.so` proxies open/read/write on `/dev/infiniband/uverbsN`
  so `libibverbs` consumers can enumerate HCAs.

```bash
POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')

# sysfs / libibumad (always works):
kubectl exec "$POD" -- ibstat
kubectl exec "$POD" -- ibstatus

# libibverbs enumeration (modalias matches libmlx5's match table):
kubectl exec "$POD" -- ibv_devinfo -l
kubectl exec "$POD" -- ibv_devices

# Subnet management direct-route walk (cross-node fabric scan):
kubectl exec "$POD" -- iblinkinfo
```

Full per-device `ibv_devinfo` (without `-l`) intentionally is not supported:
after libibverbs claims the device, `libmlx5`'s `verbs_open_device` issues
real uverbs `ioctl()`s that a userspace `LD_PRELOAD` shim cannot fake. The
same port-level information (state, phys state, GID, LID, rate, link layer)
is available through `ibstatus`, which reads it from the rendered sysfs
tree.

### Defaults per profile

| Profile | Enabled | HCA | Speed | HCAs per GPU |
|---|---|---|---|---|
| `a100`  | yes | ConnectX-6 (`MT4123`) | HDR 200 Gb/s | 1 |
| `h100`  | yes | ConnectX-7 (`MT4129`) | NDR 400 Gb/s | 1 |
| `b200`  | yes | ConnectX-7 (`MT4129`) | NDR 400 Gb/s | 1 |
| `gb200` | yes | ConnectX-7 (`MT4129`) | NDR 400 Gb/s | 1 |
| `l40s`  | no  | — | — | — |
| `t4`    | no  | — | — | — |

### `infiniband:` block schema

| Field | Default | Notes |
|---|---|---|
| `enabled` | `false` | Must be `true` to render any tree |
| `hca_type` | `MT4129` | Shows up as `CA type` in `ibstat` output |
| `fw_version` | `28.39.2048` | Firmware version |
| `hw_rev` | `0x0` | Hardware revision |
| `board_id` | `MT_0000000838` | Mellanox board ID |
| `link_layer` | `InfiniBand` | `InfiniBand` or `Ethernet` |
| `rate_gbps` | `400` | One of `100` (EDR), `200` (HDR), `400` (NDR), `800` (XDR) |
| `port_state` | `ACTIVE` | `DOWN`, `INIT`, `ARMED`, `ACTIVE`, `ACTIVE_DEFER` |
| `phys_state` | `LinkUp` | `Disabled`, `Polling`, `Training`, `LinkUp`, ... |
| `hcas_per_gpu` | `1` | Total HCAs = `gpu.count * hcas_per_gpu` |
| `hca_count` | `0` | If non-zero, used instead of `gpu.count * hcas_per_gpu` |
| `guid_prefix` | `a088c20300ab` | First 12 hex digits of every node/port GUID; HCA index appended |
| `node_desc_template` | `{node_name} mlx5_{idx}` | `{node_name}` and `{idx}` are interpolated |

### Disable IB on a profile

Set `enabled: false` either inline:

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set gpu.profile=h100 \
  --set-string 'gpu.customConfig=infiniband: { enabled: false }'
```

…or via a custom profile file (preferred for full control).

## PCIe topology mocking

Each profile carries a `pcie_topology:` block describing the host's PCI
root-complex layout. When the DaemonSet starts, `render-pci-sysfs` reads
it and writes a fake sysfs tree at `/var/lib/nvml-mock/sys/...` matching
what real Linux kernels expose. Topology-aware consumers (NVIDIA DRA
driver, device plugins computing NUMA hints) resolve "which PCIe root
complex a GPU lives on" via a standard `readlink()` + path parse against
the rendered tree:

```bash
$ readlink /var/lib/nvml-mock/sys/bus/pci/devices/0000:07:00.0
../../../devices/pci0000:00/0000:07:00.0

$ cat /var/lib/nvml-mock/sys/devices/pci0000:00/0000:07:00.0/numa_node
0
```

### Defaults per profile

| Profile | Root complexes | NUMA nodes | Devices per root |
|---|---|---|---|
| `a100`  | 2 (`pci0000:00`, `pci0000:80`) | 2 (dual EPYC) | 4 |
| `h100`  | 2 (`pci0000:00`, `pci0000:80`) | 2 (dual socket) | 4 |
| `b200`  | 2 (`pci0000:00`, `pci0000:80`) | 2 (dual socket) | 4 |
| `gb200` | 4 (`pci0000:00`, `:40`, `:80`, `:c0`) | 4 (per Grace pair) | 2 |
| `l40s`  | 2 (`pci0000:00`, `pci0000:80`) | 2 (dual socket) | 4 |
| `t4`    | 1 (`pci0000:00`) | 1 | 4 |

### `pcie_topology:` block schema

```yaml
pcie_topology:
  root_complexes:
    - id: "pci0000:00"            # sysfs root-complex dir, format "pciDDDD:BB"
      numa_node: 0                 # numa_node value for every child device
      devices:
        - "0000:07:00.0"           # canonical 4-digit-domain BDF
        - "0000:0F:00.0"
    - id: "pci0000:80"
      numa_node: 1
      devices:
        - "0000:87:00.0"
        - "0000:90:00.0"
```

`render-pci-sysfs` validates the block at startup and fails the
DaemonSet under `set -e` if it finds a typo:

- Every BDF listed under a root complex must also appear in `devices[]`.
- Each BDF may belong to at most one root complex.
- Root complex IDs must match `pciDDDD:BB`.
- BDFs must use 4-digit-domain form (`DDDD:BB:DD.F`); the legacy NVML
  `busIdLegacy` 8-digit form is rejected.

If a profile omits `pcie_topology:` entirely the renderer falls back to
a flat single-root layout (every device under `pci0000:00`, NUMA 0).

### Cross-node `ibping`

Sysfs mocking alone lets `ibstat` / `iblinkinfo` work, but real `ibping`
needs UMAD I/O. The chart always preloads `libibmockumad.so` alongside
`libibmocksys.so`, starts `mock-ib` in each pod, and exposes a headless
Service on port 18515 for TCP fabric relay between nvml-mock pods.

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set gpu.profile=a100 \
  --set gpu.count=2 \
  --wait --timeout 120s
```

On a multi-node cluster, pick two nvml-mock pods on different nodes. Read
the server LID from sysfs, start a server, and ping by LID from the client:

```bash
SERVER_POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')
CLIENT_POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[1].metadata.name}')

LID=$(kubectl exec "$SERVER_POD" -- sh -c \
  "tr -d '[:space:]' < /var/lib/nvml-mock/ib/sys/class/infiniband/mlx5_0/ports/1/lid")

kubectl exec "$SERVER_POD" -- ibping -S &
sleep 2
kubectl exec "$CLIENT_POD" -- ibping -c 3 "$LID"
```

For automated cross-node validation (including peer restart and retries), use
`tests/e2e/validate-ibping.sh`. LID-based ping is the supported path;
cross-node `ibping -G <port_guid>` is supported (use `0x` hex without colons;
see `pkg/network/mockib/README.md`). Companion fabric validators:

- [`tests/e2e/validate-iblinkinfo.sh`](../../../../tests/e2e/validate-iblinkinfo.sh)
  — direct-route walk reports peer GUIDs without duplicate-port errors.
- [`tests/e2e/validate-ibv-devinfo.sh`](../../../../tests/e2e/validate-ibv-devinfo.sh)
  — `ibv_devinfo -l` claims every rendered HCA via libmlx5; `ibstatus`
  confirms ACTIVE / LinkUp port state.

See [`pkg/network/mockib/README.md`](../../../../pkg/network/mockib/README.md#mock-ibping)
for env vars (`MOCK_IB`, `MOCK_IB_PING_FABRIC`, `MOCK_IB_PEERS`,
`MOCK_IB_DEBUG_SMP`, …) and architecture details.

## Configuration

### Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `gpu.profile` | `a100` | GPU profile: `a100`, `h100`, `b200`, `gb200`, `gb300`, `l40s`, or `t4` |
| `gpu.count` | `8` | Number of mock GPUs per node |
| `gpu.customConfig` | `""` | Inline YAML to override profile config entirely |
| `gpu.dynamicMetrics.enabled` | `false` | Make the mock return time-varying temperature / power / utilization readings instead of the static profile values. See [Dynamic Metrics](#dynamic-metrics) below. |
| `gpu.dynamicMetrics.seed` | `0` | RNG seed; `0` uses a time-based seed, non-zero produces reproducible sequences. |
| `gpu.dynamicMetrics.temperature.*` | see `values.yaml` | `base_c`, `variance_c`, `ramp_c`, `ramp_period_sec` for the GPU temperature generator. |
| `gpu.dynamicMetrics.power.*` | see `values.yaml` | `base_mw`, `variance_mw` for the power-draw generator (clamped to `power.min_limit_mw`/`max_limit_mw` from the profile). |
| `gpu.dynamicMetrics.utilization.*` | see `values.yaml` | `pattern` (`idle` \| `busy` \| `burst` \| `steady`), `gpu_min/max`, `memory_min/max`, `burst_period_sec`. |
| `gpu.failureInjection.enabled` | `false` | Enable simulated GPU failures (lost / fallen off bus / uncorrectable ECC). See [Failure Injection](#failure-injection) below. |
| `gpu.failureInjection.mode` | `healthy` | Failure mode: `healthy` (default, no-op), `lost`, `fallen_off_bus`, or `ecc_uncorrectable`. With the inert default, `enabled: true` alone produces a healthy device — you must set `mode` explicitly to engage failures. |
| `gpu.failureInjection.probability` | `0.0` | Per-call probability `[0, 1]` for stochastic failure activation. |
| `gpu.failureInjection.after_calls` | `0` | Activate failure deterministically after N guarded NVML calls (0 = disabled). |
| `gpu.failureInjection.seed` | `0` | RNG seed for probability rolls; `0` uses a time-based seed. |
| `gpu.failureInjection.xid.code` | `0` | Xid error code delivered via the NVML event set (`NVML_EVENT_TYPE_XID_CRITICAL_ERROR`) once tripped. `0` = no Xid. |
| `image.repository` | `ghcr.io/nvidia/nvml-mock` | Container image repository |
| `image.tag` | `latest` | Container image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `driverVersion` | `""` (auto) | NVIDIA driver version to mock. When empty, auto-derived from `gpu.profile` (even if `gpu.customConfig` is set): A100/H100/L40S/T4 → `550.163.01`, B200/GB200 → `560.35.03`. For non-standard GPUs configured via `gpu.customConfig`, explicitly set `driverVersion`. |
| `nodeSelector` | `{}` | Node selector for DaemonSet |
| `tolerations` | `[{operator: Exists}]` | Pod tolerations (default: tolerate all) |
| `integrations.fakeGpuOperator.enabled` | `false` | Create per-profile ConfigMaps for fake-gpu-operator discovery |
| `integrations.fakeGpuOperator.profileLabels` | `{"run.ai/gpu-profile": "true"}` | Labels on profile ConfigMaps for discovery |
| `infiniband.ping.port` | `18515` | TCP port for fabric relay between nvml-mock pods (`mock-ib` / `ibping` always enabled) |

### GPU Profiles

Built-in profiles provide realistic hardware specs for common data center GPUs.
Select a profile with `--set gpu.profile=<name>`:

```bash
# Deploy as an 8-GPU H100 node
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=h100

# Deploy as a 4-GPU B200 node
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=b200 \
  --set gpu.count=4
```

#### Profile Comparison

| | A100 | H100 | B200 | GB200 | GB300 | L40S | T4 |
|---|---|---|---|---|---|---|---|
| **Profile name** | `a100` | `h100` | `b200` | `gb200` | `gb300` | `l40s` | `t4` |
| **Full name** | A100-SXM4-40GB | H100 80GB HBM3 | B200 | GB200 NVL | GB300 NVL | L40S | Tesla T4 |
| **Architecture** | Ampere | Hopper | Blackwell | Blackwell | Blackwell Ultra | Ada Lovelace | Turing |
| **Compute capability** | 8.0 | 9.0 | 10.0 | 10.0 | 10.0 | 8.9 | 7.5 |
| **CUDA cores** | 6,912 | 16,896 | 18,432 | 18,432 | 21,632 | 18,176 | 2,560 |
| **Memory** | 40 GiB HBM2e | 80 GiB HBM3 | 192 GiB HBM3e | 192 GiB HBM3e | 288 GiB HBM3e | 48 GiB GDDR6 | 16 GiB GDDR6 |
| **NVLink** | v3, 12 links | v4, 18 links | v5, 18 links | v5, 18 links | v5, 18 links | — | — |
| **NVLink BW** | 600 GB/s | 900 GB/s | 1.8 TB/s | 1.8 TB/s | 1.8 TB/s | — | — |
| **TDP** | 400W | 700W | 1,000W | 1,000W | 1,400W | 350W | 70W |
| **PCIe** | Gen4 | Gen5 | Gen6 | Gen6 | Gen6 | Gen4 | Gen3 |
| **MIG instances** | 7 | 7 | 7 | 7 | 7 | 0 | 0 |
| **Grace CPU** | — | — | — | Yes (NVLink-C2C) | Yes (NVLink-C2C) | — | — |
| **FP8** | — | Yes | Yes | Yes | Yes | Yes | — |
| **FP4** | — | — | Yes | Yes | Yes | — | — |
| **FP6** | — | — | — | — | Yes | — | — |
| **Driver version** | 550.163.01 | 550.163.01 | 560.35.03 | 560.35.03 | 570.124.06 | 550.163.01 | 550.163.01 |

#### When to Use Each Profile

- **`a100`** (default) — broadest compatibility. Most NVIDIA software assumes A100 in docs and examples. Use this unless you need a specific architecture.
- **`h100`** — testing Hopper-specific features: FP8, Transformer Engine, PCIe Gen5, or NVLink v4 topology.
- **`b200`** — testing next-gen Blackwell features: FP4, NVLink v5, PCIe Gen6. Standalone GPU (no Grace CPU).
- **`gb200`** — testing Grace-Blackwell Superchip: NVLink-C2C to Grace CPU, unified memory, and Blackwell features.
- **`gb300`** — testing Grace-Blackwell Ultra Superchip: 288 GiB HBM3e per GPU, 1.4 kW TDP, FP6 in addition to FP4/FP8, and Blackwell Ultra driver line (570.124.06).
- **`l40s`** — testing Ada Lovelace inference workloads: FP8, PCIe Gen4, no NVLink (PCIe-only topology).
- **`t4`** — testing Turing inference GPUs: low power (70W), small memory (16 GiB), 4 GPUs per node.

### Custom Configuration

For GPU types not covered by built-in profiles, provide your own config YAML.

#### Option A: File-based (recommended)

Create a YAML file following the profile format, then pass it at install time:

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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
        uuid: "GPU-14050000-0000-0000-0000-000000000000"
        minor_number: 0
```

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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

### Dynamic Metrics

Real GPUs report metrics that change over time — temperature rises under
load, utilization fluctuates, power draw ramps. By default the mock is
fully static: whatever values are set in a profile's `thermal`, `power`,
and `utilization` sections are returned unchanged on every call.

Set `gpu.dynamicMetrics.enabled=true` to have the rendered ConfigMap
inject a `device_defaults.dynamic_metrics` block. The mock then returns
fluctuating values from `GetTemperature`, `GetPowerUsage`, and
`GetUtilizationRates`. Each sub-section (`temperature`, `power`,
`utilization`) can be tuned independently; the overlay works with any
built-in profile and with `gpu.customConfig`.

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --set gpu.profile=h100 \
  --set gpu.dynamicMetrics.enabled=true \
  --set gpu.dynamicMetrics.utilization.pattern=burst
```

Or via a values file:

```yaml
gpu:
  profile: h100
  dynamicMetrics:
    enabled: true
    seed: 0                             # set non-zero for reproducibility
    temperature:
      base_c: 60
      variance_c: 3
      ramp_c: 15
      ramp_period_sec: 120
    power:
      base_mw: 500000
      variance_mw: 50000
    utilization:
      pattern: burst                    # idle | busy | burst | steady
      gpu_min: 0
      gpu_max: 100
      memory_min: 0
      memory_max: 100
      burst_period_sec: 30
```

Utilization pattern semantics (values are always clamped to `0..100`):

| pattern  | sampled from                                          |
| -------- | ----------------------------------------------------- |
| `idle`   | bottom quarter of `[gpu_min, gpu_max]`                |
| `busy`   | top quarter of `[gpu_min, gpu_max]`                   |
| `burst`  | alternates `idle` / `busy` every `burst_period_sec`   |
| `steady` | full `[gpu_min, gpu_max]` range (default if omitted)  |

See [`pkg/gpu/mocknvml/README.md`](../../../pkg/gpu/mocknvml/README.md#dynamic-metrics-optional)
for the full engine-side reference.

### Failure Injection

Real GPUs occasionally fall off the bus, accumulate uncorrectable ECC errors,
or surface Xid events. By default the mock reports healthy hardware. Set
`gpu.failureInjection.enabled=true` to have the rendered ConfigMap inject a
`device_defaults.failure` block; the mock will then trip the device into the
configured failure mode based on the trigger you choose:

```bash
# Deterministic: device goes "lost" after the 200th NVML call
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set gpu.profile=h100 \
  --set gpu.failureInjection.enabled=true \
  --set gpu.failureInjection.mode=lost \
  --set gpu.failureInjection.after_calls=200
```

```yaml
# Stochastic + Xid: 1% chance per call to surface ECC double-bit (Xid 64),
# bounded to trip within 10k calls so CI does not hang.
gpu:
  profile: h100
  failureInjection:
    enabled: true
    mode: ecc_uncorrectable
    probability: 0.01
    after_calls: 10000
    seed: 12345
    xid:
      code: 64
```

Per-mode behaviour:

| mode                | guarded API calls return | handle lookup returns | identity getters    | ECC counters         | event set                       |
| ------------------- | ------------------------ | --------------------- | ------------------- | -------------------- | ------------------------------- |
| `healthy` (default) | normal values            | normal handle         | normal values       | zero                 | empty                           |
| `lost`              | `ERROR_GPU_IS_LOST`      | `ERROR_GPU_IS_LOST`   | `ERROR_GPU_IS_LOST` | error                | empty                           |
| `fallen_off_bus`    | `ERROR_GPU_IS_LOST`      | `ERROR_GPU_IS_LOST`   | `ERROR_GPU_IS_LOST` | error                | empty                           |
| `ecc_uncorrectable` | normal values            | normal handle         | normal values       | strictly-increasing  | one `XID_CRITICAL_ERROR` if xid |

Values rendered into the ConfigMap are validated against
[`values.schema.json`](./values.schema.json) at install / upgrade time:
typos like `mode: healhty` or out-of-range values like `probability: 1.5`
are rejected by Helm before the chart renders, so misconfigurations
surface as actionable schema errors instead of silent runtime
coercion.

Failure injection composes with `gpu.dynamicMetrics`: with both enabled the
device returns dynamic readings while healthy and switches to the configured
failure mode once the trigger fires. Once tripped a device stays tripped for
the lifetime of the pod, matching real hardware that needs a reboot to
recover.

#### Verifying with `nvidia-smi`

Each `nvidia-smi` invocation is a fresh process whose call counter starts at
0, so a narrow query like `--query-gpu=ecc.errors.uncorrected.aggregate.total`
will only ever issue **one** guarded call per GPU per invocation. To see the
failure surface from a single short command set `after_calls: 1`, or use a
richer query that issues several guarded calls per GPU (e.g. `nvidia-smi -q`)
so the trigger fires within one process.

```bash
# mode: lost / fallen_off_bus  ─  handle lookup itself fails once tripped.
# nvidia-smi prints "Unable to determine the device handle for GPU ..."
# and exits non-zero.
kubectl exec ds/nvml-mock -- nvidia-smi -L
kubectl exec ds/nvml-mock -- nvidia-smi --query-gpu=name,uuid --format=csv
kubectl exec ds/nvml-mock -- nvidia-smi -q                # "GPU is lost"

# mode: ecc_uncorrectable  ─  device stays addressable; counters grow and
# nvmlEventSetWait_v2 delivers the configured Xid once per trip.
kubectl exec ds/nvml-mock -- nvidia-smi -q -d ECC
kubectl exec ds/nvml-mock -- nvidia-smi \
  --query-gpu=ecc.errors.uncorrected.aggregate.total --format=csv
kubectl exec ds/nvml-mock -- nvidia-smi \
  --query-gpu=ecc.errors.uncorrected.aggregate.dram  --format=csv

# Any mode  ─  watch the engine trip in real time.
kubectl exec ds/nvml-mock -- env MOCK_NVML_DEBUG=1 \
  nvidia-smi -q -d ECC 2>&1 | grep -E 'failure|GPU_IS_LOST|Xid'

# One long-running process so the per-process call counter accumulates
# (useful when after_calls > 1 and you want to see a deterministic trip
# without restarting the daemonset).
kubectl exec ds/nvml-mock -- nvidia-smi \
  --query-gpu=ecc.errors.uncorrected.aggregate.total --format=csv -l 1
```

See [`pkg/gpu/mocknvml/README.md`](../../../pkg/gpu/mocknvml/README.md#failure-injection-optional)
for the full engine-side reference, including how the modes interact with
specific NVML calls.

## How It Works

The chart deploys:

1. **DaemonSet** — runs a privileged container on each node that:
   - Copies `libnvidia-ml.so.{version}` to the host at `/var/lib/nvml-mock/driver/usr/lib64/`
   - Creates symlinks (`libnvidia-ml.so.1` → `libnvidia-ml.so.{version}`)
   - Creates mock device nodes at `/var/lib/nvml-mock/driver/dev/nvidia{N,ctl,-uvm,-uvm-tools}` (CDI bind-mounts them to `/dev/nvidia*` in consumer containers)
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
