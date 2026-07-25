# AICR pre-silicon preflight harness

Runs the [AICR](https://github.com/NVIDIA/aicr) `validate` suite against an
`nvml-mock` (Mokka Sim) cluster and classifies what each check actually proves
when there is no GPU present.

This exists to answer one question, posed by the AICR maintainer on 2026-07-24:

> Does Mokka + AICR materially reduce GB200 bring-up time, or does it merely
> prove the stack deploys against mocked APIs?

An honest negative answer is a successful run. Nothing here is tuned toward a
favourable number.

## What this is not

This is a **pre-silicon integration preflight**, not a readiness gate. AICR
validation on real silicon remains the final gate. Preflight cannot judge CUDA
execution, firmware and driver compatibility, NCCL / NVLink / NVLS and the
network fabric, or workload TTFT and throughput. Those determine time to first
token. Preflight reduces the time spent on integration failures before you get
there; it does not prove you will hit first token on day one.

## Buckets

| Bucket | Meaning | Counts as coverage? |
|---|---|---|
| **A** | Exercises real integration. Could plausibly fail for a real reason. | Yes. This is the movable-left set. |
| **B** | Green only because the mock answers the API. Would pass regardless of correctness. | No. Recorded honestly because it bounds the claim. |
| **C** | Hardware-dependent. Cannot be judged pre-silicon. | No. These are why AICR-on-silicon stays the final gate. |
| **G** | Would be meaningful pre-silicon, but Mokka lacks the capability. Not hardware-dependent, so closable. | As roadmap only. Requires a tracked issue. |

Two numbers are always reported side by side:

- **A / total**: the share carrying real pre-silicon signal **today**.
- **(A + G) / total**: the share **reachable** once tracked gaps close. Roadmap,
  never a current claim.

A third number keeps the first honest. Some bucket-A checks are GPU-independent
(`gang-scheduling` is explicitly CPU-only upstream, `platform-health` is pure
control-plane). They move left, but any cluster runs them, so Mokka is not what
unlocks them. The report splits bucket A into GPU-dependent and GPU-independent
so the headline cannot be inflated by control-plane checks.

## Design invariants

The harness is built so that an incomplete run degrades to "not run" rather than
to a favourable number:

1. A check the run never reported is recorded `not-run`. It is never promoted to
   a pass. (`TestCheckAbsentFromRunIsNotRunNeverPass`)
2. CTRF `pending` and `other` mean the suite reached no verdict, and map to
   `not-run` rather than being laundered into a pass.
3. A bucket-C check reporting green under `sim` provenance is flagged
   `suspect`, because a hardware-dependent check cannot legitimately pass
   without hardware.
4. Every emitted record carries `provenance`, `sim` or `silicon`. They are never
   conflated.
5. A bucket-G row without a tracked gap issue fails catalog validation. An
   untracked gap is not a roadmap.
6. A check present in the run but absent from the catalog is reported as
   catalog drift rather than silently dropped.

## Prerequisites

- `docker`, `kind`, `kubectl`, `helm`, and a Go toolchain matching the project.
- The `aicr` CLI, built from [NVIDIA/aicr](https://github.com/NVIDIA/aicr):
  `go build -o aicr ./cmd/aicr`.
- **Real nodes.** The mock replaces `libnvidia-ml.so.1` on the host and needs a
  real kubelet and container runtime. KWOK nodes cannot host it. This harness
  therefore says nothing about GPU-stack fidelity at KWOK-scale node counts.
- Roughly 8 GiB of container memory for the cluster used here. The full 14
  component recipe stack needs considerably more.

## Running it

### 1. Stand up a Mokka cluster with the GPU stack

The GPU Operator scenario in `tests/e2e/go` already does this, and is the
supported path:

```bash
make e2e-gpu-operator E2E_PROFILES=gb200 E2E_KEEP_CLUSTER=true
```

That builds the mock image, creates the `nvml-mock-op` Kind cluster, installs
the NVIDIA Container Toolkit, configures containerd for CDI, installs the
`nvml-mock` chart with the `gb200` profile, and installs the real GPU Operator.

Add the DRA driver so `dra-support` gets a fair run:

```bash
helm upgrade nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu --install --namespace nvidia-dra-driver --create-namespace --set gpuResourcesEnabledOverride=true --set nvidiaDriverRoot=/var/lib/nvml-mock/driver --set resources.computeDomains.enabled=false --wait
```

### 2. Generate a recipe and run AICR

```bash
aicr recipe --service kind --accelerator gb200 --intent inference -o recipe-gb200-kind.yaml
```

```bash
aicr validate --recipe recipe-gb200-kind.yaml --phase deployment --phase conformance --namespace aicr-validation --fail-on-error=false --output ctrf/validate.json
```

`--fail-on-error=false` matters: the point is to record every outcome, not to
stop at the first failure.

### 3. Classify

```bash
go run ./tests/aicr-preflight -ctrf ctrf -markdown coverage.md -json report.json -cluster kind-nvml-mock-op -profile gb200
```

Omitting `-ctrf` is legal and prints the catalog with every outcome recorded
`not-run`. That is the correct output when no run happened.

## Interpreting the results

A failing check is not automatically a Mokka gap. Separate three causes before
drawing any conclusion:

1. **Missing prerequisite component.** The recipe declares 14 components; a
   cluster running only the GPU Operator will fail `platform-health` and
   `ai-service-metrics` on absent namespaces. That is harness scoping, not a
   simulation limit.
2. **Version or contract skew** between AICR and the component it inspects.
   These are real integration findings and exactly what preflight is for.
3. **A genuine Mokka capability gap.** Only these are bucket G, and each needs a
   tracked issue.

Bucket B is scope information, not failure. Do not reclassify B as A to improve
the headline.

## Files

| File | Purpose |
|---|---|
| `catalog.yaml` | The analytical judgment: one row per AICR check, with bucket, rationale, and source evidence. Reviewable independently of any run. |
| `classify.go` | Buckets, outcomes, provenance, the join, and the roll-up. |
| `ctrf.go` | CTRF report parsing and catalog loading. |
| `report.go` | JSON and markdown emission. |
| `main.go` | CLI. |

The catalog carries the judgment; the harness carries the observation. The
harness never edits a bucket.
