# AICR check coverage under Mokka

All evidence below is simulation provenance (`sim`). None of it was run on silicon.

## Roll-up

- Check results total: 210 (checks x cells)
- A meaningful: 160  ·  B trivial: 10  ·  C hardware-dependent: 40  ·  G closable gap: 0
- **Meaningful pre-silicon today: 76%** (A / total)
- **Reachable once tracked gaps close: 76%** ((A + G) / total). Roadmap, not a current claim.
- Of the A set, 100 are GPU-dependent (62% of A). The remainder run on any cluster, so Mokka is not what unlocks them.
- Never reached a verdict (blocked or not-run): 197 (94%)

### Why cells did not reach a verdict

| Cause | Meaning | Count |
|---|---|---|
| BUDGET | host memory budget exhausted; cell not attempted. | 105 |
| U | upstream dependency has not released what is needed. NOT a Mokka gap. | 21 |
| X | AICR catalog has no recipe for the combination. NOT a Mokka gap. | 42 |

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

**Blocked (BUDGET):** Not attempted. The host Docker VM holds 7.75 GiB and the inference cell already consumed the window available for this sweep. The intent axis is a recipe-resolution difference, so this cell is expected to differ from the inference cell only in component set, not in Mokka behaviour. Re-run it first when more memory is available.

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | BUDGET |  |
| expected-resources | deployment | A | yes | blocked | BUDGET |  |
| gpu-operator-version | deployment | A | no | blocked | BUDGET |  |
| check-nvidia-smi | deployment | A | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | BUDGET |  |
| inference-perf | performance | C | yes | blocked | BUDGET |  |
| dra-support | conformance | A | yes | blocked | BUDGET |  |
| gang-scheduling | conformance | A | no | blocked | BUDGET |  |
| accelerator-metrics | conformance | A | yes | blocked | BUDGET |  |
| ai-service-metrics | conformance | A | yes | blocked | BUDGET |  |
| inference-gateway | conformance | A | no | blocked | BUDGET |  |
| pod-autoscaling | conformance | B | yes | blocked | BUDGET |  |
| cluster-autoscaling | conformance | A | no | blocked | BUDGET |  |
| robust-controller | conformance | A | no | blocked | BUDGET |  |
| secure-accelerator-access | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-health | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | BUDGET |  |
| gpu-operator-health | conformance | A | yes | blocked | BUDGET |  |
| platform-health | conformance | A | no | blocked | BUDGET |  |

### `base-h100-kind-inference-stack`

Recipe: service=kind accelerator=h100 intent=inference · shape=stack workers=2 · status=blocked

**Blocked (BUDGET):** Not attempted, same memory window. This is the deliberate substitution for the brief's gb300 half of the base sweep: AICR has no gb300 accelerator, so h100 is the nearest second accelerator that both AICR and Mokka support on the kind service. It is a substitution and is never counted as gb300 coverage.

Axes off base: accelerator=h100 (substitute for gb300)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | BUDGET |  |
| expected-resources | deployment | A | yes | blocked | BUDGET |  |
| gpu-operator-version | deployment | A | no | blocked | BUDGET |  |
| check-nvidia-smi | deployment | A | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | BUDGET |  |
| inference-perf | performance | C | yes | blocked | BUDGET |  |
| dra-support | conformance | A | yes | blocked | BUDGET |  |
| gang-scheduling | conformance | A | no | blocked | BUDGET |  |
| accelerator-metrics | conformance | A | yes | blocked | BUDGET |  |
| ai-service-metrics | conformance | A | yes | blocked | BUDGET |  |
| inference-gateway | conformance | A | no | blocked | BUDGET |  |
| pod-autoscaling | conformance | B | yes | blocked | BUDGET |  |
| cluster-autoscaling | conformance | A | no | blocked | BUDGET |  |
| robust-controller | conformance | A | no | blocked | BUDGET |  |
| secure-accelerator-access | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-health | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | BUDGET |  |
| gpu-operator-health | conformance | A | yes | blocked | BUDGET |  |
| platform-health | conformance | A | no | blocked | BUDGET |  |

