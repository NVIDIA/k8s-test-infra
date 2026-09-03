# nvml-mock with the NVIDIA GPU Operator

Deploys the official [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator)
against simulated GPUs. The operator is the real upstream `nvidia/gpu-operator`
chart from `helm.ngc.nvidia.com/nvidia`, not a fork, and its device plugin,
GFD, dcgm-exporter and validator operands all run unmodified. Only the two
operands that need real hardware are turned off.

## Prerequisites

- **A Kubernetes cluster and a valid `KUBECONFIG`.** This demo installs into
  whatever cluster your current context points at. Check yours with
  `kubectl config current-context`.
- **Helm 3.8 or newer.** Install it from the official docs:
  <https://helm.sh/docs/intro/install/>
- `kubectl`, matching your cluster version.
- **containerd in CDI mode on every node, with the `nvidia` runtime handler
  registered.** This one is specific to the GPU Operator: with the toolkit
  operand disabled, CDI is what actually attaches the mock devices to a
  container, and toolkit-validation goes through the same path. A stock Kind
  node has neither.

> **No cluster yet?** `make cluster-create` from the repository root builds the
> CDI-enabled Kind node image ([`deployments/kind-nvidia-cdi/`](https://github.com/NVIDIA/k8s-test-infra/tree/main/deployments/kind-nvidia-cdi))
> and creates a `mokka` cluster from it: one control-plane and two workers
> (`local/kind/default.kind.yaml`). Node count is worth knowing before you
> start, because it is what drives the image-pull time described below. That is
> the cluster this demo is meant for. The plain `kind create cluster` in the
> [quick start](../../quickstart.md) gives you a node without CDI, which is
> fine for the other demos and not for this one. `make cluster-delete` tears it
> down.
>
> `PROFILE` selects the Kind topology: `default` (the three nodes above) or
> `compute-domain` (four workers, for the ComputeDomain demo), and it also
> picks the cluster name. It is unrelated to `GPU_PROFILE` below, which selects
> the simulated hardware.

> **One nvml-mock demo per cluster at a time.** This demo installs a release
> called `nvml-mock-operator` into a namespace called `mokka-operator`, both
> distinct from the [standalone demo](../standalone/README.md)'s `nvml-mock` in
> `mokka` and the [failure-injection demo](../failure-injection/README.md)'s
> `nvml-mock-failure` in `mokka-failure`. That is what it has to be: the chart
> creates a ClusterRole and a ClusterRoleBinding named after the release
> (`templates/rbac.yaml`), and those are cluster-scoped, so a namespace alone
> cannot separate two installs. The second one would fail on ownership
> metadata before creating anything.
>
> **Separate names stop release adoption, and nothing more.** The chart's
> hostPath mounts are fixed and release-independent (`/var/lib/nvml-mock`,
> `/var/run/cdi`, `/run/nvidia`, and the NFD features directory), and it ships
> `nodeSelector: {}` with `tolerations: [{operator: Exists}]`, so every one of
> these DaemonSets lands on every node and writes the same per-node host state
> whatever it is called. A live run of a co-located pair left the shared mock
> config in a failure-injected state, which any real workload scheduled on that
> node then read. Do not run two of these demos against one cluster at the same
> time.
>
> `run.sh` enforces this rather than only warning about it: after the preflight
> it calls `demo::require_no_sibling_release` once per sibling and exits `4` if
> either the standalone or the failure-injection release is already installed
> in its namespace, naming the release and printing the `helm uninstall` that
> clears it. `DEMO_ASSUME_YES=true` overrides, with a warning that says what is
> being overridden. The check is namespace-scoped, so it catches a deliberate
> co-location (`NAMESPACE=mokka ./run.sh`) and not two demos left in their own
> default namespaces; the paragraph above is still the rule.

### Why the release is called `nvml-mock-operator`

The name has to contain the string `nvml-mock`. `nvml-mock.fullname`
(`templates/_helpers.tpl`) collapses to the bare release name only when the
release name contains the chart name; otherwise it prepends it. Rendered with
`helm template`, the difference is:

```
release=nvml-mock-operator   ->  DaemonSet: nvml-mock-operator
release=gpu-operator-demo    ->  DaemonSet: gpu-operator-demo-nvml-mock
```

A name like `gpu-operator-demo` would leave `run.sh` rolling out a DaemonSet
that does not exist. Every object the script names is derived from the one
`RELEASE_NAME` variable for the same reason.

## What is disabled, and why

| Setting | Value | Reason |
|---|---|---|
| `driver.enabled` | `false` | nvml-mock supplies the driver root; there is no kernel module to install. |
| `toolkit.enabled` | `false` | The mock libraries are staged on the host by the nvml-mock DaemonSet, so the toolkit has nothing to inject. CDI carries the devices instead. |
| `dcgm.enabled` | `false` | The standalone nv-hostengine DaemonSet is not needed: dcgm-exporter embeds the host engine in-process, and libdcgm loads the mock NVML like any other consumer. |
| `dcgmExporter.enabled` | `true` | Kept on. It reads the mock through libdcgm, so it is part of what this demo proves. |
| `mig.strategy` | `none` | The mock does not implement MIG. Leaving it on makes the device plugin enumerate MIG devices, and the CDI spec generator treats any non-NOT_FOUND return as fatal. |
| `migManager.enabled` | `false` | Same reason. |
| `nodeStatusExporter.enabled` | `false` | Untested against the mock. |
| `cdi.enabled` / `cdi.default` | `true` | The container runtime reads `/var/run/cdi/nvidia.yaml`, which nvml-mock generates. This is what replaces the toolkit operand. |
| `validator.cuda.env.WITH_WORKLOAD` | `false` | The mock's kernel launch is a no-op, so the vectorAdd workload would not prove anything. |

### The driver root, which is the whole reason this demo exists

The device plugin, GFD and dcgm-exporter each get

```yaml
env:
  - name: NVIDIA_DRIVER_ROOT
    value: "/var/lib/nvml-mock/driver"
```

so they load the mock `libnvidia-ml.so` instead of looking for a real driver in
`/run/nvidia/driver` or on the host. The validator needs the same thing spelled
differently: `DRIVER_INSTALL_DIR` and `LD_LIBRARY_PATH` pointing into the mock
root, plus `DISABLE_DEV_CHAR_SYMLINK_CREATION=true` because the `/dev/char`
symlink step runs `modprobe nvidia`, which cannot work in a Kind container.

Those are per-operand nested environment lists. They are the reason the chart's
`NOTES.txt` points at this walkthrough rather than printing a list of `--set`
flags: `--set gfd.env[0].name=...` chains drop values silently, helm types
`--set validator.cuda.env[0].value="false"` as a bool against a string field,
and an unquoted `env[0]` gets glob-expanded by zsh before helm ever sees it.
A values file is the only form of this configuration that works.

### Three node labels that look wrong and are not

Inspecting the node labels after a run turns up three that seem to contradict
everything above. None of them breaks anything, and none is a statement about
the mock:

- `nvidia.com/gpu.deploy.driver=true` and
  `nvidia.com/gpu.deploy.container-toolkit=true` are the Operator's own
  scheduling hints, written by GFD for operands it manages. They say "this node
  is eligible for that operand", not "that operand is here". With
  `driver.enabled=false` and `toolkit.enabled=false` the DaemonSets are never
  created, so the labels have nothing to select and sit unused. Confirm with
  `kubectl -n gpu-operator get ds`, which lists neither.
- `nvidia.com/mig.capable=true` reports what the simulated hardware advertises,
  not what the mock implements. The profile models a MIG-capable board, so GFD
  labels it as one. The values overlay still sets `mig.strategy: none` and
  disables `migManager`, because the mock does not implement the MIG APIs
  behind that capability, which is exactly why enumerating MIG devices has to
  stay off.

## Run it

```bash
cd docs/demo/with-gpu-operator && ./run.sh
```

The script announces the target cluster, installs nvml-mock, checks that
`nvidia-smi -L` works inside a mock pod, installs the GPU Operator with the
overlay next to it, and then asserts two things that can only be true if the
real operands read the mock: at least one node has `nvidia.com/gpu` in its
allocatable, and at least one node carries a `nvidia.com/gpu.product` label.
Both checks scan every node rather than the first one, because the operands do
not tolerate the control-plane `NoSchedule` taint.

Environment overrides:

| Variable | Default | Effect |
|---|---|---|
| `GPU_PROFILE` | `gb300` | Any profile under the chart's `profiles/` directory. |
| `NAMESPACE` | `mokka-operator` | Namespace for the nvml-mock release. |
| `OPERATOR_NAMESPACE` | `gpu-operator` | Namespace for the GPU Operator release. |
| `NVML_MOCK_IMAGE` | `ghcr.io/nvidia/nvml-mock:latest` | Published image to install. |
| `BUILD_LOCAL` | `false` | Build the image from source and side-load it with `kind load`. Kind-only, and it does not create a cluster. |
| `HELM_TIMEOUT` | `15m` | Wait budget for each helm install. |
| `DEMO_ASSUME_YES` | `false` | Proceed without the confirmation prompt off a throwaway cluster. |

Exit codes come from the shared preflight: `2` no usable cluster, `3` missing
or too-old tooling, `4` confirmation declined or impossible, `5` a
configuration the chart cannot express (a digest-pinned `NVML_MOCK_IMAGE`).

### First run on a cold cluster pulls a lot of images

Expect the whole run to take on the order of 15 to 20 minutes on a cold
cluster, most of it image pulls. Measured end to end on a freshly built
three-node Kind cluster with stock defaults: **16m34s**, of which the nvml-mock
install was about 4.5 minutes. Every helm call stayed inside the default 15
minute per-install budget, so no override was needed.

The 8-minutes-per-node figure quoted by `demo::announce_pull` is a worst case
measured on a bandwidth-limited link, not the expectation. Treat a long silence
during either install as the pull rather than a hang, and only reach for a
larger budget if a helm call actually times out:

```bash
HELM_TIMEOUT=30m ./run.sh
```

## Clean up

```bash
helm uninstall gpu-operator -n gpu-operator --kube-context <your-context>
helm uninstall nvml-mock-operator -n mokka-operator --kube-context <your-context>
```

The `-n` and `--kube-context` are load-bearing: without them helm resolves the
release from whatever context and namespace happen to be current. `run.sh`
prints both lines with the values filled in.

This demo never creates a cluster, so nothing here deletes one. If you made one
with `make cluster-create`, `make cluster-delete` removes it.

## Relationship to CI

`gpu-operator-values.yaml` in this directory is a copy of
`tests/e2e/gpu-operator-values.yaml`. Strip the comments from both and they are
identical.

Neither file is what CI installs. The `e2e-gpu-operator` job runs
`tilt ci -- --gpu-operator`, and `local/gpu-operator/gpu_operator.tiltfile`
passes a third copy, `local/gpu-operator/gpu-operator.values.yaml`. Strip the
comments from that one too and it is identical to the other two, so the
configuration in this directory is the configuration CI exercises across the
profile matrix. The file is not the file CI reads, and nothing in the
repository enforces that the three stay equal. Change a value in one, change it
in all three.
