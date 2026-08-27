# Re-run findings: the sweep against Mokka `upstream/main`, 2026-08-26

The first run ([FINDINGS.md](FINDINGS.md)) measured Mokka `v0.3.0` on 2026-08-03. This is the same
matrix re-measured against `57ef01659`, 60 commits later. AICR is held at the same pin, `0752ea14`,
so Mokka is the only axis that moved. That is verified rather than assumed: the base cell's generated
recipe is byte-identical to the one recorded on 2026-08-03.

Both runs remain reproducible and neither overwrites the other:

| Run | Cells file | Results | Mokka |
|---|---|---|---|
| First | [cells.yaml](cells.yaml) | [results/](results/) | `v0.3.0` |
| This one | [cells-57ef016.yaml](cells-57ef016.yaml) | [results-57ef016/](results-57ef016/) | `57ef01659` |

## 1. The headline, which is not the one that was expected

The session handoff predicted that #672 would flip `gpu-operator-health` from fail to pass, and that
"the honest verdict would improve on a re-run". **It did not.** The base cell's nine dispatched
verdicts are byte-identical to the first run: 3 passed, 5 failed, 1 other.

#672 is real and it landed. A differential probe of the two published images, same config file, same
command, only the image differing:

| Image | `nvmlPciInfo_t.busId` |
|---|---|
| `nvml-mock:0.3.0` | `0000:01:00.0` (4-digit domain, the bug) |
| `nvml-mock:sha-57ef016` | `00000000:01:00.0` (8-digit domain, fixed) |

And the consumer error changed accordingly:

```
v0.3.0       unable to read PCI device vendor id for :0a:00.0
sha-57ef016  unable to read PCI device vendor id for 0000:0a:00.0
```

The BDF is now well formed. The path is simply not there. Mokka renders a correct tree at
`/var/lib/nvml-mock/sys/bus/pci/devices/0000:0a:00.0/vendor` (`0x10de`), but its entries are
*relative symlinks* into a sibling `devices/` subtree, and `gpu-feature-discovery` mounts the real
host `/sys`. Two grafts were tried and both fail: replacing GFD's `/sys` breaks the container
outright (it loses `/sys/class/dmi`), and mounting only `bus/pci/devices` leaves the symlinks
resolving against the real `/sys/devices`. Evidence:
[`results/gfd-sysfs-evidence.log`](results/gfd-sysfs-evidence.log).

**This does not promote the GFD failure to a Mokka gap.** D-014 classified it `K` and named the
distinguishing experiment: run the same cell with `cdi.enabled=true` and the toolkit installed. That
reasoning survives. `tests/e2e/VERSION-MATRIX.md` confirms CI exercises the GPU Operator's own GFD
operand, and `tests/e2e/kind-gpu-operator-config.yaml` differs from this sweep in exactly the way
D-014 named (`enable_cdi = true`, an `nvidia` runtime handler, the toolkit installed). What #672
changes is the diagnosis, not the classification: the "Mokka hands GFD a malformed bus ID" theory is
dead by measurement, and what remains is a mount-visibility question. **The status is still
"not distinguished", and that cell is now affordable.** It is the single most valuable cell not yet run.

## 2. What did move: `dra-support`, for an upstream reason

One verdict flipped in the entire re-run.

| Check | First run | This run |
|---|---|---|
| `dra-support` | **failed** (DRA v25.12.0) | **passed** (DRA v0.5.0) |

D-009 blocked the ComputeDomain work as cause `U` because `--set altProcDevices` was "on the DRA
driver's main branch and in no release, including v25.12.0". That was correct when written and is no
longer true: **k8s-dra-driver-gpu v0.5.0, released 2026-08-19, ships it**, and its own `values.yaml`
documents the flag as being for mock NVML.

Measured end to end: Mokka rendered `235 nvidia-caps-imex-channels` into its substitute
`/proc/devices`, v0.5.0 consumed it, both kubelet-plugin containers (`compute-domains` and `gpus`)
came up on both workers, 12 ResourceSlice devices were published, and the first run's abort
(`error parsing '/proc/devices'`) appears **zero** times in 400 lines of driver logs.

Mokka's IMEX rendering already worked at `v0.3.0`. What was missing was a released consumer.

**Caveat that must travel with this result:** v0.5.0's chart was renamed
(`nvidia-dra-driver-gpu` to `dra-driver-nvidia-gpu`), so its default resource names no longer match
what AICR at `0752ea14` looks for. This cell passed `--set nameOverride=nvidia-dra-driver-gpu` to
restore the expected name; AICR itself was not modified. Installed with chart defaults, `dra-support`
fails `NOT_FOUND` on the name alone regardless of what the driver is doing. That rename is real drift
and is worth raising on the AICR side. Evidence:
[`results/dra-altprocdevices-evidence.log`](results/dra-altprocdevices-evidence.log).