### `base-gb300-kind-inference-stack`

Recipe: service=kind accelerator=gb300 intent=inference · shape=stack workers=2 · status=blocked

**Blocked (X):** AICR's recipe catalog at 0752ea14 has no gb300 accelerator. Verified by running the CLI, not by reading it: `aicr recipe --service kind --accelerator gb300 --intent inference` exits 2 with "[INVALID_REQUEST] no recipe provides accelerator 'gb300'". The catalog knows a100, b200, gb200, h100, h200, l40s and rtx-pro-6000. Mokka ships a gb300 profile; AICR has no accelerator to match it to. This is an AICR catalog gap, not a Mokka gap.

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

**Blocked (X):** Same as base-gb300-kind-inference-stack: AICR has no gb300 accelerator.

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

Axes off base: nvidia-dra-driver-gpu=installed (base cell had none)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | not-run |  | check absent from run output |
| expected-resources | deployment | A | yes | not-run |  | check absent from run output |
| gpu-operator-version | deployment | A | no | not-run |  | check absent from run output |
| check-nvidia-smi | deployment | A | yes | not-run |  | check absent from run output |
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

### `axis-nri-cdi`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=blocked

**Blocked (BUDGET):** Not attempted; host memory window exhausted after the base cell.

Axes off base: nri.deviceInjectionMode=cdi (base is raw)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | BUDGET |  |
| expected-resources | deployment | A | yes | blocked | BUDGET |  |
| gpu-operator-version | deployment | A | no | blocked | BUDGET |  |
| check-nvidia-smi | deployment | A | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | BUDGET |  |
| inference-perf | performance | C | yes | blocked | BUDGET |  |
| dra-support | conformance | A | yes | blocked | BUDGET |  |
| gang-scheduling | conformance | A | no | blocked | BUDGET |  |
| accelerator-metrics | conformance | A | yes | blocked | BUDGET |  |
| ai-service-metrics | conformance | A | yes | blocked | BUDGET |  |
| inference-gateway | conformance | A | no | blocked | BUDGET |  |
| pod-autoscaling | conformance | B | yes | blocked | BUDGET |  |
| cluster-autoscaling | conformance | A | no | blocked | BUDGET |  |
| robust-controller | conformance | A | no | blocked | BUDGET |  |
| secure-accelerator-access | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-health | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | BUDGET |  |
| gpu-operator-health | conformance | A | yes | blocked | BUDGET |  |
| platform-health | conformance | A | no | blocked | BUDGET |  |

### `axis-allocation-watcher-on`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=blocked

**Blocked (BUDGET):** Not attempted. Note in advance that any check reading GPU memory utilization as evidence of real work stays bucket B whatever this cell shows, because the allocation bytes are synthetic by construction.

Axes off base: allocationWatcher.enabled=true (base is false)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | BUDGET |  |
| expected-resources | deployment | A | yes | blocked | BUDGET |  |
| gpu-operator-version | deployment | A | no | blocked | BUDGET |  |
| check-nvidia-smi | deployment | A | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | BUDGET |  |
| inference-perf | performance | C | yes | blocked | BUDGET |  |
| dra-support | conformance | A | yes | blocked | BUDGET |  |
| gang-scheduling | conformance | A | no | blocked | BUDGET |  |
| accelerator-metrics | conformance | A | yes | blocked | BUDGET |  |
| ai-service-metrics | conformance | A | yes | blocked | BUDGET |  |
| inference-gateway | conformance | A | no | blocked | BUDGET |  |
| pod-autoscaling | conformance | B | yes | blocked | BUDGET |  |
| cluster-autoscaling | conformance | A | no | blocked | BUDGET |  |
| robust-controller | conformance | A | no | blocked | BUDGET |  |
| secure-accelerator-access | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-health | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | BUDGET |  |
| gpu-operator-health | conformance | A | yes | blocked | BUDGET |  |
| platform-health | conformance | A | no | blocked | BUDGET |  |

