# Local Development

Mokka's local loop is a Kind cluster plus [Tilt](https://tilt.dev/). Tilt builds
the images, installs the charts, and redeploys on save; you create the cluster
once and leave it running.

```bash
make cluster-create          # once
tilt up -- --gpu-operator    # the dev loop
```

## Prerequisites

`docker`, [`kind`](https://kind.sigs.k8s.io/), [`tilt`](https://tilt.dev/),
`helm`, `kubectl`, and a Go toolchain matching `go.mod`. On macOS,
[Colima](https://github.com/abiosoft/colima) as well, for the one scenario that
loads kernel modules.

## Step 1 — create a cluster

Two cluster shapes exist, selected with `PROFILE`:

| `PROFILE`        | Shape                                                    | Cluster name           | Use with                          |
|------------------|----------------------------------------------------------|------------------------|-----------------------------------|
| `default`        | 1 control-plane + 2 workers                              | `mokka`                | everything except compute-domain  |
| `compute-domain` | 1 control-plane + 4 workers in two NVLink cliques of two | `mokka-compute-domain` | `--compute-domain`, `--topograph` |

```bash
make cluster-create                         # default
make cluster-create PROFILE=compute-domain

make cluster-delete                         # PROFILE must match creation
```

The default shape serves both a single release and a per-worker fleet, so
switching between them needs no rebuild.

> [!NOTE]
> **The control-plane node is deliberately excluded**
>
> `local/nvml-mock.values.yaml` pins the DaemonSet to nodes labelled
> `mokka.nvidia.com/type=sgpu`. Without that the mock lands on the control
> plane too — it tolerates every taint — while the GPU Operator, FGO and NFD
> operands stop at the `NoSchedule` taint. You would get a node advertising a
> driver nobody consumes.

### A cluster that can load kernel modules

Simulation needs nothing from the kernel, but `--gpu-operator-driver` runs the
GPU Operator's driver installer, which compiles kernel modules against the node
kernel's headers and loads them. Kind nodes share their host's kernel, so that
host needs a stock distro kernel whose headers the Ubuntu archive serves.
OrbStack and Docker Desktop do not run one; on macOS, create the cluster inside
a Colima VM instead. A Linux workstation running a distro kernel needs none of
this.

```bash
make colima-cluster-create    # starts the VM if needed, then creates mokka-colima in it
DOCKER_CONTEXT=colima-mokka tilt up --context kind-mokka-colima -- --gpu-operator-driver
```

Neither your Docker nor your kubectl context changes. `DOCKER_CONTEXT` is what
points docker, kind and tilt at the VM, and the cluster is named
`mokka-colima` so its kubeconfig entry cannot replace that of a local `mokka`
cluster. Starting the VM also raises its inotify instance limit: Ubuntu's
default of 128 is shared by every Kind node, and a three-node cluster running the
GPU Operator exhausts it, which breaks `kubectl logs -f` with "too many open
files". `make colima-cluster-delete` removes the cluster; `make colima-stop`
and `make colima-delete` stop or remove the VM, whose size comes from
`COLIMA_START_FLAGS`.

## Step 2 — start Tilt

`tilt up` with no flags installs one nvml-mock release across every worker using
the chart's [default profile](../helm-chart.md#when-to-use-each-profile).
Everything else is a flag.

### Choosing what the fleet looks like

| Flag | Effect |
|---|---|
| `--gpu-profile <name>` | Which GPU profile the release uses, instead of the chart's default. One of `a100`, `h100`, `b200`, `gb200`, `gb300`, `l40s`, `t4` |
| `--multi-gpu-profile` | One release per worker instead of one for the fleet: `a100` on `worker-0`, `t4` on `worker-1`. Ignores `--gpu-profile` |
| `--compute-domain` | GB200 profile with an NVLink topology overlay. Needs the `compute-domain` cluster |
| `--nvmlmock-image <ref>` | Skip the image build and pin the release to an image you already built, as CI does |

### Adding consumers

| Flag | Deploys |
|---|---|
| `--gpu-operator` | The NVIDIA GPU Operator, in CDI mode |
| `--gpu-operator-driver` | The GPU Operator with its driver installer enabled, from the original NVIDIA image, on `worker-0` alone. Needs [a cluster that can load kernel modules](#a-cluster-that-can-load-kernel-modules) |
| `--dra` | The NVIDIA DRA driver |
| `--fgo` | Run:ai's fake-gpu-operator alongside Mokka, splitting workers into two pools |
| `--topograph` | [Topograph](../guides/topograph/README.md), labelling each worker with its NVLink domain |
| `--observability` | kube-prometheus-stack and a Grafana dashboard for the mock fleet |
| `--dynamo` | The NVIDIA Dynamo operator and a `qwen3` inference graph whose mocker worker runs on a mock GPU |
| `--slinky` | cert-manager, the Slinky slurm-operator and a Slurm cluster with one GPU `slurmd` per mock-GPU worker |
| `--control-plane` | The Mokka control-plane image and the `mokka-crds` chart |

### What composes and what does not

Most flags stack. These do not:

| Combination | Why |
|---|---|
| `--fgo` with `--gpu-operator` | FGO replaces the GPU Operator |
| `--fgo` with `--compute-domain` | Different fleet shapes |
| `--compute-domain` with `--multi-gpu-profile` or `--gpu-profile` | The compute-domain scenario fixes both |
| `--slinky` with `--dynamo` | `slurmd` claims every mock GPU, so the Dynamo worker could never schedule |
| `--gpu-operator-driver` with `--multi-gpu-profile` or `--compute-domain` | One kernel hosts one driver, so the scenario simulates GPUs on `worker-0` alone |

Five flags imply others: `--topograph` implies `--compute-domain`, because
cliques only exist there; `--observability` implies `--gpu-operator`, because
`dcgm-exporter` is one of its operands; `--dynamo` and `--slinky` imply
`--gpu-operator`, because their workloads request `nvidia.com/gpu`; and
`--gpu-operator-driver` implies `--gpu-operator`, which it configures.

> [!NOTE]
> **`--gpu-operator-driver` is alpha**
>
> The scenario runs the node in the kernel driver mode (`driver.mode: kernel`)
> and has the NRI plugin adapt the GPU Operator's driver and toolkit containers
> (`nri.gpuOperator`). The driver
> container installs headers and compiles and installs the real driver, then
> loads Mokka's simulated modules
> ([`shims/nvidia-kmod`](../../shims/nvidia-kmod/README.md)) in its place, and
> its root serves the mock NVML to its startup probe and to every other operand.
> A driver pod that finds no driver loaded with its configuration compiles the
> driver again, which takes a minute or two. `nvidia-persistenced` is replaced
> by a stand-in, because the daemon needs the Resource Manager (RM), which the
> modules do not simulate.

```bash
tilt up -- --gpu-profile gb200
tilt up -- --multi-gpu-profile --gpu-operator
tilt up -- --gpu-operator --dra
tilt up -- --observability
tilt up -- --dynamo
tilt up -- --slinky
DOCKER_CONTEXT=colima-mokka tilt up --context kind-mokka-colima -- --gpu-operator-driver   # see Step 1

make cluster-create PROFILE=compute-domain
tilt up -- --compute-domain --dra
```

## Manual triggers in the Tilt UI

Some scenarios add buttons rather than running automatically.

**`compute-domain-tests`** — `check-fabric` asserts the topology overlay gave
each node the expected clique and cluster UUID; `topology-rebind` live-rebinds
the topology and re-asserts.

**`observability-tests`** — `inject-thermal` pins one GPU's temperature and
asserts Prometheus serves exactly that value while its siblings keep varying;
`inject-xid` trips an uncorrectable ECC fault and asserts the Xid reaches
`DCGM_FI_DEV_XID_ERRORS`. Both inject with `nvml-mock-ctl` rather than
`helm upgrade`, which would recycle the exporter and tear a hole in the series,
and both **fail if the fault does not reach Prometheus** — the scrape path is
asserted, not eyeballed.

Grafana is port-forwarded to <http://localhost:3000/d/mokka-gpu> (`admin` /
`mokka`) once its resource is ready.

**`dynamo-tests`** — `dynamo-smoke` asserts the `qwen3` worker sees exactly
one GPU whose name matches its node's `nvidia.com/gpu.product` label, then
sends a chat completion through the frontend and checks the token count. The
frontend is port-forwarded to <http://localhost:8000/v1/models>. The
[NVIDIA Dynamo guide](../guides/dynamo/README.md) walks through the same setup
from published artifacts.

**`slinky-tests`** — `slurm-smoke` asserts every Slurm node is idle with the
GPUs it was configured with, that a 2-GPU job is given exactly 2 and they are
the node's profile, and that a job using every GPU on every node runs. The
[Slinky guide](../guides/slinky/README.md) walks through the same setup from
published artifacts.

## Overriding Helm values

Values layer in this order, last wins:

| Source | Committed |
|---|---|
| Chart defaults | — |
| `local/nvml-mock.values.yaml` — shared local baseline | yes |
| `local/<consumer>/nvml-mock.values.yaml` — per-consumer tweaks | yes |
| `local/nvml-mock.values.local.yaml` — your machine | **no**, gitignored |
| Tilt flags that map to a chart value: `--gpu-profile`, `--nvmlmock-image`, `--control-plane` | — |

A flag reaches Helm as `--set`, which beats every file. So `gpu.profile` in a
values file takes effect only when `--gpu-profile` is not passed.

```bash
cat > local/nvml-mock.values.local.yaml <<'EOF'
gpu:
  count: 2
infiniband:
  enabled: true
  mockTier: full
EOF
```

Tilt does not watch values files, and the Tiltfile looks for the personal file only
when it loads. Create it before `tilt up`; after editing any values file, trigger
the `nvml-mock` resource in the Tilt UI to re-apply it.

## Related

| To read about | See |
|---|---|
| Running the tests | [Testing](testing.md) |
| What a profile defines | [Configuration](../configuration.md) |
| Changing state on a running node | [Runtime Control](../nvml-mock-ctl.md) |
