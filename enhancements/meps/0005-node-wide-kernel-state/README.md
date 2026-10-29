# MEP-0005: Node-wide kernel state delivery

Author: [Carlos Eduardo Arango Gutierrez](https://github.com/ArangoGutierrez)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [Relationship to MEP-0004](#relationship-to-mep-0004)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Feasibility evidence](#feasibility-evidence)
  - [Surface split](#surface-split)
  - [Mechanism A: NRI mount adjustment](#mechanism-a-nri-mount-adjustment)
  - [Mechanism B: createContainer hook](#mechanism-b-createcontainer-hook)
  - [Path safety](#path-safety)
  - [Feature gates](#feature-gates)
  - [Threat model](#threat-model)
  - [Test plan](#test-plan)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

Mokka simulates NVIDIA kernel state, but it only delivers that state to
containers that opt in, and only through mechanisms that need the consumer's
cooperation. A real GPU node does the opposite: with the driver loaded, kernel
state is visible to every container regardless of whether it was given a GPU.

This MEP delivers kernel-global state to every container on a Mokka node, using
two mechanisms split by what the runtime permits: plain bind mounts for the
sysfs surfaces, and an NRI-injected `createContainer` hook for the procfs
surfaces. Devices and libraries stay allocation-aware. What every container
receives is only the read-only kernel tier: no device nodes, no mock libraries,
no `LD_PRELOAD` and no environment variables, so a container with neither a GPU
allocation nor the devices annotation still sees zero GPUs. Each mechanism
ships behind its own alpha feature gate, and with both gates off the behaviour
is byte-identical to today.

## Motivation

[#911][issue-911] reports that GPU Operator v26.7.1 operands never start on a
Mokka node. The cause is [gpu-operator#2881][pr-2881] (merged 2026-09-14,
backported via #2896, shipped in v26.7.1), which gates the
`toolkit-validation` init container of six operands on:

```sh
until [ -f /run/nvidia/validations/toolkit-ready ] && \
      { grep -q '^nvidia ' /proc/modules || [ -e /dev/dxg ]; }; do
  echo waiting for nvidia container stack to be setup; sleep 5
done
```

That init container runs the GPU Operator image itself, which is distroless
plus a **statically linked** busybox, carries `NVIDIA_VISIBLE_DEVICES=void`,
and is not annotated for Mokka device injection.

Every delivery mechanism Mokka has today is closed for that container, and each
of the following was executed rather than inferred:

| Mechanism | Why it does not reach the container |
| --- | --- |
| `libmockfs` `LD_PRELOAD` shim | busybox is static, so the loader never maps the shim |
| Bind mount in the OCI spec | runc refuses any mount whose target is inside `/proc` |
| Container toolkit CDI | the image sets `NVIDIA_VISIBLE_DEVICES=void` |
| NRI plugin | `inject.Adjust` leaves a container with no GPU allocation and no opt-in annotation unmodified, and even a served container gets `/sys/module` only from the CDI spec in `cdi` mode, while `raw` is the chart default |

The NRI row was re-checked against `main` at `42f5a70bf`, after
allocation-aware injection landed. Executed: `inject.Skip` on a privileged
`toolkit-validation` container with `NVIDIA_VISIBLE_DEVICES=void` returns
`no GPU allocation and no opt-in annotation`, and the same container with the
devices annotation is served. Read from source: `kernelModuleMounts` in
`internal/agent/cri/spec.go` is carried only by the two CDI specs.

[#793][issue-793] is the same bug in a different surface: the node agent stages
`/proc/driver/nvidia`, and nothing serves it at the kernel path, so no consumer
reads it.

Both issues share one root cause. Kernel-global state is delivered behind the
*allocation* opt-in, so a container that legitimately has no GPU allocation
cannot observe that the driver is loaded. On real hardware that observation
needs no allocation at all.

### Goals

- Deliver `/proc/modules`, `/sys/module`, `/proc/driver/nvidia` and the PCI
  sysfs tree to every container on a Mokka node, including unannotated ones and
  consumers that are statically linked or bypass libc path resolution.
- Fix [#911][issue-911] from the Mokka side for every GPU Operator release that
  carries the gate: v26.7.1 and v26.7.2 read `/proc/modules`, and the first
  release with [gpu-operator#2959][pr-2959] reads
  `/sys/module/nvidia/initstate`.
- Fix [#793][issue-793] with the same mechanism.
- Keep devices and libraries allocation-aware, as `inject.Adjust` makes them
  today: a container with neither a GPU allocation nor the devices annotation
  continues to see zero GPUs.
- Preserve the MEP-0002 invariant that exactly one component emits CDI device
  references for a container.
- Ship both parts disabled by default, behind independent alpha feature gates
  from [#883][pr-883].

### Non-Goals

- Kata Containers support for the procfs tier. A `createContainer` hook runs on
  the host side of a VM boundary, so it cannot mount into the guest.
- crun support in alpha. Only runc 1.2.9 has been exercised. crun is a
  promotion criterion, not an alpha claim.
- Changing how devices, libraries or IMEX channels are delivered.
- Loading a real kernel module on CI hosts. That is MEP-0004's `kernel` mode;
  see [Alternatives](#alternatives).
- Creating `/dev/dxg`, which would make a Linux node claim to be WSL.
- CI canary work against unpinned GPU Operator releases. That is tracked
  separately and deliberately excluded here.

## Proposal

Split the simulated surface into two tiers with different delivery rules.

**Kernel tier, unconditional and node-wide.** `/proc/modules`, `/sys/module`,
`/proc/driver/nvidia` and the PCI sysfs pair. Delivered to every container the
NRI plugin sees, subject only to the existing excluded namespaces
(`kube-system` by default) and the existing opt-out annotation
(`nvml-mock.nvidia.com/inject: "false"`). There is no opt-in, because a real
GPU node has none.

**Allocation tier, opt-in and unchanged.** The `/dev/nvidia*` nodes and the
mock libraries. These stay where `inject.Adjust` puts them today: a container
gets them when it holds a device-plugin or DRA GPU allocation, or carries the
`nvml-mock.nvidia.com/devices` annotation.

What makes the kernel tier safe to deliver unconditionally is that it is
read-only and carries no device nodes, so it cannot widen a container's GPU
allocation. It also uses plain mounts and a hook rather than CDI device
references, so MEP-0002's "exactly one source of CDI references per container"
invariant is untouched.

### Relationship to MEP-0004

[MEP-0004][pr-843] adds a `kernel` driver simulation mode, in which stub kernel
modules make the kernel itself serve `/proc/modules`, `/sys/module` and
`/proc/driver/nvidia` live, and keeps today's `userspace` mode as the default.
The two proposals cover different modes and compose:

- In `userspace` mode, which stays the default, this MEP is what delivers the
  kernel surfaces to consumers the preload shim cannot reach. MEP-0004's own
  surface table lists `/proc/modules` as a full gap in that mode.
- In `kernel` mode the kernel already serves these surfaces, so both gates of
  this MEP stay off on such a node. The procfs tier would only mount a staged
  copy over the live file.
- `kernel` mode needs one isolated kernel per simulated node, as the MEP-0004
  author notes on [#843][comment-843]. A scale lane that runs several simulated
  nodes on one host shares one set of modules between them, so a rolling driver
  upgrade on one node cannot unload modules that another node's workloads still
  hold, and the GPU Operator scale tests have hit exactly that. Such a lane
  stays on `userspace` mode, which is the mode [#911][issue-911] breaks.

### Notes/Constraints/Caveats

- **runc refuses spec mounts inside `/proc`.** This is not a bug to work
  around in the spec; it is why the procfs tier needs a different mechanism.
- **procfs has no `mkdir`.** `/proc/driver/nvidia` cannot be created inside a
  container's procfs, so the mount has to land on the parent `/proc/driver`,
  and the staged tree must already contain `nvidia/`.
- **Mounting the parent would mask other kernel entries** such as
  `/proc/driver/rtc`. The node agent therefore mirrors the node's real
  `/proc/driver` entries beside the simulated `nvidia/`, which is the pattern
  `kmod.Render` already uses for `/sys/module`.
- **Every Mokka service applies every gate.** The [#883][pr-883] gates are
  `component-base` gates declared once in `internal/features`, and the chart
  passes its one `featureGates` map to the node agent, the NRI plugin and the
  control plane. The values schema lists the gates a chart version accepts and
  rejects any other, so both new gates go into the schema and into
  `docs/feature-gates.md`.
- **The kernel tier changes what the plugin skips.** Today `inject.Adjust`
  leaves a container with no GPU allocation and no opt-in annotation exactly as
  authored, and only such a skipped container bypasses the node agent's staging
  gate. With either gate on, that container gets a kernel-tier-only adjustment
  instead, so every container the plugin sees passes the staging gate check.
  The implementation also needs its own marker for a container that has
  already been served, because `hasOverlay` keys on the overlay mount, which a
  kernel-tier-only container never gets.
- **`buildNRISpec` is left unchanged.** Moving its kernel mounts onto the
  adjustment path would be an ungated behaviour change, and gate-off must mean
  today's behaviour exactly.
- **[#1136][pr-1136] binds a writable `/sys/module` into the GPU Operator's
  driver container**, so that `k8s-driver-manager` can unload modules. That
  draft routes the driver and driver-manager containers as specialized
  workloads, and the sysfs tier must leave their `/sys/module` mounts alone: a
  read-only `/sys/module` over the driver container's would break the unload it
  simulates.

### Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| A hook that cannot be executed fails container creation for every pod on the node | The plugin stats the hook binary before injecting a reference to it, in the same shape as the existing `cdiSpecStaged` check |
| A mount whose source is missing fails the whole pod | Every mount source is stat'd before it is emitted. `pciSysfsMounts` guards the same hazard today by gating on the condition its tree is rendered under; a stat is used here because the kernel tier has no single such condition |
| A hook that hangs exceeds its OCI `timeout` and still fails the container | The injected hook carries an explicit `Timeout`, and its work is bounded to two mounts with no network, locks or unbounded allocation. Residual risk is accepted and documented |
| An image redirects a mount target through a symlink, and the hook mounts onto a host path as root | `openat2` with `RESOLVE_IN_ROOT|RESOLVE_NO_MAGICLINKS`, then mounting through `/proc/self/fd/N`. See [Path safety](#path-safety) |
| The staged hook binary becomes the highest-value target on the node | It is root-owned under the agent's overlay root, written only by the agent DaemonSet, and gated separately from the sysfs tier |
| A container in `cdi` mode that receives GPUs gets `/sys/module` from `buildNRISpec` and again from the sysfs tier | Expected to be benign because source, destination and options are identical, but **unverified**. The e2e asserts it explicitly rather than assuming it |
| A container created while the node agent is staging is left unmodified, and NRI does not see it again | The plugin fails open on a closed staging gate, as it does today for GPU containers. A `toolkit-validation` loop created in that window keeps waiting, as in [#911][issue-911] today, until its pod is recreated. **Open for review:** whether the kernel tier should bypass the gate, as the ComputeDomain daemon does, and rely on the per-source stat alone |
| Feature gate skew between the agent and the plugin | The chart passes one gate list to both containers of the node pod, so skew needs a hand-edited manifest. It stays safe in both directions because of the stat-before-inject check: worst case is no injection, never a broken pod |

## Design Details

### Feasibility evidence

Executed on 2026-09-23 against runc 1.2.9 (spec 1.2.0), containerd 2.2.0,
kernel 6.12.76-linuxkit, NRI v0.12.3, using the real
`nvcr.io/nvidia/gpu-operator:v26.7.1` image. Every result below has a
discriminating control.

| Probe | Control | Result |
| --- | --- | --- |
| Mount over `/proc/modules` in the OCI spec | same bundle, same runc | Refused, rc=1: `check proc-safety of /proc/modules mount: ... cannot be mounted because it is inside /proc` |
| Same mount from a `createContainer` hook | same bundle, same runc | Succeeded, rc=0. `grep -q '^nvidia ' /proc/modules` returns 0 inside the container |
| NRI-injected hook, unannotated pod, real operator image, verbatim #2881 command | plugin stopped, identical pod | With the plugin: init container exits 0. Without it: the pod loops on `waiting for nvidia container stack to be setup`, which is [#911][issue-911] reproduced |
| Was the preload shim responsible? | `LD_PRELOAD` printed from inside | `LD_PRELOAD=[]`. Delivery was the hook, not the shim |
| `/proc/driver/nvidia/version` readable | same pod | Yes, which closes [#793][issue-793] with the same mechanism |
| Resolve a target through a rootfs-escaping symlink | flags present vs absent | Without the resolve flags the lookup left the rootfs and landed on a host path. With them the kernel refused |

The hook fired for the init container as well as the main container, which is
the case [#911][issue-911] depends on.

Not verified, and therefore not claimed anywhere in this MEP: crun, Kata,
fail-open behaviour when a staged source is missing, the production
`internal/nri` code path (a throwaway plugin was used), GPU Operator releases
other than v26.7.1, and interaction with what landed on `main` after the spike:
the allocation-aware selection in `inject.Adjust` and the node agent's staging
gate.

### Surface split

| Surface | Mechanism | Reason |
| --- | --- | --- |
| `/sys/module` | NRI mount adjustment | sysfs mounts are permitted, and this already works on the CDI path |
| PCI sysfs pair | NRI mount adjustment | same |
| `lsmod` script | NRI mount adjustment | plain file mount |
| `/proc/modules` | `createContainer` hook | runc refuses a spec mount |
| `/proc/driver` | `createContainer` hook | same, and procfs has no `mkdir` |

### Mechanism A: NRI mount adjustment

A new step in `inject.Adjust`, a sibling of `mountOverlay`, emits the sysfs
mounts for every container the plugin sees, including the ones `Adjust` skips
today for having no GPU allocation and no opt-in annotation. It does not
consult the allocation, the device annotation or `DeviceInjectionMode`, which
is what makes the tier node-wide. Today `/sys/module` reaches a container only
through `kernelModuleMounts`, which only the two CDI specs carry: the container
toolkit's, and `buildNRISpec`'s in `cdi` mode. That is why
[gpu-operator#2959][pr-2959]'s gate still fails on default settings: `raw` is
the chart default, and the `toolkit-validation` container receives no GPUs in
either mode.

Each source is stat'd before it is emitted. A mount with a missing source fails
the entire pod, so a surface the node agent has not staged yet degrades the
injection instead of blocking container creation. That is the fail-open
contract the `inject` package already documents for its steps. The node agent
and the plugin now run in one pod, and the staging gate keeps the plugin off a
tree that is being rewritten, but neither guarantees that a given surface has
been rendered on this node, so any surface may legitimately be absent when a
container is created.

### Mechanism B: createContainer hook

A small binary shipped in the Mokka image and staged by the node agent onto the
host under the overlay root, in the same way `internal/agent/gpudriver` already
stages `nvidia-smi` and the `lsmod` script. It has to be a host path: the hook's
`path` is resolved in the runtime's filesystem view, not the container's.

runc executes it inside the container's mount namespace after the rootfs mounts
are set up and before `pivot_root`, passing the OCI container state as JSON on
stdin. The hook reads `.bundle` and derives `<bundle>/rootfs`, which is a
runtime-supplied value rather than a guess. The injected hook carries an
explicit `Timeout`.

It performs two read-only mounts:

- `/proc/modules`, file over file.
- `/proc/driver`, directory over directory, carrying the mirrored node entries
  plus the simulated `nvidia/`.

The hook exits 0 unconditionally. A missing source, a refused resolution, an
unavailable `openat2` or a failed `mount` each skip that one mount and log. A
refused resolution logs at warning level, because it means an image tried to
redirect a mount.

**Installation impact.** Neither tier adds a chart component, a host
prerequisite or an install step. The hook binary ships in the existing Mokka
image and the existing node agent stages it, as it stages `nvidia-smi` today.
A user turns the tiers on with entries in the chart's `featureGates` map, and
an install that sets neither gets no new mount, hook or staged file. What the
procfs tier needs from the node is a runtime that runs OCI `createContainer`
hooks and kernel 5.6 or newer for `openat2`. On a node without them the procfs
surfaces are not delivered and nothing else changes.

### Path safety

The rootfs comes from a user-supplied image, so resolving
`<bundle>/rootfs/proc/modules` by string concatenation and calling `mount`
would follow whatever symlinks that image contains. The hook runs as root,
before `pivot_root`, with the host filesystem still visible. This is the same
class of defect as CVE-2024-0132 in the container toolkit.

The hook opens the rootfs `O_PATH|O_DIRECTORY`, resolves each target with
`openat2` under `RESOLVE_IN_ROOT|RESOLVE_NO_MAGICLINKS`, and mounts through
`/proc/self/fd/N`. `RESOLVE_IN_ROOT` re-scopes absolute symlinks and `..` to
the rootfs, so a planted link can only ever resolve back inside the container.
This is the primitive runc uses for its own rootfs operations.

Mounting through `/proc/self/fd/N` is what removes the time-of-check to
time-of-use window rather than narrowing it: `openat2` returns a descriptor,
and the mount names the inode that was already resolved, so there is no second
path walk to race. The property to assert in tests is therefore *the target is
reached through a descriptor and never re-resolved by name*.

`openat2` requires kernel 5.6 or newer. If it returns `ENOSYS` the hook skips
the mount. It does not fall back to unsafe resolution. The rule the whole
design follows is **fail open on delivery, fail closed on safety**: a surface
that cannot be delivered safely is not delivered, and never degrades into being
delivered unsafely.

### Feature gates

Two gates declared in `internal/features`, the registry [#883][pr-883] added,
both `featuregate.Alpha` with `Default: false`:

| Gate | Read by | Covers |
| --- | --- | --- |
| `KernelStateSysfs` | NRI plugin | the adjustment-path sysfs and `lsmod` mounts |
| `KernelStateProcfs` | node agent and NRI plugin | staging the hook binary, mirroring `/proc/driver`, and injecting the hook reference |

Every service applies every gate, so turning a tier on is one entry in the
chart's `featureGates` map, for example
`featureGates: {KernelStateSysfs: true, KernelStateProcfs: true}`, and
`AllAlpha: true` turns both on. Both gates have to be added to the values
schema, which rejects any gate it does not list.

With both gates off, no new mount, hook or staged artifact is produced, and
`buildNRISpec` is unchanged, so the resulting container is byte-identical to
one produced by the current release. That is the form of "gate off means
today's behaviour" that a test can actually assert.

Promotion to beta requires the acceptance e2e green across the profile matrix,
a reviewed threat model, and a crun lane. It is not time-based.

### Threat model

**What changes.** Today a compromised staged tree yields code running as the
*container's* user through `LD_PRELOAD`. This MEP adds a binary that runs as
**root, at every container creation, on every node**. The trust assumption is
not new, since the overlay root was already trusted, but the consequence of
violating it is considerably larger.

**Trust boundaries.** Attacker-controlled: the container image and therefore
every byte of the rootfs, plus pod annotations. Node-agent controlled and
root-owned: the staged overlay. Runtime-supplied and trusted: the OCI state on
stdin, the bundle path, and the hook path in the spec, which comes from plugin
configuration and never from pod input. The assumed attacker can create pods
with arbitrary images in any non-excluded namespace, and cannot write the host
filesystem or the plugin's configuration.

| ID | Threat | Mitigation | Residual |
| --- | --- | --- | --- |
| T1 | An image plants a symlink at a mount target, redirecting a root mount onto a host path | `openat2` with `RESOLVE_IN_ROOT|RESOLVE_NO_MAGICLINKS` | None known for the resolution step |
| T2 | Time-of-check to time-of-use swap between resolving the target and mounting it | The mount names an already-resolved descriptor via `/proc/self/fd/N`, so there is no second name resolution. No container process exists yet at `createContainer` time, so the only lever is static image content | Mount *sources* live under the agent-controlled overlay root and are not attacker-writable |
| T3 | Denial of service by making the hook fail, blocking pod creation | Unconditional exit 0 | A hook that *hangs* past its `timeout` still fails the container. Bounded work is the only mitigation |
| T4 | Replacing the staged hook binary yields root on every container creation | Root-owned path, written only by the agent DaemonSet, gated separately from the sysfs tier | Anyone with node root already has this |
| T5 | The mirrored `/proc/driver` discloses host entries to containers | `/proc/driver` is host-global and already visible in a container's procfs, so mirroring is expected to disclose nothing new | **Unverified.** The implementation confirms it rather than assuming it |

Mount targets that land in image-controlled territory, such as the `lsmod`
script at `/usr/local/bin`, are spec mounts performed by runc under its own
path protections, not by this hook.

Explicitly not defended against: a compromised node agent or containerd, both
of which are already root on the node; `kube-system`, which remains excluded;
and Kata, which is out of scope for the procfs tier.

### Test plan

Every test below names the defect it catches and the mutation that turns it
red. A test with no such mutation is not evidence.

**Unit, `internal/nri/inject`.** No privileges required.

| Test | Defect caught | Mutation that turns it red |
| --- | --- | --- |
| Kernel mounts emitted for a container with no GPU allocation and no annotation | [#911][issue-911]: kernel state hidden behind the allocation opt-in | delete the new step, or restore the skip in `Adjust` |
| Gate off yields an adjustment identical to the current release | a gated feature that is not actually gated | make the step ignore the gate |
| Missing staged source is skipped, not emitted | a missing mount source fails the whole pod | delete the stat |
| Missing hook binary means no hook reference is injected | an unexecutable hook kills every pod on the node | delete the stat |
| The injected hook carries a `Timeout` | a wedged hook hangs container creation unbounded | drop the `Timeout` |

The existing `TestAdjust(Suppresses|CDIMode|RawMode)` set and the allocation
and selection tests must stay green, so that kernel state does not perturb
device injection.

**Unit, the hook package.** Linux, still unprivileged: resolution needs
`openat2` but not root, so these run in normal CI. Only `mount` needs
privilege, which is the e2e's job.

| Test | Defect caught | Mutation that turns it red |
| --- | --- | --- |
| A target escaping the rootfs is refused | T1 and T2, the CVE-2024-0132 class | remove `RESOLVE_IN_ROOT` |
| `openat2` unavailable means skip, never unsafe fallback | fail-closed-on-safety silently becoming fail-open | make the `ENOSYS` path fall back |
| Every failure mode exits 0 | a hook failure becoming a pod-creation failure | return non-zero on one path |

The `ENOSYS` case uses an injected resolver seam, not an environment
manipulation. Breaking a syscall through the environment behaves differently
inside and outside a sandbox, which makes it theatre in at least one of them.

**End to end.** kind, both gates on, with this lane overriding the v26.3.3 pin
that the default lane keeps. It runs against GPU Operator v26.7.1 for the
`/proc/modules` gate, and against the first release that carries
[gpu-operator#2959][pr-2959] for the `initstate` gate.

1. A pod with no GPU request and no annotation, running a statically linked
   binary, reads `^nvidia ` from `/proc/modules` and reads
   `/proc/driver/nvidia/version`, with `LD_PRELOAD` empty.
2. That same pod sees zero `/dev/nvidia*`, so the tier boundary holds.
3. The v26.7.1 operands start: the unchanged #2881 check passes.
4. On the #2959 release, the operands start with only `KernelStateSysfs` on.
5. With both gates off, the operands hang exactly as they do today.
6. A container in `cdi` mode that receives GPUs is inspected for duplicate
   mounts.

The two tiers are separately falsifiable, which is what makes this more than a
smoke test. Removing the procfs tier fails the #2881 check. Removing the sysfs
tier fails `grep -qsx live /sys/module/nvidia/initstate`, which is the check
gpu-operator#2959 puts in place of it. Each tier is pinned to a distinct
upstream gate.

## Drawbacks

- It puts root-executed code in the container creation path of every container
  on a Mokka node. That is the single largest privilege increase in the
  project's history, and it is the reason for a separate gate, a threat model,
  and a resolver test with a mutation check.
- It is runtime-coupled. The hook mechanism works on runc and, in principle,
  crun; it cannot work for Kata, so `/proc/driver/nvidia` and `/proc/modules`
  remain undelivered there.
- It adds a second delivery mechanism alongside bind mounts, so a reader must
  now know which surface arrives by which route. The surface split table exists
  to keep that answerable.
- Mounting the parent `/proc/driver` requires mirroring node entries that
  Mokka otherwise has no reason to read.
- Every container the plugin sees now goes through the node agent's staging
  gate check, where today only containers that ask for a GPU, InfiniBand or
  IMEX do. That costs a loopback request per container creation, bounded by
  the plugin's 500 ms `agentCheckTimeout` while the agent is frozen.

## Alternatives

**Rely on [gpu-operator#2959][pr-2959] alone.** It changes the operand gate to
read `/sys/module/nvidia/initstate`, which Mokka already renders, and it is
right on its own merits, since `/proc/modules` lists a module while it is
still loading. It merged on 2026-10-05 and its backport is on the operator's
`release-26.7` branch, but v26.7.2 does not carry it, so v26.7.1 and v26.7.2
keep the `/proc/modules` gate. Rejected as the whole fix because it is not
sufficient on its own either: Mokka renders `initstate` but delivers it only
through the CDI specs, which the `toolkit-validation` container never
receives, and `raw` is the chart default. Together with this MEP's sysfs tier
it is sufficient, so from that operator release on `KernelStateSysfs` alone
makes the operands start, and the procfs tier remains for v26.7.1, v26.7.2 and
[#793][issue-793].

**Keep pinning the GPU Operator chart.** [#912][pr-912] pins v26.3.3 and
unblocks CI today. Rejected as a fix: a pin buys a green pipeline, not the
ability to test the operator release that customers run.

**Extend the `LD_PRELOAD` shim to cover `/proc/driver/nvidia`.** Cheap, and it
covers C consumers such as `nvidia-modprobe`. Rejected as the primary fix: it
cannot reach a statically linked consumer, which is exactly the case in
[#911][issue-911], and Go consumers issue `openat` directly.

**Simulate the driver with custom kernel modules ([#856][pr-856], the PoC of
[MEP-0004][pr-843]).** Stub `nvidia`, `nvidia_uvm`, `nvidia_modeset` and
`nvidia_peermem` modules make the kernel itself serve `/proc/modules`,
`/sys/module` and `/proc/driver/nvidia`, live, to every container. That needs
no hook, no root at container creation and no `/proc/driver` mirror, reaches
static and libc-bypassing consumers alike, and has the highest fidelity of any
option here. Not adopted as this MEP's fix because of the costs MEP-0004 lists
for it: the modules are built for the running kernel against its distribution
headers, so Docker Desktop's kernel cannot build them and a macOS Kind cluster
has to run in a Linux VM; one kernel carries one simulated GPU node; and a bug
in the stubs can crash the host. MEP-0004 keeps the current `userspace`
simulation as the default and adds `kernel` as an opt-in mode, so this MEP
fixes the default mode rather than competing with it. On a `kernel`-mode node
the procfs tier has nothing to add, and would only mount a staged copy over the
live `/proc/modules`. See [Relationship to MEP-0004](#relationship-to-mep-0004)
for why the scale-test lane cannot use `kernel` mode.

**Create `/dev/dxg` to satisfy the check's second arm.** Rejected: it makes a
Linux node claim to be WSL, which is a lie with consequences well beyond this
check.

**Deliver kernel state through the allocation opt-in, as today.** Rejected: it
is the status quo that produced [#911][issue-911] and [#793][issue-793], and it
requires a new fix for each upstream check that reads kernel state.

**Route every surface through the hook, retiring the bind mounts.** Rejected:
it makes the low-risk sysfs tier inherit the blast radius of root-at-creation,
discards a mechanism that already works, and collapses the two gates into one.

[issue-911]: https://github.com/NVIDIA/k8s-test-infra/issues/911
[issue-793]: https://github.com/NVIDIA/k8s-test-infra/issues/793
[pr-843]: https://github.com/NVIDIA/k8s-test-infra/pull/843
[comment-843]: https://github.com/NVIDIA/k8s-test-infra/pull/843#issuecomment-6467036865
[pr-856]: https://github.com/NVIDIA/k8s-test-infra/pull/856
[pr-883]: https://github.com/NVIDIA/k8s-test-infra/pull/883
[pr-912]: https://github.com/NVIDIA/k8s-test-infra/pull/912
[pr-1136]: https://github.com/NVIDIA/k8s-test-infra/pull/1136
[pr-2881]: https://github.com/NVIDIA/gpu-operator/pull/2881
[pr-2959]: https://github.com/NVIDIA/gpu-operator/pull/2959
