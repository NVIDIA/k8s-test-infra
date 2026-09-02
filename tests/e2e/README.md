# nvml-mock Go E2E

This directory contains the end-to-end tests for `nvml-mock`, the mock
`libnvidia-ml.so` chart used to simulate GPU nodes on Kind without physical GPU
hardware.

The active E2E entrypoint is the Go/Ginkgo harness under [`go/`](go/). It is the
Go port of [`docs/demo/standalone/demo.sh`](../../docs/demo/standalone/demo.sh)
and also covers the failure-injection flow from
[`docs/demo/failure-injection/run.sh`](../../docs/demo/failure-injection/run.sh).

## What The Harness Does

The suite does not own the cluster or the image. Tilt provisions both, with
`make cluster-create` and `tilt up -- <flags>` locally, or `tilt ci` in CI.
`make e2e` then does these steps:

1. Attach to that cluster with `E2E_KUBE_CONTEXT` and `E2E_CLUSTER_NAME`.
2. Run the checks for each selected GPU profile.
3. Reshape the mock with `helm upgrade --install`, for the scenarios that need a
   different shape. These runs set `image.repository` and `image.tag` to `E2E_IMAGE`.
4. Collect diagnostics into `E2E_ARTIFACTS` if a spec fails.

Step 3 is the reason why `E2E_IMAGE` must match the image that Tilt deployed. The
suite does not build or load it. If the ref is different, `helm upgrade` sets the
DaemonSet to an image that no node has, and the rollout stops with
`ImagePullBackOff` instead of the real cause.

The suite uses the default kubeconfig and passes the `E2E_KUBE_CONTEXT` context
explicitly to Helm and kubectl.

## Running Locally

Prerequisites:

- `docker`
- `kind`
- `kubectl`
- `helm`
- Go toolchain matching the project version

Common commands:

```bash
# Default local run: one profile, gb200.
make e2e

# Run a single explicit profile.
make e2e E2E_PROFILES=a100

# Run multiple profiles on the same cluster.
make e2e E2E_PROFILES="a100 h100"

# Run only selected use cases with Ginkgo labels.
make e2e E2E_GINKGO_FLAGS='--label-filter="nvidia-smi || nvlink"'

# Use the image that Tilt deployed. Do this if the ref is not the default,
# because the reshaping scenarios set the DaemonSet to this ref.
make e2e E2E_IMAGE=ghcr.io/nvidia/nvml-mock:tilt-<digest> E2E_PROFILES=a100

# Build and load the default ref. Do this before a scenario that reshapes the mock.
docker build -t nvml-mock:e2e -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:e2e --name mokka
```

`make e2e` intentionally targets only `./tests/e2e/go`, not `./tests/e2e/go/...`.
The suite package launches real Kind, Docker, Helm, and kubectl operations.
Helper packages such as `profile` and `ibutil` are ordinary unit-test packages.

## Profiles

Profiles describe the GPU topology the chart should render. The harness reads
the selected profile names from `E2E_PROFILES`.

Local default:

```text
gb200
```

CI default:

```text
a100, h100, b200, gb200, gb300, t4
```

The profile data source is:

```text
deployments/nvml-mock/helm/nvml-mock/profiles/
```

The `profile` package decodes those chart files and derives the expected GPU
count, NVLink topology, InfiniBand devices, fabricmanager behavior, and PCI root
complexes used by the assertions.

## Kind Config Selection

The default cluster topology is:

```text
docs/demo/kind.yaml
```

Profiles that need special cluster wiring can add:

```text
docs/demo/kind-<profile>.yaml
```

All profiles in a single `E2E_PROFILES` run must resolve to the same Kind config.
If two selected profiles require different configs, run them separately. The CI
matrix already runs each profile in its own job, so profile-specific Kind config
files are naturally isolated there.

## Scenario Layout

The Go suite is organized so scenario files read like a test map and supporting
code lives in small helper files:

