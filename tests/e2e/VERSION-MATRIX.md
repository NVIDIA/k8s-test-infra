# nvml-mock E2E Consumer Version Matrix

Tested component versions for the mock GPU E2E test suite.

## Tested Versions

| Component | Version | Chart / Image | Status |
|---|---|---|---|
| NVIDIA Device Plugin | v0.18.2 | `nvcr.io/nvidia/k8s-device-plugin:v0.18.2` | Tested in CI |
| DRA Driver (GPU) | v0.10.x | `nvidia/nvidia-dra-driver-gpu` (Helm) | Tested in CI |
| GPU Feature Discovery | v0.17.0 | `nvcr.io/nvidia/gpu-feature-discovery:v0.17.0` | Tested in CI |
| CUDA vectorAdd sample | cuda12.5.0 | `nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0` | Tested in CI |
| GPU Operator (driver disabled) | latest (unpinned) | `nvidia/gpu-operator` (Helm) | Tested in CI |
| GPU Operator (managed driver) | v26.3.3 (pinned) | `nvidia/gpu-operator` (Helm) + `mock-driver` image | Tested in CI |
| GPU Operator (host driver masquerade) | latest (unpinned) | `nvidia/gpu-operator` (Helm) | Tested in CI |

## Component Coverage

### Fully Tested (CI)
- **Device Plugin** (standalone DaemonSet): discovers mock GPUs via NVML, registers `nvidia.com/gpu` resource
- **DRA Driver** (Helm chart): discovers mock GPUs via NVML, publishes ResourceSlices
- **GPU Feature Discovery** (standalone DaemonSet): reads GPU attributes via NVML, labels nodes
- **CUDA Validator** (Job): runs vectorAdd against mock libcuda.so

### GPU Operator (three modes, all in CI)
- **Driver disabled** (`gpu-operator-values.yaml`, job `e2e-gpu-operator`):
  disables driver, toolkit, DCGM, MIG manager; enables device plugin, GFD,
  and validator against nvml-mock's `/run/nvidia/driver` symlink. Installs the
  latest operator chart (unpinned).
- **Host driver masquerade** (baseline + `gpu-operator-hostdriver-values.yaml`
  delta, job `e2e-gpu-operator-hostdriver`): nvml-mock's `hostDriver.enabled`
  puts nvidia-smi and the mock libs at standard host paths; the validator
  takes its preinstalled host-driver branch (`IS_HOST_DRIVER=true`) and no
  component carries driver-root env overrides. Also asserts manifest-driven
  uninstall leaves no host residue. Installs the latest operator chart
  (unpinned; the host-driver detection contract is version-stable).
- **Managed driver** (baseline + `gpu-operator-driver-values.yaml` delta, job
  `e2e-gpu-operator-driver`): `driver.enabled=true` with the
  [mock-driver image](../../docs/mock-driver.md) substituted via
  `driver.repository/image/version`. Exercises DaemonSet rendering, the
  k8s-driver-manager init flow, the startup-probe → `.driver-ctr-ready`
  handshake, and the validator's operator-managed branch. Pinned to the
  operator version whose contract is vendored under `contract/` -- a lifecycle
  test, not driver functionality (DCGM/MIG/upgrades remain uncovered).

### Not Supported
- **DCGM / DCGM Exporter**: requires full driver telemetry stack
- **MIG Manager**: requires real driver for MIG partition operations
- **Container Toolkit**: not needed (mock libs placed on host by nvml-mock chart)
- **Node Status Exporter**: depends on DCGM

## Kind Cluster Requirements
- Kubernetes 1.31+ (for DRA: `DynamicResourceAllocation` feature gate)
- containerd with CDI enabled (for DRA)
- Standard Kind cluster for device plugin / GFD tests

## Updating Versions
When updating component versions:
1. Update the image tag in the relevant manifest under `tests/e2e/`
2. Test locally with `kind` before updating CI
3. Update this matrix document
