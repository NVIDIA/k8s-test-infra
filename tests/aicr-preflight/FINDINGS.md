# Mokka + AICR pre-silicon preflight, POC findings

**Date:** 2026-07-25 · **Ran by:** Carlos Eduardo Arango Gutierrez · **For review by:** Mark Chmarny
**Cluster:** 1-node Kind (`nvml-mock-op`), Kubernetes v1.36.1, linux/arm64, `nvml-mock` `gb200` profile,
GPU Operator (ClusterPolicy `ready`), `nvidia-dra-driver-gpu` v25.12.0, AICR at commit `06256cc8`
**All evidence below is simulation provenance (`sim`). Nothing here has been confirmed on silicon.**

## The question

Does Mokka + AICR materially reduce GB200 bring-up time, or does it merely prove the stack deploys
against mocked APIs? (Mark Chmarny, 2026-07-24.)

## Answer

It does more than prove deployment against mocked APIs, but by less than the headline coverage number
suggests, and the strongest single result is a real integration defect found pre-silicon rather than a
green suite. Of 21 AICR checks, 14 are analytically meaningful under Mokka, but only 9 of those depend
on the GPU stack at all, and only 9 checks of the 21 actually executed in this run. The honest verdict
is go-with-scope: preflight is real for operator install, NVML-facing components, resource
advertisement and control-plane behaviour, and it is nothing at all for the hardware-dependent path
that determines first token.

## 1. Check coverage

Of **21** AICR checks: **14 meaningful under Mokka (A)**, **0 pass trivially (B)**,
**4 hardware-dependent and not runnable (C)**, **3 blocked by closable Mokka gaps (G)**.

- **Today: 66.7% of the AICR suite carries real pre-silicon signal** (A / total).
- **Reachable: 81.0% once the tracked gaps close** ((A + G) / total). Roadmap, not a current claim.

Two qualifications, both of which cut against the headline:

1. **Only 9 of the 14 bucket-A checks depend on the GPU stack.** The other 5
   (`gpu-operator-version`, `gang-scheduling`, `inference-gateway`, `robust-controller`,
   `platform-health`) are control-plane checks that any cluster would run. `gang-scheduling` is
   deliberately CPU-only upstream. They move left, but Mokka is not what unlocks them. **The share
   Mokka specifically unlocks is 9/21, or 42.9%.**
2. **Only 9 of 21 checks executed.** 5 passed, 4 failed, 12 were not run because the generated
   `kind` + `gb200` recipe did not select them. Bucket assignment is analysis over the whole catalog;
   it is not evidence that the other 12 would behave as classified.

| Bucket | Count | What it means for a customer |
|---|---:|---|
| A meaningful | 14 | integration work that can move left, before silicon |
| B trivial | 0 | see note below |
| C hardware | 4 | why AICR-on-silicon remains the final gate |
| G gap | 3 | not covered yet, but closable in Mokka |

**On B being empty.** This is a real result rather than an oversight, and it is worth stating because
an empty B bucket in a study like this deserves suspicion. AICR's checks are written as integration
assertions, not value assertions, so none is green purely because the mock answered an API. The
corresponding limitation, which the bucket scheme does not capture, is that where Mokka's values are
synthetic a check validates the path and stays silent on the values. `accelerator-metrics` is the
clearest case: it requires `DCGM_FI_DEV_GPU_UTIL`, `FB_USED`, `GPU_TEMP` and `POWER_USAGE` to be
present, and Mokka's `FB_USED` is a constant zero. The exporter-to-NVML path is genuinely exercised;
the telemetry values are not checked by anyone.

Full catalog: [`results/2026-07-25-gb200-kind/coverage.md`](results/2026-07-25-gb200-kind/coverage.md).
Machine-readable: [`report.json`](results/2026-07-25-gb200-kind/report.json).

### What actually ran