## 3. AICR's own snapshot agent cannot be reached by library interception

Raised by Giulio Calzolari in review on 2026-08-27, and verified here independently against the
published image before being written down.

**This run recorded the symptom a day earlier without diagnosing it.** The base cell's validate log,
line 12:

```
snapshot has no GPU data but cluster topology shows GPU-capable nodes
  - agent likely ran on a non-GPU node
  fix_1=--node-selector nvidia.com/gpu.present=true   fix_2=--node-selector kubernetes.io/hostname=<gpu-node>
  fix_3=--require-gpu                                  fix_4=--runtime-class nvidia
```

All four fixes AICR suggests there are about node **placement**, and the agent had already been
auto-targeted onto a GPU-labelled node (line 5 of the same log). So AICR's own diagnostic points away
from the cause.

**The cause.** `ghcr.io/nvidia/aicr:latest` is a distroless `ko` image with no shell, so the binary
was extracted with `docker cp` and its ELF headers parsed directly:

```
/ko-app/aicr: ELF 64-bit LSB executable, ARM aarch64, statically linked, Go BuildID=..., stripped
phdrs      : PT_PHDR, PT_NOTE, PT_LOAD, PT_LOAD, PT_LOAD, GNU_STACK
PT_INTERP  : ABSENT      PT_DYNAMIC : ABSENT
```

With no `PT_INTERP` the kernel never invokes a dynamic loader, so `LD_PRELOAD` and `LD_LIBRARY_PATH`
are inert **by construction, not by configuration**. With no `PT_DYNAMIC` there is no dynamic symbol
resolution to hook at all. Mokka's NRI plugin injects an overlay mount and a library environment;
against this binary the environment half has nothing to act on, so the agent reads the node's real
sysfs and finds no NVIDIA devices.

**What it does not affect.** `check-nvidia-smi` and `accelerator-metrics` run their own pods, which
are ordinary dynamically linked consumers reachable through the NRI overlay. Those passes stand. What
it affects is the cluster inventory AICR builds *before* dispatching checks.

**It is a distinct cause class.** Not `K`, `U`, `X` or `BUDGET`. Not a Mokka defect: Mokka renders and
injects correctly. Not an AICR defect: a static build is a deliberate, legitimate choice for a
`ko`-built agent. It is an interception-model mismatch, and no configuration on either side closes it.
Closing it needs the agent to read GPU state from the Kubernetes API rather than sysfs, or a
filesystem-level graft of Mokka's rendered sysfs, or a dynamically linked agent build.

**Note the convergence with section 1.** Two independent consumers, both defeated by reading the real
`/sys` instead of Mokka's rendered tree: GFD is dynamically linked and fails on a sysfs *path*, the
agent is static and fails on sysfs *content*. The common remedy is a filesystem graft, not library
interception. That is a sharper conclusion than either finding alone.
Evidence: [`results/aicr-agent-static-linkage-evidence.log`](results/aicr-agent-static-linkage-evidence.log).

## 4. Coverage: the memory unlock was the real win

| | First run | This run |
|---|---|---|
| Reached a verdict | 13 / 210 (**6%**) | 53 / 210 (**25%**) |
| Passed | 4 | 20 |
| Failed | 9 | 32 |
| Blocked | 168 | 84 |
| Blocked `BUDGET` | 105 | **0** |
| Blocked `U` | 21 | **0** |
| Blocked `X` | 42 | 84 |
| Analytical ceiling | 76% | 76% |

The ceiling is unchanged because the catalog is unchanged. What changed is how much of it was
actually measured: four times as many check-results reached a real verdict, and the host memory
budget no longer blocks anything.

The largest single gain is the `h100` cell, which was `BUDGET`-blocked before. **AICR dispatches 14
of 21 checks there against 9 for `gb200`, and 5 pass against 3.** `gpu-operator-version` passes on
h100 and fails on gb200, and `gang-scheduling`, `inference-gateway`, `pod-autoscaling`,
`cluster-autoscaling` and `secure-accelerator-access` are dispatched at all only there.

## 5. Two cells were misattributed in the first run

Both were recorded as blocked for reasons that were never tested, and both are actually `X`.

`aicr recipe --service kind --accelerator gb200 --intent training` exits 2 with
`[INVALID_REQUEST] no recipe provides intent 'training'`. On the `kind` service, training resolves
for **h100 and no other accelerator**. Full matrix:
[`results/aicr-recipe-matrix-evidence.log`](results/aicr-recipe-matrix-evidence.log).

