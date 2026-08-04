# Mokka + AICR pre-silicon preflight: sweep findings

**Date:** 2026-08-03 · **Ran by:** Carlos Eduardo Arango Gutierrez · **For review by:** Mark Chmarny
**Cluster:** kind v0.32.0, Kubernetes v1.36.1, containerd 2.3.1, 1 control plane + 2 workers, arm64,
one host, 7.75 GiB Docker VM
**Mokka:** nvml-mock v0.3.0, image `ghcr.io/nvidia/nvml-mock:0.3.0`
(`sha256:3b9bcf33d8bf442fe203062b78abeef7633172b4e589b23192e27edb6e81d3cc`), chart 0.3.0
(`sha256:99e0de7e7c3292e9f814e15841c6c7a5bec8f64ce07620dd9f5c05ccb68b516e`), profile `gb200`, NRI path
**AICR:** `upstream/main` @ `0752ea148aa1ae849cbef86ec59d0c970a582496` (2026-07-31)
**GPU Operator:** v25.3.4 · **DRA driver:** nvidia-dra-driver-gpu 25.12.0

**All evidence below is simulation provenance (`sim`). None of it was run on silicon.**

## The question

Does Mokka + AICR materially reduce GB200 bring-up time, or does it merely prove the stack deploys
against mocked APIs? (Mark Chmarny, 2026-07-24.)

## Answer

AICR ran unmodified against a Mokka cluster and dispatched 9 of its 21 checks; 3 passed for real
integration reasons, including `check-nvidia-smi`, which exercises device-plugin allocation and the
`dlopen` of `libnvidia-ml.so.1` end to end inside a CUDA base image. **None of the 5 failures was
caused by a Mokka capability gap**: they were a GPU Operator version that violates the recipe's own
constraint, an unreleased upstream DRA flag, and recipe components the host memory budget did not let
me deploy. So this is more than "the stack deploys against mocked APIs", but the run is far too small
to put a number on bring-up time saved, and it says nothing whatsoever about scale.

## 1. What actually ran

AICR resolved `--service kind --accelerator gb200 --intent inference` to a **14-component** recipe
over 5 overlays, then **selected 9 of the 21 catalog checks** as applicable to this cluster. The other
12 were never dispatched: the 4 performance checks selected 0 (no NCCL or inference workload
declared for this criteria pair), and 8 conformance checks skipped at selection because the recipe
components they target were not present.

| Check | Phase | Bucket | Outcome | Why |
|---|---|---|---|---|
| `operator-health` | deployment | A | **pass** | GPU Operator pods reconciled and healthy against the mock driver surface |
| `check-nvidia-smi` | deployment | A | **pass** | CUDA base-image pod ran `nvidia-smi` on every schedulable GPU node and produced the banner, driver version, CUDA version and success marker |
| `accelerator-metrics` | conformance | A | **pass** | real dcgm-exporter served `DCGM_FI_DEV_GPU_UTIL`, `FB_USED`, `GPU_TEMP`, `POWER_USAGE` off the mock NVML |
| `gpu-operator-version` | deployment | A | fail | `v25.3.4 does not satisfy constraint ">= v25.10.0"` |
| `dra-support` | conformance | A | fail | `nvidia-dra-driver-gpu-controller` Deployment not found |
| `ai-service-metrics` | conformance | A | fail | no Prometheus service on port 9090 in `monitoring` |
| `gpu-operator-health` | conformance | A | fail | `ClusterPolicy state=notReady (want ready)` |
| `platform-health` | conformance | A | fail | 9 recipe namespaces not found |
| `expected-resources` | deployment | A | inconclusive | Job pod disappeared before the result could be read; recorded `not-run`, never a pass |

Raw CTRF: [`results/base-gb200-kind-inference-stack/ctrf/validate.json`](results/base-gb200-kind-inference-stack/ctrf/validate.json).

### Every failure, attributed

This is the part worth checking hardest, because it is where a misleading result would come from.

