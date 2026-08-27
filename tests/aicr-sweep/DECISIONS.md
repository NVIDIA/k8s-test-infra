# Decisions log: Mokka + AICR compatibility sweep

> **First run, 2026-08-03.** The 2026-08-26 re-run
> ([FINDINGS-57ef016.md](FINDINGS-57ef016.md)) revisited two of these decisions
> with fresh measurements:
>
> - **D-009** blocked the ComputeDomain cells as cause `U` because
>   `altProcDevices` was in no DRA driver release. **v0.5.0 shipped it on
>   2026-08-19**, and `dra-support` now passes. The cell itself is `X` for a
>   different reason: it declares `intent=training`, which resolves no recipe on
>   `kind`. See [results/dra-altprocdevices-evidence.log](results/dra-altprocdevices-evidence.log).
> - **D-014** classified the GFD failure `K` and named the experiment that would
>   settle it. That reasoning **survives** the re-run, and the experiment is still
>   the one to run. What changed is the diagnosis, not the verdict. See
>   [results/gfd-sysfs-evidence.log](results/gfd-sysfs-evidence.log).

Every judgment call made while running this sweep, with its rationale. The mission brief told me to
decide and keep going instead of asking, so this file is the audit trail for what I decided and why.

Format: decision, the options considered, what I chose, why, and what it costs.

---

## D-001: Branch base is `upstream/main`, not the `v0.3.0` tag

**Options**

1. Branch from the `v0.3.0` tag (`e3546d09`), so the tree matches the pinned artifact exactly.
2. Branch from `upstream/main` (`c8dc7077`).

**Chosen:** option 2.

**Why:** `v0.3.0` is an ancestor of `upstream/main`, and the only commit between them is
`docs(e2e): correct the device plugin's CI status in VERSION-MATRIX (#579)`, which changes no code.
A PR must merge cleanly into `main`, and branching from a tag that is one docs commit behind buys
nothing. The pin that matters is the pin on the deployed artifacts (image `ghcr.io/nvidia/nvml-mock:0.3.0`
and chart `--version 0.3.0`), not the pin on the branch base. The sweep never installs a
locally built image unless a cell explicitly records that it did.

**Cost:** the tree carries one docs commit that the `v0.3.0` artifact does not. No effect on results.

---

## D-002: This PR supersedes PR #503; I did not touch #503

