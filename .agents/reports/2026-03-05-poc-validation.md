# PoC Validation Report: DRA + Device Plugin on Kind with gpu-mock

> Date: 2026-03-05
> Status: **Scripts ready, pending execution**
> Branch: `feat/gpu-mock-poc-validation`

## Overview

This report documents the PoC validation of NVIDIA GPU consumers (DRA driver and device plugin) running against the gpu-mock infrastructure on Kind clusters.

## Test Environment

| Component | Version |
|-----------|---------|
| gpu-mock | Wave 0 complete (PRs #226, #227, #228) |
| GPU profile | A100 (DGX A100, 8 GPUs, 40 GiB HBM2e each) |
| NVIDIA device plugin | v0.18.2 |
| NVIDIA DRA driver | v0.10.x (latest from Helm) |
| Kind | latest |
| Kubernetes | v1.32.x (Kind default) |

## Architecture

```
Kind Cluster (single control-plane node)
|
+-- gpu-mock DaemonSet
|   |-- Builds libnvidia-ml.so (mock) from Go source
|   |-- Copies to host: /var/lib/nvidia-mock/driver/usr/lib64/
|   |-- Creates device nodes: /var/lib/nvidia-mock/dev/nvidia{0-7}
|   |-- Deploys config: /var/lib/nvidia-mock/driver/config/config.yaml
|   `-- Labels node: nvidia.com/gpu.present=true
|
+-- NVIDIA Device Plugin (Phase 1)
|   |-- Loads mock libnvidia-ml.so via --nvidia-driver-root
|   |-- Discovers 8 mock GPUs via NVML API
|   `-- Registers nvidia.com/gpu: 8 on node
|
+-- NVIDIA DRA Driver (Phase 2)
    |-- Loads mock libnvidia-ml.so via nvidiaDriverRoot
    |-- Discovers 8 mock GPUs via NVML API
    `-- Publishes ResourceSlices with mock GPU UUIDs
```

## Scripts

All scripts are in `tests/poc-validation/`:

| Script | Purpose |
|--------|---------|
| `setup-kind-cluster.sh` | Creates Kind cluster, builds+loads gpu-mock image, installs Helm chart |
| `deploy-device-plugin.sh` | Deploys device plugin with MOCK_NVML_DEBUG=1, validates GPU count |
| `deploy-dra-driver.sh` | Deploys DRA driver, validates ResourceSlice GPU count |
| `capture-nvml-traces.sh` | Extracts NVML call traces from container logs |
| `run-all.sh` | Orchestrates full validation (both phases) |

### Usage

```bash
# Full validation (both device plugin and DRA)
cd tests/poc-validation
./run-all.sh --profile a100 --gpu-count 8

# Or run phases individually:
./setup-kind-cluster.sh --profile a100 --gpu-count 8
./deploy-device-plugin.sh --expected-gpus 8
./capture-nvml-traces.sh

# For DRA (requires --dra flag for Kind cluster):
./setup-kind-cluster.sh --profile a100 --gpu-count 8 --dra
./deploy-dra-driver.sh --expected-gpus 8
./capture-nvml-traces.sh
```

## Expected NVML Functions Called

Based on analysis of NVIDIA device plugin v0.18.2 and DRA driver source code:

### Device Plugin (nvidia-device-plugin v0.18.2)

Core discovery functions (must work):
- `nvmlInit_v2` / `nvmlShutdown`
- `nvmlSystemGetDriverVersion`
- `nvmlSystemGetNVMLVersion`
- `nvmlDeviceGetCount_v2`
- `nvmlDeviceGetHandleByIndex_v2`
- `nvmlDeviceGetUUID`
- `nvmlDeviceGetName`
- `nvmlDeviceGetMinorNumber`
- `nvmlDeviceGetPciInfo_v3`
- `nvmlDeviceGetMemoryInfo_v2`
- `nvmlDeviceGetCudaComputeCapability`
- `nvmlDeviceGetMigMode`
- `nvmlDeviceGetGpuInstanceId`

Health monitoring (called periodically, can return NOT_SUPPORTED):
- `nvmlDeviceGetNvLinkState`
- `nvmlDeviceGetNvLinkRemotePciInfo_v2`
- `nvmlDeviceGetFieldValues` (XID errors)

### DRA Driver (nvidia-dra-driver-gpu v0.10.x)

Core discovery (must work):
- `nvmlInit_v2` / `nvmlShutdown`
- `nvmlSystemGetDriverVersion`
- `nvmlDeviceGetCount_v2`
- `nvmlDeviceGetHandleByIndex_v2`
- `nvmlDeviceGetUUID`
- `nvmlDeviceGetName`
- `nvmlDeviceGetArchitecture`
- `nvmlDeviceGetCudaComputeCapability`
- `nvmlDeviceGetMemoryInfo_v2`
- `nvmlDeviceGetPciInfo_v3`
- `nvmlDeviceGetBrand`
- `nvmlDeviceGetMigMode`
- `nvmlDeviceGetMaxMigDeviceCount`

Resource publishing (needed for ResourceSlice attributes):
- `nvmlDeviceGetNvLinkState`
- `nvmlDeviceGetNvLinkRemotePciInfo_v2`
- `nvmlDeviceGetNvLinkVersion`

## Phase Gate Criteria

| Criterion | Status |
|-----------|--------|
| Kind cluster creates successfully | Pending |
| gpu-mock DaemonSet deploys and creates mock files | Pending |
| Device plugin discovers mock GPUs | Pending |
| Node reports `nvidia.com/gpu: 8` | Pending |
| DRA driver publishes ResourceSlices | Pending |
| ResourceSlices contain 8 GPUs with correct UUIDs | Pending |
| NVML call traces captured | Pending |

## Known Limitations

1. **MOCK_NVML_DEBUG environment**: The debug logging is compiled into the .so, but the env var must be set in the consumer container's environment, not in the gpu-mock pod. The debug manifests handle this.

2. **DRA driver init container**: The DRA driver Helm chart may run init containers that probe nvidia-smi. The mock nvidia-smi script handles basic version queries.

3. **LD_LIBRARY_PATH**: Device plugin and DRA driver use `--nvidia-driver-root` to find libnvidia-ml.so. The mock .so must be at the exact path they expect: `$DRIVER_ROOT/usr/lib64/libnvidia-ml.so.1`.

4. **CDI for DRA**: DRA requires CDI (Container Device Interface) support in containerd. The `kind-dra-config.yaml` enables this.

## NVML Call Trace Analysis

> To be populated after execution. Run `capture-nvml-traces.sh` to generate.

The trace output will be in `tests/poc-validation/logs/nvml-trace-summary.md` and will categorize each NVML function as:
- **Implemented**: Bridge function exists, returns profile-driven data
- **Stub (NOT_SUPPORTED)**: Returns NOT_SUPPORTED, consumer handles gracefully
- **Stub (FUNCTION_NOT_FOUND)**: Version-gated, not available for configured driver version

Functions that return NOT_SUPPORTED but are called by consumers are candidates for Wave 2+ implementation.

## Next Steps

1. Execute `run-all.sh` on a machine with Docker available (CI runner or dev machine)
2. Capture NVML traces and update this report with actual results
3. Use trace data to prioritize T4 (NVML Batch 1) and T5 (NVML Batch 2) function lists
4. If DRA fails: document exact error and escalate to distinguished-engineer