| Check | Phase | Bucket | Outcome | Note |
|---|---|---|---|---|
| `operator-health` | deployment | A | pass | real operator reconciled against the mock |
| `gpu-operator-version` | deployment | A | pass | constraint `>= v25.10.0` satisfied |
| `check-nvidia-smi` | deployment | A | **pass** | device plugin, CDI, toolkit, `libnvidia-ml.so.1` resolution |
| `expected-resources` | deployment | A | fail | missing `agentgateway` Deployment (prerequisite absent) |
| `gpu-operator-health` | conformance | A | pass | ClusterPolicy `ready`, see caveat below |
| `accelerator-metrics` | conformance | A | pass | real dcgm-exporter against mock NVML |
| `dra-support` | conformance | G | fail | see gap #498 |
| `ai-service-metrics` | conformance | A | fail | no Prometheus in `monitoring` (prerequisite absent) |
| `platform-health` | conformance | A | fail | 8 namespaces absent (prerequisites) |

Three of the four failures are missing prerequisite components rather than simulation limits: this
harness deployed the GPU Operator and the DRA driver, while the recipe declares 14 components. That is
harness scoping, and it is recorded as such rather than counted against Mokka.

Evidence that these checks read live state rather than passing trivially: installing the DRA driver
mid-run moved `platform-health` from 9 missing namespaces to 8.

**Caveat on `gpu-operator-health`.** ClusterPolicy reaching `ready` is a weaker statement under Mokka
than on silicon. Two sub-validations are deliberately neutered: cuda-validation runs with
`WITH_WORKLOAD=false` because kernel launch is a no-op under the mock, and the toolkit stage is
short-circuited by a pre-touched `/run/nvidia/validations/toolkit-ready` marker. This row should not be
read as validating the driver or the toolkit.

## 2. The strongest result is a defect, not a pass

`check-nvidia-smi` passing is the cleanest demonstration of the value proposition. It schedules a pod
requesting `nvidia.com/gpu` and runs `nvidia-smi` inside it, which exercises device-plugin allocation,
CDI injection, container-toolkit wiring, and `dlopen` resolution of `libnvidia-ml.so.1`. That is
precisely the chain that broke in NVIDIA/k8s-test-infra#455, where three missing `//export` symbols
made `RTLD_NOW` abort while build, unit tests, and lint all stayed green. It proves the plumbing
resolves. It does not prove the GPU computes anything.

`dra-support` failing is more informative still, and it took two passes to characterise correctly.

On this cluster the real `nvidia-dra-driver-gpu` chart installs and publishes all 8 GB200 devices as
DRA ResourceSlices off the mock NVML. The check still fails, because it also requires a
`nvidia-dra-driver-gpu-controller` Deployment that the chart templates only when
`resources.computeDomains.enabled=true`. Enabling that makes the kubelet plugin's `compute-domains`
container CrashLoopBackOff:

```
error getting nvcap for IMEX channel '0': error getting device major:
error parsing '/proc/devices': unexpected regex match: []
```

**The first conclusion drawn from this was wrong**, and it is worth recording because the correction
changes the recommendation. The initial reading was that Mokka lacks an `nvidia-caps-imex-channels`
device class and that closing it meant inventing a way to fake `/proc/devices`, sized M.

In fact the DRA driver already solves this. Its chart exposes an `altProcDevices` value whose
documented example path is literally `/var/lib/nvml-mock/imex/proc-devices`, mounting an alternate
file and passing `ALT_PROC_DEVICES_PATH`. Its CI (`hack/ci/mock-nvml/setup-mock-gpu.sh`, step 7b)
builds the whole surface against `nvml-mock`: the proc-devices file with `235 nvidia-caps-imex-channels`
appended, a `fabric-imex-mgmt` capability file, and 2048 channel device nodes.

So the real gap is **packaging, not capability**, and it is smaller: `nvml-mock` does not ship that
surface, and the released NGC chart v25.12.0 that this repo's e2e uses has no `altProcDevices` key.
Both halves exist; neither is reachable from the `nvml-mock` chart.

Attempting the obvious workaround confirmed why the indirection exists. Building the surface by hand
and mounting it over `/proc/devices` in the container is rejected by runc:

```
error mounting "/var/lib/nvml-mock/imex/proc-devices" to rootfs at "/proc/devices":
... cannot be mounted because it is inside /proc
```

The bucket does not change: a user of the `nvml-mock` chart alone still cannot run this check, so it
stays G. The size drops to S and the fix is adaptation rather than invention. Tracked as #498.