### `axis-failure-injection-ecc`

Recipe: service=kind accelerator=gb200 intent=inference · shape=stack workers=2 · status=blocked

**Blocked (BUDGET):** Not attempted; host memory window exhausted after the base cell.

Axes off base: gpu.failureInjection.mode=ecc_uncorrectable (base is off)

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | BUDGET |  |
| expected-resources | deployment | A | yes | blocked | BUDGET |  |
| gpu-operator-version | deployment | A | no | blocked | BUDGET |  |
| check-nvidia-smi | deployment | A | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | BUDGET |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | BUDGET |  |
| inference-perf | performance | C | yes | blocked | BUDGET |  |
| dra-support | conformance | A | yes | blocked | BUDGET |  |
| gang-scheduling | conformance | A | no | blocked | BUDGET |  |
| accelerator-metrics | conformance | A | yes | blocked | BUDGET |  |
| ai-service-metrics | conformance | A | yes | blocked | BUDGET |  |
| inference-gateway | conformance | A | no | blocked | BUDGET |  |
| pod-autoscaling | conformance | B | yes | blocked | BUDGET |  |
| cluster-autoscaling | conformance | A | no | blocked | BUDGET |  |
| robust-controller | conformance | A | no | blocked | BUDGET |  |
| secure-accelerator-access | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-health | conformance | A | yes | blocked | BUDGET |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | BUDGET |  |
| gpu-operator-health | conformance | A | yes | blocked | BUDGET |  |
| platform-health | conformance | A | no | blocked | BUDGET |  |

### `axis-compute-domain-fabric`

Recipe: service=kind accelerator=gb200 intent=training · shape=fabric workers=4 · status=blocked

**Blocked (U):** The NVIDIA DRA driver resolves the nvidia-caps-imex-channels device major out of /proc/devices and needs --set altProcDevices to read Mokka's substitute. That flag is on the DRA driver's main branch and is in no release, including v25.12.0, as the chart's own values.yaml records at lines 307-325. Mokka v0.3.0 already renders the IMEX surface, so this is an upstream release gap, not a Mokka gap.

Axes off base: imex.mockChannels.enabled=true, topology.domains=two cliques

| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |
|---|---|---|---|---|---|---|
| operator-health | deployment | A | yes | blocked | U |  |
| expected-resources | deployment | A | yes | blocked | U |  |
| gpu-operator-version | deployment | A | no | blocked | U |  |
| check-nvidia-smi | deployment | A | yes | blocked | U |  |
| nccl-all-reduce-bw | performance | C | yes | blocked | U |  |
| nccl-all-reduce-bw-net | performance | C | yes | blocked | U |  |
| nccl-all-reduce-bw-nvls | performance | C | yes | blocked | U |  |
| inference-perf | performance | C | yes | blocked | U |  |
| dra-support | conformance | A | yes | blocked | U |  |
| gang-scheduling | conformance | A | no | blocked | U |  |
| accelerator-metrics | conformance | A | yes | blocked | U |  |
| ai-service-metrics | conformance | A | yes | blocked | U |  |
| inference-gateway | conformance | A | no | blocked | U |  |
| pod-autoscaling | conformance | B | yes | blocked | U |  |
| cluster-autoscaling | conformance | A | no | blocked | U |  |
| robust-controller | conformance | A | no | blocked | U |  |
| secure-accelerator-access | conformance | A | yes | blocked | U |  |
| slinky-slurm-health | conformance | A | yes | blocked | U |  |
| slinky-slurm-imex-channel | conformance | A | yes | blocked | U |  |
| gpu-operator-health | conformance | A | yes | blocked | U |  |
| platform-health | conformance | A | no | blocked | U |  |