```text
tests/e2e/go/
  e2e_suite_test.go              # Ginkgo entrypoint and suite lifecycle
  suite_build.go                 # Docker image build
  suite_paths.go                 # repo/chart/profile/Kind path resolution
  suite_cluster.go               # Kind setup, teardown, diagnostics, pod lookup
  suite_helm.go                  # nvml-mock Helm release construction
  scenario_standalone_test.go    # standalone demo scenario map
  scenario_standalone_setup.go   # per-profile install/setup helper
  scenario_failure_injection.go  # failure-injection scenario helpers
  scenario_gfd_test.go           # standalone GPU Feature Discovery scenario
  scenario_nri_test.go           # node-wide NRI ambient-injection scenario
  framework/                     # thin wrappers for kind, helm, and kubectl
  framework/pod/                 # generic pod.tpl.yaml plus the Spec that renders it
  assertions/                    # domain assertions for nvidia-smi, NVLink, IB, PCI
  profile/                       # profile parser and topology expectations
  ibutil/                        # InfiniBand output normalization helpers
```

Keep new test scenarios in separate `scenario_*.go` files. Keep framework code
generic; scenario-specific chart values and assertions should stay in the suite
or `assertions/`.

## Standalone Scenario

For each selected profile, the standalone scenario installs or upgrades the
`nvml-mock` release on the shared cluster and runs these checks:

- `labels`: record node labels once for the suite.
- `fgo`: verify fake GPU operator profile ConfigMaps.
- `mockfiles`: verify mock driver files, device nodes, NVML symlink, and config.
- `nvidia-smi`: verify host and in-pod GPU inventory.
- `nvlink`: verify NVLink topology, gated by fabricmanager settings.
- `ib`: verify InfiniBand mock devices and commands.
- `pcisysfs`: verify PCI sysfs topology.
- `ibping`: verify cross-node `ibping` and `iblinkinfo`.
- `failure-injection`: verify healthy, ECC, lost GPU, and fallen-off-bus modes.

The failure-injection upgrades reuse the installed Helm values and set fast
rolling-update options on the baseline release:

```text
updateStrategy.rollingUpdate.maxUnavailable=100%
terminationGracePeriodSeconds=1
```

Helm release stdout for `nvml-mock` is hidden during normal runs, but it remains
captured and is included in command errors.

## DRA Scenario

The `dra` scenario creates a Kind cluster from
[`go/assets/kind-dra-config.yaml`](go/assets/kind-dra-config.yaml), installs the
selected `nvml-mock` profile, validates the DRA driver-root mock files,
`nvidia-smi`, and NVLink topology, then installs the NVIDIA DRA driver.

After the DRA driver is ready, the scenario verifies ResourceSlice GPU count and
applies a `ResourceClaimTemplate` plus test pod. The pod must reach `Running`,
which proves DRA scheduling and `NodePrepareResources` succeeded.

## GPU Operator Scenario

The `gpu-operator` scenario creates a Kind cluster from
[`go/assets/kind-gpu-operator-config.yaml`](go/assets/kind-gpu-operator-config.yaml),
installs NVIDIA Container Toolkit in the Kind node, configures containerd for
CDI mode, installs the selected `nvml-mock` profile, and installs GPU Operator
with [`go/assets/gpu-operator-values.yaml`](go/assets/gpu-operator-values.yaml).

The scenario waits for the operator validator pod, records GFD labels, and
verifies profile-derived allocatable `nvidia.com/gpu` resources from the bundled
device plugin.

With `dcgmExporter` enabled in the operator values, the scenario also validates
DCGM against the mock NVML ([`go/assertions/dcgm.go`](go/assertions/dcgm.go),
`dcgm` / `xid` labels). It scrapes dcgm-exporter through the API-server pod proxy
and asserts `DCGM_FI_DEV_*` telemetry, time-varying power, and `DCGM_FI_PROF_*`
GPM metrics on Hopper+ profiles, then injects an Xid and asserts
`DCGM_FI_DEV_XID_ERRORS` (last, since it leaves the mock in a failed state).

## Multi-Node Scenario