## 3. Back-test against real failures

**Primary back-test: pending real failure data.** No captured DSX-OS GB200 NVL72 bring-up failure set
exists in any searchable location. DSX-OS configs live on internal GitLab, so the artifacts are not
reachable from the repositories searched. Tracking issue: #501. Leads: Andre Prado and the DSX Sw
Bringup org, the DGXC Capacity Bring-up thread, and Aleksei Vasilevskii's outstanding runbook request.
No failure set was synthesized to fill the gap.

**Secondary, explicitly relabelled proxy.** Against real hardware-encountered failures from **cloud**
GB200 (`p6e-gb200.36xlarge`, EKS/OKE) recorded in NVIDIA/aicr issues. This is a different platform and
a different context from an NVL72 rack bring-up, and it needs Mark's agreement before it carries any
weight.

| # | Real failure | Caught pre-silicon? | By which check | Note |
|---|---|---|---|---|
| aicr#859 | bundler emits `manifestFiles` as `-post`, breaking prereq ConfigMaps, GB200/EKS deploy hangs | **caught** | `operator-health`, `expected-resources` | ordering bug, needs no GPU; this run reproduced the shape (prereq absent, check failed) |
| aicr#1255 | EKS GB200 recipe K8s floor (>= 1.32.4) below the DRA `resource.k8s.io/v1` requirement (1.34+) | **caught** | readiness version constraint, `dra-support` | pure control-plane version skew |
| aicr#1553 | `tools/cleanup` leaves `nvidia_uvm` wedged on driver-managed nodes | **missed** | none | kernel module state; Mokka runs `driver.enabled=false` and has no kernel module. Bucket C, permanently |
| aicr#1861 | long GB200 validate phase outlives the AWS token, K8s calls 401 | **missed** | none | cloud IAM credential lifetime; out of simulation scope, not a Mokka gap |

**Caught 2 of 4.** Both misses are explained by category rather than by a fixable gap: one is
driver and kernel state that is inherently hardware-dependent, one is cloud credential lifetime that
simulation has no view of. Neither would be closed by any item in the gap backlog.

**Four caveats, all material:**

