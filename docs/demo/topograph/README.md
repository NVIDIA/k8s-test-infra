# nvml-mock + topograph Demo

End-to-end walkthrough of [NVIDIA/topograph](https://github.com/NVIDIA/topograph)
performing **in-cluster topology discovery** against an nvml-mock-simulated
GPU cluster on Kind — **no physical GPUs, HCAs, or switches**.

topograph reads each node's NVLink **accelerator domain** (NVLink clique)
straight out of the mock `nvidia-smi -q` output, then writes the standard
`network.topology.nvidia.com/accelerator` label that topology-aware schedulers
(e.g. the Kueue topology-aware scheduling plugin) consume.

The demo runs on its own cluster (`nvml-mock-topograph`, 1 control-plane +
4 workers) and partitions the workers into two NVLink cliques:

```text
domain "demo-domain"              uuid 00000000-0000-0000-0000-0000000000ab
  clique 0:
    - nvml-mock-topograph-worker
    - nvml-mock-topograph-worker2
  clique 1:
    - nvml-mock-topograph-worker3
    - nvml-mock-topograph-worker4
```

After the run, the four workers carry:

```text
nvml-mock-topograph-worker    network.topology.nvidia.com/accelerator=00000000-0000-0000-0000-0000000000ab.0
nvml-mock-topograph-worker2   network.topology.nvidia.com/accelerator=00000000-0000-0000-0000-0000000000ab.0
nvml-mock-topograph-worker3   network.topology.nvidia.com/accelerator=00000000-0000-0000-0000-0000000000ab.1
nvml-mock-topograph-worker4   network.topology.nvidia.com/accelerator=00000000-0000-0000-0000-0000000000ab.1
```

— i.e. the label value is `<ClusterUUID>.<CliqueId>`, and the two cliques
land in two distinct accelerator domains.

## Prerequisites

The demo expects the following tools on `$PATH`:

| Tool      | Tested version | Notes |
|---        |---             |---    |
| `docker`  | 24+            | Daemon must be running. Multi-stage build uses the Go base image. |
| `kind`    | v0.24+         | Creates the `nvml-mock-topograph` cluster (1 cp + 4 workers). |
| `kubectl` | v1.30+         | Used for `exec`, `rollout`, `get`/`label` against in-cluster pods. |
| `helm`    | v3.13+         | Installs the nvml-mock chart and the topograph chart `0.4.0`. |
| `bash`    | 3.2+           | `run.sh` uses `set -euo pipefail` — no bash 4+ features. |

Network access is required to pull the topograph Helm chart from
`https://NVIDIA.github.io/topograph` and the topograph container images.

## Quick start

```bash
./run.sh
```

The script is idempotent. By default an existing `nvml-mock-topograph`
cluster is reused; pass `FORCE_RECREATE=true ./run.sh` to tear it down and
start clean. Other env knobs: `GPU_PROFILE` (default `gb200`),
`TOPOGRAPH_CHART_VERSION` (default `0.4.0`), `TOPOGRAPH_NS` (default
`topograph`).

## How it works

The integration has three moving parts. nvml-mock supplies the simulated
hardware identity; topograph's **node-data-broker** harvests it; topograph's
**server** turns it into a node label.

1. **nvml-mock advertises the NVLink clique.** The chart is installed with
   `topology.enabled=true` and a two-clique topology document
   ([`clique-topology.yaml`](./clique-topology.yaml)). The mock
   `libnvidia-ml.so` overlays that document on the per-profile YAML at
   `LoadConfig()` time (keyed by `NODE_NAME`), so `nvidia-smi -q` inside each
   worker pod reports the per-node `Fabric → ClusterUUID` /
   `Fabric → CliqueId`:

   ```text
   Fabric
       CliqueId                          : 0
       ClusterUUID                       : 00000000-0000-0000-0000-0000000000ab
   ```

2. **topograph's node-data-broker reads it and annotates the node.** The
   broker runs as a DaemonSet pinned to GPU nodes
   (`nodeSelector: nvidia.com/gpu.present=true`). Its init container is told
   where to find the device-plugin DaemonSet
   (`device-plugin-daemonset=nvml-mock`,
   `gpu-operator-namespace=default`), execs `nvidia-smi -q` in the
   co-located nvml-mock pod, and writes the discovered clique onto the node
   as the annotation `topograph.nvidia.com/cluster-id=<ClusterUUID>.<CliqueId>`.
   See [`topograph-values.yaml`](./topograph-values.yaml).

3. **topograph's server labels the node.** The `node-observer` triggers a
   topology generation; the server runs the `infiniband-k8s` provider with
   `useGpuCliqueLabel=false` and the `k8s` engine, which turns the
   `cluster-id` annotation into the
   `network.topology.nvidia.com/accelerator` label.

`run.sh` then asserts that both clique-0 workers share one accelerator value,
both clique-1 workers share another, and the two values differ.

### Why `useGpuCliqueLabel: false`

topograph can also read the clique from a pre-existing
`nvidia.com/gpu.clique` **node label** (the path the real GPU Operator
populates). When that label is present, topograph deliberately *skips*
writing its own `accelerator` label — it assumes the scheduler already keys
off `nvidia.com/gpu.clique`. To demonstrate topograph producing the
`accelerator` label itself, this demo uses the annotation path
(`useGpuCliqueLabel: false`): the broker collects the clique via
`nvidia-smi -q` and topograph writes the label.

## Known limitation — switchless fabric (no leaf/spine/core)

topograph's full value is building a multi-tier network tree
(`accelerator` → `leaf` → `spine` → `core`) from the InfiniBand fabric via
`ibnetdiscover`. **nvml-mock's simulated fabric is switchless** — it models
point-to-point HCAs with no managed switches — so `ibnetdiscover` surfaces no
switch hierarchy. topograph's broker still attempts IB discovery (and that
attempt is tolerated/ignored), but only the **NVLink accelerator domain** is
derived. You will therefore see the `accelerator` label but **no**
`leaf` / `spine` / `core` labels; `run.sh` prints a `NOTE` to that effect at
the end.

