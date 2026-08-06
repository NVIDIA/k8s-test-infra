# nvml-mock E2E Consumer Version Matrix

Tested component versions for the mock GPU E2E test suite.

## Tested Versions

"Status" means exactly what CI does, not what the suite is capable of. Three of
these components are installed as GPU Operator operands from a Helm chart that
carries no `--version`, so their versions **float**: the value recorded here is
what a run resolved on the stated date, not a constraint. Only Node Feature
Discovery is pinned (`scenario_nfd_test.go:126`).

| Component | Version | Chart / Image | Status |
|---|---|---|---|
| NVIDIA Device Plugin (standalone) | v0.18.2 | `nvcr.io/nvidia/k8s-device-plugin:v0.18.2` | **Pinned** + runs in CI (`e2e-nri` ×6 profiles, `e2e-multi-node`) |
| NVIDIA Device Plugin (GPU Operator operand) | floats — v0.19.3 observed 2026-07-29 | from the `gpu-operator` chart | Not pinned; runs in CI |
| DRA Driver (GPU) | floats — chart carries no `--version` | `nvidia/nvidia-dra-driver-gpu` (Helm) | Not pinned; runs in CI |
| GPU Feature Discovery (standalone) | v0.8.2 | `nvcr.io/nvidia/gpu-feature-discovery:v0.8.2` | Pinned, **not run in CI** — see below |
| Node Feature Discovery | v0.19.0 | `nfd/node-feature-discovery` (Helm) | **Pinned** + runs in CI |
| CUDA vectorAdd sample | cuda12.5.0 | `nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0` | Pinned, **not run in CI** — see below |
| GPU Operator | floats — v26.3.3 observed 2026-07-29 | `nvidia/gpu-operator` (Helm) | Not pinned; runs in CI |
| DCGM | 3.3.9 | `nvcr.io/nvidia/cloud-native/dcgm:3.3.9-1-ubuntu22.04` | Spike script only (`spike-dcgm.sh`) |
| DCGM Exporter (spike) | 3.3.9-3.6.1 | `nvcr.io/nvidia/k8s/dcgm-exporter:3.3.9-3.6.1-ubuntu22.04` | Spike script only (`spike-dcgm.sh`) |
| DCGM Exporter (GPU Operator operand) | floats — 4.5.3-4.8.2-distroless observed 2026-07-29 | from the `gpu-operator` chart | Not pinned; runs in CI |

The standalone GFD image path stops at **v0.8.2**. Later GFD releases ship
inside `nvcr.io/nvidia/k8s-device-plugin`, so
`nvcr.io/nvidia/gpu-feature-discovery:v0.17.0` — which this table previously
named — does not resolve.

## Component Coverage

### Tested in CI
- **Device Plugin** (standalone DaemonSet, `device-plugin-mock.yaml`): the pinned upstream `k8s-device-plugin:v0.18.2` discovers mock GPUs via NVML and registers the `nvidia.com/gpu` resource. Applied by `deployDevicePlugin`, which has three callers — `scenario_nri_test.go` (`e2e-nri`, 6 profiles), `scenario_multi_node_test.go` (`e2e-multi-node`, in `BeforeAll`), and the gated validator scenario. The first two run on every pipeline. The `e2e-nri` legs additionally assert MEP-0002 composition: a requesting pod sees exactly its allocated GPU, two pods on one node stay isolated, and the suppression rule is mutation-checked.
- **DRA Driver** (Helm chart): discovers mock GPUs via NVML, publishes ResourceSlices, and schedules a pod with a ResourceClaim (`e2e-dra`, 6 profiles)
- **Node Feature Discovery** (Helm chart): derives the PCI vendor label from the feature file nvml-mock writes, not nvml-mock itself (`e2e-nfd`)
- **GPU Operator** (Helm chart + values overlay): its own device plugin, GFD, dcgm-exporter and validator operands, at unpinned versions (`e2e-gpu-operator`, 6 profiles) — see the overlay section below

### Written but NOT run in CI

Two scenarios live in `scenario_validator_test.go` and are excluded **twice
over**: `BeforeAll` skips unless `E2E_RUN_NGC=true`, which is set in no
workflow, and the default label filter (`Makefile`, `E2E_DEFAULT_LABEL_FILTER`)
leads with `!validator`. A green pipeline says nothing about either of them.

- **GPU Feature Discovery** (standalone DaemonSet, `gfd-mock.yaml`): would read GPU attributes via NVML and label nodes. The GFD CI *does* exercise is the GPU Operator's operand.
- **CUDA Validator** (Job, `validator-mock.yaml`): runs vectorAdd against the mock `libcuda.so`. This one would **fail** if the gate were opened — the mock exports one of the 450 driver entry points that image resolves. See [`docs/cuda-mock.md`](../../docs/cuda-mock.md).

Enabling these is tracked in [#446](https://github.com/NVIDIA/k8s-test-infra/issues/446).

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
  (Go `gpu-operator` scenario, `xid` label). Health watches (`dcgmi health`) for
  PCIe/ECC/NVLink/thermal/power also work in the container-level spike.
- Validated in CI by the Go `gpu-operator` scenario (`dcgm`/`xid` labels,
  `tests/e2e/go/assertions/dcgm.go`); the container-level recipe is
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