| Failure | Cause | Whose problem |
|---|---|---|
| `gpu-operator-version` | I installed v25.3.4; the gb200 recipe requires `>= v25.10.0` (the Blackwell floor) | **mine.** And the check did its job: this is precisely the kind of version violation preflight is meant to catch before silicon. |
| `dra-support` | The controller Deployment only exists when `resources.computeDomains.enabled=true`. Enabling it makes the kubelet plugin CrashLoopBackOff with `error parsing '/proc/devices': unexpected regex match: []`. The DRA driver needs `--set altProcDevices` to read Mokka's substitute, and that flag is on the driver's main branch and in **no** release including v25.12.0. | **upstream (cause U).** Mokka already renders the IMEX surface. Verified by enabling it and reading the crash, not by citing the docs. |
| `ai-service-metrics` | Prometheus is 1 of the 14 recipe components; I deployed 2 | **mine (scope).** 7.75 GiB does not hold the full stack. |
| `platform-health` | 9 recipe namespaces absent, same reason | **mine (scope).** |
| `gpu-operator-health` | `ClusterPolicy` stays `notReady` because `gpu-feature-discovery` crashes | **cause K, with a caveat.** See §3. |

**Zero of the five failures is a Mokka capability gap.** I want that stated plainly because the
opposite conclusion would have been the convenient one.

## 2. Coverage, two numbers, both shown

**Analytical** (what each check would prove under Mokka, judged from AICR source before any run,
catalogued with a file:line citation each in [`catalog.yaml`](catalog.yaml)):

| Bucket | Count | Share of 21 |
|---|---|---|
| **A meaningful** | 16 | 76% |
| B trivial | 1 | 5% |
| C hardware-dependent | 4 | 19% |
| **G closable Mokka gap** | 0 | 0% |

- **Analytical ceiling today: 76%** (A / total).
- **Reachable once tracked gaps close: 76%** ((A + G) / total). No difference, because at v0.3.0 no
  catalogued check is blocked by a missing Mokka capability. That is a real change from the
  2026-07-25 POC, where `dra-support` and two others sat in G. v0.3.0 shipped those.

**Observed** (what this run actually demonstrated):

- AICR dispatched **9 of 21** checks (43%).
- **3 checks (14% of the suite) reached a meaningful pass.**
- 5 failed, none for a Mokka reason. 1 was inconclusive.

**Do not read 76% as a measured result.** It is the ceiling if the whole recipe stack is deployed.
The measured number from this run is 14%, and the gap between the two is almost entirely the host
memory budget, not Mokka. Of the 16 A-bucket checks, **10 are GPU-dependent** and 6 run on any
cluster at all, so Mokka is not what unlocks that 6.

## 3. The one real gap this sweep found

**The PCI bus ID reaches NVML consumers without its domain prefix.** Two independent consumers, one
symptom:

```
DRA driver v25.12.0 (go-nvlib):
  W nvlib.go:491] error getting PCIe root for device 0, continuing without attribute:
                  invalid PCI Bus ID format: :0a:00.0      (all 8 devices)

gpu-feature-discovery (GPU Operator v25.3.4):
  E main.go:127] error creating labeler: error creating resource labeler:
                 unable to read PCI device vendor id for :0a:00.0:
                 open /sys/bus/pci/devices/:0a:00.0/vendor: no such file or directory
```

The `gb200` profile declares `bus_id: "0000:0A:00.0"`, and Mokka renders
`/var/lib/nvml-mock/sys/bus/pci/devices/0000:0a:00.0` correctly. The domain is lost somewhere between
the profile and what the consumer reads back over NVML. Evidence:
[`results/pci-busid-evidence.log`](results/pci-busid-evidence.log).

