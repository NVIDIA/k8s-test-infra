# nvml-mock E2E Consumer Version Matrix

Tested component versions for the mock GPU E2E test suite.

## Tested Versions

| Component | Version | Chart / Image | Status |
|---|---|---|---|
| NVIDIA Device Plugin | v0.18.2 | `nvcr.io/nvidia/k8s-device-plugin:v0.18.2` | Tested in CI |
| DRA Driver (GPU) | v0.10.x | `nvidia/nvidia-dra-driver-gpu` (Helm) | Tested in CI |
| GPU Feature Discovery | v0.17.0 | `nvcr.io/nvidia/gpu-feature-discovery:v0.17.0` | Tested in CI |
| CUDA vectorAdd sample | cuda12.5.0 | `nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0` | Tested in CI |
| GPU Operator | v24.9.2 | `nvidia/gpu-operator` (Helm) | Pinned + tested in CI |
| DCGM | 3.3.9 | `nvcr.io/nvidia/cloud-native/dcgm:3.3.9-1-ubuntu22.04` | Spike script (`spike-dcgm.sh`) |
| DCGM Exporter | 3.3.9-3.6.1 | `nvcr.io/nvidia/k8s/dcgm-exporter:3.3.9-3.6.1-ubuntu22.04` | Tested in CI (via GPU Operator) |

## Component Coverage

### Fully Tested (CI)
- **Device Plugin** (standalone DaemonSet): discovers mock GPUs via NVML, registers `nvidia.com/gpu` resource
- **DRA Driver** (Helm chart): discovers mock GPUs via NVML, publishes ResourceSlices
- **GPU Feature Discovery** (standalone DaemonSet): reads GPU attributes via NVML, labels nodes
- **CUDA Validator** (Job): runs vectorAdd against mock libcuda.so

### Values Overlay Only (GPU Operator)
The GPU Operator is tested via a values overlay (`gpu-operator-values.yaml`) that:
- Disables driver, toolkit, standalone DCGM host engine, MIG manager (require real kernel modules)
- Enables device plugin, GFD, dcgm-exporter, and validator with mock driver root

### DCGM / DCGM Exporter
dcgm-exporter runs with its embedded nv-hostengine against the mock NVML:
- **DEV telemetry** (`DCGM_FI_DEV_*`): temperature, power, clocks, utilization,
  memory, ECC, remapped rows, energy, Xid — via the standard NVML getters and
  `nvmlDeviceGetFieldValues`.
- **Time-varying telemetry**: CI installs nvml-mock with dynamic metrics
  enabled on every profile, so `DCGM_FI_DEV_POWER_USAGE` changes over time; the
  validator asserts the variation across two scrapes.
- **Profiling** (`DCGM_FI_PROF_*`): served by the mock GPM implementation
  (`pkg/gpu/mocknvml/engine/gpm.go`) on Hopper+ profiles (h100, b200, gb200,
  gb300). Pre-Hopper profiles report GPM unsupported — real DCGM would use the
  driver-internal perfworks path there, which cannot be mocked.
- **Failure injection** (`DCGM_FI_DEV_XID_ERRORS`): CI injects an Xid via the
  nvml-mock failure-injection knobs and asserts dcgm-exporter surfaces the code
  (`tests/e2e/validate-dcgm-xid.sh`). Health watches (`dcgmi health`) for
  PCIe/ECC/NVLink/thermal/power also work in the container-level spike.
- Validated in CI by `tests/e2e/validate-dcgm-metrics.sh` and
  `tests/e2e/validate-dcgm-xid.sh`; the container-level recipe is
  `tests/e2e/spike-dcgm.sh`.

### Not Supported
- **dcgmi diag levels 2-4**: the NVVS plugins execute real CUDA workloads
  (memtest, targeted stress); the mock libcuda cannot produce valid results
- **MIG Manager**: requires real driver for MIG partition operations
- **Container Toolkit**: not needed (mock libs placed on host by nvml-mock chart)
- **Node Status Exporter**: untested with the mock (kept disabled in the overlay)

## Kind Cluster Requirements
- Kubernetes 1.31+ (for DRA: `DynamicResourceAllocation` feature gate)
- containerd with CDI enabled (for DRA)
- Standard Kind cluster for device plugin / GFD tests

## Updating Versions
When updating component versions:
1. Update the image tag in the relevant manifest under `tests/e2e/`
2. Test locally with `kind` before updating CI
3. Update this matrix document