1. Cloud GB200, not a DSX-OS NVL72 rack bring-up. Different topology, different failure modes.
2. n=4. Far too small to generalize, and not a random sample.
3. Two of the four (#859, #1255) are failures of AICR's own bundler and recipe metadata, caught by
   AICR's own validators. That is partly circular and should be discounted accordingly.
4. This proxy was run over an explicit objection. A devil's-advocate review recommended against running
   any proxy back-test, on the grounds that numbers from non-NVL72 hardware invite exactly the
   conflation the guardrail exists to prevent. The objection is recorded here because Mark may share
   it; the mitigation is that the primary back-test remains openly pending and this table is never
   quoted as the bring-up result.

## 4. Bring-up-time read

Not quantified, and this POC cannot honestly quantify it. Estimating hours saved needs a real bring-up
timeline with per-failure cost attached, which is exactly the missing data in section 3.

What can be said: the failure classes that move left are prerequisite ordering, missing components,
version and API skew, resource advertisement, and driver-userspace resolution. The classes that do not
move are CUDA execution, firmware and driver compatibility, NCCL / NVLink / NVLS and fabric, and
TTFT and throughput.

- Failures moved left: demonstrated for 2 of 4 proxy records, both control-plane class.
- Rough time saved per bring-up: **not established**.
- Confidence: **low**, because the bring-up timeline data does not exist yet.

## 5. Gap backlog

Summary issue: **#502**.

| Gap | Issue | Blocks | Coverage unlocked | Size | Status |
|---|---|---|---|---:|---|
| nvml-mock does not ship the mock IMEX surface the DRA compute-domain plugin needs | [#498](https://github.com/NVIDIA/k8s-test-infra/issues/498) | `dra-support`, `slinky-slurm-imex-channel` | 2 checks, G to A | S | open |
| No simulated node provisioner | [#499](https://github.com/NVIDIA/k8s-test-infra/issues/499) | `cluster-autoscaling` | 1 check, G to A | M | open |
| GFD e2e assertion is warning-only and can never fail | [#500](https://github.com/NVIDIA/k8s-test-infra/issues/500) | confidence in the resource-advertisement surface | test quality | S | **closed in this PR** |

- **Closed during this POC:** 1 gap (#500). It did not move a bucket, because it was a defect in our
  own e2e rather than in Mokka's capability. It is reported as a test-quality fix, not as coverage.
- **Highest-leverage remaining:** #498. It unlocks 2 checks, is the only gap on the GB200 critical
  path since ComputeDomains is how IMEX is expressed in DRA, and is now sized S because the DRA
  driver already ships the consumption mechanism and the setup shell already exists in its CI.
- **Deliberately not worth closing:** MIG. Mokka's MIG coverage is zero (every GpuInstance and
  ComputeInstance call is a stub), but **no check in the AICR catalog requires MIG**, so closing it
  would unlock no coverage. Filed nowhere on purpose, recorded here so nobody refiles it as a blocker.

## 6. Limits

- **Fidelity at scale is unproven and is not claimed.** The mock replaces `libnvidia-ml.so.1` on the
  host and needs a real kubelet, so KWOK nodes cannot run it. This POC ran on **one node**. It says
  nothing whatsoever about GPU-stack fidelity at KWOK-scale node counts. #499 would compose the two
  axes for the autoscaling check specifically, and even that would prove only the control loop, not
  node-level fidelity.
- **12 of 21 checks did not run.** Their buckets are analysis, not observation.
- **Single architecture and single profile.** linux/arm64, `gb200`, 1 node, 8 simulated GPUs.
- **Back-test data status:** pending real DSX-OS NVL72 failure history (#501).

## 7. Claims this supports (and does not)

| Claim | Supported? | Basis |
|---|---|---|
| Pre-silicon **integration preflight** for the A-bucket checks | **Yes, scoped** | 5 checks passed and 4 failed for real, diagnosable reasons on a simulated cluster; `check-nvidia-smi` exercised the full allocation-to-NVML chain |
| **Reduces** time to first token | **Directionally, not quantified** | 2 of 4 proxy failures were control-plane class and would surface pre-silicon; no timeline data exists to attach hours to that |
| **Hit** first token on day one | **No** | simulation cannot prove it; the entire hardware-dependent path is untested by construction |
| AICR-on-silicon remains the final readiness gate | **Yes** | bucket C is non-empty by construction: NCCL, NVLS, firmware and driver, and TTFT are all unrunnable here |
| Deep GPU-stack fidelity at KWOK scale | **No** | not tested; the mock cannot run on KWOK nodes at all |
| Mokka validates CUDA execution | **No** | 1 CUDA driver-API symbol (`cuInit`); the operator's cuda-validation runs `WITH_WORKLOAD=false` because kernel launch is a no-op |

## 8. Recommendation

**Go with scope**, on a narrower claim than the original pitch.

The defensible product statement is: *Mokka + AICR is a pre-silicon integration preflight covering
operator install, DRA and resource advertisement, NVML-facing components, and control-plane behaviour.
AICR validation on real silicon remains the final readiness gate.* Roughly 43% of the AICR suite is
what Mokka specifically unlocks today, rising toward 57% if #498 and #499 close.

Three things would change the answer materially, in priority order:

1. **Real DSX-OS NVL72 bring-up failure history** (#501). Without it there is no defensible
   time-saved number, and the bring-up-time claim stays qualitative. This is the single highest-value
   unblock and it needs a human, not more engineering.
2. **Close #498.** It is the only gap on the GB200 critical path and it unlocks the DRA check that
   most directly supports the "GPU Operator + DRA installation moves earlier" claim.
3. **Run the full 14-component recipe.** 12 of 21 checks did not execute here. A larger cluster would
   convert analysis into observation, which is what makes the coverage number defensible rather than
   argued.

## Appendix

- Harness and buckets: [`README.md`](README.md)
- Catalog with per-check rationale and source evidence: [`catalog.yaml`](catalog.yaml)
- Raw run: [`results/2026-07-25-gb200-kind/`](results/2026-07-25-gb200-kind/)
