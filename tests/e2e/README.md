# Mokka End-to-End Tests

These tests prove that real NVIDIA software — `nvidia-smi`, the device plugin,
the DRA driver, the GPU Operator — behaves as though it were running on GPU
hardware when it is actually running against `mokka` on a CPU-only Kind
cluster.

The suite is written in Go with [Ginkgo](https://onsi.github.io/ginkgo/) and
lives under [`go/`](go/). It began as the Go port of the
[standalone demo](../../docs/guides/standalone/demo.sh) and the
[failure-injection demo](../../docs/guides/failure-injection/run.sh), and has since
grown to cover DRA, the GPU Operator, multi-node fleets, node-wide NRI
injection, and NFD label provenance.

For which upstream consumer versions are actually exercised, and which are only
written but not run, see the [version matrix](VERSION-MATRIX.md).

## How the suite fits together

Three ideas explain most of the harness.

### The suite attaches to a cluster; it does not create one

`make e2e` does not create a Kind cluster and does not build or load the mock
image. Both must exist first. The suite then:

1. attaches to the cluster named by `E2E_CLUSTER_NAME` through the
   `E2E_KUBE_CONTEXT` kubeconfig context;
2. runs the selected specs for each selected GPU profile;
3. reshapes the mock with `helm upgrade --install` for scenarios that need a
   different GPU shape, setting `image.repository` and `image.tag` to
   `E2E_IMAGE`;
4. writes diagnostics to `E2E_ARTIFACTS` when a spec fails.

Step 3 is why `E2E_IMAGE` must name the image already present on every node. If
it names anything else, `helm upgrade` points the DaemonSet at an image no node
has, and the rollout fails with `ImagePullBackOff` instead of the real problem.

### GPU profiles drive the expectations

A profile is a chart values file describing one GPU model's topology. The
harness reads the profiles named in `E2E_PROFILES` from the chart itself:

```text
deployments/nvml-mock/helm/nvml-mock/profiles/
```

The `profile` package decodes those files and derives the expected GPU count,
NVLink topology, InfiniBand (IB) devices, fabricmanager behaviour and PCI root
complexes. Assertions compare observed output against those derivations rather
than against hardcoded values, so a profile change updates the expectations with
it. The chart profiles are the deployed source of truth.

### Labels select the specs

Every spec carries Ginkgo labels, and every profile name is also a label. Select
work with `--label-filter`. This is how a scenario is run in isolation, and how
CI splits the suite into parallel jobs.

**`make e2e` does not run everything.** It applies a default filter written
entirely as exclusions:

```make
E2E_DEFAULT_LABEL_FILTER ?= !validator && !dra && !gpu-operator && !multi-node && !nri && !nfd
```

The filter is phrased that way because the standalone scenario has no scenario
label of its own — it is whatever remains once the six specialised scenarios are
excluded. So a bare `make e2e` runs the standalone scenario only; use the
per-scenario targets for the rest.

Specs also skip themselves when the selected profile cannot support them: no
fabric means no fabric-health checks, no InfiniBand means no `ibping`, fewer
than two GPUs means no isolation check. A consolidated list of skipped specs is
printed at the end of every run, so a skip is never silent.

## Getting started

### Prerequisites

- `docker`, `kind`, `kubectl`, `helm`
- A Go toolchain matching the version in `go.mod`

### Provision the cluster and image

The cluster always comes from `make cluster-create`, locally and in CI. It
builds the CDI-enabled Kind node image and creates a cluster from
`local/kind/$(PROFILE).kind.yaml`, where `PROFILE` defaults to `default`. That
yields a cluster named `mokka`, so Kind derives the context `kind-mokka` — which
is what the suite defaults to.

Tilt does the rollout: it installs the chart and, given `--nvmlmock-image`,
pins the DaemonSet to an image you already built.

```bash
make cluster-create

docker build -t nvml-mock:e2e -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:e2e --name mokka

tilt up -- --nvmlmock-image=nvml-mock:e2e     # or `tilt ci` for a headless run
```

[`local/kind/default.kind.yaml`](../../local/kind/default.kind.yaml) gives one
control-plane and two workers named `worker-0` and `worker-1`, turns on the
`DynamicResourceAllocation` feature gate, and enables the containerd NRI socket
that the node-wide injection scenario requires.

### Run

```bash
# Standalone scenario, default profile (gb200).
make e2e

# One explicit profile.
make e2e E2E_PROFILES=a100

# Several profiles against the same cluster.
make e2e E2E_PROFILES="a100 h100"

# A subset of checks.
make e2e E2E_GINKGO_FLAGS='--label-filter="nvidia-smi || nvlink"'

# Against an image Tilt deployed under a different ref.
make e2e E2E_IMAGE=ghcr.io/nvidia/nvml-mock:tilt-<digest>
```

`make e2e` targets `./tests/e2e/go` and deliberately not `./tests/e2e/go/...`.
Only the root package drives a real cluster; the subpackages hold ordinary unit
tests that must not be scoped by `E2E_PROFILES`.

## Scenarios

| Scenario | Target | Primary labels | What it proves |
|---|---|---|---|
| Standalone | `make e2e` | `labels`, `fgo`, `mockfiles`, `nvidia-smi`, `nvlink`, `ib`, `pcisysfs`, `ibping`, `failure-injection`, `runtime-control` | The mock renders a complete GPU node: driver files, device nodes, `nvidia-smi` inventory, NVLink topology, IB devices, PCI sysfs tree, cross-node `ibping`, and the four failure modes |
| DRA driver | `make e2e-dra` | `dra` | Dynamic Resource Allocation (DRA) scheduling works end to end: ResourceSlices report the profile's GPU count, and a pod using a `ResourceClaimTemplate` reaches `Running`, which requires `NodePrepareResources` to have succeeded |
| GPU Operator | `make e2e-gpu-operator` | `gpu-operator`, `device-plugin`, `dcgm`, `xid`, `pcisysfs`, `runtime-control` | The full operator stack accepts the mock: the validator pod starts, GPU Feature Discovery (GFD) labels appear, allocatable `nvidia.com/gpu` matches the profile, and DCGM telemetry is answered |
| Multi-node fleet | `make e2e-multi-node` | `multi-node` | A heterogeneous fleet works: separate A100 and T4 releases on different workers, correct per-node mock files and IB behaviour, and a GPU workload scheduled across them |
| Node-wide NRI injection | `make e2e-nri` | `nri`, `nri-*`, `compute-domain`, `imex-channels` | An ordinary pod that requests no GPU, mounts no hostPath and sets no `MOCK_*` env still sees GPUs, via Node Resource Interface (NRI) ambient injection |
| NFD label provenance | `make e2e-nfd` | `nfd`, `nfd-provenance` | Node Feature Discovery (NFD) derives `feature.node.kubernetes.io/pci-10de.present` from the feature file the mock writes — and that the mock does not write the label itself. Pinned to `a100`, because the label is vendor-only and identical across profiles |
| CUDA validator | opt-in | `validator` | The CUDA vectorAdd sample runs against the mock CUDA library. **Skipped by default** — see below |

### Node-wide NRI injection

The Go port of the [node-wide injection demo](../../docs/guides/node-wide-injection).
It installs the profile with `nri.enabled=true` and, for fabric-attached
profiles, a generated two-clique ComputeDomain overlay derived from the
discovered worker names. It then applies a plain
[`nri-gpu-agent.yaml`](go/assets/nri-gpu-agent.yaml) DaemonSet and asserts that
the pod spec never requests `nvidia.com/gpu`, that `nvidia-smi -L` inside it
lists the profile's GPUs, and — on fabric profiles — that each node reports its
assigned clique and cluster UUID.

```bash
make e2e-nri                    # gb200: fabric and ComputeDomain checks included
make e2e-nri E2E_PROFILES=t4    # plain injection; fabric checks skip
```

This scenario carries the largest label vocabulary in the suite. The `nri-*`
labels select individual behaviours — device-plugin interaction
(`nri-dp-isolation`, `nri-dp-suppression`, `nri-dp-optin`, `nri-dp-plain`,
`nri-dp-scheduling`), Container Device Interface handling (`nri-cdi`,
`nri-cdi-inject`, `nri-cdi-suppression`), failure propagation (`nri-failure`,
`nri-failure-detect`, `nri-failure-inject`, `nri-failure-recover`), and
`nri-imex`, `nri-ib-minimal`, `nri-alloc-memory`, `nri-device-plugin`,
`nri-inject`.

### GPU Operator and DCGM

With `dcgmExporter` enabled in [`gpu-operator-values.yaml`](gpu-operator-values.yaml),
the scenario also validates DCGM against the mock
([`go/assertions/dcgm.go`](go/assertions/dcgm.go), labels `dcgm` and `xid`). It
scrapes dcgm-exporter through the API-server pod proxy and asserts
`DCGM_FI_DEV_*` telemetry, time-varying power, and `DCGM_FI_PROF_*` GPM metrics
on Hopper and later profiles.

The Xid injection runs **last**, because it leaves the mock in a failed state.

### CUDA validator

Skipped by default: the GFD and CUDA validator images come from `nvcr.io`, and
CI has no credential path for them yet
([#446](https://github.com/NVIDIA/k8s-test-infra/issues/446)). Enable it locally
once those images are reachable:

```bash
make e2e E2E_RUN_NGC=true E2E_GINKGO_FLAGS='--label-filter="validator"'
```

It applies [`device-plugin-mock.yaml`](go/assets/device-plugin-mock.yaml), waits
for allocatable GPUs, applies [`gfd-mock.yaml`](go/assets/gfd-mock.yaml),
verifies the required GFD labels, then runs
[`validator-mock.yaml`](go/assets/validator-mock.yaml).

### Reference Kind configs

The scenarios do not create clusters, but three of them expect a specific
cluster shape. These files record it, and are the configs to use when building
that cluster by hand:

- [`kind-dra-config.yaml`](kind-dra-config.yaml)
- [`kind-gpu-operator-config.yaml`](kind-gpu-operator-config.yaml)
- [`kind-multi-node-config.yaml`](kind-multi-node-config.yaml)

## Reference

### Environment variables

Read by the suite:

| Variable | Default | Controls |
|---|---|---|
| `E2E_PROFILES` | `gb200` | Profiles to exercise. Comma- or space-separated. Read at package init, so `--label-filter` can narrow the generated specs but never widen them |
| `E2E_IMAGE` | `nvml-mock:e2e` | Ref of the image already on every node. The suite never builds or loads it |
| `E2E_CLUSTER_NAME` | `mokka` | Kind cluster name, used in attach errors and diagnostics |
| `E2E_KUBE_CONTEXT` | `kind-mokka` | Kubeconfig context, passed explicitly to helm and kubectl. Node discovery goes through this, not the cluster name |
| `E2E_ARTIFACTS` | `artifacts/e2e/go` | Where failure diagnostics are written |
| `E2E_RUN_NGC` | `false` | Run scenarios needing `nvcr.io` images. Truthy: `1`, `true`, `yes`, `on` |
| `E2E_REPO_ROOT` | walks up for `go.mod` | Overrides repo-root discovery for chart and profile paths |
| `E2E_HELM_TIMEOUT` | `5m` | `helm upgrade --install --wait` |
| `E2E_READY_TIMEOUT` | `2m` | DaemonSet, pod and label readiness waits |
| `E2E_OPERAND_SETTLE_TIMEOUT` | `5m` | Only the GPU Operator Xid spec, which must outlast a reconcile replacing the operands |
| `E2E_POLL_INTERVAL` | `2s` | `Eventually` poll interval |

An unparseable duration falls back to the default silently.

Read by the Makefile, not the suite:

| Variable | Default | Controls |
|---|---|---|
| `E2E_TIMEOUT` | `90m` | Ginkgo's whole-suite timeout |
| `E2E_GINKGO_FLAGS` | the default label filter | Flags passed to Ginkgo |
| `E2E_DEFAULT_LABEL_FILTER` | see above | The filter `E2E_GINKGO_FLAGS` wraps |

`E2E_PROFILES_DIR` and `E2E_CLUSTER_TIMEOUT` have accessors in
`framework/config` but **no callers** — setting them changes nothing. The
profiles directory is resolved from the repo root instead.

Some timeouts are constants rather than variables: NFD label waits (3m), the
`ibping` retry budget (5 attempts, 10s apart), the operator validator wait (5m),
and the runtime-override TTL waits (30s).

### Label filter examples

```bash
make e2e E2E_PROFILES=h100 E2E_GINKGO_FLAGS='--label-filter="failure-injection"'
make e2e E2E_GINKGO_FLAGS='--label-filter="nvidia-smi || nvlink"'
make e2e E2E_GINKGO_FLAGS='--label-filter="gb200 && ibping"'
make e2e-nri E2E_GINKGO_FLAGS='--label-filter="nri-cdi-suppression"'
```

### Where things live

```text
tests/e2e/go/
  scenario_*.go     one scenario each; *_test.go files carry the Ginkgo specs
  framework/        thin wrappers over kind, helm, kubectl, and diagnostics
  assertions/       domain assertions: nvidia-smi, NVLink, IB, PCI sysfs, GFD, DCGM
  profile/          profile parser and the topology expectations derived from it
  ibutil/           InfiniBand output normalisation
  assets/           embedded manifests applied by scenarios
```

## Continuous integration

CI runs the suite through
[`nvml-mock-e2e-go.yaml`](../../.github/workflows/nvml-mock-e2e-go.yaml).

The image is built **once** per run in a dedicated job, exported to a tarball
and uploaded as a run-scoped artifact. Every scenario job depends on that build,
downloads the artifact, loads it with `make image-load`, creates its own Kind
cluster, loads the image onto the nodes, and rolls it out with
`tilt ci -- --nvmlmock-image="$E2E_IMAGE"`. Using an artifact rather than a
registry keeps this working on fork pull requests with no credentials.

Six scenario jobs run in parallel: `e2e`, `e2e-dra`, `e2e-gpu-operator`,
`e2e-multi-node`, `e2e-nri` and `e2e-nfd`. Each runs one GPU profile per matrix
entry, with `fail-fast: false` so one profile's failure does not cancel the
others. `e2e-nfd` is pinned to `a100`; `e2e-multi-node` has no matrix and uses
its own fixed A100/T4 topology. There is no validator job.

Pull requests get the full matrix — `a100`, `h100`, `b200`, `gb200`, `gb300`,
`t4`. Manual `workflow_dispatch` defaults to `gb200` alone for a fast run. The
`l40s` profile is supported by the chart and the `profile` package but appears
in no CI matrix, so it is only ever exercised locally.

CI stages [`ci/nvml-mock.values.yaml`](ci/nvml-mock.values.yaml) as Tilt's
local-override values file, which turns on dynamic metrics with a fixed seed and
a 100% rolling-update budget.

## Troubleshooting

The suite never deletes the cluster, so a failed run can be inspected directly.

| Symptom | Likely cause | What to do |
|---|---|---|
| Pods stuck in `ImagePullBackOff` after a reshaping spec | `E2E_IMAGE` names an image that is not on the nodes | Set `E2E_IMAGE` to the ref Tilt deployed, or `kind load` the ref you passed |
| Attach fails or times out at startup | Cluster name or context does not match | Confirm `kind get clusters`; the defaults expect `mokka` and `kind-mokka` |
| A scenario is silently absent from the run | The default label filter excludes it | Use the scenario's own target, or override `E2E_GINKGO_FLAGS` |
| `validator` specs all skip | `E2E_RUN_NGC` is `false` | Set `E2E_RUN_NGC=true`, with `nvcr.io` images reachable |
| GPU Operator waits time out | Operand replacement outlasts the readiness wait | Raise `E2E_OPERAND_SETTLE_TIMEOUT` rather than `E2E_READY_TIMEOUT` |
| Assertions fail right after a DCGM run | Xid injection ran and left the mock failed | Expected; it is ordered last. Reinstall the release before re-running other specs |

Inspect a failed cluster:

```bash
kubectl --context kind-mokka get pods -A
helm --kube-context kind-mokka -n mokka status nvml-mock
```

Diagnostics are collected **per failing spec**, into a per-scenario
subdirectory of `E2E_ARTIFACTS` (`artifacts/e2e/go/<scenario>`): pod and node
state, cluster events, and `nvml-mock` logs — plus the previous container
instance's logs when a restart was observed, which is usually the interesting
one. The DRA scenario adds ResourceSlice and ResourceClaim dumps. Collection is
best-effort and never fails a spec.

In CI these are printed into the job log, not uploaded as an artifact, so read
them from the failed job's output.

Delete the cluster when finished:

```bash
make cluster-delete          # or: kind delete cluster --name mokka
```

## Adding a scenario

Put each scenario in its own `scenario_*.go` file and give its specs labels —
one for the scenario, plus finer labels for individually runnable behaviours.
Add a `make e2e-<scenario>` target if it needs its own cluster shape, and add
its label to `E2E_DEFAULT_LABEL_FILTER` so a bare `make e2e` does not pick it
up.

Keep `framework/` generic. Scenario-specific chart values and assertions belong
in the scenario file or in `assertions/`.

Three constraints are easy to get wrong:

- **Containers are `Ordered`, and `BeforeAll` does real setup.** Running one
  spec with `--focus` only works if its container's `BeforeAll` runs too. Put
  readiness barriers in `BeforeAll`, not in a spec that a filter might exclude.
- **Some specs deliberately leave the cluster dirty.** The GPU Operator Xid spec
  and the standalone failure-injection specs mutate the release and are ordered
  last for that reason. If you add a spec that mutates shared state, order it
  last and restore what you can in `AfterAll`.
- **Pin work to a node.** Runtime overrides are per-node hostPath files, so a
  spec that changes state on one node and reads it from a pod on another will
  pass locally and fail in CI. Use the node-pinned helpers, and select workers
  through `Cluster.Workers` rather than by position — the mock tolerates the
  control-plane taint, so a positional pick can land there.

The `e2e` build tag keeps the cluster-driving suite out of `go test ./...`. The
subpackages are untagged and run in the normal unit-test path:

```bash
# Unit tests: no cluster required.
go test ./tests/e2e/go/framework/... ./tests/e2e/go/assertions/... \
        ./tests/e2e/go/profile ./tests/e2e/go/ibutil

# The two pure unit tests inside the e2e-tagged root package.
go test -tags=e2e ./tests/e2e/go \
  -run 'TestDRAResourceClaimManifest|TestValidatorGFDRequiredLabels'
```

`profile/` holds a drift guard that checks **all** known profiles regardless of
`E2E_PROFILES`, which is why it must stay outside `make e2e`.