This matters beyond the two log lines. The repo currently documents `dra.k8s.io/pcieRoot` as
unavailable (issue #265) and attributes it to Go's `os` package issuing raw syscalls that the
`LD_PRELOAD` shim cannot intercept. **The DRA driver log shows the failure happening earlier than
that, at bus-ID string parsing, before any sysfs read is attempted.** So #265's stated diagnosis is
at least incomplete, and the fix may be much smaller than the docs imply. I did not isolate whether
the defect is in the engine, the CGo bridge, or go-nvml marshalling, so I am not claiming which.

It is bucket **G**: closable, not hardware-dependent. It is the only one.

**Also worth noting, not a gap:** the GFD crash above is why `ClusterPolicy` never reached `ready`,
which is why `gpu-operator-health` failed. I classified that **K** rather than **G** because the
repo's own `e2e-gpu-operator` scenario asserts GFD labels and passes on the CDI path with the
container toolkit installed, whereas this sweep runs stock kind with `cdi.enabled=false`. The
distinguishing cell did not fit in the memory budget, so the honest status is **not distinguished**.
Reasoning in [DECISIONS.md](DECISIONS.md) D-014.

## 4. What the matrix did not cover, and why

10 cells planned, 2 ran. Nothing is hidden: blocked cells stay in the denominator.

| Cause | Cells | Meaning |
|---|---|---|
| **BUDGET** | 5 | Host memory. 7.75 GiB fits one cluster shape at a time; a loaded 4-worker cluster needs ~2.9 GiB and OOMs mid-run, which produces a fake failure that reads like a Mokka finding. |
| **X** | 2 | **AICR has no `gb300` accelerator.** `aicr recipe --service kind --accelerator gb300 --intent inference` exits 2: `no recipe provides accelerator 'gb300'`. The catalog knows a100, b200, gb200, h100, h200, l40s, rtx-pro-6000. Mokka ships a gb300 profile; AICR has nothing to match it to. **Not a Mokka gap.** This is yours to decide on, Mark, and I did not file an issue in NVIDIA/aicr. |
| **U** | 1 | ComputeDomain, per §1. |

The brief asked for every recipe across `{gb200, gb300}` and single/multi-node. The gb300 half is
impossible on the AICR side. I substituted `h100` as the second accelerator, and that cell is
recorded as a substitution, never as gb300 coverage. It did not run either, for budget.

## 5. Claims this supports, and does not

| Claim | Supported? | Basis |
|---|---|---|
| Pre-silicon **integration preflight** for the A-bucket checks | **Partly** | 3 checks passed for real integration reasons on a real run. 16 are analytically capable. But only 9 of 21 were dispatched, so the demonstration is narrow. |
| AICR runs **unmodified** on a Mokka cluster | **Yes** | No AICR flag, recipe or check was modified. The only accommodations were cluster-side (`operator.runtimeClass=runc`). |
| Node-wide injection needs no pod change | **Yes** | A plain `debian:bookworm-slim` pod with `resources: {}`, no hostPath and no runtimeClassName ran `nvidia-smi -L` and listed 8 GB200s. |
| The GPU stack really installs against the mock | **Yes** | GPU Operator reconciled; device plugin advertised `nvidia.com/gpu: 8` per worker; dcgm-exporter served real DCGM fields; the DRA driver published 16 devices across 2 ResourceSlices. |
| **Reduces** time to first token | **Not demonstrated here** | Plausible from the 3 real passes, but this run has no baseline to measure against and no bring-up failure history to back-test. See §6. |
| **Hit** first token on day one | **No** | Simulation cannot prove it. The hardware-dependent path is untested by construction. |
| AICR-on-silicon remains the final readiness gate | **Yes** | Bucket C is non-empty by construction: 4 checks (3 NCCL variants plus `inference-perf`) cannot be judged without hardware. |
| Deep GPU-stack fidelity at KWOK scale | **No** | Not tested. This ran on 3 containers on one laptop. |

## 6. What is missing, honestly

**The back-test did not happen.** The POC design called for replaying a known GB200 NVL72 bring-up
and checking which of its real failures a Mokka preflight would have caught. That was the money
metric. It needs a real failure set from a real bring-up, and I do not have one: it was the top open
blocker in the POC tracker on 2026-07-24 and it is still open. **Without it there is no defensible
bring-up-time number, so this note does not give one.** Anyone quoting a time saving from this run is
quoting something I did not measure.

**Scale is untested and this run cannot speak to it.** kind on one host. That is the whole caveat.

## 6b. KWOK + Mokka: combining them makes the suite report green without running

Run after the sections above, on Carlos's call and over a panel dissent I agreed with. Full writeup:
[HYBRID-KWOK-FINDING.md](HYBRID-KWOK-FINDING.md). Evidence:
[results/kwok-false-pass-evidence.log](results/kwok-false-pass-evidence.log).

Added 250 hollow KWOK GPU nodes to the Mokka cluster, giving a 253-node fleet advertising 2016
`nvidia.com/gpu`, and re-ran the identical recipe. AICR dispatched **the same 9 of 21 checks** as the
baseline, so this is not a coverage increase. What changed is the verdicts: **3 pass / 5 fail /
1 inconclusive became 9 pass / 0 fail, in 4.2s and 5.2s against 9m 0s. Nothing executed.**

Four verified facts compose into it: AICR validator Jobs tolerate every taint (empty key,
`operator: Exists`, asserted at `deployer_test.go:516`); KWOK nodes carry the full GPU label set and
real `nvidia.com/gpu` capacity; so the scheduler places validators on them, 250 hollow against 2 real;
and KWOK's `pod-complete` stage transitions any Job-owned pod to `Succeeded`. The container status
shows an empty `imageID` and `started: false` with `startedAt == finishedAt`, so the image was never
pulled, and `kubectl logs` returns `MethodNotAllowed` because there is no kubelet.

**`--node-selector` does not mitigate it.** Measured: the flag reaches the snapshot agent's pod spec
and stops. `deployer.go:66` states it is passed to inner workloads via `AICR_NODE_SELECTOR`, never
applied to the validator Job's own pod spec. With the flag set as documented, all four deployment
checks still passed in 4.2s on hollow nodes.

Not a Mokka gap, and not a KWOK bug. It is a composition hazard whose actionable half is in AICR, and
it is exactly the architecture Eliran's per-nodepool `backend` design produces.

**Excluded from the coverage matrix on purpose.** These 9 passes are all false; feeding them into the
denominator would raise the headline using fabricated results.

Incidentally, the horizontal axis behaved: 250 hollow nodes cost about 1 GiB and took
`kubectl get nodes` from 0.06s to 0.30s. No controller churn.

## 7. One structural finding worth your time, Mark

AICR already ships a KWOK lane (`kwok/`, `.ctlptl-kwok.yaml`, `make kwok-test-all`) that covers 90
non-OCP recipes end to end through resolve, bundle, deploy and schedule. Its own README states the
boundary:

> KWOK validates scheduling, not runtime: node selectors, tolerations, resource requests, scheduling
> decisions, and Helm chart generation are checked. Container execution, GPU functionality, and
> network connectivity are NOT.

And its pass criterion is that no pod is left unscheduled. **`aicr validate` cannot run under KWOK at
all**, because every validator is a containerized Job that needs a real pod on a real kubelet. Only
one check in the whole validator tree even detects a KWOK node, and it skips
(`slinky_slurm_health_check.go:346-353`).

That is the vertical-versus-horizontal argument, stated in your own repo rather than by me: KWOK gives
recipe and scheduling breadth cheaply; the 21 validator checks need a real kubelet with a real GPU
stack under it. Mokka is what lets those 21 run before silicon. This sweep got 9 of them dispatched
and 3 passing on a laptop.

## 8. Recommendation

**Go, with scope, and with the claim kept narrow.** Specifically:

1. Re-run this sweep on a host with 32 GiB or more so the full 14-component recipe deploys. The gap
   between the 14% observed and the 76% analytical is almost entirely memory, and closing it is the
   single highest-value next step. Nothing about Mokka needs to change first.
2. Install GPU Operator `>= v25.10.0` for gb200 so `gpu-operator-version` is testing the intended
   thing.
3. Get a real GB200 bring-up failure set, or drop the bring-up-time claim entirely until someone does.
   Do not let it be quoted from this run.
4. Fix the PCI bus-ID gap in §3. It is small, it unblocks `dra.k8s.io/pcieRoot`, and it may correct a
   wrong diagnosis already written into issue #265.
5. Decide, on your side, whether AICR should carry a `gb300` accelerator. Mokka has the profile.

What I would **not** do yet: put a bring-up-time number in any leadership-facing artifact. The
integration story is real and demonstrable; the time saving is not measured.

## Appendix

- Harness and how to re-run: [README.md](README.md)
- Every judgment call, with rationale: [DECISIONS.md](DECISIONS.md)
- Per-check analytical buckets with source citations: [catalog.yaml](catalog.yaml)
- The permutation matrix, including cells that did not run: [cells.yaml](cells.yaml)
- Machine-readable results: [results/report.json](results/report.json)
- Rendered coverage read: [results/coverage.md](results/coverage.md)
- Supersedes the 2026-07-25 POC in [PR #503](https://github.com/NVIDIA/k8s-test-infra/pull/503),
  which predates v0.3.0.
