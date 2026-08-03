# Artifact 1: AICR check-coverage catalog under Mokka

**Purpose:** the core deliverable. For every AICR validation and conformance check, classify what it
actually proves when run on a Mokka-enabled cluster with no silicon. Mark's bar: separate checks that
exercise **real integration** from checks that **pass trivially against mocked APIs**. A check in
bucket B is not a win; recording it honestly as B is what makes the catalog credible.

**Status:** filled from a real run on 2026-08-03. `Result` columns come only from that run; nothing is
inferred. **Every row is `sim` provenance.** Source of the check list: `NVIDIA/aicr`
`recipes/validators/catalog.yaml` at `upstream/main` `0752ea14` (21 checks: 4 deployment,
4 performance, 13 conformance).

The machine-readable form of the bucket column, with a file:line citation for every rationale, is
[`catalog.yaml`](catalog.yaml). This page is the human view.

## Buckets

| Bucket | Meaning | Counts as POC value? |
|---|---|---|
| **A meaningful under Mokka** | Exercises real integration logic: GPU Operator or DRA install, NVML-facing components, resource advertisement, scheduling, control-plane behavior. Could plausibly fail for a real reason. | **Yes**, this is the movable-left set |
| **B passes trivially against mock** | Green only because the mock answers the API; no real integration exercised. Would pass regardless of correctness. | **No**, record honestly, it bounds the claim |
| **C hardware-dependent, N/A in sim** | Cannot be judged pre-silicon: CUDA execution, firmware and driver compat, NCCL / NVLink / NVLS, fabric data plane, TTFT / throughput. | **No**, these define why AICR-on-silicon stays the final gate |
| **G gap: blocked by a missing Mokka capability** | Could be meaningful pre-silicon, but Mokka does not simulate the needed surface yet. **Not** hardware-dependent, so it is closable. | **Yes, as roadmap**: file an issue, size it |

**Result vocabulary.** `pass` / `fail` mean the suite reached that verdict. `not dispatched` means
AICR did not select the check for this recipe and cluster, so it never ran; it is never counted as a
pass. `inconclusive` means the suite reached no verdict (CTRF `other`).

## Catalog

