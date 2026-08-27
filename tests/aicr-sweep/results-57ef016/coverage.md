# AICR check coverage under Mokka

All evidence below is simulation provenance (`sim`). None of it was run on silicon.

## Roll-up

- Check results total: 210 (checks x cells)
- A meaningful: 160  ·  B trivial: 10  ·  C hardware-dependent: 40  ·  G closable gap: 0
- **Meaningful pre-silicon today: 76%** (A / total)
- **Reachable once tracked gaps close: 76%** ((A + G) / total). Roadmap, not a current claim.
- Of the A set, 100 are GPU-dependent (62% of A). The remainder run on any cluster, so Mokka is not what unlocks them.
- Never reached a verdict (blocked or not-run): 157 (75%)

### Why cells did not reach a verdict

| Cause | Meaning | Count |
|---|---|---|
| X | AICR catalog has no recipe for the combination. NOT a Mokka gap. | 84 |

## Cells

### `base-gb200-kind-inference-stack`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=ran

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | pass |  |  |
| expected-resources | deployment | A | yes | not-run |  |  |
| gpu-operator-version | deployment | A | no | fail |  |  |
| check-nvidia-smi | deployment | A | yes | pass |  |  |
| nccl-all-reduce-bw | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-net | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-nvls | performance | C | yes | not-run |  | check absent from run output |
| inference-perf | performance | C | yes | not-run |  | check absent from run output |
| dra-support | conformance | A | yes | fail |  |  |
| gang-scheduling | conformance | A | no | not-run |  | check absent from run output |
| accelerator-metrics | conformance | A | yes | pass |  |  |
| ai-service-metrics | conformance | A | yes | fail |  |  |
| inference-gateway | conformance | A | no | not-run |  | check absent from run output |
| pod-autoscaling | conformance | B | yes | not-run |  | check absent from run output |
| cluster-autoscaling | conformance | A | no | not-run |  | check absent from run output |
| robust-controller | conformance | A | no | not-run |  | check absent from run output |
| secure-accelerator-access | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-health | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-imex-channel | conformance | A | yes | not-run |  | check absent from run output |
| gpu-operator-health | conformance | A | yes | fail |  |  |
| platform-health | conformance | A | no | fail |  |  |

### `base-gb200-kind-training-stack`

Recipe: service=kind accelerator=gb200 intent=training · shape=stack workers=2 · status=blocked

**Blocked (X):** RECLASSIFIED from BUDGET. The first run recorded this cell as blocked on host memory and predicted it would "differ from the inference cell only in component set". That prediction was never tested. It is wrong: AICR at 0752ea14 resolves no training recipe on the kind service for gb200. `aicr recipe --service kind --accelerator gb200 --intent training` exits 2 with "[INVALID_REQUEST] no recipe provides intent 'training'". On kind, training resolves for h100 and for no other accelerator. The cell fails at recipe resolution, before memory could ever matter, so the cause is X. Evidence: results/aicr-recipe-matrix-evidence.log.

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | X |  |
| expected-resources | deployment | A | yes | blocked | X |  |
| gpu-operator-version | deployment | A | no | blocked | X |  |
| check-nvidia-smi | deployment | A | yes | blocked | X |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | X |  |
| inference-perf | performance | C | yes | blocked | X |  |
| dra-support | conformance | A | yes | blocked | X |  |
| gang-scheduling | conformance | A | no | blocked | X |  |
| accelerator-metrics | conformance | A | yes | blocked | X |  |
| ai-service-metrics | conformance | A | yes | blocked | X |  |
| inference-gateway | conformance | A | no | blocked | X |  |
| pod-autoscaling | conformance | B | yes | blocked | X |  |
| cluster-autoscaling | conformance | A | no | blocked | X |  |
| robust-controller | conformance | A | no | blocked | X |  |
| secure-accelerator-access | conformance | A | yes | blocked | X |  |
| slinky-slurm-health | conformance | A | yes | blocked | X |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | X |  |
| gpu-operator-health | conformance | A | yes | blocked | X |  |
| platform-health | conformance | A | no | blocked | X |  |

