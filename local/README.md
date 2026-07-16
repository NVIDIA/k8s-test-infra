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

# 2. Start the dev stack (default: a100 profile, single node)
tilt up
```

## Step 1 — Create a cluster

All profiles use the same custom Kind node image (`kind-node-nv:latest`) built from `local/kind/Dockerfile`, which pre-installs the NVIDIA container runtime.

| `PROFILE=`        | Kind config                          | Cluster name                  | Use with         |
|-------------------|--------------------------------------|-------------------------------|------------------|
| `single` (default)| `local/kind/single.kind.yaml`        | `kind-gpu-test`               | basic, gpu-operator, dra |
| `multi`           | `local/kind/multi.kind.yaml`         | `kind-gpu-test`               | `--multi`, dra (heterogeneous fleet) |
| `compute-domain`  | `local/kind/compute-domain.kind.yaml`| `kind-nvml-mock-compute-domain` | `--compute-domain` |

```bash
make cluster-create                      # single-node (default)
make cluster-create PROFILE=multi        # 1 CP + 2 workers (a100 / t4)
make cluster-create PROFILE=compute-domain  # 1 CP + 4 workers (NVLink cliques)

make cluster-delete                      # tear down (PROFILE= must match creation)
```

## Step 2 — Start Tilt

### Basic: Single-node GPU profile

```bash
tilt up                                  # default: a100
tilt up -- --gpu-profile h100
tilt up -- --gpu-profile gb200
```

Supported `--gpu-profile` values: `a100`, `h100`, `b200`, `gb200`, `gb300`, `l40s`, `t4`, `vr200`.

### Multi-node heterogeneous fleet (requires `PROFILE=multi` cluster)

Installs one nvml-mock release per worker, pinned by node selector. Profiles are fixed to `a100` and `t4` (matching the worker labels in `local/kind/multi.kind.yaml`).

```bash
tilt up -- --multi
```

### With NVIDIA GPU Operator

Deploys the GPU Operator on top of nvml-mock using CDI mode. Compatible with `single` and `multi` cluster profiles.

```bash
tilt up -- --gpu-operator
tilt up -- --gpu-operator --gpu-profile gb200
tilt up -- --multi --gpu-operator
```

### With NVIDIA DRA driver

Deploys the DRA driver on top of nvml-mock. Compatible with all cluster profiles including `--multi` (each worker publishes a distinct ResourceSlice).

```bash
tilt up -- --dra
tilt up -- --multi --dra
tilt up -- --gpu-operator --dra          # GPU Operator + DRA together
```

### Compute-domain scenario (requires `PROFILE=compute-domain` cluster)

Reconfigures nvml-mock with a GB200 profile and NVLink topology overlay. Mutually exclusive with `--multi` and `--gpu-profile`.

```bash
make cluster-create PROFILE=compute-domain
tilt up -- --compute-domain
tilt up -- --compute-domain --dra        # with DRA driver
```

The Tilt UI exposes two manual triggers under the `compute-domain-tests` label:
- **check-fabric** — asserts the topology overlay assigned the expected `cliqueId`/`clusterUUID` to each node
- **topology-rebind** — live-rebinds the NVLink topology and re-asserts the new clique assignment

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
