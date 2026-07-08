# nvml-mock Go E2E

This directory contains the end-to-end tests for `nvml-mock`, the mock
`libnvidia-ml.so` chart used to simulate GPU nodes on Kind without physical GPU
hardware.

The active E2E entrypoint is the Go/Ginkgo harness under [`go/`](go/). It is the
Go port of [`docs/demo/standalone/demo.sh`](../../docs/demo/standalone/demo.sh)
and also covers the failure-injection flow from
[`docs/demo/failure-injection/run.sh`](../../docs/demo/failure-injection/run.sh).

## What The Harness Does

`make e2e` runs one Ginkgo suite that owns the full test lifecycle:

1. Build the `nvml-mock:e2e` image, unless `E2E_SKIP_BUILD=true`.
2. Create one multi-node Kind cluster from [`docs/demo/kind.yaml`](../../docs/demo/kind.yaml).
3. Load the image into Kind.
4. Install `nvml-mock` into the dedicated `nvml-mock-system` namespace.
5. Run the standalone demo checks for each selected GPU profile.
6. Collect diagnostics on failure.
7. Keep the Kind cluster by default for debugging.

The suite uses the default kubeconfig but always passes the explicit Kind context
`kind-nvml-mock-e2e` to Helm and kubectl.

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

# Reuse a pre-built image and skip the in-suite Docker build.
docker build -t nvml-mock:e2e -f deployments/nvml-mock/Dockerfile .
make e2e E2E_SKIP_BUILD=true E2E_IMAGE=nvml-mock:e2e E2E_PROFILES=a100

# Delete the Kind cluster during teardown instead of keeping it.
make e2e E2E_KEEP_CLUSTER=false
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
  scenario_validator_test.go     # CUDA vectorAdd validator scenario
  framework/                     # thin wrappers for kind, helm, kubectl, docker
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

## CUDA Validator Scenario

The `validator` scenario applies [`go/assets/validator-mock.yaml`](go/assets/validator-mock.yaml),
which runs the CUDA vectorAdd sample against the mock CUDA library mounted from
the `nvml-mock` DaemonSet.

The validator Job requests `nvidia.com/gpu`, so the scenario first applies
[`go/assets/device-plugin-mock.yaml`](go/assets/device-plugin-mock.yaml), waits
for the mock device plugin DaemonSet, and waits for allocatable GPUs on the
profile node.

This scenario is skipped by default because the validator and device-plugin
images are pulled from `nvcr.io`. Enable it when those images are available:

```bash
make e2e E2E_RUN_NGC=true E2E_GINKGO_FLAGS='--label-filter="validator"'
```

## Labels

Every profile is also a Ginkgo label, for example `a100`, `h100`, `gb200`, or
`t4`.

Use-case labels:

- `labels`
- `fgo`
- `nvidia-smi`
- `nvlink`
- `ib`
- `pcisysfs`
- `ibping`
- `failure-injection`
- `validator`

Examples:

```bash
make e2e E2E_PROFILES=h100 E2E_GINKGO_FLAGS='--label-filter="failure-injection"'
make e2e E2E_GINKGO_FLAGS='--label-filter="nvidia-smi || nvlink"'
make e2e E2E_GINKGO_FLAGS='--label-filter="gb200 && ibping"'
make e2e E2E_RUN_NGC=true E2E_GINKGO_FLAGS='--label-filter="validator"'
```

## Environment Variables

| Variable | Default | Purpose |
|---|---:|---|
| `E2E_PROFILES` | `gb200` | Space- or comma-separated profile names. |
| `E2E_IMAGE` | `nvml-mock:e2e` | Image ref to build and Kind-load. |
| `E2E_SKIP_BUILD` | `false` | Skip the in-suite Docker build and reuse `E2E_IMAGE`. |
| `E2E_KEEP_CLUSTER` | `true` | Keep the Kind cluster after the suite. Set `false` to delete it. |
| `E2E_ARTIFACTS` | `artifacts/e2e` | Directory for failure diagnostics. |
| `E2E_BUILDX_GHA_CACHE` | `false` | Enable buildx GitHub Actions cache flags. |
| `E2E_GOLANG_VERSION` | empty | Optional Docker build arg override. |
| `E2E_RUN_NGC` | `false` | Run scenarios that need `nvcr.io` images, such as `validator`. |
| `E2E_CLUSTER_TIMEOUT` | `5m` | Kind cluster setup timeout. |
| `E2E_HELM_TIMEOUT` | `5m` | Helm install/upgrade timeout. |
| `E2E_READY_TIMEOUT` | `2m` | Kubernetes readiness wait timeout. |
| `E2E_POLL_INTERVAL` | `2s` | Polling interval for readiness checks. |

## CI Behavior

CI runs the harness through
[`nvml-mock-e2e-go.yaml`](../../.github/workflows/nvml-mock-e2e-go.yaml).

The workflow:

1. Detects the project Go version unless one is explicitly provided.
2. Builds `nvml-mock:e2e` once per matrix leg with buildx and GHA cache.
3. Sets `E2E_SKIP_BUILD=true` so the harness reuses that image.
4. Runs one GPU profile per matrix job.
5. Prints collected diagnostics if the job fails.

Manual workflow dispatch defaults to `gb200` for a fast run. The reusable CI
workflow defaults to the full profile matrix.

## Diagnostics

On spec failure, the harness writes diagnostics under `E2E_ARTIFACTS`. The
collector captures common Kubernetes state, `nvml-mock` logs, and relevant node
files where possible.

Because `E2E_KEEP_CLUSTER=true` by default, local failures leave the Kind cluster
available for inspection:

```bash
kubectl --context kind-nvml-mock-e2e get pods -A
helm --kube-context kind-nvml-mock-e2e -n nvml-mock-system status nvml-mock
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