### `base-h100-kind-inference-stack`

Recipe: service=kind accelerator=h100 intent=inference · shape=stack workers=2 · status=ran

Axes off base: accelerator=h100 (substitute for gb300)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | pass |  |  |
| expected-resources | deployment | A | yes | not-run |  |  |
| gpu-operator-version | deployment | A | no | pass |  |  |
| check-nvidia-smi | deployment | A | yes | pass |  |  |
| nccl-all-reduce-bw | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-net | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-nvls | performance | C | yes | not-run |  | check absent from run output |
| inference-perf | performance | C | yes | not-run |  | check absent from run output |
| dra-support | conformance | A | yes | fail |  |  |
| gang-scheduling | conformance | A | no | fail |  |  |
| accelerator-metrics | conformance | A | yes | pass |  |  |
| ai-service-metrics | conformance | A | yes | fail |  |  |
| inference-gateway | conformance | A | no | fail |  |  |
| pod-autoscaling | conformance | B | yes | fail |  |  |
| cluster-autoscaling | conformance | A | no | skip |  |  |
| robust-controller | conformance | A | no | not-run |  | check absent from run output |
| secure-accelerator-access | conformance | A | yes | pass |  |  |
| slinky-slurm-health | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-imex-channel | conformance | A | yes | not-run |  | check absent from run output |
| gpu-operator-health | conformance | A | yes | fail |  |  |
| platform-health | conformance | A | no | fail |  |  |

### `base-gb300-kind-inference-stack`

Recipe: service=kind accelerator=gb300 intent=inference · shape=stack workers=2 · status=blocked

**Blocked (X):** Unchanged from the first run and re-verified by running the CLI at the same pin: `aicr recipe --service kind --accelerator gb300 --intent inference` exits 2 with "[INVALID_REQUEST] no recipe provides accelerator 'gb300'". The catalog knows a100, b200, gb200, h100, h200, l40s and rtx-pro-6000. Mokka ships a gb300 profile, and #699 made it the chart default, so the two sides have drifted further apart rather than closer. Not a Mokka gap.

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | X |  |
| expected-resources | deployment | A | yes | blocked | X |  |
| gpu-operator-version | deployment | A | no | blocked | X |  |
| check-nvidia-smi | deployment | A | yes | blocked | X |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | X |  |
| inference-perf | performance | C | yes | blocked | X |  |
| dra-support | conformance | A | yes | blocked | X |  |
| gang-scheduling | conformance | A | no | blocked | X |  |
| accelerator-metrics | conformance | A | yes | blocked | X |  |
| ai-service-metrics | conformance | A | yes | blocked | X |  |
| inference-gateway | conformance | A | no | blocked | X |  |
| pod-autoscaling | conformance | B | yes | blocked | X |  |
| cluster-autoscaling | conformance | A | no | blocked | X |  |
| robust-controller | conformance | A | no | blocked | X |  |
| secure-accelerator-access | conformance | A | yes | blocked | X |  |
| slinky-slurm-health | conformance | A | yes | blocked | X |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | X |  |
| gpu-operator-health | conformance | A | yes | blocked | X |  |
| platform-health | conformance | A | no | blocked | X |  |

### `base-gb300-kind-training-stack`

Recipe: service=kind accelerator=gb300 intent=training · shape=stack workers=2 · status=blocked

**Blocked (X):** Same as base-gb300-kind-inference-stack, and now doubly so: AICR reports both "no recipe provides accelerator 'gb300'" and "no recipe provides intent 'training'" for this combination.

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | X |  |
| expected-resources | deployment | A | yes | blocked | X |  |
| gpu-operator-version | deployment | A | no | blocked | X |  |
| check-nvidia-smi | deployment | A | yes | blocked | X |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | X |  |
| inference-perf | performance | C | yes | blocked | X |  |
| dra-support | conformance | A | yes | blocked | X |  |
| gang-scheduling | conformance | A | no | blocked | X |  |
| accelerator-metrics | conformance | A | yes | blocked | X |  |
| ai-service-metrics | conformance | A | yes | blocked | X |  |
| inference-gateway | conformance | A | no | blocked | X |  |
| pod-autoscaling | conformance | B | yes | blocked | X |  |
| cluster-autoscaling | conformance | A | no | blocked | X |  |
| robust-controller | conformance | A | no | blocked | X |  |
| secure-accelerator-access | conformance | A | yes | blocked | X |  |
| slinky-slurm-health | conformance | A | yes | blocked | X |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | X |  |
| gpu-operator-health | conformance | A | yes | blocked | X |  |
| platform-health | conformance | A | no | blocked | X |  |