| # | AICR check | Area | Bucket | Result under Mokka | Evidence provenance | Why this bucket | Gap issue | Would it catch a real bring-up failure? |
|---|---|---|---|---|---|---|---|---|
| 1 | `operator-health` | operator-install | **A** | **pass** | sim | Reconciles the real GPU Operator against the mock driver surface and requires its pods healthy. This sweep saw it fail for a real reason first (operands demanded a runtime class the node lacked) and pass after the fix. | n/a | **Yes.** A broken operand or unschedulable DaemonSet fails it. |
| 2 | `expected-resources` | control-plane | **A** | inconclusive | sim | Verifies every recipe componentRef and fans out to 37 in-process chainsaw health checks, one of which gates on `ClusterPolicy status.state == ready`. | n/a | **Yes.** Missing prerequisites and stuck operands fail it. |
| 3 | `gpu-operator-version` | operator-install | **A** | **fail** | sim | Evaluates the deployed operator version against the recipe constraint. Failed correctly here: `v25.3.4 does not satisfy ">= v25.10.0"`. GPU-independent: it reads a Helm release, so any cluster runs it. | n/a | **Yes, and it did.** This is a version-skew catch, pre-silicon. |
| 4 | `check-nvidia-smi` | nvml-surface | **A** | **pass** | sim | The strongest case in the suite. Schedules a CUDA base-image pod per GPU node and requires the NVIDIA-SMI banner, a driver version, a CUDA version and a success marker, exercising device-plugin allocation, device injection and `dlopen` of `libnvidia-ml.so.1`. | n/a | **Yes.** This is the classic "driver stack does not come up" failure. |
| 5 | `nccl-all-reduce-bw` | nccl-nvlink-fabric | **C** | not dispatched | n/a | Measures collective bus bandwidth. Mokka simulates fabric management, not the data plane; NVLink counters accrue from a clock epoch, not traffic. | n/a | No. Needs silicon. |
| 6 | `nccl-all-reduce-bw-net` | nccl-nvlink-fabric | **C** | not dispatched | n/a | As above, plus asserts the NET transport carried traffic. Needs EFA or RoCE. | n/a | No. Needs silicon. |
| 7 | `nccl-all-reduce-bw-nvls` | nccl-nvlink-fabric | **C** | not dispatched | n/a | As above, plus asserts NVLS multicast availability across an NVL72 IMEX domain. Mokka renders IMEX channels but runs no collectives. | n/a | No. Needs silicon. |
| 8 | `inference-perf` | ttft-throughput | **C** | not dispatched | n/a | Runs AIPerf and asserts throughput and p99 TTFT. The mock executes no CUDA kernel, so there is no inference to measure. This check is the literal definition of what simulation cannot prove. | n/a | No. Needs silicon. This is why the claim is "reduce" and never "hit first token on day one". |
| 9 | `dra-support` | dra | **A** | **fail** | sim | Requires a served `resource.k8s.io`, an available DRA controller Deployment, a ready kubelet plugin, a validated `*.nvidia.com` ResourceSlice, and a behavioural ResourceClaim pod. Mokka drove the ResourceSlice half correctly (16 devices across 2 slices). Failed on the controller, which needs ComputeDomains, which needs an unreleased upstream flag: **cause U, not a Mokka gap**. | n/a (upstream) | **Yes.** A driver that cannot enumerate devices fails it. |
| 10 | `gang-scheduling` | scheduling | **A** | not dispatched | n/a | Real KAI scheduling integration, but the validator uses CPU-only workers by design and asserts zero resourceClaims. Any cluster runs it, so Mokka is not what unlocks it. | n/a | Yes, but not a GPU-stack failure. |
| 11 | `accelerator-metrics` | telemetry | **A** | **pass** | sim | Real dcgm-exporter scraped `DCGM_FI_DEV_GPU_UTIL`, `FB_USED`, `GPU_TEMP` and `POWER_USAGE` off the mock NVML. The whole exporter path is exercised. It asserts presence, not magnitude, which is why it is A and not B; the values themselves are clock-driven. | n/a | **Yes.** A telemetry path that does not come up fails it. |
| 12 | `ai-service-metrics` | telemetry | **A** | **fail** | sim | Requires a DCGM series in Prometheus and a reachable custom-metrics API. Failed here because Prometheus is 1 of the 14 recipe components and this run deployed 2: **scope, not a Mokka gap**. | n/a | **Yes.** Broken scrape or adapter registration fails it. |
| 13 | `inference-gateway` | control-plane | **A** | not dispatched | n/a | GatewayClass Accepted, Gateway Programmed, CRDs present, proxy endpoints Ready. Real reconciliation, no GPU-stack contact. | n/a | Yes, but not a GPU-stack failure. |
| 14 | `pod-autoscaling` | telemetry | **B** | not dispatched | n/a | Gates on a `dcgm_gpu_power_usage` external metric, then drives an HPA against a 10 W target that an idle GPU already exceeds. Under Mokka the power number is clock-driven and never responds to load, so the HPA scales on a signal that cannot fall. The wiring is exercised; GPU-driven autoscaling is not. **Recorded B deliberately.** | n/a | No. It would pass whether or not autoscaling-on-GPU-load works. |
| 15 | `cluster-autoscaling` | scheduling | **A** | not dispatched | n/a | Karpenter provisions nodes from pending pods. Real scheduling integration; AICR's own KWOK provider is the intended substrate, so it is not GPU-dependent. | n/a | Yes, but not a GPU-stack failure. |
| 16 | `robust-controller` | control-plane | **A** | not dispatched | n/a | Requires the controller, a reachable ValidatingWebhook, and proof the webhook rejects an invalid CR. Real admission-path integration. | n/a | Yes, but not a GPU-stack failure. |
| 17 | `secure-accelerator-access` | scheduling | **A** | not dispatched | n/a | The check that most directly exercises MEP-0002: a granted container must see exactly one GPU, an unauthorized sibling must see no `/dev/nvidia*`, and no hostPath may carry `/dev/nvidia`. **The highest-value check not yet run in this sweep.** | n/a | **Yes.** Wrong device-spec passing or NRI suppression fails it. |
| 18 | `slinky-slurm-health` | control-plane | **A** | not dispatched | n/a | Slurm controller, scheduling, a GPU job and accounting. Notably the only validator in AICR that detects a simulated node and skips, and it detects KWOK, not Mokka. | n/a | Yes. |
| 19 | `slinky-slurm-imex-channel` | dra | **A** | not dispatched | n/a | Concurrent Slurm jobs must receive distinct IMEX channels. v0.3.0 renders the channel surface, so this is fabric **management**, reachable in principle. Do not confuse with NVLS throughput, which is C. | n/a | Yes. |
| 20 | `gpu-operator-health` | operator-install | **A** | **fail** | sim | Requires the operator Deployment, `ClusterPolicy status.state == ready`, and a ready dcgm-exporter DaemonSet. Failed because GFD crashed, keeping ClusterPolicy `notReady`. Classified **cause K** (this sweep's stock-kind, no-toolkit configuration) rather than G, because the repo's own CDI-path e2e asserts GFD labels and passes. **Not distinguished** by a controlled cell; see DECISIONS.md D-014. | n/a | **Yes.** This is the deepest single "did the GPU stack come up" assertion. |
| 21 | `platform-health` | control-plane | **A** | **fail** | sim | Requires every enabled component namespace Active and every expected resource verified. Failed on 9 absent namespaces: **scope, not a Mokka gap**. | n/a | Yes, but not a GPU-stack failure. |

## Roll-up

- Checks total: **21** · **A: 16** · B: **1** · C: **4** · **G (closable gaps): 0**
- **Analytical ceiling: 76%** (A / total). What carries real pre-silicon signal *if the full recipe
  stack is deployed*. **This is not a measured result.**
- **Reachable once tracked gaps close: 76%** ((A + G) / total). Identical, because no catalogued check
  is blocked by a missing Mokka capability at v0.3.0. That is a genuine improvement over the
  2026-07-25 POC, where `dra-support` and two others sat in G; v0.3.0 shipped them.
- **Measured this run: 14%.** AICR dispatched 9 of 21 checks; 3 reached a meaningful pass. The gap
  between 14% and 76% is almost entirely the 7.75 GiB host memory budget, not Mokka.
- Of the 16 A-bucket checks, **10 are GPU-dependent**. The other 6 run on any cluster, so Mokka is
  not what unlocks them, and they must not be counted as GPU coverage.
- Gaps filed: **1** (PCI bus ID loses its domain prefix; see FINDINGS.md §3). Closed during this
  sweep: **0**. Resulting bucket moves: **0**.
- **Notable B-bucket surprise:** `pod-autoscaling`. It reads a GPU power metric that, under Mokka, is
  clock-driven and cannot fall, so it would pass regardless of whether GPU-driven autoscaling works.
- **Notable A-bucket win:** `gpu-operator-version` failed for a genuinely correct reason, catching a
  GPU Operator version that violates the gb200 recipe's own `>= v25.10.0` floor. That is preflight
  doing exactly its job, pre-silicon.

## Rules used to fill this in

1. **No result without a run.** Empty beats inferred. `not dispatched` is recorded as such and is
   never a pass.
2. **Bucket B is not failure, it is scope.** It tells a customer what preflight does not cover.
3. **A check blocked by a missing Mokka capability is bucket G**, needs a tracked issue, and is
   distinct from a check blocked by an upstream release (U), by the kind environment (K), or by the
   AICR catalog (X). Conflating these is the easiest way to produce a misleading result.
4. **Provenance stays attached.** Every row is `sim`. If any row is later confirmed on silicon, change
   it to `silicon` and date it.
