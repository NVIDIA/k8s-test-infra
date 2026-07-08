# nvml-mock E2E Tests

End-to-end tests that deploy NVIDIA GPU consumers on a Kind cluster using the
nvml-mock chart (mock NVML + CUDA libraries instead of real hardware).

## Go harness (Ginkgo)

The e2e suite is the Go/Ginkgo port of [`docs/demo/standalone/demo.sh`](../../docs/demo/standalone/demo.sh).
It owns the **full lifecycle** — Kind cluster create/teardown, image
build/load, `helm upgrade --install`, validation, and failure diagnostics —
behind a single entrypoint that runs identically locally and in CI. It is gated
by the `e2e` build tag, so it never affects the normal `go test ./...` /
`go build ./...` paths.

**One shared cluster.** A single multi-node Kind cluster
([`docs/demo/kind.yaml`](../../docs/demo/kind.yaml): 1 control-plane + 3
workers) is created **once** for the whole suite. Each selected GPU profile is
then deployed onto that same cluster via `helm upgrade --install` (a chart
upgrade, **not** a cluster rebuild) and validated in place. The cluster is only
rebuilt when a scenario genuinely needs different cluster-level config; the demo
flow uses one topology for everything, including cross-node ibping (the 3
workers each run an nvml-mock pod).

```bash
# Default single-profile run (gb200)
make e2e

# One profile, fast inner loop
make e2e E2E_PROFILES=a100

# Scope with Ginkgo labels (each profile is also a label)
make e2e E2E_GINKGO_FLAGS='--label-filter=a100'

# Reuse a pre-built image (skip the in-suite build)
docker build -t nvml-mock:e2e -f deployments/nvml-mock/Dockerfile .
make e2e E2E_SKIP_BUILD=true E2E_IMAGE=nvml-mock:e2e E2E_PROFILES=a100
```

Prerequisites: `docker`, `kind`, `kubectl`, `helm`, and a Go toolchain. The
cluster uses the default kubeconfig with the explicit Kind context
(`kind-nvml-mock-e2e`) and is kept by default for debugging; set
`E2E_KEEP_CLUSTER=false` to delete it during teardown.

### Checks per profile (ported from `demo.sh`)

Each profile runs, against the shared cluster:

| Check | demo.sh step | Source of truth |
|---|---|---|
| fake-GPU-operator profile ConfigMaps (`run.ai/gpu-profile=true`) | profile ConfigMaps | chart `integrations-fgo.yaml` |
| `nvidia-smi` GPU inventory | step 7 | `profiles/<name>.yaml` |
| NVLink topology (gated on fabricmanager) | step 7b | `profiles/<name>.yaml` |
| InfiniBand mock (`ibstat` / `ibv_devinfo`) | step 8 | `profiles/<name>.yaml` |
| PCI sysfs topology (device count, relative symlinks, numa_node, root complexes) | step 9 | `profiles/<name>.yaml` `pcie_topology` |
| cross-node `ibping` + `iblinkinfo` | steps 10–11 | `profiles/<name>.yaml` |
| failure injection (`healthy`, `ecc_uncorrectable`, `lost`, `fallen_off_bus`) | `docs/demo/failure-injection/run.sh` | `h100` profile |

### Environment knobs

| Variable | Default | Purpose |
|---|---|---|
| `E2E_PROFILES` | `gb200` | profiles to run against the shared cluster |
| `E2E_IMAGE` | `nvml-mock:e2e` | image ref the harness builds + kind-loads |
| `E2E_SKIP_BUILD` | `false` | reuse a pre-built `E2E_IMAGE` |
| `E2E_KEEP_CLUSTER` | `true` | keep the Kind cluster after the suite; set `false` to delete it |
| `E2E_ARTIFACTS` | `artifacts/e2e` | where failure diagnostics are written |
| `E2E_BUILDX_GHA_CACHE` | `false` | add `--cache-to/--cache-from type=gha` to the in-suite build (CI) |

The single source of truth for per-profile expectations (GPU count, HCA count,
NV# links, fabricmanager state, PCIe root complexes) is the chart `profiles/`
directory, decoded by the `profile` package. `profile/profile_test.go` cross-checks those derivations
against the engine oracle (`pkg/gpu/mocknvml/engine/topology_test.go`) so the
chart and engine profile copies cannot silently drift.

The default Kind topology comes from [`docs/demo/kind.yaml`](../../docs/demo/kind.yaml).
Profiles that need different cluster wiring can add
`docs/demo/kind-<profile>.yaml`; the harness uses that file when the selected
profile matches. A single run must use one Kind config, so profiles that need
different configs should run in separate `E2E_PROFILES` invocations (the CI
matrix already does this).

CI runs this suite via [`nvml-mock-e2e-go.yaml`](../../.github/workflows/nvml-mock-e2e-go.yaml),
which builds the image once per leg with the buildx GHA layer cache and then
runs the harness with `E2E_SKIP_BUILD=true`.

## What runs in CI

The `e2e-device-plugin` and `e2e-dra` jobs run automatically on every push.
They verify:

- nvml-mock DaemonSet deploys and creates mock device files
- NVIDIA device plugin discovers mock GPUs and registers `nvidia.com/gpu`
- DRA driver discovers mock GPUs and publishes ResourceSlices

## Standalone GFD/validator steps (disabled)

The `e2e-device-plugin` job has standalone GFD and CUDA validator steps that
pull directly from `nvcr.io`. These are disabled (`if: false`) because the
standalone images may require NGC authentication. GPU Operator images are
public and do not require NGC credentials -- the separate `e2e-gpu-operator`
job uses that path and works without auth.

To run the standalone steps locally (NGC auth may be needed):

```bash
# 1. Create Kind cluster
kind create cluster --name nvml-mock-e2e

# 2. Build and load nvml-mock image
docker build -t nvml-mock:e2e -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:e2e --name nvml-mock-e2e

# 3. Install nvml-mock chart
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock --set image.tag=e2e --set gpu.count=2 \
  --wait --timeout 120s

# 4. Deploy device plugin
kubectl apply -f tests/e2e/device-plugin-mock.yaml
kubectl -n kube-system wait --for=condition=ready pod -l name=nvidia-device-plugin-mock --timeout=120s

# 5. Pull, load, and deploy GFD (may require: docker login nvcr.io)
docker pull nvcr.io/nvidia/gpu-feature-discovery:v0.17.0
kind load docker-image nvcr.io/nvidia/gpu-feature-discovery:v0.17.0 --name nvml-mock-e2e
kubectl apply -f tests/e2e/gfd-mock.yaml

# 6. Pull, load, and run CUDA validator (may require: docker login nvcr.io)
docker pull nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0
kind load docker-image nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0 --name nvml-mock-e2e
kubectl apply -f tests/e2e/validator-mock.yaml
kubectl wait --for=condition=complete job/gpu-validator-mock --timeout=120s
```

## Enabling standalone GFD/validator in CI

Once confirmed that the standalone `nvcr.io` images are publicly accessible:

1. Remove the `if: false` conditions from the GFD and validator steps
2. The image pull + kind load is already embedded in the step scripts

## Files

| File | Purpose |
|---|---|
| `device-plugin-mock.yaml` | Device plugin DaemonSet for mock GPUs |
| `gfd-mock.yaml` | GPU Feature Discovery DaemonSet |
| `validator-mock.yaml` | CUDA vectorAdd validator Job |
| `gpu-operator-values.yaml` | GPU Operator Helm values overlay |
| `kind-dra-config.yaml` | Kind config with DRA feature gates |
| `VERSION-MATRIX.md` | Tested component versions |
