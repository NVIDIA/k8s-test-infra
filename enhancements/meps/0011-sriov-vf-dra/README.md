# MEP-0011: Simulate SR-IOV network VFs for the SR-IOV DRA driver

Author: [Giulio Calzolari](https://github.com/giuliocalzo)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Background: how the driver discovers VFs](#background-how-the-driver-discovers-vfs)
  - [What Mokka does today](#what-mokka-does-today)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Spike evidence](#spike-evidence)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Profile configuration](#profile-configuration)
  - [State compilation](#state-compilation)
  - [Rendering the PF/VF tree](#rendering-the-pfvf-tree)
  - [The net class tree](#the-net-class-tree)
  - [NRI injection](#nri-injection)
  - [Helm values](#helm-values)
  - [Guide](#guide)
  - [Test plan](#test-plan)
  - [Compatibility](#compatibility)
  - [Implementation](#implementation)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

This MEP lets a profile declare SR-IOV-capable network adapters: physical
functions (PFs) carrying a fixed number of virtual functions (VFs). The node
agent renders them into the kernel-path PCI tree it already serves, together
with a new `sys/class/net` subtree, and the NRI plugin bind-mounts those paths
into any pod that opts in with an annotation. The
[SR-IOV DRA driver](https://github.com/k8snetworkplumbingwg/dra-driver-sriov)
(`dra-driver-sriov`, k8snetworkplumbingwg) then discovers the VFs unmodified,
publishes them as `ResourceSlice` devices, and the scheduler places pods that
claim them through `ResourceClaim`s — on CPU nodes with no NIC hardware.

Success for this MEP is the DRA control-plane path: devices advertised, claims
allocated, pods scheduled, and the driver's CDI prepare completed. Moving a VF
netdev into the pod's network namespace is a kernel operation that no sysfs
fake can satisfy, so the claimed pod does not reach `Running`; the guide and
the e2e scenario assert allocation, not networking.

## Motivation

Dynamic Resource Allocation went GA in Kubernetes 1.34, and networking is its
second major consumer after GPUs. `dra-driver-sriov` is the k8s networking
working group's DRA driver for SR-IOV NICs: it discovers VFs on each node,
advertises them with a rich attribute set (vendor, device, PF, NUMA node,
PCIe root, link type), and allocates them to pods through structured
parameters. Anyone building or testing on top of it — scheduler policies,
`SriovResourcePolicy` configurations, topology-aware alignment between GPUs
and NICs — needs nodes with SR-IOV hardware today.

That is exactly the gap Mokka exists to close. Mokka already simulates the
compute side of a GPU node (NVML, MIG, vGPU, IMEX) and the fabric side
(InfiniBand HCAs, NVLink). The host-network side — the ConnectX Ethernet
ports that every DGX/HGX board carries, split into VFs for container
networking — is the missing device class. Simulating it makes Mokka useful
for the DRA networking ecosystem at any scale a Kind cluster can host, and
lets GPU-plus-NIC alignment scenarios (a VF claimed from the NIC nearest the
allocated GPU) run on laptops and CI runners.

### Background: how the driver discovers VFs

The driver's kubelet plugin discovers devices once at startup, from plain
file reads. It enumerates `/sys/bus/pci/devices` through
[ghw](https://github.com/jaypipes/ghw) and keeps devices whose `class` is
`0x02` (network). For each candidate it:

1. skips the device if `physfn` exists (that marks a VF);
2. resolves the PF netdev name — `sriovnet.GetUplinkRepresentor` first, which
   on a non-switchdev device returns `ErrRepresentorNotFound`, and the driver
   then falls back to listing the PF's `net/` directory;
3. reads `numa_node`, resolves the PCIe root by following the device's
   `/sys/bus/pci/devices/<bdf>` symlink, and reads the link type from
   `/sys/class/net/<ifname>/type`;
4. queries the eswitch mode over devlink netlink, falling back to `legacy`
   on any error;
5. lists the PF's `virtfnN` symlinks; each target's basename is a VF address,
   and each VF contributes its `device` id and its index `N`.

Every step either reads a file Mokka can render or degrades gracefully when
the kernel interface behind it is absent. ghw additionally parses each
device's `modalias`, and its PCI database comes from `hwdata`, which the
driver image bundles.

Discovery is only half the story: nothing is advertised until a
`SriovResourcePolicy` in the driver's namespace matches the node and the
device. Advertisement is opt-in by design, so the guide has to create a
policy and its `DeviceAttributes`.

### What Mokka does today

Two renderers produce sysfs trees, with different consumers in mind:

- `internal/pcisysfs` renders the kernel-path PCI tree under the node
  overlay (`/var/lib/nvml-mock/sys/devices` plus the
  `sys/bus/pci/devices` BDF symlinks). GPUs and NVSwitches live here. Go
  consumers reach it through real mounts: the `nvidia.com/gpu` CDI spec maps
  `sys/devices` and `sys/bus/pci/devices` over the container's `/sys`
  (`internal/agent/cri/spec.go`).
- `internal/ib/sysfs` renders the InfiniBand class tree under `<overlay>/ib`.
  C tools reach it through the `libibmocksys` LD_PRELOAD shim. Its HCAs
  deliberately carry no `physfn`/`virtfn` entries, and they are absent from
  the kernel-path PCI tree.

Neither tree contains a network-class PCI function a Go binary can see, no
`sys/class/net` exists at a kernel path, and the NRI plugin injects no `/sys`
mounts. A Go consumer that reads network sysfs therefore sees only the real
(NIC-less) node. That is the gap this MEP fills.

### Goals

- A profile can declare SR-IOV NICs: how many PFs a node carries and how many
  VFs each PF exposes.
- The rendered tree satisfies `dra-driver-sriov` discovery end to end,
  including the `pcieRoot` and `numaNode` attributes, with NICs placed on the
  PCIe bus adjacent to the GPUs they serve, the way DGX/HGX boards do.
- A pod opts into the fake network sysfs with one annotation, with no
  patches to the driver's chart beyond `podAnnotations`.
- A runnable scenario guide: Kind cluster, Mokka, `dra-driver-sriov`, a
  resource policy, and a pod whose `ResourceClaim` is allocated and
  scheduled.
- An e2e scenario asserting the published `ResourceSlice` contents and the
  claim allocation.

### Non-Goals

- **A claimed pod reaching `Running`.** The driver's NRI hook invokes
  sriov-cni, which must find a real kernel netdev and move it into the pod's
  netns, then apply VF properties (spoofchk, VLAN) through PF netlink
  operations. No sysfs fake satisfies netlink. A stub CNI shipped by Mokka
  could close this later; see [Alternatives](#alternatives).
- Switchdev/eswitch mode. Rendered NICs are `legacy`; the devlink query
  degrades to that on its own.
- RDMA-capable VFs. Discovery consults rdmamap, finds nothing, and publishes
  `rdmaCapable: false`. Pairing VFs with the simulated IB HCAs is future
  work.
- VF driver rebinding (`vfio-pci`), Multus mode, and VF netdevs. Discovery
  needs none of them.
- Runtime VF reconfiguration. Like MIG and vGPU, the layout is static per
  profile; a config change re-renders through the normal agent reconcile.
- Projecting SR-IOV topology through the control-plane CRD
  (`SGPURackProfile`). `ControlPlaneSource` is still a stub (#614).

## Proposal

Extend the kernel-path PCI tree, not the IB tree. SR-IOV NICs become
first-class PCI functions in `internal/pcisysfs`, because that tree already
has the two properties discovery needs: real BDF symlinks into
`sys/devices/<rc>/` (so the driver's `pcieRoot` readlink works) and an
existing mount path into containers. A new `sys/class/net` subtree joins it
under the same overlay.

Delivery reuses the NRI plugin's annotation pattern: a pod annotated
`nvml-mock.nvidia.com/sriov=true` gets three read-only bind mounts —
`sys/devices`, `sys/bus/pci/devices` and `sys/class/net` from the overlay
onto the container's `/sys` paths. The driver's chart already exposes
`kubeletPlugin.podAnnotations`, so a values file is the entire integration.

The profile grows a top-level `sriov` block, a sibling of `infiniband`,
compiled into the agent state the same way (`compileNetwork`'s pattern) and
staged by the existing `pcibus` simulator, which owns the kernel-path tree.

### User Stories

#### Story 1: DRA networking on a laptop

A developer working on `SriovResourcePolicy` matching rules installs Mokka on
Kind with `sriov.enabled=true`, installs `dra-driver-sriov` with one
annotation in its values, and iterates on policies against four fake
ConnectX VFs per node — `kubectl get resourceslices` shows the effect of each
policy edit in seconds, with no lab hardware booked.

#### Story 2: Scale testing ResourceSlice publication

A scheduler engineer spins up 200 Kind workers, each advertising 8 VFs, and
measures apiserver and scheduler behaviour with 1,600 network devices in
`ResourceSlice`s — the 100k-node ambition Mokka holds for GPUs, applied to
DRA networking.

#### Story 3: GPU/NIC topology alignment

A platform team testing topology-aware allocation claims a GPU and a VF in
one `ResourceClaim` and asserts both devices report the same `pcieRoot`,
because Mokka placed the NIC on the PCIe bus adjacent to the GPU, as on the
real board.

### Spike evidence

A spike (2026-11-11) ran `dra-driver-sriov:latest` unmodified on a Kind
v1.37 cluster against a hand-built tree of exactly the files listed in the
[Background](#background-how-the-driver-discovers-vfs), bind-mounted over the
plugin container's `/sys/bus/pci/devices` and `/sys/class/net`:

| Stage | Result |
|---|---|
| Discovery | 1 PF + 4 VFs, correct vendor/device/NUMA/link type |
| sriovnet | `ErrRepresentorNotFound` → sysfs fallback, as read from source |
| devlink | error swallowed, eswitch mode `legacy` |
| ResourceSlice | published, full attribute set |
| ResourceClaim | `allocated,reserved`; pod scheduled |
| CDI prepare | env vars and NAD config with injected `deviceID` |
| Pod sandbox | fails in sriov-cni, as expected (see Non-Goals) |

The only blemish was an empty `pcieRoot`, because the spike's device entries
were plain directories; `pcisysfs` renders real symlinks, which fixes it —
and is itself a reason to render NICs there rather than in a standalone tree.

### Notes/Constraints/Caveats

- Discovery runs once at plugin startup, with no re-scan. The NRI plugin's
  staging gate (`inject.Gated`) already holds container creation until the
  agent reports staged, which orders the render before the driver starts. A
  profile change that re-renders the tree needs a driver pod restart to be
  seen — the same constraint real hardware reconfiguration has.
- Mounting the overlay's `sys/devices` whole over the container's
  `/sys/devices` hides the host's other device classes from that container
  (the known #689 trade-off). For the driver pod this is deliberate: ghw
  must enumerate only the fake bus. The narrower `sys/class/net` mount hides
  the node's real netdevs from the plugin's file reads; the driver only
  resolves names and link types through it, and its netlink sockets are
  unaffected, so `hostNetwork` keeps working.
- The fake NICs also become visible to GPU workloads through the existing
  CDI mounts, exactly as NVSwitches already are. `lspci` in a consumer pod
  gains network functions; this is realistic and intended.
- The driver has no tagged release; the guide pins the image by digest and
  states the Kubernetes floor (1.34, `resource.k8s.io/v1`).

### Risks and Mitigations

- **The driver is v1alpha1 and moving.** Discovery could start reading files
  this MEP does not render. Mitigation: the rendered surface is the kernel's
  SR-IOV ABI, not the driver's current read set — `sriov_totalvfs`,
  `sriov_numvfs` and the `driver` symlink are rendered even though today's
  driver skips them. The e2e scenario runs against a pinned digest; bumps
  are deliberate.
- **Mount-over-sysfs behaviour differs across runtimes.** The spike verified
  kubelet hostPath mounts over `/sys` subpaths on containerd/Kind; NRI bind
  mounts take the same kernel path. Other runtimes are covered by the
  guide's troubleshooting section, not by promises.
- **Annotation opt-in is manual.** If the user forgets the annotation the
  driver discovers no net-class devices and exits with a discovery error.
  The guide makes the annotation step explicit and the troubleshooting
  section names the symptom.

## Design Details

### Profile configuration

A new top-level profile block, sibling of `infiniband`, following its
defaulting and validation pattern (`internal/ib/config` as the model):

```yaml
# =============================================================================
# SR-IOV NICs - host-network adapters carved into VFs for the SR-IOV
# DRA driver. Rendered into the kernel-path PCI tree (sys/devices,
# sys/bus/pci/devices) and a kernel-path sys/class/net.
# =============================================================================
sriov:
  enabled: true
  nics_per_gpu: 1          # PFs = gpu.count * nics_per_gpu ...
  nic_count: 0             # ... unless overridden outright (0 = derive)
  vfs_per_nic: 4           # virtfn0..N-1 on every PF
```

The NIC identity is fixed in v1: a ConnectX-6 Dx Ethernet PF
(`15b3:101d`, class `0x020000`) with `15b3:101e` VFs — the same
one-identity-per-release simplification the IB renderer applies
(hard-coded ConnectX-5) and the spike validated against the driver. Making
the model configurable is additive later.

Validation rejects `vfs_per_nic < 1` and `vfs_per_nic > 6`: under the flat
single-slot layout `pcisysfs` renders, one PCI bus holds the PF at function
0, function 1 stays reserved (matching real ConnectX numbering), and VFs
occupy functions 2..7. Larger VF counts can later switch to multi-bus VF
placement; v1 keeps the bound.

### State compilation

`FileSource.compileState` gains a `compileSriov` step mirroring
`compileNetwork`: it re-unmarshals the profile bytes into the new config
type, applies defaults, and produces a comparable `agent.SriovShape`
(`Enabled`, `NICCount`, `VFsPerNIC`) on `agent.State`. The engine's
`YAMLConfig` stays unaware of the block, like `infiniband`.

### Rendering the PF/VF tree

`internal/pcisysfs` grows the SR-IOV vocabulary; `internal/agent/pcibus`
feeds it. No new simulator: the kernel-path tree must be pruned as one unit,
and `pcibus.Stage` already owns it.

Placement reuses the IB placement helpers (`nextFreeBus` and the netdev
naming in `internal/ib/sysfs/pci.go`, exported or moved to a shared
package): each PF takes the next free PCI bus after the GPU it serves, on
that GPU's root complex and NUMA node, with the IB HCAs' buses listed as
occupied so the two never collide. Without GPUs, PFs get distinct addresses
on an unknown NUMA node, as HCAs do.

Per PF at `<bdf>` (function 0), the renderer writes under
`sys/devices/<rc>/<bdf>/`, alongside the attribute files `pcisysfs` already
emits (`vendor`, `device`, `class`, `revision`, `numa_node`,
`local_cpulist`, `config`, ...):

```
modalias                      pci:v000015B3d0000101Dsv...bc02sc00i00
sriov_totalvfs                vfs_per_nic
sriov_numvfs                  vfs_per_nic
net/<ifname>/                 empty dir; the name is the systemd-style
                              netdev name derived from the BDF
virtfn0..N-1 -> ../<vf-bdf>   relative sibling links, kernel layout
```

Per VF (functions 2..N+1 on the PF's bus): the standard attribute files
with the VF identity, `modalias`, and `physfn -> ../<pf-bdf>`. Every PF and
VF gets its `sys/bus/pci/devices/<bdf>` symlink, which is what makes the
driver's `pcieRoot` resolution work. A `sys/bus/pci/drivers/mlx5_core/`
directory gains a symlink per function, mirroring `renderDriverBinding`'s
nvidia layout, so a future driver-based `resourceFilters` match has
something to read.

`prune` extends to the new entries; `Clear` already removes everything.
Disabling the block retracts the NICs on the next reconcile, exactly as an
emptied topology retracts GPUs.

### The net class tree

A kernel-path `sys/class/net/<ifname>/` is new (the existing one lives under
`<overlay>/ib` for the shim). The renderer emits, per PF:

```
type        1                 ARPHRD_ETHER; the driver maps it to "ethernet"
operstate   up
carrier     1
device -> ../../devices/<rc>/<pf-bdf>    relative, resolves inside the
                                         mounted overlay
```

The driver reads only `type`; `operstate`, `carrier` and the `device` link
cost nothing and keep the tree honest for `ip`-style tooling and for
sriov-cni's first reads (which the spike showed resolve
`<ifname>/device/sriov_numvfs` through exactly this link).

### NRI injection

A fourth independent opt-in joins `inject.Config`
(`internal/nri/inject/adjust.go`), default annotation key
`nvml-mock.nvidia.com/sriov`, flag `--sriov-annotation`, helm
`nri.sriovAnnotation` — the `infiniband` annotation is the template. An
annotated container receives three read-only, rprivate bind mounts:

| Overlay source | Container target |
|---|---|
| `/var/lib/nvml-mock/sys/devices` | `/sys/devices` |
| `/var/lib/nvml-mock/sys/bus/pci/devices` | `/sys/bus/pci/devices` |
| `/var/lib/nvml-mock/sys/class/net` | `/sys/class/net` |

plus the staging gate it shares with every other injection. A
`warnUnlessNICsStaged` check mirrors the IB one: injection with no staged PF
logs a warning naming the likely cause (profile without `sriov.enabled`).

No specialized-workload entry hardcodes the driver's chart labels; the
annotation keeps Mokka ignorant of who consumes the tree.

### Helm values

Following the `infiniband` precedent, the chart exposes the block under
`sriov.*` and folds it into the rendered profile through the `gpuConfig`
helper, with `values.schema.json` entries and template-time `fail` checks
for the validation bounds. `docs/helm-chart-values.md` is regenerated. The
block defaults to `enabled: false`; profiles that model DGX-class boards
(`h100.yaml`, `gb200.yaml`) may enable it in a follow-up once the guide
settles.

### Guide

`docs/guides/sriov-dra.md`, registered in the Scenarios table of
`docs/guides/README.md` ("SR-IOV DRA driver — mock NIC VFs published as
ResourceSlices, and a pod's claim allocated against them", ~10 min):

1. Kind cluster from the shared `kind.yaml` (NRI already enabled; k8s 1.34+
   noted as the floor).
2. Install Mokka with `sriov.enabled=true`.
3. Install `dra-driver-sriov` by digest, with a values file setting
   `kubeletPlugin.podAnnotations: {nvml-mock.nvidia.com/sriov: "true"}`.
4. Apply the NetworkAttachmentDefinition CRD, a `SriovResourcePolicy` and
   `DeviceAttributes` matching the mock vendor (`15b3`).
5. `kubectl get resourceslices -o yaml` — inspect the published VFs and
   their attributes.
6. Create a `ResourceClaimTemplate` and a pod; verify the claim reports
   `allocated,reserved` and the pod is scheduled to the Mokka node.
7. A boxed note states plainly why the pod stays in `ContainerCreating`
   (sriov-cni needs a real netdev) and that this is the expected end state.
8. Troubleshooting: missing annotation (driver logs "could not retrieve PCI
   devices"), policy not matching (empty slice list), NRI socket absent.

### Test plan

- **Unit:** `pcisysfs` render/prune round-trips for PF/VF layouts (virtfn
  targets, physfn backlinks, function numbering, netdev naming, pruning on
  shrink); config defaulting and validation bounds; `compileSriov` hashing
  and comparability; NRI adjust tables gain the annotation cases.
- **Helm:** `make helm-tests` covers the values plumbing and the fail
  checks.
- **e2e:** `tests/e2e/go/scenario_sriov_test.go`, `Label("sriov")`,
  following `scenario_dra_test.go`: install the driver (pinned digest) and
  the policy, assert the `ResourceSlice` device count and attribute set,
  create a claimed pod, assert allocation and scheduling. New `e2e-sriov`
  Make target; `sriov` joins `E2E_DEFAULT_LABEL_FILTER`'s exclusions and the
  CI workflow matrix.
- The spike's reproduction script is superseded by the guide and deleted
  with the spike.

### Compatibility

The block is off by default; a profile without `sriov` renders byte-identical
trees to today's. The new mounts only touch annotated pods. The overlay
gains `sys/class/net` and new PCI functions, both invisible to existing
consumers until mounted. No API, CRD or shim changes; `resource.k8s.io/v1`
is required only by the guide's consumer, not by Mokka.

### Implementation

1. `pcisysfs`: SR-IOV rendering + `sys/class/net`, with the placement
   helpers extracted from `internal/ib/sysfs`.
2. Config block, `compileSriov`, `pcibus` feeding, helm plumbing.
3. NRI annotation + mounts + staging warning.
4. Guide and e2e scenario.

Each lands separately; the feature is inert until step 2 and unreachable by
consumers until step 3.

## Drawbacks

- One more device class to keep honest against a moving v1alpha1 consumer;
  the ABI-surface argument above contains, but does not remove, the drift
  risk.
- The demo ends at a pod that never starts. That is honest about what a
  sysfs simulation can do, but it is a weaker closing beat than the GPU
  guides' running workload, and users will ask why (the guide answers it).
- Whole-tree `/sys/devices` and `/sys/class/net` mounts make the annotated
  pod blind to the real node's devices. Fine for the DRA driver, surprising
  for a hypothetical consumer that wants both real and fake; such a consumer
  has no path in this design.

## Alternatives

- **Render NICs in the IB tree and extend the LD_PRELOAD shims.** Rejected:
  the driver is a Go binary; Go's raw syscalls bypass LD_PRELOAD (the same
  reason the NVIDIA DRA driver misses `pcieRoot` today, #265). Kernel-path
  mounts are the only interception that works here.
- **A standalone `sriov` sysfs tree with its own simulator.** Rejected: the
  PF/VF functions must live in the same `sys/devices`/`sys/bus/pci` tree the
  driver enumerates, next to the GPUs, or `pcieRoot` and topology alignment
  stories fall apart; splitting ownership of one tree between two simulators
  complicates pruning for no gain.
- **Patch the driver's DaemonSet from the guide instead of NRI injection**
  (what the spike did). Works, but every chart upgrade re-raises the patch,
  and Mokka's existing pattern — annotate and let NRI inject — is strictly
  less to maintain and applies to future sysfs-reading consumers too.
- **Ship a stub sriov-cni so the claimed pod reaches `Running`.** Deferred,
  not rejected: a Mokka-provided CNI that answers ADD/DEL with a dummy
  interface would complete the workload story and may become its own MEP.
  It is orthogonal to everything here — discovery and allocation neither
  need it nor change with it.
- **Model VF counts per-GPU in the control-plane CRD.** Blocked on
  `ControlPlaneSource` (#614); the profile block is the only live
  configuration channel today, and the CRD's `NetworkTopology` can adopt
  the same fields once projection exists.