**Follow-up:** to exercise topograph's switch-tier discovery against
nvml-mock, the mock `ibnetdiscover` would need to expose a leaf/spine switch
topology. That is tracked as a future enhancement to the mock InfiniBand
subnet-manager.

## Manual reproduction

The commands below mirror what `run.sh` issues, for the default
`nvml-mock-topograph` cluster name.

```bash
# 1. Create the cluster (1 control-plane + 4 workers).
kind create cluster --name nvml-mock-topograph \
    --config docs/demo/topograph/kind.yaml

# 2. Build + load the nvml-mock image.
docker build -t nvml-mock:topograph-demo -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:topograph-demo --name nvml-mock-topograph

# 3. Install nvml-mock (gb200 + two NVLink cliques + FGO labels).
helm upgrade --install nvml-mock deployments/nvml-mock/helm/nvml-mock \
    -f docs/demo/topograph/clique-topology.yaml \
    --set image.repository=nvml-mock \
    --set image.tag=topograph-demo \
    --set integrations.fakeGpuOperator.enabled=true \
    --set gpu.profile=gb200 \
    --wait --timeout 180s
# Recycle so the freshly built (same-tagged) image is the one running.
kubectl rollout restart daemonset/nvml-mock
kubectl rollout status  daemonset/nvml-mock --timeout=120s

# 4. Sanity check: nvml-mock exposes the clique to nvidia-smi -q.
POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector spec.nodeName=nvml-mock-topograph-worker \
    -o jsonpath='{.items[0].metadata.name}')
kubectl exec "$POD" -- nvidia-smi -q | grep -E 'ClusterUUID|CliqueId'

# 5. Install topograph (infiniband-k8s provider + k8s engine).
helm repo add topograph https://NVIDIA.github.io/topograph
helm repo update
helm upgrade --install topograph topograph/topograph --version 0.4.0 \
    --namespace topograph --create-namespace \
    -f docs/demo/topograph/topograph-values.yaml \
    --wait --timeout 180s

# 6. Inspect the result.
kubectl get nodes -L network.topology.nvidia.com/accelerator
```

`-worker` / `-worker2` should report accelerator
`00000000-0000-0000-0000-0000000000ab.0`; `-worker3` / `-worker4` should
report `…ab.1`.

> **Image tag gotcha.** The demo always tags the image
> `nvml-mock:topograph-demo`. When you reuse an existing cluster, a rebuilt
> image keeps the same tag, so `helm upgrade` leaves the DaemonSet pod
> template untouched and Kubernetes never recycles the pods — they keep the
> previously loaded image. `run.sh` issues an explicit
> `kubectl rollout restart daemonset/nvml-mock` after the install to force
> the freshly built image into the running pods.

## Custom cluster name

If you rename the Kind cluster, the worker names in
[`clique-topology.yaml`](./clique-topology.yaml) must change in lockstep —
each Kind worker is named `<cluster-name>-worker[N]`, and the topology
document keys cliques by node name. `run.sh` derives the clique node arrays
from `CLUSTER_NAME`, but the checked-in `clique-topology.yaml` hard-codes the
default `nvml-mock-topograph-*` names to keep the example faithful.

## Clean up

```bash
kind delete cluster --name nvml-mock-topograph
```
