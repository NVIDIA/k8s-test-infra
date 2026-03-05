# GPU Mock E2E Consumer Version Matrix

Tested component versions for the mock GPU E2E test suite.

## Tested Versions

| Component | Version | Chart / Image | Status |
|---|---|---|---|
| NVIDIA Device Plugin | v0.18.2 | `nvcr.io/nvidia/k8s-device-plugin:v0.18.2` | Tested in CI |
| DRA Driver (GPU) | v0.10.x | `nvidia/nvidia-dra-driver-gpu` (Helm) | Tested in CI |
| GPU Feature Discovery | v0.17.0 | `nvcr.io/nvidia/gpu-feature-discovery:v0.17.0` | Tested in CI |
| CUDA vectorAdd sample | cuda12.5.0 | `nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0` | Tested in CI |
| GPU Operator | v24.9.x | `nvidia/gpu-operator` (Helm) | Values overlay provided |

## Component Coverage

### Fully Tested (CI)
- **Device Plugin** (standalone DaemonSet): discovers mock GPUs via NVML, registers `nvidia.com/gpu` resource
- **DRA Driver** (Helm chart): discovers mock GPUs via NVML, publishes ResourceSlices
- **GPU Feature Discovery** (standalone DaemonSet): reads GPU attributes via NVML, labels nodes
- **CUDA Validator** (Job): runs vectorAdd against mock libcuda.so

### Values Overlay Only (GPU Operator)
The GPU Operator is tested via a values overlay (`gpu-operator-values.yaml`) that:
- Disables driver, toolkit, DCGM, MIG manager (require real kernel modules)
- Enables device plugin, GFD, and validator with mock driver root

### Not Supported
- **DCGM / DCGM Exporter**: requires full driver telemetry stack
- **MIG Manager**: requires real driver for MIG partition operations
- **Container Toolkit**: not needed (mock libs placed on host by gpu-mock chart)
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
