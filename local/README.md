# Local development environment

This directory contains Tiltfiles, Helm value overrides, and Kind cluster configs for running the nvml-mock stack locally.

## Prerequisites

- [kind](https://kind.sigs.k8s.io/) — local Kubernetes clusters
- [tilt](https://tilt.dev/) — live-reload dev orchestrator
- [helm](https://helm.sh/) — used by Tilt internally
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- Docker

## Quick start

```bash
# 1. Build the Kind node image and create a cluster
make cluster-create

# 2. Start the dev stack
tilt up -- --gpu-operator
```

## Step 1 — Create a cluster

All profiles use the same custom Kind node image (`kind-node-nv:latest`) built from `deployments/kind-nvidia-cdi/Dockerfile`, which pre-installs the NVIDIA container runtime and bakes CDI into containerd.

The default profile spans 1 control-plane + 2 workers (a100 + t4), so no cluster rebuild is needed when switching between homogeneous (single Helm release) and heterogeneous (per-GPU-profile) nvml-mock installation, or when enabling `--fgo`.

| `PROFILE=`          | Kind config                           | Cluster name                    | Use with                                                             |
|---------------------|---------------------------------------|---------------------------------|----------------------------------------------------------------------|
| `default` (default) | `local/kind/default.kind.yaml`        | `kind-mokka`                    | basic, gpu-operator, dra, `--multi-gpu-profile`, `--fgo`             |
| `compute-domain`    | `local/kind/compute-domain.kind.yaml` | `kind-mokka-compute-domain`     | `--compute-domain`                                                   |

```bash
make cluster-create                         # 1 CP + 2 workers (a100 / t4) — the default
make cluster-create PROFILE=compute-domain  # 1 CP + 4 workers (NVLink cliques)

make cluster-delete                         # tear down (PROFILE= must match creation)
```

## Step 2 — Start Tilt

### Homogeneous fleet (single Helm release)

Installs one nvml-mock release that covers every node in the cluster (control-plane + both workers) with the same GPU profile. Pick the profile via `--gpu-profile`.

```bash
tilt up                                  # default: a100
tilt up -- --gpu-profile h100
tilt up -- --gpu-profile gb200
```

Supported `--gpu-profile` values: `a100`, `h100`, `b200`, `gb200`, `gb300`, `l40s`, `t4`.

### Heterogeneous fleet (per-GPU-profile releases)

Installs one nvml-mock release per worker, pinned by node selector `kubernetes.io/hostname=<worker>`. Profiles are fixed to `a100` on `worker-0` and `t4` on `worker-1` (the worker names come from `local/kind/default.kind.yaml`). `--gpu-profile` is ignored in this mode.

```bash
tilt up -- --multi-gpu-profile
```

### With NVIDIA GPU Operator

Deploys the GPU Operator on top of nvml-mock using CDI mode. Composes with both homogeneous and heterogeneous nvml-mock modes.

```bash
tilt up -- --gpu-operator
tilt up -- --gpu-operator --gpu-profile gb200
tilt up -- --multi-gpu-profile --gpu-operator
```

### With NVIDIA DRA driver

Deploys the DRA driver on top of nvml-mock. Composes with both homogeneous and heterogeneous modes; under `--multi-gpu-profile` each worker publishes a distinct ResourceSlice.

```bash
tilt up -- --dra
tilt up -- --multi-gpu-profile --dra
tilt up -- --gpu-operator --dra          # GPU Operator + DRA together
```

### With Run:ai Fake GPU Operator (FGO)

Deploys [FGO](https://github.com/run-ai/fake-gpu-operator) alongside nvml-mock, splitting workers into two pools:
- **integration** (a100 worker) — nvml-mock provides the NVML shim, full `nvidia-smi` output
- **scale** (t4 worker) — FGO fake backend, no nvml-mock required

Mutually exclusive with `--gpu-operator` (FGO replaces it) and `--compute-domain`. The FGO overlay pins nvml-mock to the integration pool regardless of `--multi-gpu-profile`, so both invocations produce the same runtime shape (single nvml-mock release on the a100 worker; FGO manages the t4 worker).

```bash
tilt up -- --fgo                         # nvml-mock on integration pool, FGO on scale
tilt up -- --multi-gpu-profile --fgo     # same runtime shape, per-profile release plumbing
```

Verify:
```bash
# nvml-mock running on integration worker (release is installed to
# `mokka` — same namespace CI's Ginkgo suite queries).
kubectl get pods -n mokka -l app.kubernetes.io/name=nvml-mock -o wide
kubectl get configmaps -A -l run.ai/gpu-profile=true

# FGO managing scale worker
kubectl get pods -n gpu-operator -o wide
```

### Compute-domain scenario (requires `PROFILE=compute-domain` cluster)

Reconfigures nvml-mock with a GB200 profile and NVLink topology overlay. Mutually exclusive with `--multi-gpu-profile` and `--gpu-profile`.

```bash
make cluster-create PROFILE=compute-domain
tilt up -- --compute-domain
tilt up -- --compute-domain --dra        # with DRA driver
```

The Tilt UI exposes two manual triggers under the `compute-domain-tests` label:
- **check-fabric** — asserts the topology overlay assigned the expected `cliqueId`/`clusterUUID` to each node
- **topology-rebind** — live-rebinds the NVLink topology and re-asserts the new clique assignment

### With NVIDIA topograph

Deploys [topograph](https://github.com/NVIDIA/topograph), a network-topology discovery service, on top of the compute-domain cluster. Topograph reads `nvidia.com/gpu.clique` node labels and translates them into `network.topology.nvidia.com/*` labels (`accelerator`, `tier-0`, `tier-1`, ...) that topology-aware schedulers (KAI, Kueue TAS) consume.

`--topograph` implies `--compute-domain` — cliques only exist in the compute-domain cluster. `--dra` is **not** required: `nvidia.com/gpu.clique` is a static label baked into each worker node in `local/kind/compute-domain.kind.yaml`. Topograph reads it directly from the Kubernetes API; no runtime label writer is needed. Incompatible combos (`--topograph --fgo`, `--topograph --multi-gpu-profile`, `--topograph --gpu-profile <p>`) surface via the existing compute-domain guardrails.

Data path: `nvidia.com/gpu.clique` static node label → topograph `infiniband-k8s` provider (with `useGpuCliqueLabel: true`) → writes `network.topology.nvidia.com/{accelerator,tier-0,tier-1,...}` on all nodes.

```bash
make cluster-create PROFILE=compute-domain   # required for the 4-worker cluster shape
tilt up -- --topograph                        # implies --compute-domain; --dra optional
```

### With Prometheus + Grafana (observability)

Deploys [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) and ships a Grafana dashboard for the mock fleet. The point is that the real, unmodified NVIDIA `dcgm-exporter` reads the mock `libnvidia-ml.so`, Prometheus discovers it through the ServiceMonitor the GPU Operator generates, and Grafana renders it — on a cluster with no GPUs.

`--observability` implies `--gpu-operator`, since `dcgm-exporter` is one of the Operator's operands; the observability overlay re-enables it (the default GPU Operator values switch it off, as nothing was scraping it). The stack installs before the Operator because it ships the ServiceMonitor CRD. `local/observability/nvml-mock.values.yaml` also turns on `gpu.dynamicMetrics`, without which every panel is a flat line of profile constants.

```bash
make cluster-create
tilt up -- --observability
```

Grafana is port-forwarded to <http://localhost:3000/d/mokka-gpu> (`admin` / `mokka`) once the `grafana` resource is ready.

The Tilt UI exposes two manual triggers under the `observability-tests` label. Each injects a fault into a running mock with `nvml-mock-ctl` — never a `helm upgrade`, which would recycle the exporter and tear a hole in the series — and then **fails if the fault does not reach Prometheus**, so the whole scrape path is asserted rather than eyeballed:

- **inject-thermal** — pins one GPU's temperature and asserts Prometheus serves exactly that value while its siblings keep varying
- **inject-xid** — trips an uncorrectable ECC fault and asserts the Xid reaches `DCGM_FI_DEV_XID_ERRORS`

See [observability/README.md](observability/README.md) for the dashboard panels, the scenario tunables, the two couplings that fail silently, and the behaviours that look like bugs and are not — chiefly that the Xid panel is empty until a fault fires, and that the injected Xid code alternates between runs by design.

## Helm value overrides for nvml-mock

Values are layered in this order (last wins):

| File                                     | Committed           | Purpose                                                  |
|------------------------------------------|---------------------|----------------------------------------------------------|
| Chart defaults                           | —                   | upstream chart defaults                                  |
| `local/nvml-mock.values.yaml`            | yes                 | shared local-dev baseline (IB mode, rollout speed, etc.) |
| `local/<consumer>/nvml-mock.values.yaml` | yes                 | per-consumer tweaks (e.g. `local/gpu-operator/`)         |
| `local/nvml-mock.values.local.yaml`      | **no** (gitignored) | personal per-machine overrides                           |

To add a personal override that won't be committed:

```bash
cat > local/nvml-mock.values.local.yaml <<'EOF'
infiniband:
  enabled: true
  mode: full
gpu:
  count: 2
EOF
```

Tilt picks it up automatically on the next save.