| Cell | Was | Is | Why |
|---|---|---|---|
| `base-gb200-kind-training-stack` | `BUDGET` | `X` | The note predicted it would "differ from the inference cell only in component set". It does not resolve a recipe at all, so memory never mattered. |
| `axis-compute-domain-fabric` | `U` | `X` | Declares `intent=training` on `kind`. Recipe resolution fails before the DRA driver can. The `altProcDevices` question it was meant to answer is now carried by `targeted-dra-installed`. |

That is 42 check-results moving from `BUDGET`/`U` to `X`. This is the first run's own stated
discipline applied to itself: the `gb300` block was verified by running the CLI, and these two were
not.

## 6. An injected ECC fault moves no verdict

With `gpu.failureInjection` set to `enabled=true mode=ecc_uncorrectable probability=1`, the mock
reports 6 and 9 volatile DRAM Uncorrectable errors on two GPUs while all four still enumerate.
**Every one of the nine dispatched verdicts is identical to the base cell**, including
`check-nvidia-smi` and `accelerator-metrics`, which both pass.

That is a statement about the AICR check set, not about Mokka: the injection works end to end, and
the checks this recipe dispatches do not read ECC state, so a degraded-but-enumerable GPU is
indistinguishable from a healthy one. It says nothing about checks this recipe does not dispatch.

Worth noting for the first run's record: the axis as declared in `cells.yaml`
(`gpu.failureInjection.mode=ecc_uncorrectable` alone) is **inert**. The chart gates injection on
`enabled` and never trips at the default probability of 0. Had that cell run as written it would have
measured nothing and looked like a clean negative.

## 7. Corrections to first-run figures

The first run's numbers were correct when measured. #727 remodelled `gb200`/`gb300` as a 4-GPU NVL72
compute tray, so these figures no longer describe current main:

| [FINDINGS.md](FINDINGS.md) claim | Then | Now |
|---|---|---|
| device plugin advertised `nvidia.com/gpu: N` per worker | 8 | **4** (measured on both workers) |
| node-wide injection pod listed N GB200s | 8 | 4 |
| DRA driver published N devices across ResourceSlices | 16 | 12 |

`h100` still stages 8 GPUs, which confirms #727 was correctly scoped to the Grace-Blackwell profiles.

## 8. Traps hit while running this, recorded so they are not repeated

**A failed install produced a plausible, flattering, wrong result.** The first attempt at the ECC cell
returned 1 passed / 7 failed, with `check-nvidia-smi` and `accelerator-metrics` both flipping. That
read as clean evidence that AICR detects the injected fault. It was not: `--set
gpu.failureInjection.probability=1.0` types `1.0` as a **string**, `values.schema.json` wants a
number, and the Mokka install failed. The cell ran against a cluster with no mock at all, and the two
checks "flipped" only because there were no GPU nodes. Use `--set-json` for numeric values. The
driver script now aborts a cell when the Mokka install fails rather than measuring an empty cluster,
and every other cell in this run was audited for the same failure mode (all clean: `check-nvidia-smi`
passed in each, which is impossible without GPU nodes).

**The stale-citation pattern from the first run recurred.** D-009 cites the DRA chart's `values.yaml`
"at lines 307 to 325". v25.12.0's `values.yaml` is 270 lines, so that range does not exist in the file
a reader would open; the line numbers refer to the main-branch file. The substantive claim was right,
the citation points somewhere else.

## 9. What to do next, in order

1. **Run D-014's distinguishing cell**: the base cell with `cdi.enabled=true` and the
   nvidia-container-toolkit installed. It is the only thing standing between the GFD failure being
   `K` and being `G`, it has been open since the first run, and the memory that blocked it is gone.
2. **Raise the agent's static linkage with AICR.** Its snapshot reports no GPU data on a working mock
   cluster, and its own remediation hints point at node placement, which cannot fix it. Reading GPU
   state from the Kubernetes API rather than sysfs would close it for every mock backend, not just
   this one.
3. **Raise the DRA chart rename with AICR.** `dra-support` cannot pass against v0.5.0 with default
   chart values, because AICR looks for `nvidia-dra-driver-gpu-controller` and the chart now produces
   `dra-driver-nvidia-gpu-controller`.
4. **Close #498 and fix the stale note** in `deployments/nvml-mock/helm/nvml-mock/values.yaml`, which
   still says `altProcDevices` "is NOT in any release (absent at v25.12.0)".
5. **Raise `gb300` with AICR again.** #699 made `gb300` the Mokka chart default while AICR still has
   no such accelerator, so the two sides have drifted further apart, not closer.
