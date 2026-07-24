<!--
Copyright 2026 NVIDIA CORPORATION
SPDX-License-Identifier: Apache-2.0
-->

# NVSentinel thermal-margin detection + remediation demo

This demo proves that [NVSentinel](https://github.com/NVIDIA/nvsentinel) — NVIDIA's
open-source GPU health monitoring and fault-remediation service — can **detect** a
GPU thermal condition, **remediate** the affected node (cordon + drain), and
**auto-recover** it (uncordon) once the GPU cools down, all on a local Kind cluster
with **no physical GPUs**. The GPUs are simulated by `nvml-mock`.

The fault under test is a **thermal-margin violation**. NVSentinel's
`GpuThermalMarginWatch` compares each GPU's live *signed* T.Limit margin
(DCGM field 153) against the per-GPU **hardware slowdown offset**. That offset is
not a DCGM field: the NVSentinel `metadata-collector` reads it once from NVML
field 194 (`NVML_FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT`) and publishes it to
`gpu_metadata.json`. When the margin drops below the offset the GPU is unhealthy;
when it rises back the check clears.

```
nvml-mock (fake libnvidia-ml.so)
   │  GPU pinned hot → T.Limit margin (field 153) goes negative
   │  slowdown offset (NVML field 194) → metadata-collector → gpu_metadata.json
   ▼
GPU Operator standalone DCGM (nv-hostengine :5555)
   ▼
NVSentinel GPU Health Monitor  ──►  platform-connector  ──►  MongoDB (change streams)
   (GpuThermalMarginWatch)                                    │
                                          fault-quarantine ◄──┘  (cordon)
                                                  │
                                          node-drainer            (drain → workload reschedules)
```

On **cooldown**, DCGM field 153 is a *live gauge*: the next health-monitor poll
sees the margin re-open above the slowdown offset, the monitor emits healthy
events, and `fault-quarantine` **uncordons** the node — **no DCGM restart needed**
(unlike latched XID/ECC faults, which stay latched until the hostengine is
restarted).

## Topology

One control-plane + two workers:

- **Workers** (`nvml-mock-gpu=true`): run the `nvml-mock` DaemonSet and all GPU
  Operator operands (DCGM, device plugin, GFD). Each advertises 8 mock GPUs.
- **Control-plane**: runs the NVSentinel control-plane pipeline (MongoDB,
  platform-connector, fault-quarantine, node-drainer). Pinning it there means
  draining a *GPU worker* never evicts the pipeline doing the draining.

When one worker's GPU overheats, NVSentinel cordons/drains it and the sample GPU
workload reschedules onto the second, healthy worker.

## Requirements

- Docker, [Kind](https://kind.sigs.k8s.io/), Helm, `kubectl`
- `jq` (optional — only used to pretty-print node conditions)
- Network access to `ghcr.io` (NVSentinel chart), `helm.ngc.nvidia.com`
  (GPU Operator), `public.ecr.aws` (MongoDB), and `nvidia.github.io`
  (container-toolkit packages).
- An `arm64` or `amd64` host. The demo runs a standalone MongoDB
  (`public.ecr.aws/docker/library/mongo:8.0.3`) instead of the chart's bundled
  Bitnami MongoDB, so it works on Apple Silicon too (see below).

## Run it

```bash
cd docs/demo/nv-sentinel
./run.sh
```

The script is idempotent and reuses the cluster; set `FORCE_RECREATE=true` to
rebuild from scratch. Useful overrides: `GPU_PROFILE`, `HOT_TEMP_C`, `TARGET_GPU`,
`NVSENTINEL_VERSION`, `GPU_OPERATOR_VERSION`, `CERT_MANAGER_VERSION`.

## What the script does

1. **Cluster** — creates the Kind cluster from [`kind.yaml`](kind.yaml) (CDI
   enabled in containerd), labels both workers `nvml-mock-gpu=true`, and installs
   `nvidia-container-toolkit` (CDI mode) into each worker.
2. **Mock GPUs** — builds/loads the `nvml-mock` image and installs the chart onto
   the workers.
3. **GPU Operator** — installs it with [`gpu-operator-values.yaml`](gpu-operator-values.yaml),
   which disables the real driver/toolkit (the mock provides them) and **enables
   the standalone DCGM** DaemonSet + Service that NVSentinel polls.
4. **cert-manager** — installed as a TLS dependency.
5. **MongoDB** — deploys [`mongodb.yaml`](mongodb.yaml): a single-node replica set
   using the official multi-arch image, serving TLS with a cert-manager cert.
6. **NVSentinel** — installs the chart with [`nvsentinel-values.yaml`](nvsentinel-values.yaml),
   wired to the external MongoDB and the standalone DCGM. This enables the
   `metadata-collector` (for the slowdown offset) and turns the thermal-margin
   watch from dry-run into an active, remediating check.
7. **Sample workload** — [`sample-workload.yaml`](sample-workload.yaml), a pod that
   requests one `nvidia.com/gpu` so the drainer has something to evict.
8. **Phase 1 — detect + remediate** — pins one worker's GPU to a hot temperature
   (`HOT_TEMP_C`, default 90 °C) and waits for NVSentinel to cordon it; the sample
   workload reschedules to the other worker.
9. **Phase 2 — auto-recover** — clears the temperature override and waits for
   NVSentinel to uncordon the node. No DCGM restart is involved.

## The fault and the recovery

Heat the GPU (done by the script):

```bash
MOCK=$(kubectl --context kind-nvml-mock-nvsentinel -n nvml-mock-system \
  get pod -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')
kubectl --context kind-nvml-mock-nvsentinel -n nvml-mock-system exec "$MOCK" -- \
  nvml-mock-ctl temp --gpu 0 90
```

The mock's h100 profile slows down at 87 °C, so pinning 90 °C makes the T.Limit
margin (DCGM field 153) go to about `-3 °C`. Because that is below the GPU's
slowdown offset (`0 °C` for the mock, read from NVML field 194), the GPU Health
Monitor's `GpuThermalMarginWatch` fails with `GPU_TEMP_HW_SLOWDOWN_VIOLATION`,
producing a node condition such as:

```
GpuThermalMarginWatch=True: GPU 0 thermal margin -3°C below HW slowdown T.Limit (slowdown=0°C)
```

`fault-quarantine` cordons the node → `node-drainer` drains it.

Cool the GPU (also done by the script):

```bash
kubectl --context kind-nvml-mock-nvsentinel -n nvml-mock-system exec "$MOCK" -- \
  nvml-mock-ctl reset --gpu 0
```

Field 153 is a live gauge, so the next health-monitor poll sees a healthy
(positive) margin and `fault-quarantine` uncordons the node. **No DCGM restart is
needed** — that is the key difference from a latched XID/ECC fault.

> **Mock capability note.** This flow relies on two `nvml-mock` behaviors added
> for it: the mock exposes the T.Limit threshold field values
> (`NVML_FI_DEV_TEMPERATURE_*_TLIMIT`, ids 193–196) so the metadata-collector can
> read the slowdown offset, and its `nvmlDeviceGetMarginTemperature` returns a
> *signed* margin that goes negative past the slowdown limit (rather than clamping
> at 0) so the watch can actually trip.

## Why these config choices matter

`nvsentinel-values.yaml` sets five non-default options that are essential for the
demo to complete cleanly (all documented inline in that file):

- **`global.metadataCollector.enabled: true`**. The `metadata-collector` DaemonSet
  reads each GPU's slowdown T.Limit offset (NVML field 194) once and writes it to
  `gpu_metadata.json`. Without it, `GpuThermalMarginWatch` never arms and logs
  *"missing slowdown TLIMIT threshold metadata"*. It is off by default.
- **`labeler.assumeDriverInstalled: true`**. The metadata-collector only schedules
  on nodes labeled `nvsentinel.dgxc.nvidia.com/driver.installed=true`, which the
  labeler normally sets only when it sees a real `nvidia-driver-daemonset`. This
  demo disables the real driver (the mock provides `libnvidia-ml.so`), so we tell
  the labeler to assume the driver is present on every `gpu.present` node — the
  same knob NVIDIA documents for hosts with pre-baked drivers.
- **`gpu-health-monitor.dcgmFieldsMonitoring.gpuTempLimitStoreOnly: false`**.
  `GpuThermalMarginWatch` ships in **dry-run** (store-only) mode: it emits events
  but never touches the node. Turning store-only off lets a closing thermal margin
  actually drive the cordon/drain pipeline (and the uncordon on recovery).
- **`gpu-health-monitor.dcgmHealthCheck.suppressedErrorCodes`** includes
  `DCGM_FR_NVLINK_EFFECTIVE_BER_THRESHOLD`. The mock reports an NVLink effective-BER
  threshold breach on every GPU at boot — a mock-data artifact, not the fault under
  test. Left active it stays a "failing check" forever and keeps the node quarantined
  even after the GPU cools, so recovery never completes. Suppressing it lets all
  checks clear on cooldown.
- **`fault-quarantine.circuitBreaker.enabled: false`**. The circuit breaker trips
  when ≥ 50% of GPU nodes are cordoned within a 5-minute window and then halts *all*
  event processing (including the uncordon on recovery). With only two GPU workers a
  single legitimate cordon already meets that threshold, so it is disabled for this
  tiny cluster. **Leave it enabled (the default) on real, larger clusters.**
- **`node-drainer.userNamespaces[*].mode: Immediate`**. The default `AllowCompletion`
  mode waits for each pod to finish gracefully; the sample GPU workload never
  completes on its own, so it would never be evicted and you would not see it move to
  the healthy worker. `Immediate` makes the drain → reschedule step observable. Real
  clusters typically keep `AllowCompletion`.

## A note on host resources

The demo runs GPU Operator + DCGM + MongoDB + the full NVSentinel pipeline. On a
busy host — for example if you have several Kind clusters running at once — the GPU
workers can be CPU-saturated during GPU Operator bring-up, which slows image pulls
and pod readiness (cert-manager and DCGM in particular). `run.sh` uses generous
waits, but if a step times out, re-running it (it reuses the cluster) or freeing up
other clusters usually resolves it.

## Why standalone MongoDB instead of the chart's built-in one

NVSentinel's `mongodb-store` subchart uses the Bitnami MongoDB chart, whose images
(`bitnamilegacy/*`) are published amd64-only and whose containers run Bitnami-only
startup scripts. On arm64 (and after Bitnami's image relocation) that MongoDB cannot
start. This demo therefore runs a plain, official-image MongoDB and points NVSentinel
at it as an **external datastore** (`global.mongodbStore.enabled=false` +
`global.datastore.*`). NVSentinel requires change streams (fault-quarantine and the
analyzer watch them), which need a replica set, so `mongodb.yaml` runs a single-node
replica set (`rs0`). It also talks TLS to the datastore, so MongoDB serves TLS with a
cert-manager-issued cert and the CA is handed to NVSentinel via
`global.datastore.tls.caSecretName`.

## Inspecting the result

```bash
CTX=kind-nvml-mock-nvsentinel
kubectl --context $CTX get nodes
kubectl --context $CTX -n nvsentinel get pods
# cordon / quarantine / uncordon events:
kubectl --context $CTX -n nvsentinel logs -l app.kubernetes.io/instance=nvsentinel \
  --prefix --tail=500 | grep -iE 'cordon|quarantin|recovered'
```

## Cleanup

```bash
kind delete cluster --name nvml-mock-nvsentinel
```
