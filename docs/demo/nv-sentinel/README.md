<!--
Copyright 2026 NVIDIA CORPORATION
SPDX-License-Identifier: Apache-2.0
-->

# NVSentinel XID detection + remediation demo

This demo proves that [NVSentinel](https://github.com/NVIDIA/nvsentinel) — NVIDIA's
open-source GPU health monitoring and fault-remediation service — can **detect** a
GPU XID error, **remediate** the affected node (cordon + drain), and **recover** it
(uncordon) once the fault clears, all on a local Kind cluster with **no physical
GPUs**. The GPUs are simulated by `nvml-mock`.

```
nvml-mock (fake libnvidia-ml.so)
   │  injected XID 79 ("GPU has fallen off the bus")
   ▼
GPU Operator standalone DCGM (nv-hostengine :5555)
   ▼
NVSentinel GPU Health Monitor  ──►  platform-connector  ──►  MongoDB (change streams)
                                                              │
                                          fault-quarantine ◄──┘  (cordon)
                                                  │
                                          node-drainer            (drain → workload reschedules)
```

On **reset**, DCGM re-reads the now-healthy mock, the health monitor emits healthy
events, and `fault-quarantine` **uncordons** the node.

## Topology

One control-plane + two workers:

- **Workers** (`nvml-mock-gpu=true`): run the `nvml-mock` DaemonSet and all GPU
  Operator operands (DCGM, device plugin, GFD). Each advertises 8 mock GPUs.
- **Control-plane**: runs the NVSentinel control-plane pipeline (MongoDB,
  platform-connector, fault-quarantine, node-drainer). Pinning it there means
  draining a *GPU worker* never evicts the pipeline doing the draining.

When one worker's GPU is failed, NVSentinel cordons/drains it and the sample GPU
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
rebuild from scratch. Useful overrides: `GPU_PROFILE`, `XID`, `TARGET_GPU`,
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
   wired to the external MongoDB and the standalone DCGM.
7. **Sample workload** — [`sample-workload.yaml`](sample-workload.yaml), a pod that
   requests one `nvidia.com/gpu` so the drainer has something to evict.
8. **Phase 1 — detect + remediate** — injects XID 79 on one worker's GPU and waits
   for NVSentinel to cordon it; the sample workload reschedules to the other worker.
9. **Phase 2 — recover** — resets the mock GPU, restarts DCGM, and waits for
   NVSentinel to uncordon the node.

## The fault and the recovery

Inject (done by the script):

```bash
MOCK=$(kubectl --context kind-nvml-mock-nvsentinel -n nvml-mock-system \
  get pod -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')
kubectl --context kind-nvml-mock-nvsentinel -n nvml-mock-system exec "$MOCK" -- \
  nvml-mock-ctl fail --gpu 0 --mode ecc_uncorrectable --after-calls 1 --xid 79
```

DCGM surfaces this as `DCGM_FR_FALLEN_OFF_BUS` (a fatal error). The GPU Health
Monitor emits a fatal health event → `fault-quarantine` cordons the node →
`node-drainer` drains it.

Reset (also done by the script):

```bash
kubectl --context kind-nvml-mock-nvsentinel -n nvml-mock-system exec "$MOCK" -- \
  nvml-mock-ctl reset --gpu all
kubectl --context kind-nvml-mock-nvsentinel -n gpu-operator \
  rollout restart daemonset/nvidia-dcgm daemonset/nvidia-dcgm-exporter
```

DCGM latches XID/DBE errors until the hostengine re-reads the (now healthy) mock,
so the DCGM restart is required to clear them. Once the checks go green,
`fault-quarantine` uncordons the node.

## Why these config choices matter

`nvsentinel-values.yaml` sets three non-default options that are essential for the
demo to complete cleanly (all documented inline in that file):

- **`gpu-health-monitor.dcgmHealthCheck.suppressedErrorCodes`** includes
  `DCGM_FR_NVLINK_EFFECTIVE_BER_THRESHOLD`. The mock reports an NVLink effective-BER
  threshold breach on every GPU at boot — a mock-data artifact, not the fault under
  test. Left active it stays a "failing check" forever and keeps the node quarantined
  even after the injected XID is reset, so recovery never completes. Suppressing it
  lets all checks clear on reset.
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
