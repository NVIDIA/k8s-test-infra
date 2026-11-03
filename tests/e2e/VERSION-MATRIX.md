# nvml-mock E2E Consumer Version Matrix

Tested component versions for the mock GPU E2E test suite.

## Tested Versions

"Status" means exactly what CI does, not what the suite is capable of. Every
Helm chart CI installs is pinned: the GPU Operator
(`local/gpu-operator/gpu_operator.tiltfile`), Node Feature Discovery
(`nfdVersion` in `scenario_nfd_test.go`) and the DRA driver (`local/dra/dra.tiltfile`). The
Operator's operands carry no pin of their own; their versions are that chart's
defaults.

The GPU Operator is held at **v26.3.3**. v26.7.1 gates every operand's
`toolkit-validation` init container on an `nvidia` line in `/proc/modules`
([NVIDIA/gpu-operator#2881](https://github.com/NVIDIA/gpu-operator/pull/2881)).
In the userspace driver mode Mokka serves that file only through the
`libmockfs.so` preload, which those init containers never receive, so GFD, the
device plugin and dcgm-exporter wait forever. The kernel driver mode runs
**v26.7.2**: there the simulated kernel modules are loaded for real, and the
kernel serves the line itself.

| Component | Version | Chart / Image | Status |
|---|---|---|---|
| NVIDIA Device Plugin (standalone) | v0.18.2 | `nvcr.io/nvidia/k8s-device-plugin:v0.18.2` | **Pinned** + runs in CI (`e2e-nri` ×6 profiles, `e2e-multi-node`) |
| NVIDIA Device Plugin (GPU Operator operand) | v0.19.3 | from the `gpu-operator` v26.3.3 chart | Pinned by the chart; runs in CI |
| DRA Driver (GPU) | 0.5.0 | `nvidia/dra-driver-nvidia-gpu` (Helm) | **Pinned** + runs in CI |
| Node Feature Discovery | v0.19.0 | `nfd/node-feature-discovery` (Helm) | **Pinned** + runs in CI |
| GPU Operator | v26.3.3; v26.7.2 in the kernel driver mode | `nvidia/gpu-operator` (Helm) | **Pinned** + runs in CI |
| DCGM | 3.3.9 | `nvcr.io/nvidia/cloud-native/dcgm:3.3.9-1-ubuntu22.04` | Last version a manual container spike resolved; not run |
| DCGM Exporter (standalone) | 3.3.9-3.6.1 | `nvcr.io/nvidia/k8s/dcgm-exporter:3.3.9-3.6.1-ubuntu22.04` | Last version a manual container spike resolved; not run |
| DCGM Exporter (GPU Operator operand) | 4.5.3-4.8.2-distroless | from the `gpu-operator` v26.3.3 chart | Pinned by the chart; runs in CI |

GPU Feature Discovery (GFD) is tested only as the GPU Operator's operand. A
standalone GFD scenario duplicated that coverage without running in CI, and
was removed after
[#1083](https://github.com/NVIDIA/k8s-test-infra/pull/1083).

## Component Coverage

### Tested in CI
- **Device Plugin** (standalone DaemonSet, `device-plugin-mock.yaml`): the pinned upstream `k8s-device-plugin:v0.18.2` discovers mock GPUs via NVML and registers the `nvidia.com/gpu` resource. Applied by `deployDevicePlugin`, which has two callers: `scenario_nri_test.go` (`e2e-nri`, 6 profiles) and `scenario_multi_node_test.go` (`e2e-multi-node`, in `BeforeAll`). Both run on every pipeline. The `e2e-nri` legs additionally assert MEP-0002 composition: a requesting pod sees exactly its allocated GPU, two pods on one node stay isolated, and the suppression rule is mutation-checked.
- **DRA Driver** (Helm chart): discovers mock GPUs via NVML, publishes ResourceSlices, and schedules a pod with a ResourceClaim (`e2e-dra`, 6 profiles)
- **Node Feature Discovery** (Helm chart): derives the PCI vendor label from the feature file nvml-mock writes, not nvml-mock itself (`e2e-nfd`)
- **GPU Operator** (Helm chart + values overlay): its own device plugin, GFD, dcgm-exporter and validator operands (`e2e-gpu-operator`, 6 profiles) — see the overlay section below
- **GPU Operator in the kernel driver mode** (`e2e-gpu-operator-kernel`, gb300): the same operands, plus the operator's driver and container toolkit from NVIDIA's original images. The driver container installs its driver on Mokka's simulated kernel modules, and the operator's upgrade controller moves it to another driver version
- **vGPU** (`scenario_vgpu_test.go`): the stock `nvidia-smi` reads a vGPU host's types and running instances, and a licensed vGPU guest (`e2e-vgpu`, a100). The job installs the GPU Operator, whose GFD operand labels the guest node `nvidia.com/vgpu.*` from the PCI capability.

### Values Overlay Only (GPU Operator)
The GPU Operator is tested via a values overlay (`gpu-operator-values.yaml`) that:
- Disables driver, toolkit, standalone DCGM host engine, MIG manager (require real kernel modules); the kernel driver mode's overlay, `local/kernel-mods/gpu-operator.values.yaml`, enables the driver and toolkit
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
  (Go `gpu-operator` scenario, `xid` label). Health watches (`dcgmi health`) for
  PCIe/ECC/NVLink/thermal/power were confirmed by hand against a standalone DCGM
  container, but nothing exercises them now.
- Validated in CI by the Go `gpu-operator` scenario (`dcgm`/`xid` labels,
  `tests/e2e/go/assertions/dcgm.go`), which scrapes the GPU Operator's
  dcgm-exporter operand.

### Not Supported
- **dcgmi diag levels 2-4**: the NVVS plugins execute real CUDA workloads
  (memtest, targeted stress), which need a real driver and GPU
- **MIG Manager**: requires real driver for MIG partition operations
- **Container Toolkit operand**: disabled (`toolkit.enabled=false`); the
  Mokka node daemon installs the toolkit's runtime and registers the
  `nvidia` handler instead
- **Node Status Exporter**: untested with the mock (kept disabled in the overlay)

## Kind Cluster Requirements
- Kubernetes 1.31+ (for DRA: `DynamicResourceAllocation` feature gate)
- containerd 1.7+ managed by systemd; the Mokka node daemon turns CDI on,
  registers the `nvidia` runtime handler, and turns NRI on for the NRI
  scenario and the DRA and GPU Operator NRI specs
- Standard Kind cluster for device plugin / GFD tests

## Updating Versions
When updating component versions:
1. Update the image tag in the relevant manifest under `tests/e2e/`
2. Test locally with `kind` before updating CI
3. Update this matrix document
