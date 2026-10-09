# MEP-0005: Node-wide kernel state delivery

Author: [Carlos Eduardo Arango Gutierrez](https://github.com/ArangoGutierrez)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
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
surfaces. Devices and libraries stay allocation-aware, so an unannotated
container still sees zero GPUs. Each mechanism ships behind its own alpha
feature gate, and with both gates off the behaviour is byte-identical to today.

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
| NRI CDI spec kernel mounts | only in `cdi` injection mode, and only when annotated |

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
- Fix [#911][issue-911] on GPU Operator v26.7.1 from the Mokka side, without
  depending on [gpu-operator#2959][pr-2959] merging upstream.
- Fix [#793][issue-793] with the same mechanism.
- Keep devices and libraries allocation-aware, per [#884][pr-884]: an
  unannotated container continues to see zero GPUs.
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
- Loading a real kernel module on CI hosts.
- Creating `/dev/dxg`, which would make a Linux node claim to be WSL.
- CI canary work against unpinned GPU Operator releases. That is tracked
  separately and deliberately excluded here.

## Proposal

Split the simulated surface into two tiers with different delivery rules.

**Kernel tier, unconditional and node-wide.** `/proc/modules`, `/sys/module`,
`/proc/driver/nvidia` and the PCI sysfs pair. Delivered to every container the
NRI plugin serves, subject only to the existing excluded namespaces
(`kube-system`) and the existing opt-out annotation. There is no opt-in,
because a real GPU node has none.

**Allocation tier, opt-in and unchanged.** The `/dev/nvidia*` nodes and the
mock libraries. These stay gated on `nvml-mock.nvidia.com/devices` and become
allocation-aware under [#884][pr-884].

What makes the kernel tier safe to deliver unconditionally is that it is
read-only and carries no device nodes, so it cannot widen a container's GPU
allocation. It also uses plain mounts and a hook rather than CDI device
references, so MEP-0002's "exactly one source of CDI references per container"
invariant is untouched.

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
- **Feature gates are component-scoped.** Per [#883][pr-883], passing a gate to
  a component that does not register it fails that component's startup. Any
  gate read by both the node agent and the NRI plugin must be registered in
  both.
- **`buildNRISpec` is left unchanged.** Moving its kernel mounts onto the
  adjustment path would be an ungated behaviour change, and gate-off must mean
  today's behaviour exactly.

### Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| A hook that cannot be executed fails container creation for every pod on the node | The plugin stats the hook binary before injecting a reference to it, in the same shape as the existing `cdiSpecStaged` check |
| A mount whose source is missing fails the whole pod | Every mount source is stat'd before it is emitted. `pciSysfsMounts` guards the same hazard today by gating on the condition its tree is rendered under; a stat is used here because the kernel tier has no single such condition |
| A hook that hangs exceeds its OCI `timeout` and still fails the container | The injected hook carries an explicit `Timeout`, and its work is bounded to two mounts with no network, locks or unbounded allocation. Residual risk is accepted and documented |
| An image redirects a mount target through a symlink, and the hook mounts onto a host path as root | `openat2` with `RESOLVE_IN_ROOT|RESOLVE_NO_MAGICLINKS`, then mounting through `/proc/self/fd/N`. See [Path safety](#path-safety) |
| The staged hook binary becomes the highest-value target on the node | It is root-owned under the agent's overlay root, written only by the agent DaemonSet, and gated separately from the sysfs tier |
| An annotated container in `cdi` mode receives the same read-only bind twice | Expected to be benign because source, destination and options are identical, but **unverified**. The e2e asserts it explicitly rather than assuming it |
| Feature gate skew between the agent and the plugin | Safe in both directions because of the stat-before-inject check: worst case is no injection, never a broken pod |

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
`internal/nri` code path (a throwaway plugin was used), and interaction with
[#884][pr-884]'s `selectSurfaces`.

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
mounts for every served container. It does not consult the device annotation
and does not consult `DeviceInjectionMode`, which is what makes the tier
node-wide. Today those mounts reach a container only when it is annotated and
the plugin is in `cdi` mode, which is why [#884][pr-884] combined with
[gpu-operator#2959][pr-2959] still fails on default settings.

Each source is stat'd before it is emitted. A mount with a missing source fails
the entire pod, so a surface the node agent has not staged yet degrades the
injection instead of blocking container creation. That is the fail-open
contract the `inject` package already documents for its steps: nothing orders
the plugin's DaemonSet after the agent's, so any surface may legitimately be
absent when a container is created.

### Mechanism B: createContainer hook

A small binary shipped in the Mokka image and staged by the node agent onto the
host under the overlay root, in the same way `stage.go` already stages
`nvidia-smi` and the `lsmod` script. It has to be a host path: the hook's
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

Two gates from [#883][pr-883], both `StageAlpha` and therefore disabled by
default:

| Gate | Read by | Covers |
| --- | --- | --- |
| `KernelState.Sysfs` | NRI plugin | the adjustment-path sysfs and `lsmod` mounts |
| `KernelState.Procfs` | node agent and NRI plugin | staging the hook binary, mirroring `/proc/driver`, and injecting the hook reference |

`KernelState.Procfs` is registered in both components and must appear in both
`featureGates.nodeAgent` and `featureGates.nri`, because a gate sent to a
component that does not register it fails that component's startup.

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
| Kernel mounts emitted for an unannotated container | [#911][issue-911]: kernel state hidden behind the allocation opt-in | delete the new step |
| Gate off yields an adjustment identical to the current release | a gated feature that is not actually gated | make the step ignore the gate |
| Missing staged source is skipped, not emitted | a missing mount source fails the whole pod | delete the stat |
| Missing hook binary means no hook reference is injected | an unexecutable hook kills every pod on the node | delete the stat |
| The injected hook carries a `Timeout` | a wedged hook hangs container creation unbounded | drop the `Timeout` |

The existing `TestAdjust(Suppresses|CDIMode|RawMode)` set must stay green, so
that kernel state does not perturb device injection.

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

**End to end.** kind, both gates on, GPU Operator v26.7.1, with this lane
overriding the v26.3.3 pin that the default lane keeps.

1. An unannotated pod running a statically linked binary reads `^nvidia ` from
   `/proc/modules` and reads `/proc/driver/nvidia/version`, with `LD_PRELOAD`
   empty.
2. That same pod sees zero `/dev/nvidia*`, so the tier boundary holds.
3. The v26.7.1 operands start: the unchanged #2881 check passes.
4. With both gates off, the operands hang exactly as they do today.
5. An annotated container in `cdi` mode is inspected for duplicate mounts.

The two tiers are separately falsifiable, which is what makes this more than a
smoke test. Removing the procfs tier fails the #2881 check. Removing the sysfs
tier fails `grep -qsx live /sys/module/nvidia/initstate`, which is the check
[gpu-operator#2959][pr-2959] proposes. Each tier is pinned to a distinct
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

## Alternatives

**Wait for [gpu-operator#2959][pr-2959].** It changes the operand gate to read
`/sys/module/nvidia/initstate`, which Mokka can already serve. Rejected as a
dependency: it is unmerged and needs review, it does nothing for the v26.7.1
release that is broken now, and asking upstream to accommodate Mokka is not a
short-term strategy. It remains worth supporting on its own merits, since
`/proc/modules` lists a module while it is still loading. Note that #2959 alone
is not sufficient either: it passes only for annotated containers in `cdi`
mode, and [#884][pr-884] keeps `raw` as the default.

**Keep pinning the GPU Operator chart.** [#912][pr-912] pins v26.3.3 and
unblocks CI today. Rejected as a fix: a pin buys a green pipeline, not the
ability to test the operator release that customers run.

**Extend the `LD_PRELOAD` shim to cover `/proc/driver/nvidia`.** Cheap, and it
covers C consumers such as `nvidia-modprobe`. Rejected as the primary fix: it
cannot reach a statically linked consumer, which is exactly the case in
[#911][issue-911], and Go consumers issue `openat` directly.

**Load a real dummy `nvidia` kernel module on CI hosts.** Rejected: it puts
kernel code and per-kernel builds into a test tool, and it cannot run on Docker
Desktop or managed nodes. It remains viable as an optional CI-only extra.

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
[pr-883]: https://github.com/NVIDIA/k8s-test-infra/pull/883
[pr-884]: https://github.com/NVIDIA/k8s-test-infra/pull/884
[pr-912]: https://github.com/NVIDIA/k8s-test-infra/pull/912
[pr-2881]: https://github.com/NVIDIA/gpu-operator/pull/2881
[pr-2959]: https://github.com/NVIDIA/gpu-operator/pull/2959