### `targeted-dra-installed`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=ran

Axes off base: imex.mockChannels.enabled=true, nvidia-dra-driver-gpu=v0.5.0 with altProcDevices (first run used v25.12.0, which lacked it)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | not-run |  |  |
| expected-resources | deployment | A | yes | fail |  |  |
| gpu-operator-version | deployment | A | no | fail |  |  |
| check-nvidia-smi | deployment | A | yes | pass |  |  |
| nccl-all-reduce-bw | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-net | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-nvls | performance | C | yes | not-run |  | check absent from run output |
| inference-perf | performance | C | yes | not-run |  | check absent from run output |
| dra-support | conformance | A | yes | pass |  |  |
| gang-scheduling | conformance | A | no | not-run |  | check absent from run output |
| accelerator-metrics | conformance | A | yes | pass |  |  |
| ai-service-metrics | conformance | A | yes | fail |  |  |
| inference-gateway | conformance | A | no | not-run |  | check absent from run output |
| pod-autoscaling | conformance | B | yes | not-run |  | check absent from run output |
| cluster-autoscaling | conformance | A | no | not-run |  | check absent from run output |
| robust-controller | conformance | A | no | not-run |  | check absent from run output |
| secure-accelerator-access | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-health | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-imex-channel | conformance | A | yes | not-run |  | check absent from run output |
| gpu-operator-health | conformance | A | yes | fail |  |  |
| platform-health | conformance | A | no | fail |  |  |

### `axis-nri-cdi`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=ran

Axes off base: nri.deviceInjectionMode=cdi (base is raw)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | pass |  |  |
| expected-resources | deployment | A | yes | not-run |  |  |
| gpu-operator-version | deployment | A | no | fail |  |  |
| check-nvidia-smi | deployment | A | yes | pass |  |  |
| nccl-all-reduce-bw | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-net | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-nvls | performance | C | yes | not-run |  | check absent from run output |
| inference-perf | performance | C | yes | not-run |  | check absent from run output |
| dra-support | conformance | A | yes | fail |  |  |
| gang-scheduling | conformance | A | no | not-run |  | check absent from run output |
| accelerator-metrics | conformance | A | yes | pass |  |  |
| ai-service-metrics | conformance | A | yes | fail |  |  |
| inference-gateway | conformance | A | no | not-run |  | check absent from run output |
| pod-autoscaling | conformance | B | yes | not-run |  | check absent from run output |
| cluster-autoscaling | conformance | A | no | not-run |  | check absent from run output |
| robust-controller | conformance | A | no | not-run |  | check absent from run output |
| secure-accelerator-access | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-health | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-imex-channel | conformance | A | yes | not-run |  | check absent from run output |
| gpu-operator-health | conformance | A | yes | fail |  |  |
| platform-health | conformance | A | no | fail |  |  |

### `axis-allocation-watcher-on`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=ran

Axes off base: allocationWatcher.enabled=true (base is false)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | pass |  |  |
| expected-resources | deployment | A | yes | not-run |  |  |
| gpu-operator-version | deployment | A | no | fail |  |  |
| check-nvidia-smi | deployment | A | yes | pass |  |  |
| nccl-all-reduce-bw | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-net | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-nvls | performance | C | yes | not-run |  | check absent from run output |
| inference-perf | performance | C | yes | not-run |  | check absent from run output |
| dra-support | conformance | A | yes | fail |  |  |
| gang-scheduling | conformance | A | no | not-run |  | check absent from run output |
| accelerator-metrics | conformance | A | yes | pass |  |  |
| ai-service-metrics | conformance | A | yes | fail |  |  |
| inference-gateway | conformance | A | no | not-run |  | check absent from run output |
| pod-autoscaling | conformance | B | yes | not-run |  | check absent from run output |
| cluster-autoscaling | conformance | A | no | not-run |  | check absent from run output |
| robust-controller | conformance | A | no | not-run |  | check absent from run output |
| secure-accelerator-access | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-health | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-imex-channel | conformance | A | yes | not-run |  | check absent from run output |
| gpu-operator-health | conformance | A | yes | fail |  |  |
| platform-health | conformance | A | no | fail |  |  |

