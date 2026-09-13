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
`helm`, `kubectl`, and a Go toolchain matching `go.mod`.

## Step 1 — create a cluster

Every cluster uses a custom Kind node image built from
`deployments/kind-nvidia-cdi/`, which pre-installs the NVIDIA container runtime
and bakes CDI into containerd. `make cluster-create` builds it if needed.

Two cluster shapes exist, selected with `PROFILE`:

| `PROFILE` | Shape | Cluster name | Use with |
|---|---|---|---|
| `default` | 1 control-plane + 2 workers | `mokka` | everything except compute-domain |
| `compute-domain` | 1 control-plane + 4 workers with NVLink cliques | `mokka-compute-domain` | `--compute-domain`, `--topograph` |

```bash
make cluster-create                         # default
make cluster-create PROFILE=compute-domain

make cluster-delete                         # PROFILE must match creation
```

The default shape carries both an `a100` and a `t4` worker, so you can switch
between a single release and a per-worker fleet without rebuilding the cluster.

!!! note "The control-plane node is deliberately excluded"
    `local/nvml-mock.values.yaml` pins the DaemonSet to nodes labelled
    `mokka.nvidia.com/type=sgpu`. Without that the mock lands on the control
    plane too — it tolerates every taint — while the GPU Operator, FGO and NFD
    operands stop at the `NoSchedule` taint. You would get a node advertising a
    driver nobody consumes.

## Step 2 — start Tilt

`tilt up` with no flags installs one nvml-mock release across every worker using
the `a100` profile. Everything else is a flag.

### Choosing what the fleet looks like

| Flag | Effect |
|---|---|
| `--gpu-profile <name>` | Which GPU profile the release uses. One of `a100`, `h100`, `b200`, `gb200`, `gb300`, `l40s`, `t4` |
| `--multi-gpu-profile` | One release per worker instead of one for the fleet: `a100` on `worker-0`, `t4` on `worker-1`. Ignores `--gpu-profile` |
| `--compute-domain` | GB200 profile with an NVLink topology overlay. Needs the `compute-domain` cluster |

### Adding consumers

| Flag | Deploys |
|---|---|
| `--gpu-operator` | The NVIDIA GPU Operator, in CDI mode |
| `--dra` | The NVIDIA DRA driver |
| `--fgo` | Run:ai's fake-gpu-operator alongside Mokka, splitting workers into two pools |
| `--topograph` | [topograph](https://github.com/NVIDIA/topograph) network-topology discovery |
| `--observability` | kube-prometheus-stack and a Grafana dashboard for the mock fleet |
| `--control-plane` | The Mokka control-plane image and the `mokka-crds` chart |

### What composes and what does not

Most flags stack. These do not:

| Combination | Why |
|---|---|
| `--fgo` with `--gpu-operator` | FGO replaces the GPU Operator |
| `--fgo` with `--compute-domain` | Different fleet shapes |
| `--compute-domain` with `--multi-gpu-profile` or `--gpu-profile` | The compute-domain scenario fixes both |

Two flags imply others: `--topograph` implies `--compute-domain`, because
cliques only exist there; `--observability` implies `--gpu-operator`, because
`dcgm-exporter` is one of its operands.

```bash
tilt up -- --gpu-profile gb200
tilt up -- --multi-gpu-profile --gpu-operator
tilt up -- --gpu-operator --dra
tilt up -- --observability

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

## Overriding Helm values

Values layer in this order, last wins:

| Source | Committed |
|---|---|
| Chart defaults | — |
| `local/nvml-mock.values.yaml` — shared local baseline | yes |
| `local/<consumer>/nvml-mock.values.yaml` — per-consumer tweaks | yes |
| `local/nvml-mock.values.local.yaml` — your machine | **no**, gitignored |

```bash
cat > local/nvml-mock.values.local.yaml <<'EOF'
gpu:
  count: 2
infiniband:
  enabled: true
  mockTier: full
EOF
```

Tilt picks it up on the next save.

## Related

| To read about | See |
|---|---|
| Running the tests | [Testing](testing.md) |
| What a profile defines | [Configuration](../configuration.md) |
| Changing state on a running node | [Runtime Control](../nvml-mock-ctl.md) |