**Context:** PR [#503](https://github.com/NVIDIA/k8s-test-infra/pull/503), "poc: Mokka + AICR
pre-silicon integration preflight", is open, draft, and conflicting. It is the 2026-07-25 attempt at
the same POC, on branch `poc/mokka-aicr-presilicon-preflight`. It carries a working harness at
`tests/aicr-preflight/`, a catalog of 21 AICR checks, a findings note, and diagrams. Two of its
commits already merged to `main` separately: [#541](https://github.com/NVIDIA/k8s-test-infra/pull/541)
(mock IMEX surface) and [#542](https://github.com/NVIDIA/k8s-test-infra/pull/542) (GFD label
assertion). A third, [#512](https://github.com/NVIDIA/k8s-test-infra/pull/512), closed tier-1 gaps
that POC found.

**Options**

1. Push new commits onto `poc/mokka-aicr-presilicon-preflight` and reuse PR #503.
2. Branch from #503's head so the new PR contains its history.
3. Branch from `upstream/main`, write the sweep fresh, and mark #503 superseded.

**Chosen:** option 3.

**Why:** the mission brief names the branch `poc/mokka-aicr-kind-sweep` explicitly, so option 1 is out.
Option 2 drags stale pre-v0.3.0 results into the new PR, and #503 already conflicts with `main`, so it
needs a rebase before it could be a base. The 2026-07-25 results predate v0.3.0 (cut 2026-08-01) and
predate this sweep's scope, which is every recipe across a permutation matrix rather than one recipe.
Reporting them next to fresh v0.3.0 numbers invites a reader to mix the two.

**Cost:** `docs/aicr-preflight/` and the harness overlap between two open PRs. I state the supersede
relationship in the PR body and recommend closing #503. I did not close it: closing a PR is an
outward-facing action, and it needs Carlos's explicit approval.

**Carried forward from #503 by design, not by copy:** the A/B/C/G bucket definitions, the
"degrade to not-run, never to a pass" invariant set, the CTRF result parsing approach, and the
GPU-dependent split inside bucket A. Those were sound and re-deriving them would waste effort.

---

## D-003: Harness lives at `tests/aicr-sweep/`

**Options**

1. `deployments/nvml-mock/examples/aicr-sweep/`, as the mission brief suggests.
2. `tests/aicr-sweep/`.
3. Extend `tests/e2e/go/` with sweep scenarios.

**Chosen:** option 2.

**Why:** the brief says "adjust to fit" the repo's conventions. `deployments/nvml-mock/examples/`
does not exist in this repo, so option 1 invents a directory. Test harnesses in this repo live under
`tests/`: `tests/e2e/`, `tests/mocknvml/`, `tests/poc-validation/`, and the superseded
`tests/aicr-preflight/`. Option 3 is wrong because `tests/e2e/go` is a Ginkgo suite that gates CI,
and a long sweep that deliberately records failures does not belong in a gating suite.

**Cost:** none. `tests/aicr-sweep/` matches the neighbours.

---

## D-004: No brainstorming skill session; the brief is the plan input

**Context:** the standing engineering rule starts every non-trivial task with an interactive
brainstorming skill. The mission brief says "EXECUTE, DO NOT ASK" and "Do NOT come back to me with a
menu of options", and the START-HERE file repeats it.

**Chosen:** skip the interactive brainstorm. Write the plan and this decision log instead, which is
what the brief's Phase 0 asks for.

**Why:** the brainstorming skill works by asking the user questions. Running it would violate an
explicit instruction from the same user who set the standing rule. The brief wins on precedence, and
it asks for the same artifact a brainstorm would produce: a written plan plus recorded decisions.

---

## D-005: The v0.3.0 image must be pulled, not built, wherever a cell claims v0.3.0 provenance

**Why:** the repo's e2e harness builds `nvml-mock:e2e` from the working tree by default. A sweep that
builds from the tree and then labels the result "v0.3.0" is reporting a number that nobody can
reproduce. Every cell records the image reference and its digest. Cells that must use a locally built
image record that fact and are excluded from the v0.3.0 headline.

---

## D-006: Docker VM memory is the binding constraint, and the sweep degrades rather than fabricates

**Context:** the host is arm64 macOS with 14 CPUs and 8.3 GB allocated to the Docker VM. A full
factorial sweep of every AICR recipe across every axis does not fit.

**Chosen:** run the base sweep first and completely, then axis cells in priority order until the
budget runs out. Every cell that does not run is recorded `blocked` with the reason. `blocked` is
never reported as `pass`, and the coverage denominator always includes blocked cells.

**Why:** the brief is explicit that a missing input blocks one cell, not the run, and that "not run"
is never "pass". Reporting a partial matrix honestly is the required behaviour.

---

## D-007: AICR is pinned to `upstream/main` at `0752ea14`, not to the local checkout

**Context:** the local clone at `/Users/eduardoa/src/github/nvidia/aicr` sits on branch `agents-workbench`
at `11f63576` (2026-07-26). Its `upstream/main` is `0752ea148aa1ae849cbef86ec59d0c970a582496`
(2026-07-31). They differ by 88 files and 12172 insertions in exactly the paths this study reads:
`validators/`, `recipes/`, `cmd/`, `tests/chainsaw/`. `validators/deployment/nvidia_smi.go` alone
differs by 164 lines, and that file is the source of `check-nvidia-smi`.

**Chosen:** pin to `upstream/main` at `0752ea14`. Extract that ref into scratch space with
`git archive`, build the CLI there, never `git checkout` inside the AICR clone.

**Why:** a coverage number measured against a stale local branch is not reproducible by anyone else.
`git archive` into scratch makes no commit and writes nothing into the AICR repo, which keeps the
sweep inside its "only write to k8s-test-infra" constraint.

**Verified:** `CGO_ENABLED=0 go build -mod=vendor -o bin/aicr ./cmd/aicr` returns rc=0 from that tree.

---

## D-008: The `gb300` half of the base sweep is blocked by AICR, not by Mokka

**Finding, verified by running the CLI rather than by reading it:**

```
$ aicr recipe --service kind --accelerator gb300 --intent inference
[cli] command failed: error=[INVALID_REQUEST] no recipe provides accelerator 'gb300'
      for criteria(service=kind, accelerator=gb300, intent=inference) exitCode=2
```

The same command succeeds for `gb200` (14 components, 5 overlays) and for `h100` (14 components,
6 overlays). AICR's catalog at `0752ea14` knows seven accelerators: `a100`, `b200`, `gb200`, `h100`,
`h200`, `l40s`, `rtx-pro-6000`. `grep -c gb300 recipes/registry.yaml` returns 0, and `gb300` appears
nowhere under `recipes/`.

**Consequence:** the brief's base sweep says "every AICR recipe x {gb200, gb300}". The `gb300` half
cannot run, because AICR has no GB300 recipe. Mokka ships a `gb300` profile; AICR has no accelerator
to match it to.

**Chosen:** record every `gb300` cell as `blocked`, cause code **`X` (AICR catalog gap)**. It is
deliberately not `G`, because Mokka lacks nothing here, and deliberately not `K`, because nothing about
kind causes it. I added a distinct cause code rather than forcing it into an existing bucket: forcing
it into `G` would put an issue on Mokka's backlog for work Mokka does not owe.

**Not filed:** the brief forbids opening issues in NVIDIA/aicr without asking. This goes to Mark in the
findings note, since the catalog is his side of the boundary.

**Substitution, stated openly:** where the brief wants a second accelerator to contrast against gb200,
the sweep uses `h100`, which AICR supports on `kind` and Mokka also ships. The findings label that a
substitution. It is never presented as gb300 coverage.

---

## D-009: ComputeDomain cells are blocked by an unreleased upstream flag, not by a Mokka gap

Mokka v0.3.0 renders the IMEX surface (`imex.mockChannels.enabled`, channel device nodes, and a
substitute `/proc/devices`). The NVIDIA DRA driver reads the `nvidia-caps-imex-channels` major out of
`/proc/devices` and needs `--set altProcDevices=...` to look elsewhere. That flag is on the DRA
driver's main branch and is in no release, including v25.12.0. The chart's own `values.yaml` states
this at lines 307 to 325.

**Chosen:** cause code **`U` (upstream dependency)**, not `G`. Mokka already ships the capability.
Filing a Mokka gap here would file against the wrong repo.

---

## D-010: Sequential clusters of two shapes, not one large cluster

**Measured, not estimated:** the Docker VM has 7.75 GiB total and showed 4.08 GiB available with the
host's unrelated containers running. An idle 5-node kind cluster costs 1152 MiB (control plane 682 MiB,
each worker about 117 MiB). A fully loaded worker costs an estimated 550 to 700 MiB, of which
dcgm-exporter with its embedded nv-hostengine is 250 to 400 MiB, over half.

**Chosen:** two cluster shapes, created and destroyed in sequence, never coexisting.

| Shape | Nodes | Carries | Rationale |
|---|---|---|---|
| `stack` | 1 control plane + 2 workers | GPU Operator, device plugin, GFD, dcgm-exporter, DRA driver | memory heavy, node light |
| `fabric` | 1 control plane + 4 workers | NRI injection, IMEX channels, ComputeDomain topology, no operator, no dcgm | node heavy, memory light |

**Why:** 4 loaded workers needs about 2.9 GiB and leaves roughly 1.2 GiB, which image pulls, Helm, and
the validator Job all spike into at once. That is an OOM kill in the middle of a sweep, which produces
a fake failure that looks like a Mokka finding. The two shapes never need to coexist: the two-clique
ComputeDomain topology needs 4 workers but does not need the operator or dcgm.

**Not chosen:** raising the Docker Desktop VM memory would remove this constraint entirely and is a
settings change rather than an engineering one. I did not change it, because it mutates the user's
machine configuration outside the throwaway kind cluster.

---

## D-011: Triage rule for separating `G` (Mokka gap) from `K` (kind artifact)

The brief calls conflating these the easiest way to produce a misleading result. The rule the sweep
applies, decided before any cell ran so it cannot be bent to fit a number:

> **Did Mokka promise to render this path or speak this protocol?**
>
> If the failing read targets something Mokka stages (`/var/lib/nvml-mock/**`, `MOCK_IB_ROOT`, injected
> `/dev/nvidia*`, the topology ConfigMap) or a protocol Mokka speaks for real (IMEX gRPC, the mock-ib
> TCP relay, an NVML or DCGM call), the failure is **G**.
>
> If it targets real host kernel state that Mokka never claimed (NUMA nodes, real PCI tree, the NVLink
> data plane, RDMA verbs, kernel modules), the failure is **K**.

Verified inputs behind the rule, from a probe cluster on this host:

- `/sys/devices/system/node/` does not exist in a kind node under the Docker Desktop VM at all, so every
  NUMA affinity assertion is a guaranteed false negative. **K**.
- `/sys/class/infiniband` does not exist, so RDMA verbs and real IB bandwidth are out of reach. **K**.
- The ComputeDomain demo runs the real `nvidia-imex` daemon in `--nogpu` mode and forms domains over the
  real gRPC peer protocol on the pod network. Failures there are real control-plane semantics. **G**.

---

## D-012: kind version skew against CI is recorded on every cell

CI pins `KIND_VERSION: v0.31.0` (`.github/workflows/nvml-mock-e2e-go.yaml`). This host has kind 0.32.0,
whose default node image is Kubernetes v1.36.1. The sweep therefore runs one minor version ahead of CI.

**Chosen:** record the kind and Kubernetes versions in every result record, and re-test any DRA or GPU
Operator failure that is unique to the sweep before classifying it `G`.

**Why:** version skew is the most likely source of a spurious Mokka finding, and a spurious `G` costs a
maintainer real time.

---

## D-013: `operator.runtimeClass=runc`, and why that is not cheating

**Observed, on a real run:** every GPU Operator operand stayed in `Init:0/1` with

```
Failed to create pod sandbox: rpc error: code = Unknown desc = unable to get OCI runtime
for sandbox "...": no runtime for "nvidia" is configured
```

**Diagnosis, verified rather than assumed:** the operator creates a RuntimeClass named `nvidia` with
handler `nvidia` and sets `runtimeClassName: nvidia` on every operand. `docker exec <node> grep
runtimes /etc/containerd/config.toml` shows a stock kind node has exactly two handlers, `runc` and
`test-handler`. On a real cluster the NVIDIA container toolkit installs the `nvidia` handler. This
sweep runs the NRI path on a stock `kindest/node`, where no toolkit exists.

**Options**

1. Install nvidia-container-toolkit into the kind nodes, as `tests/e2e/go` does for its GPU Operator
   scenario, or use the repo's pre-baked node image.
2. Point the operator at a handler that exists: `operator.runtimeClass=runc`.
3. Hand-write an `nvidia` handler into containerd's config that aliases runc.

**Chosen:** option 2.

**Why:** under the NRI path no special runtime is needed. NRI hooks `CreateContainer` regardless of
which OCI runtime handles the container, which is the whole point of node-wide injection, and the
brief states the per-node CDI plus container-toolkit install is obsolete at v0.3.0. Option 1 would
reintroduce the obsolete path just to satisfy a label. Option 3 produces the same effect as option 2
with more moving parts and a containerd restart race.

**Effect, measured after the patch:** the validator advanced from `Init:0/4` to `Init:3/4` to
`Running`, both workers advertised `nvidia.com/gpu: 8`, and dcgm-exporter reached `Running`. So the
operator's driver, toolkit and CUDA validation stages all pass against the mock.

**Recorded as a finding, not hidden:** a first-time user following the GPU Operator path on stock
kind hits this wall with a message that names neither Mokka nor the toolkit. That is worth a docs
line in the chart README regardless of this sweep.

---

## D-014: The GFD failure is classified `K`, not `G`, and here is the reasoning I could have got wrong

**Observed:** `gpu-feature-discovery` exits with

```
error creating labeler: error creating resource labeler: unable to read PCI device vendor id
for :0a:00.0: open /sys/bus/pci/devices/:0a:00.0/vendor: no such file or directory
```

That kept `ClusterPolicy` at `notReady` for the whole run.

**The tempting call:** file it as a Mokka gap. The BDF is malformed (`:0a:00.0` has no domain), the
profile declares `0000:0A:00.0`, and Mokka renders `/var/lib/nvml-mock/sys/bus/pci/devices/0000:0a:00.0`
correctly. It looks like Mokka handed GFD a bad bus ID.

**Why I did not file it:** the repo's own `e2e-gpu-operator` scenario asserts GFD labels and passes,
and [#542](https://github.com/NVIDIA/k8s-test-infra/pull/542) made that assertion strict. The
difference between that scenario and this cell is mine, not Mokka's: the repo scenario installs the
container toolkit and runs with `cdi.enabled=true`, and this sweep runs stock kind with
`cdi.enabled=false` (see D-013). A green CI scenario on the CDI path is direct evidence that GFD
works against this mock when the supported path is used.

**Chosen:** cause **K**, an artifact of this sweep's stock-kind, no-toolkit configuration. Recorded
with the exact error so a reader can re-open it, and queued as a targeted cell rather than an issue.

**What would change the call:** running the same cell with `cdi.enabled=true` and the toolkit
installed. If GFD still fails there, it becomes **G** and gets an issue. That cell did not fit in the
memory budget, so the honest status is "not distinguished", and the findings note says so rather than
claiming either answer.

This is the exact failure mode the brief warned about, so I am writing down the reasoning rather than
just the verdict.

---

## D-015: Hybrid KWOK + Mokka cell runs, over a panel dissent that I agreed with

**The question:** would combining KWOK (horizontal, node-count breadth) with Mokka (vertical, per-node
GPU-stack depth) give this study a scale story?

**The blocking fact, established before deciding:** `aicr validate` launches every one of the 21 checks
as a containerized Kubernetes Job. A Job scheduled onto a KWOK node executes nothing, because a KWOK
node has no kubelet; `stage-fast` walks the pod through phases without running a container. **Adding N
KWOK nodes therefore adds exactly zero AICR check coverage**, and cannot move either the measured 14%
or the 76% ceiling.

**The recommendation panel overturned my proposal** to run a hybrid experiment. Its argument, which I
agreed with on reading it: "KWOK nodes break `check-nvidia-smi`" is not a discovery, it is definitional,
and spending an evening measuring it documents the obvious. Two further points I checked and conceded:

- The DRA ResourceSlice-at-scale angle collapses for the same reason. The DRA kubelet plugin is a
  DaemonSet and will not run on KWOK nodes either.
- The one genuinely unknown question in the area (do the GPU Operator ClusterPolicy loop and NFD master
  degrade as node count grows?) belongs to the Perf-and-Scale V-Team workstream. The POC SSOT says
  explicitly not to conflate that 5K-node effort with this correctness-of-checks POC.

**Chosen anyway: run it.** Carlos reaffirmed the hybrid experiment after reading the panel's dissent and
my flip. That is his call to make, and it is recorded here rather than quietly followed.

**What the cell is designed to measure**, given the above rules out a coverage number. The output is a
*discipline*, not a percentage: what a fleet operator must do to combine the two without corrupting an
AICR run.

| Cell | KWOK nodes | Question |
|---|---|---|
| `hybrid-kwok-tainted` | present, `NoSchedule` | do Mokka and KWOK nodes coexist without changing any AICR verdict against the pure-Mokka baseline? |
| `hybrid-kwok-schedulable` | present, schedulable, advertising `nvidia.com/gpu` | which checks change verdict, and by how much, when fleet-surveying checks can target hollow nodes? |

The delta between those two cells and the `base-gb200-kind-inference-stack` baseline is the finding. If
the delta is zero in the tainted case and non-zero in the schedulable case, the result is a concrete
node-selector and taint requirement for anyone building a combined fleet, which is directly useful to
Eliran's per-nodepool `backend` design.

**What this cell will NOT be allowed to claim,** written down before it runs so the result cannot drift:
it is not AICR coverage at scale, it is not a bring-up-time number, and it is not evidence of GPU-stack
fidelity at KWOK node counts. The panel's dissent is reported alongside the result.

**Outcome, recorded after the run:** the experiment did not document the obvious. It produced a
**false pass**, not the predicted false fail: with 250 KWOK nodes present, all 9 AICR checks reported
green in under 10 seconds with no container ever executing, and the documented `--node-selector`
mitigation does not work because AICR never applies it to the validator Job pod spec. Both the panel
and I were wrong about the direction of the result. Carlos's override was correct. Full writeup in
`HYBRID-KWOK-FINDING.md`; the false passes are deliberately excluded from `cells.yaml` and the rollup.