### `axis-failure-injection-ecc`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=ran

Axes off base: gpu.failureInjection=enabled=true mode=ecc_uncorrectable probability=1 (base is off)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | pass |  |  |
| expected-resources | deployment | A | yes | not-run |  |  |
| gpu-operator-version | deployment | A | no | fail |  |  |
| check-nvidia-smi | deployment | A | yes | pass |  |  |
| nccl-all-reduce-bw | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-net | performance | C | yes | not-run |  | check absent from run output |
| nccl-all-reduce-bw-nvls | performance | C | yes | not-run |  | check absent from run output |
| inference-perf | performance | C | yes | not-run |  | check absent from run output |
| dra-support | conformance | A | yes | fail |  |  |
| gang-scheduling | conformance | A | no | not-run |  | check absent from run output |
| accelerator-metrics | conformance | A | yes | pass |  |  |
| ai-service-metrics | conformance | A | yes | fail |  |  |
| inference-gateway | conformance | A | no | not-run |  | check absent from run output |
| pod-autoscaling | conformance | B | yes | not-run |  | check absent from run output |
| cluster-autoscaling | conformance | A | no | not-run |  | check absent from run output |
| robust-controller | conformance | A | no | not-run |  | check absent from run output |
| secure-accelerator-access | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-health | conformance | A | yes | not-run |  | check absent from run output |
| slinky-slurm-imex-channel | conformance | A | yes | not-run |  | check absent from run output |
| gpu-operator-health | conformance | A | yes | fail |  |  |
| platform-health | conformance | A | no | fail |  |  |

### `axis-compute-domain-fabric`

Recipe: service=kind accelerator=gb200 intent=training · shape=fabric workers=4 · status=blocked

**Blocked (X):** RECLASSIFIED from U. The first run blocked this on the DRA driver lacking --set altProcDevices, which was true of v25.12.0 and is no longer true: v0.5.0, released 2026-08-19, ships altProcDevices, and its own values.yaml documents it as being for mock NVML. But the cell cannot run for a reason that predates that: it declares intent=training on the kind service, and AICR at 0752ea14 resolves no such recipe for gb200. The recipe-resolution failure comes first, so the cause is X, not U. The altProcDevices question it was meant to answer is now carried by targeted-dra-installed instead. Evidence: results/aicr-recipe-matrix-evidence.log.

Axes off base: imex.mockChannels.enabled=true, topology.domains=two cliques

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | X |  |
| expected-resources | deployment | A | yes | blocked | X |  |
| gpu-operator-version | deployment | A | no | blocked | X |  |
| check-nvidia-smi | deployment | A | yes | blocked | X |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | X |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | X |  |
| inference-perf | performance | C | yes | blocked | X |  |
| dra-support | conformance | A | yes | blocked | X |  |
| gang-scheduling | conformance | A | no | blocked | X |  |
| accelerator-metrics | conformance | A | yes | blocked | X |  |
| ai-service-metrics | conformance | A | yes | blocked | X |  |
| inference-gateway | conformance | A | no | blocked | X |  |
| pod-autoscaling | conformance | B | yes | blocked | X |  |
| cluster-autoscaling | conformance | A | no | blocked | X |  |
| robust-controller | conformance | A | no | blocked | X |  |
| secure-accelerator-access | conformance | A | yes | blocked | X |  |
| slinky-slurm-health | conformance | A | yes | blocked | X |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | X |  |
| gpu-operator-health | conformance | A | yes | blocked | X |  |
| platform-health | conformance | A | no | blocked | X |  |

