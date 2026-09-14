# Mokka + AICR compatibility sweep

> **There are two runs.** This README describes the harness and the first run
> (Mokka `v0.3.0`, 2026-08-03). A second run against `upstream/main` `57ef01659`
> on 2026-08-26 is written up in [FINDINGS-57ef016.md](FINDINGS-57ef016.md), and
> pairs with [cells-57ef016.yaml](cells-57ef016.yaml) and
> [results-57ef016/](results-57ef016/). Both runs regenerate byte-identically and
> neither overwrites the other. Any figure below is the first run's.

Runs the [AICR](https://github.com/NVIDIA/aicr) recipe and validation suite against an
`nvml-mock` (Mokka) cluster on kind, and classifies what each check actually proves when no GPU is
present.

This exists to answer one question, put by the AICR maintainer on 2026-07-24:

> Does Mokka + AICR materially reduce GB200 bring-up time, or does it merely prove the stack deploys
> against mocked APIs?

An honest negative answer is a successful run. Nothing here is tuned toward a favourable number.

## What this is not

This is a **pre-silicon integration preflight**, not a readiness gate. AICR validation on real silicon
remains the final gate. Preflight cannot judge CUDA execution, firmware and driver compatibility,
NCCL / NVLink / NVLS and the network fabric, or workload time-to-first-token. Those determine first
token. Preflight reduces the integration failures you carry into silicon; it does not prove you will
hit first token on day one.

**This sweep runs on kind, so it proves nothing about scale.** Every number here comes from a
handful of containers on one laptop. Fidelity at KWOK-scale node counts is unproven and is not
claimed.

## Buckets and causes

Two independent axes. Keeping them separate is the whole point.

**Bucket** says what a check would prove. It is an analytical judgment from AICR source, recorded in
[`catalog.yaml`](catalog.yaml), and the harness never edits it to match a result.

| Bucket | Meaning | Counts as coverage? |
|---|---|---|
| **A** | Exercises real integration. Could plausibly fail for a real reason. | Yes. This is the movable-left set. |
| **B** | Green only because the mock answers the API. Would pass regardless of correctness. | No. Recorded honestly because it bounds the claim. |
| **C** | Hardware-dependent. Cannot be judged pre-silicon. | No. These are why AICR-on-silicon stays the final gate. |
| **G** | Would be meaningful pre-silicon, but Mokka lacks the capability. Closable. | As roadmap only. Requires a tracked issue. |

**Cause** says what stopped a particular run.

| Cause | Meaning | Whose problem |
|---|---|---|
| **K** | kind or single-host artifact | nobody's. Would not occur on a real cluster. |
| **U** | upstream dependency has not released what is needed | the dependency's |
| **X** | AICR catalog has no recipe for the combination | AICR's |
| **BUDGET** | host memory exhausted, cell not attempted | this harness's |

A failure is **G** only if Mokka promised to render the path or speak the protocol. If the failing
read targets real host kernel state that Mokka never claimed (NUMA nodes, the real PCI tree, the
NVLink data plane, RDMA verbs), it is **K**. That rule was fixed before any cell ran, so it cannot be
bent to fit a number.

## Two numbers, always together

- **A / total**: the share carrying real pre-silicon signal **today**.
- **(A + G) / total**: the share **reachable** once tracked gaps close. Roadmap, never a current claim.

A third number keeps the first honest. Some bucket-A checks are GPU-independent: `gang-scheduling` is
explicitly CPU-only upstream, `platform-health` is pure control plane. They move left, but any
cluster runs them, so Mokka is not what unlocks them. The report splits bucket A into GPU-dependent
and GPU-independent so the headline cannot be inflated by control-plane checks.

## Design invariants

The harness is built so an incomplete run degrades to "not run" rather than to a favourable number.
Each invariant has a test that goes red if it regresses.

1. A check the run never reported is `not-run`. It is never promoted to a pass.
   (`TestCheckAbsentFromRunIsNotRunNeverPass`)
2. CTRF `pending` and `other` mean the suite reached no verdict, and map to `not-run` rather than
   being laundered into a pass or a fail. (`TestPendingAndOtherBecomeNotRun`)
3. A bucket-C check reporting green under `sim` provenance is flagged `suspect`, because a
   hardware-dependent check cannot legitimately pass without hardware.
   (`TestBucketCPassUnderSimIsFlaggedSuspect`)
4. Every emitted record carries `provenance`, `sim` or `silicon`. They are never conflated.
5. A bucket-G row without a tracked gap issue fails catalog validation. An untracked gap is not a
   roadmap. (`TestCatalogRejectsBucketGWithoutIssue`)
6. A check present in the run but absent from the catalog is reported as catalog drift rather than
   silently dropped. (`TestUnknownCheckInRunIsReportedAsDrift`)
7. Blocked and not-run results stay in the rollup denominator.
   (`TestRollupDenominatorIncludesBlockedAndNotRun`)

Invariant 1 was mutation-checked: flipping `OutcomeNotRun` to `OutcomePass` in `Classify` turns two
tests red.

## Prerequisites

- `docker`, `kind`, `kubectl`, `helm`, and a Go toolchain matching the project.
- The `aicr` CLI, built from [NVIDIA/aicr](https://github.com/NVIDIA/aicr) at the pinned ref:
  ```bash
  git -C <aicr-checkout> archive upstream/main | tar -x -C /tmp/aicr-pinned
  cd /tmp/aicr-pinned && CGO_ENABLED=0 go build -mod=vendor -o /tmp/bin/aicr ./cmd/aicr
  ```
- **Real nodes.** The mock replaces `libnvidia-ml.so.1` on the host and needs a real kubelet and
  container runtime. KWOK nodes cannot host it. This harness therefore says nothing about GPU-stack
  fidelity at KWOK-scale node counts.
- **Memory.** The Docker VM used for this sweep held 7.75 GiB, which fits one cluster shape at a
  time. A fully loaded worker costs 550 to 700 MiB, of which dcgm-exporter is over half. Budget
  12 to 16 GiB to run the matrix without blocking cells.

## Resource cost of a full sweep

| Shape | Nodes | Carries | Idle | Loaded (est.) |
|---|---|---|---|---|
| `stack` | 1 control plane + 2 workers | GPU Operator, device plugin, GFD, dcgm-exporter, DRA | ~920 MiB | ~2.0 GiB |
| `fabric` | 1 control plane + 4 workers | NRI injection, IMEX channels, ComputeDomain topology | ~1150 MiB | ~1.4 GiB |

The two shapes are created and destroyed in sequence and never coexist. Merging them into one
5-worker loaded cluster does not fit in 7.75 GiB and will OOM mid-sweep, which produces a fake
failure that reads like a Mokka finding.

## Running it

### 1. Create the cluster and install Mokka v0.3.0

```bash
kind create cluster --name mokka-aicr-stack --config tests/aicr-sweep/assets/kind-stack.yaml --wait 300s
```

```bash
docker save --platform linux/arm64 ghcr.io/nvidia/nvml-mock:0.3.0 -o /tmp/nvml-mock-030.tar && kind load image-archive /tmp/nvml-mock-030.tar --name mokka-aicr-stack
```

The two-step save-then-load matters: `kind load docker-image` cannot load a multi-arch image out of
Docker Desktop's containerd store.

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock --version 0.3.0 --kube-context kind-mokka-aicr-stack -n mokka --create-namespace --set image.repository=ghcr.io/nvidia/nvml-mock --set image.tag=0.3.0 --set gpu.profile=gb200 --set nri.enabled=true --set nri.deviceInjectionMode=raw --wait --timeout 6m
```

`-n mokka` is load-bearing. Install without it and the NRI plugin renders
`--excluded-namespaces=default,kube-system`, so it silently skips every pod a first-time user runs,
while the DaemonSet still reports Ready.

`image.tag` defaults to `latest`, not to the chart version. Pin it or the run is not reproducible.

### 2. Install the GPU Operator

```bash
helm install gpu-operator nvidia/gpu-operator --version v25.3.4 --kube-context kind-mokka-aicr-stack -n gpu-operator --create-namespace -f tests/aicr-sweep/assets/gpu-operator-values.yaml --set operator.runtimeClass=runc --timeout 12m
```

`operator.runtimeClass=runc` is required on a stock kind node. The operator otherwise creates a
RuntimeClass named `nvidia` and sets `runtimeClassName: nvidia` on every operand, and stock kind
containerd has only the `runc` and `test-handler` runtimes. Without this every operand fails sandbox
creation with `no runtime for "nvidia" is configured`. See DECISIONS.md D-013.

### 3. Generate the recipe and run AICR

```bash
aicr recipe --service kind --accelerator gb200 --intent inference -o recipe.yaml
```

```bash
aicr validate --recipe recipe.yaml --phase deployment --phase conformance --phase performance --namespace aicr-validation --fail-on-error=false --timeout 12m --output ctrf/validate.json
```

`--fail-on-error=false` matters: the point is to record every outcome, not to stop at the first
failure. The exit code collapses 21 checks into one number, so read the CTRF report, never the exit
code.

### 4. Classify

```bash
go run ./tests/aicr-sweep -catalog tests/aicr-sweep/catalog.yaml -cells tests/aicr-sweep/cells.yaml -results tests/aicr-sweep/results -json report.json -markdown coverage.md
```

Omitting `-results` is legal and prints the catalog with every outcome recorded `not-run`. That is
the correct output when no run happened.

## Reading the results

A failing check is not automatically a Mokka gap. Separate the causes before filing anything:

1. **Did Mokka promise this?** If the failing read targets `/var/lib/nvml-mock/**`, `MOCK_IB_ROOT`,
   an injected `/dev/nvidia*`, the topology ConfigMap, or a protocol Mokka speaks for real, it is a
   candidate **G**. Otherwise it is **K**.
2. **Is the dependency released?** ComputeDomain needs a DRA driver flag that exists only on main.
   That is **U**.
3. **Does AICR have the recipe at all?** `gb300` does not exist in AICR's catalog. That is **X**.
4. **Is it version skew?** CI pins kind v0.31.0; this sweep ran v0.32.0 with Kubernetes v1.36.1.
   Re-test any DRA or operator failure unique to the sweep before calling it **G**.

## Layout

```text
tests/aicr-sweep/
  DECISIONS.md      every judgment call made instead of asking, with rationale
  catalog.yaml      analytical A/B/C/G bucket per AICR check, with source evidence
  cells.yaml        the permutation matrix, including cells that did not run
  types.go          bucket, cause, outcome and record types
  classify.go       the join, the invariants and the rollup
  ctrf.go           CTRF report and catalog loading
  report.go         JSON and markdown rendering
  main.go           the CLI
  assets/           pinned kind configs and the GPU Operator values overlay
  results/          per-cell CTRF output and logs
```