The `multi-node` scenario creates a heterogeneous Kind fleet from
[`go/assets/kind-multi-node-config.yaml`](go/assets/kind-multi-node-config.yaml),
installs A100 and T4 `nvml-mock` releases on separate workers, verifies mock
files and InfiniBand behavior on both nodes, deploys the device plugin, and
schedules a GPU workload across the fleet.

## Node-Wide NRI Injection Scenario

The `nri` scenario is the Go port of
[`docs/demo/node-wide-injection/run.sh`](../../docs/demo/node-wide-injection).
It creates a dedicated Kind cluster from
[`go/assets/kind-nri-config.yaml`](go/assets/kind-nri-config.yaml) (four workers
with containerd NRI enabled), installs the selected `nvml-mock` profile with
`nri.enabled=true`, and — for fabric-attached profiles — a generated two-clique
ComputeDomain overlay derived from the discovered worker names.

It then applies a plain [`go/assets/nri-gpu-agent.yaml`](go/assets/nri-gpu-agent.yaml)
DaemonSet — no `nvidia.com/gpu` request, no hostPath/mock volumes, no `MOCK_*`
env — and asserts:

- the gpu-agent pod spec never requests `nvidia.com/gpu`;
- `nvidia-smi -L` inside the ambiently injected pod lists the profile's GPUs;
- `compute-domain`: on fabric profiles, each node reports its assigned clique /
  cluster UUID via the staged `check-fabric` consumer (skipped on non-fabric
  profiles such as `t4`, where the overlay is a no-op).

```bash
make e2e-nri                       # default gb200 (fabric + ComputeDomain)
make e2e-nri E2E_PROFILES=t4       # plain node-wide injection, no fabric checks
```

## Standalone GFD Scenario

The `gfd` scenario applies
[`go/assets/device-plugin-mock.yaml`](go/assets/device-plugin-mock.yaml), waits
for the mock device plugin DaemonSet and for allocatable GPUs on the profile
node, then applies [`go/assets/gfd-mock.yaml`](go/assets/gfd-mock.yaml) and
verifies that standalone GPU Feature Discovery derives the required node labels
from the mock GPU inventory.

