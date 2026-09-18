# KWOK + Mokka: combining them silently turns the AICR suite into a rubber stamp

> **First run, 2026-08-03. This finding is unaffected by the 2026-08-26 re-run**,
> which did not repeat the KWOK composition. The `14%` and `76%` figures quoted
> below are the first run's; see [FINDINGS-57ef016.md](FINDINGS-57ef016.md) for
> the current ones.

**Run 2026-08-03, cluster `mokka-hybrid`.** Raw evidence:
[`results/kwok-false-pass-evidence.log`](results/kwok-false-pass-evidence.log).

## What we set out to test

Whether combining KWOK (horizontal, node-count breadth) with Mokka (vertical, per-node GPU-stack
depth) gives this study a scale story. That is the open question Mark named on 2026-07-24: the mock
backend needs real nodes, KWOK nodes stay the fake backend, so fidelity at KWOK scale is a claim to
prove rather than assert.

**The prediction was wrong, and the recommendation panel's objection was wrong in the same direction.**
Both expected the result to be a tautology: hollow nodes cannot run `nvidia-smi`, so fleet-surveying
checks would obviously go red. The actual result is the opposite, and it is worse.

## What happened

**AICR dispatched the same 9 checks in both runs.** Adding KWOK nodes did not change which checks were
selected. It changed their verdicts.

| | pure Mokka (baseline) | + 250 KWOK nodes |
|---|---|---|
| checks dispatched | 9 of 21 | 9 of 21 (identical set) |
| deployment phase | 9m 0s, 2 pass / 1 fail / 1 inconclusive | **4.2s, 4 pass / 0 fail** |
| conformance phase | 5 selected, 1 pass / 4 fail | **5.2s, 5 pass / 0 fail** |
| overall | 3 pass / 5 fail / 1 inconclusive | **9 pass / 0 fail** |

**Every one of the 9 dispatched checks reported PASS. Nothing ran.** The other 12 stayed undispatched
in both runs, so this is not a coverage increase; it is the same 9 verdicts, fabricated.

The fleet was 253 nodes: 1 control plane, 2 real Mokka workers, 250 hollow KWOK nodes, advertising
2016 `nvidia.com/gpu` between them.

## Why

Four facts compose into a false pass. Each was verified on the live cluster, not inferred.

1. **AICR validator Job pods tolerate every taint.** Observed on the live pod: a single toleration with
   empty key and `operator: Exists`. Confirmed in source at
   `pkg/validator/job/deployer_test.go:516`, which asserts exactly that default. This is correct
   design on a real cluster: validators must be able to land on tainted GPU nodes.

2. **KWOK nodes look like GPU nodes.** They carry `nvidia.com/gpu.present`, `gpu.product`, `gpu.count`,
   the CUDA driver labels, and real `nvidia.com/gpu` capacity. Nothing above the API server can tell
   them from a Mokka node by labels alone.

3. **So the scheduler puts validators on them.** 250 hollow nodes against 2 real ones. The
   `kwok.x-k8s.io/node=fake:NoSchedule` taint does not help, because of fact 1. Observed placement,
   with the taint applied:

   ```
   aicr-check-nvidia-smi-...      kwok-gpu-0158   Succeeded
   aicr-expected-resources-...    kwok-gpu-0169   Succeeded
   aicr-gpu-operator-version-...  kwok-gpu-0037   Succeeded
   aicr-operator-health-...       kwok-gpu-0048   Succeeded
   ```

4. **KWOK then manufactures the success.** Its `pod-complete` stage matches any pod whose
   `ownerReferences[].kind` is `Job` and whose phase is `Running`, and transitions it to `Succeeded`.
   Every AICR validator is a Job.

The container status is the proof that nothing executed:

```json
{"image":"ghcr.io/nvidia/aicr-validators/deployment:latest","imageID":"","started":false,
 "state":{"terminated":{"exitCode":0,"reason":"Completed",
 "startedAt":"2026-08-03T19:11:33Z","finishedAt":"2026-08-03T19:11:33Z"}}}
```

`imageID` is empty, so the image was never pulled. `started` is false. `startedAt` equals `finishedAt`.
And `kubectl logs` on that pod returns `MethodNotAllowed`, because there is no kubelet to serve logs.

Control experiment, to rule out any other explanation: a pod running `sh -c 'exit 7'` reports
`phase=Failed, exitCode=7` on a real node, and `Succeeded` on a KWOK node.

## The obvious mitigation does not work

`aicr validate --node-selector` reads as the fix. It is not. Measured:

```
aicr-validate-6qxk2 (snapshot agent)      nodeSelector={"aicr.run/real-node":"true"}
aicr-check-nvidia-smi-2bb73994-d9hc4      nodeSelector=NONE   -> ran on kwok-gpu-0213
aicr-expected-resources-345bc8fa-fqprd    nodeSelector=NONE   -> ran on kwok-gpu-0224
aicr-gpu-operator-version-32a497d6-fh5j2  nodeSelector=NONE   -> ran on kwok-gpu-0092
aicr-operator-health-72210902-5jxcg       nodeSelector=NONE   -> ran on kwok-gpu-0103
```

The flag reaches the snapshot agent's pod spec and stops there. `pkg/validator/job/deployer.go:66`
states the intent plainly:

> `// NodeSelector is passed through to inner workloads via AICR_NODE_SELECTOR.`

It is delivered to the validator container as an environment variable, so a validator can apply it to
workloads *it* creates (the NCCL benchmark pod, the `nvidia-smi` probe pod). It is never applied to
the validator Job's own pod spec. With the flag set exactly as documented, the run still reported
4 of 4 passes in 4.2 seconds, with every validator on a hollow node.

## Whose problem this is

**Not Mokka's.** Mokka's nodes behaved correctly throughout; the pure-Mokka baseline on the identical
recipe produced honest, mixed results.

**Not KWOK's.** `pod-complete` is doing exactly what it is documented to do, and AICR's own KWOK lane
depends on that behaviour for its scheduling tests.

**It is a composition hazard, and the actionable half sits in AICR.** The validator Job pod spec has a
universal toleration and no node selector of its own, so it cannot be constrained by the operator
running the suite. On any cluster containing hollow nodes that advertise GPUs, AICR reports green
without executing.

## Why this matters beyond a lab curiosity

This is the exact architecture Eliran's `fake-gpu-operator` per-nodepool `backend` design produces:
KWOK nodes on the `fake` backend for cheap breadth, real nodes on the `mock` backend for fidelity, in
one cluster. Anyone who builds that fleet and then runs AICR against it gets a clean green
conformance report that means nothing, with no warning and no error.

The failure mode is silent, fast and flattering, which is the worst combination. A run that takes
4 seconds instead of 9 minutes and passes everything looks like good news.

## What would fix it

In rough order of how much it helps:

1. **AICR: apply `--node-selector` to the validator Job pod spec**, not only to inner workloads. That
   makes the documented mitigation actually work and is the smallest change.
2. **AICR: refuse to report a pass for a check whose pod never pulled its image.** An empty `imageID`
   with `started: false` is a reliable signal that nothing executed, and it is available in the pod
   status AICR already reads. This is a fail-closed guard that does not depend on the operator
   configuring anything correctly.
3. **AICR: detect the `kwok.x-k8s.io/node` annotation** and skip or fail, the way
   `slinky_slurm_health_check.go:346-353` already does for its own NodeSets. That precedent exists in
   the codebase; it is simply not applied to the Job deployer.
4. **Fleet side:** keep hollow nodes out of any namespace or node pool AICR targets, and do not let
   them advertise `nvidia.com/gpu` unless something can actually serve it.

**None of these is a Mokka change.** We are not filing them in `NVIDIA/aicr` without Mark's sign-off.

## What this does and does not add to the study

**Does not:** it is not AICR coverage at scale, not a bring-up-time number, and not evidence of
GPU-stack fidelity at KWOK node counts. Adding KWOK nodes cannot move the measured 14% or the 76%
ceiling, because the checks structurally cannot execute on them.

**Does:** it answers Mark's July open question with a measurement instead of an assertion. Fidelity
and scale compose *across* a fleet, not *within* a node, and combining them naively does not merely
fail to help, it actively destroys the trustworthiness of the result.

**Recorded separately from the coverage matrix on purpose.** These runs produced 9 passes that are all
false. They are deliberately excluded from `cells.yaml` and from the rollup, because feeding them into
the denominator would raise the headline coverage number using results we know are fabricated. The
raw CTRF is kept under `results/hybrid-kwok-*/` for audit.

## Control-plane observations, incidentally

Cheap to collect while the fleet was up, and unremarkable, which is itself worth knowing:

| Measure | 3 nodes | 253 nodes |
|---|---|---|
| `kubectl get nodes` wall clock | 0.06s | 0.30s |
| Docker VM memory used | 1961 MiB | 2983 MiB |

250 hollow nodes cost about 1 GiB and a 5x increase on a trivially small API call. No controller
churn, no OOM, nothing degraded. That is a genuine if narrow data point for the horizontal axis, and
it says nothing about the vertical one.

## Provenance note

The recommendation panel dissented against running this experiment, arguing it would document the
obvious, and I agreed with that dissent before Carlos overrode it. Both of us were wrong about what
the experiment would show. The dissent is recorded in
[`DECISIONS.md`](DECISIONS.md) D-015 rather than quietly dropped now that the result is interesting.