This scenario is skipped by default because the standalone GFD image is pulled
from `nvcr.io`. The Go workflow also excludes the `gfd` label until
[#446](https://github.com/NVIDIA/k8s-test-infra/issues/446) resolves the CI
image/auth path. Enable it locally when that image is available:

```bash
make e2e E2E_RUN_NGC=true E2E_GINKGO_FLAGS='--label-filter="gfd"'
```

## Labels

Every profile is also a Ginkgo label, for example `a100`, `h100`, `gb200`, or
`t4`.

Use-case labels:

- `labels`
- `fgo`
- `mockfiles`
- `nvidia-smi`
- `nvlink`
- `ib`
- `pcisysfs`
- `ibping`
- `device-plugin`
- `dra`
- `gpu-operator`
- `dcgm`
- `xid`
- `multi-node`
- `nri`
- `nri-inject`
- `nfd`
- `compute-domain`
- `failure-injection`
- `gfd`

Examples:

```bash
make e2e E2E_PROFILES=h100 E2E_GINKGO_FLAGS='--label-filter="failure-injection"'
make e2e E2E_GINKGO_FLAGS='--label-filter="nvidia-smi || nvlink"'
make e2e E2E_GINKGO_FLAGS='--label-filter="gb200 && ibping"'
make e2e E2E_PROFILES=a100 E2E_GINKGO_FLAGS='--label-filter="dra"'
make e2e E2E_PROFILES=a100 E2E_GINKGO_FLAGS='--label-filter="gpu-operator"'
make e2e E2E_PROFILES=a100,t4 E2E_GINKGO_FLAGS='--label-filter="multi-node"'
make e2e E2E_GINKGO_FLAGS='--label-filter="nri"'
make e2e E2E_PROFILES=a100 E2E_GINKGO_FLAGS='--label-filter="nfd"'
make e2e E2E_RUN_NGC=true E2E_GINKGO_FLAGS='--label-filter="gfd"'
```

## Environment Variables

| Variable | Default | Purpose |
|---|---:|---|
| `E2E_PROFILES` | `gb200` | Space- or comma-separated profile names. |
| `E2E_PROFILES_DIR` | `deployments/nvml-mock/helm/nvml-mock/profiles` | The chart profiles directory. This is the deployed source of truth. |
| `E2E_IMAGE` | `nvml-mock:e2e` | The ref of the image that is already on every node. The reshaping scenarios set the DaemonSet to this ref. The suite does not build or load it. |
| `E2E_CLUSTER_NAME` | `mokka` | The Kind cluster name. It appears in attach errors and diagnostics. |
| `E2E_KUBE_CONTEXT` | `kind-mokka` | The kubeconfig context of the cluster that the suite attaches to. |
| `E2E_ARTIFACTS` | `artifacts/e2e/go` | Directory for failure diagnostics. |
| `E2E_RUN_NGC` | `false` | Run scenarios that need `nvcr.io` images, such as `gfd`. |
| `E2E_CLUSTER_TIMEOUT` | `5m` | The limit for the cluster attach wait. |
| `E2E_HELM_TIMEOUT` | `5m` | Helm install/upgrade timeout. |
| `E2E_READY_TIMEOUT` | `2m` | Kubernetes readiness wait timeout, sized for a single rollout. |
| `E2E_OPERAND_SETTLE_TIMEOUT` | `5m` | Timeout for waits that must outlast a GPU Operator reconcile replacing its operands, not just one rollout. |
| `E2E_POLL_INTERVAL` | `2s` | Polling interval for readiness checks. |

## CI Behavior

CI runs the harness through
[`nvml-mock-e2e-go.yaml`](../../.github/workflows/nvml-mock-e2e-go.yaml).

The workflow:

1. Detects the project Go version unless one is explicitly provided.
2. Builds the `nvml-mock` image exactly once in a dedicated
   `build-nvmlmock-image` job (buildx + GHA layer cache), exports it to a
   tarball and uploads it as a run-scoped artifact (`nvml-mock-image`, 1-day
   retention — long enough to re-run an individual failed leg). Artifacts need
   no registry credentials, so this works on fork PRs without depending on a
   third-party registry.
3. Makes every leg `needs: build-nvmlmock-image`, downloads the artifact and
   loads it into the leg's Docker daemon with `make image-load`, which asserts
   the expected ref is present afterwards. The leg then creates the Kind cluster,
   loads that image onto the nodes with `kind load`, and rolls it out with
   `tilt ci -- --nvmlmock-image="$E2E_IMAGE"`.
4. Runs one GPU profile per matrix job.
5. Prints collected diagnostics if the job fails.

Manual workflow dispatch defaults to `gb200` for a fast run. The reusable CI
workflow defaults to the full profile matrix.

## Diagnostics

On spec failure, the harness writes diagnostics under `E2E_ARTIFACTS`. The
collector captures common Kubernetes state, `nvml-mock` logs, and relevant node
files where possible.

The suite does not delete the cluster. After a local failure, you can inspect it:

```bash
kubectl --context kind-mokka get pods -A
helm --kube-context kind-mokka -n mokka status nvml-mock
```

Delete it manually when done:

```bash
kind delete cluster --name nvml-mock-e2e
```

## Unit And Helper Tests

The root e2e package contains the real Ginkgo suite, so this command launches
Docker and Kind:

```bash
go test -tags=e2e ./tests/e2e/go
```

For quick helper checks, run focused tests instead:

```bash
GOCACHE="$PWD/.cache/go-build" GOWORK=off go test -tags=e2e ./tests/e2e/go \
  -run 'TestDemoReleaseTargetsDedicatedNamespace|TestUseCaseLabels|TestKindConfig|TestSelectedKindConfig|TestMaxIntegerLine|TestHasFailureMarker'

GOCACHE="$PWD/.cache/go-build" GOWORK=off go test -tags=e2e ./tests/e2e/go/framework/...
GOCACHE="$PWD/.cache/go-build" GOWORK=off go test ./tests/e2e/go/profile ./tests/e2e/go/ibutil
```

The `e2e` build tag keeps the harness out of normal `go test ./...` and
`go build ./...` paths.
