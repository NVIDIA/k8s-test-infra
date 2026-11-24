# MEP-0016: NIC Declaration and Simulation

Author: [Oleg Leizerov](https://github.com/olegleyz)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [The NIC model and the NIC instance](#the-nic-model-and-the-nic-instance)
  - [Configurations declare their result](#configurations-declare-their-result)
  - [Slots place PCI devices](#slots-place-pci-devices)
  - [The declaration stops at the boot state](#the-declaration-stops-at-the-boot-state)
  - [Generic generation and stable identifiers](#generic-generation-and-stable-identifiers)
  - [Where profiles live and how a cluster chooses them](#where-profiles-live-and-how-a-cluster-chooses-them)
  - [Checking declarations against hardware](#checking-declarations-against-hardware)
  - [User Stories](#user-stories)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [API](#api)
  - [Generation rules](#generation-rules)
  - [Node agent](#node-agent)
  - [Control plane](#control-plane)
  - [Helm chart](#helm-chart)
  - [Test plan](#test-plan)
  - [Implementation order](#implementation-order)
- [Open Questions](#open-questions)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Appendix A: Design validation](#appendix-a-design-validation)
- [Appendix B: Samples](#appendix-b-samples)
<!-- /toc -->

## Summary

Mokka has to show a node's NICs the way Linux shows real ones: PCI functions, RDMA devices, ports, netdevs and `/dev/infiniband`. This MEP proposes to **declare** NIC hardware as data, **generate** every OS surface from that declaration with rules that know nothing about a specific vendor or model, and **check** the result against captures of real nodes.

A new `SNICProfile` resource describes one NIC model and the firmware configurations it can run, each declared as its result. NIC slots in `SGPURackProfile` place NIC instances on a node and select a configuration. The node agent compiles the declaration into its desired state, as it already does for GPUs, and its simulators render the surfaces. The same profile files serve file mode and control-plane mode. A prototype of the schema and the generator is compared with five real captures, and every difference has a cause ([Appendix A](#appendix-a-design-validation)).

## Motivation

Today a node's NICs come from a small `infiniband` block in each GPU profile:

```yaml
# deployments/nvml-mock/helm/nvml-mock/profiles/gb300.yaml
infiniband:
  enabled: true
  hca_type: "MT4129"
  hcas_per_gpu: 1
  # …
```

That shape can't describe the NICs the captures show, and parts of it are wrong:
- Every HCA reports a ConnectX-5 PCI device ID (`0x1017`, fixed in `internal/ib/sysfs/render.go`).
- The NIC count is GPUs × `hcas_per_gpu`, all of one HCA type, so a node can't hold a front-end NIC next to its scale-out NICs.
- NIC functions are missing from the node's PCI bus tree (`/sys/bus/pci`); each HCA's PCI function exists only inside the InfiniBand tree.

Real nodes vary in ways software depends on:
- **One NIC across PCIe domains.** On GB300, each ConnectX-8 has its network function in one domain and its Data Direct function next to a GPU in another. Topology discovery pairs them through VPD.
- **The layout depends on the firmware configuration.** The same ConnectX-8 shows one PF on InfiniBand and four PFs on Ethernet.
- **Different kinds of NIC.** A BlueField-3 has a SoC function without an RDMA device, AWS EFA has no netdev of its own, and a cloud VM sees VFs without their PF.

[MEP-0014](../0014-network-simulation/README.md) describes the node-local network surfaces Mokka simulates. MEP-0015 (in review) declares fabrics and their topology. This MEP declares the NIC hardware those surfaces come from, and takes over the NIC sections of MEP-0015.

### Goals

1. NIC models and NIC instances are data. No rule in the resolver or the renderers names a vendor or a model. Driver reactions to operator actions are a driver's behavior, so they live in a module per driver instead.
2. NICs sit in the node's one PCIe topology, including NICs that span PCIe domains.
3. One file per NIC model serves both file mode and control-plane mode.
4. Generated surfaces match captures of real nodes, and every difference has a stated cause.
5. Identifiers are stable across runs and unique per node.

### Non-Goals

- Declaring surfaces that cluster software creates, such as VFs, switchdev representors and SMI devices. Mokka emulates the driver's reaction to those operations instead. This MEP places that emulation in the node agent; the behavior of each operation is for its own proposal, starting with VFs in MEP-0011 (in review). Operations that go through devlink or RDMA netlink, such as switchdev mode and SMI devices, are known gaps; [the operations table](#the-declaration-stops-at-the-boot-state) lists them.
- Fabric topology and network state: cabling, port state, LIDs, partitions. MEP-0015 covers them.
- Interpreting firmware parameters, or covering every possible firmware configuration.
- Physical connectors, optical modules, cable breakout and the mapping of lanes to connectors. `ports` are logical ports and their fabric-facing planes, not sockets.
- NIC fault injection.
- Built-in rack profiles, and delivering per-node state from the control plane, which belong to [MEP-0001](../0001-mokka-control-plane/README.md).

## Proposal

### The NIC model and the NIC instance

- **`SNICProfile`** is one NIC model as packaged on a card, for example `cx8-c8280z`. It holds what the NIC reports about itself: PCI IDs, firmware, HCA type, VPD. It also **defines** every firmware configuration the card can run. It holds nothing about a particular node: no addresses, no GUIDs, no NIC counts.
- **A NIC slot** in `SGPURackProfile` is one NIC instance on a node. It names the profile, can **select** one of its configurations by name (otherwise the default applies), says where each PCI device sits, and which GPUs the NIC serves.

```yaml
# SNICProfile cx8-c8280z, trimmed
kind: SNICProfile
metadata:
  name: cx8-c8280z
spec:
  identity:
    model: ConnectX-8
    hcaType: MT4131
    firmwareVersion: "40.47.3576"
    # …
  functionTypes:
    network:
      # … PCI IDs, driver mlx5_core, 16 VFs possible
    dataDirect:
      # … PCI IDs, driver mlx5_ib
  defaultConfiguration: ib-xdr-4plane
  configurations:
    - name: ib-xdr-4plane
      # …
    - name: eth-multiplane
      # …
```

```yaml
# SGPURackProfile gb300-ethernet: the slot names the profile and selects a configuration
nics:
  - slot: nic0
    profile: cx8-c8280z
    configuration: eth-multiplane
    board: 0
    # … device placement
```

The profile is the menu and the slot is the order: configuration data never lives in the slot, only the choice. A GB300 tray has four slots pointing at the same profile. Changing the NIC model or its mode changes the slot's reference; nothing is copied.

### Configurations declare their result

A configuration explicitly declares what Linux will show: these ports, these PCI functions with these classes, these RDMA devices. It doesn't carry firmware parameters for a device-specific generator to interpret. The generator stays device-agnostic: it turns whatever is declared into surfaces and never needs to know what a ConnectX or an EFA is.

```yaml
# SNICProfile cx8-c8280z: the same ASIC in two configurations
configurations:
  # NUM_OF_PF=1, LINK_TYPE_P1=IB(1), NUM_OF_PLANES_P1=4, MODULE_SPLIT_M0[0..3]=1.
  - name: ib-xdr-4plane
    ports:
      - {id: p0, technology: ib, width: 4, laneSpeed: XDR, planes: 4, capMask: "0xa751ec48"}
    functions:
      - {id: pf0, type: network, pciDevice: network, function: 0, class: "0x020700", ports: [p0]}
      - {id: dd0, type: dataDirect, pciDevice: dataDirect, function: 0, class: "0x080100"}
    rdmaDevices:
      - {id: rdma0, function: pf0, ports: [p0]}
  - name: eth-multiplane
    ports:
      - {id: p0, technology: roce, width: 2, laneSpeed: NDR, planes: 0}
      # … p1–p3 the same
    functions:
      - {id: pf0, type: network, pciDevice: network, function: 0, class: "0x020000", ports: [p0]}
      # … pf1–pf3 on functions 1–3, one port each
      - {id: dd0, type: dataDirect, pciDevice: dataDirect, function: 0, class: "0x080100"}
    rdmaDevices:
      - {id: rdma0, function: pf0, ports: [p0]}
      # … rdma1–rdma3, one per PF
```

Firmware settings don't map to a layout without device knowledge. With four planes, InfiniBand shows one PF with one 800G port, and Ethernet shows one PF per plane with a 200G port each. The settings stay as a comment for people. The cost is one declared configuration per mode someone simulates, not one per possible parameter combination. Full sample: [`cx8-c8280z`](#sample-cx8-c8280z).

### Slots place PCI devices

A NIC model has one or more PCI devices. Each function in a configuration says which device it's on (`pciDevice`) and its function number. The slot says where each device sits: its PCIe domain, its parent, the NIC's switch ports above it, its bus address and its NUMA node.

```yaml
# SGPURackProfile gb300-nvl72, slot nic0
- slot: nic0
  profile: cx8-c8280z
  board: 0
  gpus: [1]
  devices:
    network:
      rootComplex: pci0000:00
      bridges:
        - {address: "0000:01:00.0", parent: "0000:00:00.0"}   # parent: a root port
        # … five more switch ports
      address: "0000:03:00"
      parent: "0000:02:00.0"
      numaNode: 0
      vpdPosition: "-Prime"
    dataDirect:
      rootComplex: pci0009:00   # GPU 1's root complex
      # … switch ports, shown below
      address: "0009:03:00"
      parent: "0009:02:00.0"
      numaNode: 0
      vpdPosition: "-Aux[1]"
```

The configuration decides how many functions exist, so switching configuration changes the functions and never the placement: `ib-xdr-4plane` has one PF on the `network` device and `eth-multiplane` has four (`.0`–`.3`) on the same device.

Placing devices separately covers every way the captures show a NIC spanning PCIe domains:
- **GB300 ConnectX-8:** the network device in one domain, the Data Direct device next to its GPU in another ([`gb300-nvl72`](#sample-gb300-nvl72)).
- **A ConnectX-7 front-end card:** two x8 links in two domains, one port on each ([`cx7-hhhl`](#sample-cx7-hhhl), [`gb300-ethernet`](#sample-gb300-ethernet)).
- **BlueField-4:** two host links, each showing a PF for every port. A port belongs to one function per PCI device ([`bf4-vr`](#sample-bf4-vr)):

```yaml
functions:
  - {id: a0, type: network, pciDevice: hostA, function: 0, class: "0x020000", ports: [p0]}
  - {id: a1, type: network, pciDevice: hostA, function: 1, class: "0x020000", ports: [p1]}
  - {id: b0, type: network, pciDevice: hostB, function: 0, class: "0x020000", ports: [p0]}
  - {id: b1, type: network, pciDevice: hostB, function: 1, class: "0x020000", ports: [p1]}
```

**GPUs and NICs share one PCIe tree.** Root ports are declared once per node, and every GPU slot, NIC device and NIC switch port names its parent: a root port, or a switch port from any slot. On GB300 each GPU sits below a switch port of the NIC that serves it, the same switch that leads to the NIC's Data Direct function, so `rdma_topo`, `nvidia-smi topo -m` and DRA drivers that match GPUs to NICs by PCIe position see what the real node shows:

```yaml
# SGPURackProfile gb300-nvl72
topology:
  rootPorts:
    - {address: "0009:00:00.0", rootComplex: pci0009:00, id: {vendorID: "10de", deviceID: "22b1"}}
    # … one per root port on the node
  gpuSlots:
    - {index: 1, rootComplex: "pci0009:00", pciAddress: "0009:06:00.0", parent: "0009:05:00.0", numaNode: 0}
    # …
  nics:
    - slot: nic0
      devices:
        dataDirect:
          bridges:
            - {address: "0009:01:00.0", parent: "0009:00:00.0"}
            # … three more switch ports
            - {address: "0009:05:00.0", parent: "0009:04:00.0"}   # GPU 1's parent
```

NUMA attributes follow that topology; [MEP-0010](../0010-numa-aware-scheduling/README.md) proposes how NUMA is simulated. `gpus` records which GPUs the NIC serves, for locality reporting; it doesn't restrict which GPUs may use the NIC.

### The declaration stops at the boot state

The declaration describes what Linux shows after boot, before any cluster software runs: the hardware, the firmware configuration and the driver.

Everything cluster software creates stays out: VFs, switchdev representors, SMI devices, renamed netdevs, IPoIB child interfaces, GIDs from IP addresses, MTU. Where Mokka can see an operation, it emulates the driver's reaction, so the real operator runs unchanged and creates those surfaces itself. The profile only says how many VFs the hardware allows (`totalVFs: 16`), not how many are enabled.

For example, in the GB300 Ethernet capture, PF0's RDMA device has 72 ports. That is switchdev mode with a multiport eswitch, which an operator turned on: mlx5 gives each PF's vports (the host PF, 16 possible VFs and the uplink, so 18) to PF0's device. The declaration has four PFs with one port each. Producing the 72 would need Mokka to answer devlink, which it can't yet.

**Operations cluster software performs.** Whether Mokka can emulate the driver's reaction depends on the kernel interface the operation uses:

| Operation | Done by | Kernel interface | Mokka can see it | Status |
|---|---|---|---|---|
| Enable VFs | SR-IOV or NIC configuration operator | sysfs write to `sriov_numvfs` | Yes: the file is Mokka's | Planned, MEP-0011 |
| Switchdev mode, multiport eswitch | Network Operator, NIC configuration operator | devlink (generic netlink) | No | Known gap |
| VF representors | The driver, in switchdev mode | Follows switchdev | No | Known gap |
| SMI plane devices | A cluster DaemonSet (`rdma dev add … type SMI`) | RDMA netlink | No: MEP-0014 leaves RDMA netlink to the host kernel | Known gap |
| Device and netdev renames | udev, the `rdma` tool | rtnetlink, RDMA netlink | No | Known gap; names aren't compared |
| IPoIB child interfaces | `ip link add … type ipoib` | rtnetlink | No | Known gap |
| MTU, IP addresses | Network configuration, CNIs | rtnetlink | Once interfaces are kernel links (#1356) | The kernel applies them; RDMA GIDs must follow address changes |

Closing the known gaps needs Mokka to answer devlink and RDMA netlink, a change to MEP-0014's scope for a later proposal.

A few choices belong to the node image, not the NIC: whether the `umad` and IPoIB modules load, and the mode of `umad` files (600 on one image, 666 on another). They go in the rack profile:

```yaml
# SGPURackProfile gb300-ethernet
software:
  rdma: {umad: true, ipoib: true, umadMode: "666", driverVersion: 26.01-1.0.0}
```

Values the subnet manager or the fabric sets, such as port state, LIDs, partitions and the IPoIB MTU, are network state from MEP-0015.

### Generic generation and stable identifiers

Generation is a set of rules, one per family of surfaces ([Generation rules](#generation-rules)). Each reads only declared values. The only branch is on the declared port technology (`ib`, `roce` or `efa`).

Where hardware differs, the declaration says so instead of a rule guessing:

```yaml
# SNICProfile efa-c5n: what the efa driver reports, declared on the function type
efa:
  driver: efa
  rdmaNamePrefix: efa_
  rdmaNodeType: "7: unspecified"
  rdmaOmit: [hca_type, board_id, hw_rev]   # files the driver doesn't create
  rdmaEmpty: [fw_ver]                      # a file it creates empty
```

```yaml
# Slots: the system image GUID, where no rule fits every model
- slot: nic1                  # gb300-nvl72: the board's second ASIC
  systemImageGUID: distinct
- slot: frontend0             # vr-nvl72: BlueField-4
  systemImageGUID: rdma-b0    # the node GUID of RDMA device rdma-b0, on host link B's first PF
```

Identifiers (GUIDs, MACs, serial numbers, VPD `V3`, switch IDs) are hashes of where something sits: the node's coordinates, then the slot, and within it the PCI device, function number and port number, or the board and board position. So:
- the same declaration gives the same identifiers on every run;
- removing one NIC doesn't change the others';
- different nodes get different identifiers;
- things at the same coordinate share an identifier. The profile says which VPD keyword is shared at which level: one serial number per board, one `V3` per ASIC (its PF and its Data Direct function), one `VU` per board position.

```yaml
# SNICProfile cx8-c8280z
vpd:
  identities: {SN: board, V3: asic, VU: boardPosition}
```

### Where profiles live and how a cluster chooses them

Each NIC model is one file in the nvml-mock chart, holding a full `SNICProfile`. Both modes read that file, and there is no other copy.

- **Built-in or custom.** A profile shipped in the chart is built-in; one an administrator adds is custom. They share the schema and validation. The only difference is ownership: Helm owns built-in profiles, so `helm upgrade` resets edits to them, and a lasting change uses a custom profile under its own name. For example, a cluster whose front-end ConnectX-7 is an OEM variant, with the OEM's part number and board ID, adds its own profile.
- **File mode.** A GPU profile, such as `gb300`, carries the node's NIC slots, its root ports and each GPU's parent (`nics.slots`, `nics.rootPorts`, `nics.gpuParents`) in place of today's `infiniband` block; the GPU addresses stay where the GPU profile already declares them. Helm values override a slot's profile or configuration and add custom profiles.
- **Control-plane mode.** The chart renders the files as `SNICProfile` objects. A rack profile's slots name them, and a rack group in `SGPUInventory` may override a slot (`nicOverrides`), so clusters with the same GPU nodes can use different NICs or modes without a new rack profile.
- **Selection order.** The rack group's override, then the slot's configuration, then the profile's default. The fabric doesn't choose a configuration, because one technology can have several; it only validates the choice.
- **Changes.** Profiles are mutable. An edit reaches every rack that uses the profile, as an `SGPURackProfile` edit does. Selecting another configuration is a simulated firmware reset: the node's NIC functions are removed and created again in the new layout. Placement doesn't change, so the GPU association and Data Direct pairing stay. As on real hardware, the reset clears what cluster software created, and the operators create it again.

### Checking declarations against hardware

`mokka-capture collect network` records a node's network surfaces as plain-text files, one per source (RDMA sysfs, the NIC functions' PCI sysfs, VPD fields, RDMA character devices, firmware configuration and others), in the node's capture directory. `mokka-capture nic-surfaces <node dir>` turns them into one list of object, attribute, value and source file, keyed by what the hardware fixes, such as a PCI function's address. `mokka-capture diff nic <real node dir> <mock node dir>` compares two capture directories ([#1353](https://github.com/NVIDIA/k8s-test-infra/pull/1353), in review). How the generator's output reaches `diff nic` is still open ([Open Questions](#comparing-a-declaration-with-a-capture)).

```
# mokka-capture nic-surfaces tests/hardware-captures/gb300-ib/node-a (scrubbed capture)
pci/0000:03:00.0	class	0x020700	pci-nic-sysfs.txt
pci/0000:03:00.0	upstream	0x10de:0x22b2,0x15b3:0x197c,0x15b3:0x197c	pci-nic-sysfs.txt
pci/0000:03:00.0	vpd.V3	00000000000000000000000000000001	pci-vpd-fields.txt
pci/0009:03:00.0	class	0x080100	pci-nic-sysfs.txt
pci/0009:03:00.0	upstream	0x10de:0x22b1,0x15b3:0x197c,0x15b3:0x197c	pci-nic-sysfs.txt
pci/0009:03:00.0	vpd.V3	00000000000000000000000000000001	pci-vpd-fields.txt
```

The comparison checks relations, not identifier values: that a Data Direct function's `V3` equals its PF's, that both ASICs on a board share one serial number, whose node GUID a system image GUID is (its own, another function's or none), and whether a BlueField's VPD `VU` starts with its serial number. It makes one check per attribute that either side has. Every difference needs a cause in a causes file per capture:
- **schema gap:** the schema can't express it yet;
- **rule bug:** a generation rule is wrong;
- **capture blind spot:** the capture doesn't record it: the capturing pod lacked the tool, the connection dropped, or the source file is absent. A tool that ran and failed counts as captured;
- **by design:** outside the declaration on purpose: cluster software, network state;
- **deferred:** compared later, at the rendered level or once a planned feature exists, such as the attributes the node agent renders. `diff nic` has the first four today; [#1268](https://github.com/NVIDIA/k8s-test-infra/issues/1268) adds this one.

An accepted gap and a planned feature can both explain a difference, but they are different commitments, so they get different causes.

```yaml
# The first matching entry gives a difference its cause
- match: '^\S+ \S+/(0000|0002|0010|0012):03:00\.4(/|$)'
  cause: by design
  note: a VF that cluster software enabled on PF0; VFs aren't declared
```

A difference without a cause fails the check. [Test plan](#test-plan) lists the levels at which this runs.

### User Stories

#### Story 1: one rack, two fabrics

A platform team simulates two GB300 clusters, one on InfiniBand and one on Ethernet. Both inventories use the `gb300-nvl72` rack profile; the Ethernet inventory's rack group selects `eth-multiplane` for slots `nic0`–`nic3`. Each node shows the PFs, RDMA devices and netdevs its real counterpart does.

#### Story 2: adding a NIC model

A developer brings up a node with a NIC Mokka doesn't ship. They run `mokka-capture collect network` on it, or capture read-only through a pod the cluster already runs with `-exec-in`, write an `SNICProfile` and a slot, and run the conformance check until every difference has a cause. The profile then ships as built-in.

#### Story 3: an OEM variant

An administrator's cluster uses an OEM ConnectX-7 card with its own part number and board ID. They add a custom profile under a new name and point the slot at it, in values or as an object. `helm upgrade` leaves it alone.

#### Story 4: testing a network operator

An SR-IOV operator runs against a simulated node. It finds the PFs in their boot state, writes `sriov_numvfs`, and Mokka's driver emulation creates the VFs as the real driver would. Operators that switch the eswitch to switchdev or create SMI devices can't run against a simulated node yet; they're known gaps.

### Notes/Constraints/Caveats

- **Slots exist in two places.** File mode has no rack profile, so its slots live in the GPU profile; control-plane mode has them in the rack profile. A unit test keeps the two equal until built-in rack profiles exist.
- **Names aren't identities.** Linux-visible names follow the node: netdev names come from the image's naming scheme, and RDMA device names from probe order, so they shift when an earlier NIC is removed, as on real nodes. Stable identity is the coordinate-derived identifiers (GUIDs, MACs, serial numbers). Names aren't compared. The generator uses predictable names; a per-image naming setting can come later.
- **Capture coverage.** `collect network` records PCI, VPD and interface records only for ConnectX functions (PCI vendor `0x15b3`), so EFA functions are missing; RDMA devices are captured whatever their vendor. A missing sysfs file is recorded as absent and an empty one as empty. [#1268](https://github.com/NVIDIA/k8s-test-infra/issues/1268) tracks the remaining gaps. Captures of GB200 and of an InfiniBand cluster without SMI devices don't exist yet.
- **GPU parents in captures.** `collect network` doesn't record a GPU's parent yet (GPU records hold the device, NUMA node and root complex; #1268), so the samples take it from bus numbers and the captured PCI hierarchy. The GPU profiles place GPUs at their captured addresses (on `gb300`, domains `0008`, `0009`, `0018` and `0019`), so file mode builds the same tree.
- **GPU slots and `GPU_COUNT`.** In file mode a slot's `gpus` may name any GPU the profile declares, so lowering `GPU_COUNT` never invalidates a slot. A NIC can then be associated with a GPU the node doesn't show.
- **Platforms without a captured NIC.** A GPU profile moves to slots once its NICs have a captured profile. Until then it keeps today's `infiniband` block, and the node agent keeps the current path for it, so those platforms keep their NICs and the IB end-to-end tests keep running.

### Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Declarations drift from real hardware | Conformance against captures, with a cause for every difference, plus deliberate breakages that must be caught |
| The two copies of a platform's slots diverge | A unit test compares them |
| Two nodes report the same GUIDs | Node coordinates are part of every identifier |
| Putting NIC functions in the shared PCI tree breaks GPU consumers | The rendered-level check and the existing `pcibus` tests |
| Control-plane delivery isn't built | File mode ships first; control-plane NIC state lands with per-node delivery |
| A profile ships without real evidence | Only profiles with a capture that passes conformance ship as built-in |

## Design Details

### API

The Go types are the schema; `controller-gen` generates the CRDs from them, and a unit test strictly decodes every shipped profile file with them. Below, `?` marks an optional field and `# new` marks a field added to an existing resource.

#### `SNICProfile` (new, cluster-scoped)

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SNICProfile
metadata:
  name: string                      # what slots refer to, such as cx8-c8280z
spec:
  identity:                         # what the NIC reports about itself, the same in every configuration
    model: string                   # model name, such as ConnectX-8
    hcaType: string?                # RDMA hca_type, such as MT4131
    boardID: string?                # RDMA board_id, such as MT_0000001513
    firmwareVersion: string?        # RDMA fw_ver; a slot may override it
    partNumber: string?             # board part number; the default for VPD PN
    hwRev: string?                  # RDMA hw_rev and ibv_devinfo hw_ver; empty means 0x0
    vendorOUI: string?              # IEEE OUI that verbs report as vendor_id, such as "0x02c9"
  sources: [string]?                # captures or documents the values come from
  switchPort: {vendorID, deviceID}? # IDs of the NIC's own PCIe switch ports, which slots place as bridges
  vpd:                              # optional: vital product data, on function types with vpd: true
    keywords: {string: string}?     # model values by keyword (PN, EC, V2…); "name" is the product name, to which a device's vpdPosition is appended
    identities: {string: scope}?    # keywords whose value is a generated identifier, and which scope shares one value: board | asic | boardPosition
  functionTypes:                    # kinds of PCI function, keyed by a name configurations use
    <name>:
      vendorID: string              # four lowercase hex digits, no 0x
      deviceID: string
      subsystemVendorID: string?
      subsystemDeviceID: string?
      driver: string                # bound kernel driver, such as mlx5_core
      rdmaNamePrefix: string?       # kernel's RDMA device name prefix, such as mlx5_
      rdmaNodeType: string?         # node_type the driver reports; empty means "1: CA"
      rdmaOmit: [string]?           # RDMA attributes the driver doesn't create: the file is absent. Any of fw_ver, hca_type, board_id, hw_rev
      rdmaEmpty: [string]?          # RDMA attributes the driver creates with an empty value. Same choices as rdmaOmit
      totalVFs: int?                # sriov_totalvfs; 0 means no SR-IOV. Enabling VFs is cluster software's job
      vfType: string?               # the functionTypes key of the VFs this function can enable; needs totalVFs
      revision: string?             # PCI revision ID, such as "0x00"
      pcieLink:                     # optional: the link the function trains to; omitted when no capture shows it, and then no link attributes are generated
        speed: string               # as sysfs reports it, such as "64.0 GT/s PCIe"
        width: int
        maxWidth: int?              # only when it differs from width, as on a VF reporting 0 of 255
      vpd: bool?                    # whether the function exposes the profile's VPD
      vpdSuffixes: {string: string}? # appended to an identity keyword's value on this function; {function} is the PCI function number
      vpdOmit: [string]?            # identity keywords this function doesn't report
  defaultConfiguration: string      # used when neither the cluster nor the slot selects one
  configurations:                   # firmware configurations, each declared as its result
    - name: string
      ports:                        # optional: logical network ports
        - id: string
          technology: ib | roce | efa
          width: int                # lanes in the rate text, such as 4 in "800 Gb/sec (4X XDR)"
          laneSpeed: SDR | DDR | QDR | FDR10 | FDR | EDR | HDR | NDR | XDR
          planes: int               # fabric planes the port carries; 0 is a single endpoint
          capMask: string?          # capability mask of an ib or efa port, which firmware sets; roce follows a rule
          lmc: int?                 # LID mask count; empty means 0
      functions:                    # PCI functions this configuration produces
        - id: string
          type: string              # a functionTypes key
          pciDevice: string         # the PCI device a slot places, such as network or hostA
          function: int             # PCI function number within that device
          class: string             # PCI class code this configuration gives the function, such as "0x020700"
          ports: [string]?          # ports this function owns; one owner per PCI device
      rdmaDevices:                  # optional: RDMA devices the driver creates, at most one per function
        - id: string
          function: string          # a function id
          ports: [string]           # port k of the device is the k-th entry
```

#### `SGPURackProfile` (existing; new fields)

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPURackProfile
spec:
  software:
    rdma:                           # new: the node image's optional RDMA modules
      umad: bool                    # ib_umad loaded: umad and issm files, the infiniband_mad class
      ipoib: bool                   # ib_ipoib loaded: an IPoIB netdev per InfiniBand port
      umadMode: string?             # udev file mode of umad and issm files, such as "666"; empty means the kernel's 600
      driverVersion: string?        # the RDMA stack's version, as /sys/module/mlx5_core/version shows it, such as 26.01-1.0.0
  node:
    gpus:
      pci: {vendorID, deviceID}     # existing; pairs GPUs with NICs in the GPU records
    topology:
      network: removed              # MEP-0001's out-of-band adapter block; nics replaces it
      rootPorts:                    # new: the platform's PCIe root ports, once per node
        - address: string           # such as "0009:00:00.0"
          rootComplex: string       # pciDDDD:BB
          id: {vendorID, deviceID}? # upstream IDs are generated only where this is known
      gpuSlots:                     # existing: index, rootComplex, pciAddress, numaNode
        - parent: string?           # new: the root port or switch port the GPU sits below, from any slot; empty when unknown
      nics:                         # new: one entry per NIC instance on the node
        - slot: string              # unique per node, such as nic0 or frontend0
          profile: string           # an SNICProfile name
          configuration: string?    # one of the profile's configurations; empty means its default
          board: int?               # slots whose ASICs share one physical board; node-wide
          systemImageGUID: string?  # empty: node GUID of the lowest-addressed RDMA device
                                    # <rdma device id>: node GUID of that RDMA device
                                    # distinct: a GUID that is no device's node GUID
          gpus: [int]?              # GPU indexes the NIC serves; locality, not a routing rule. Any GPU the profile declares
          firmwareVersion: string?  # overrides identity.firmwareVersion
          vpd: {string: string}?    # overrides model VPD keywords, such as EC
          devices:                  # placement of each PCI device the configurations name
            <pciDevice>:
              rootComplex: string   # pciDDDD:BB
              bridges:              # the NIC's switch ports above or beside the device, parents first
                - address: string
                  parent: string    # a root port, or an earlier switch port
              address: string       # DDDD:BB:DD; function numbers come from the configuration
              parent: string?       # a root port or a switch port; omitted for a device on the root bus
              numaNode: int
              vpdPosition: string?  # suffix of the VPD product name, such as -Prime or -Aux[1]
```

`nics` replaces `topology.network`, the out-of-band adapter block MEP-0001 defined (adapter model, link speed, adapters per GPU). Keeping both would let one rack profile describe its NICs twice, so the block is removed; Mokka has no users who depend on it.

#### `SGPUInventory` (existing; new field)

File mode takes the same shape from Helm values (`nics.overrides`).

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPUInventory
spec:
  rackGroups:
    - id: string
      profileRef: {name: string}    # existing
      nicOverrides:                 # new: applied in order; a later override wins per field
        - slots: [string]           # slot names the override applies to; at least one, each an existing slot
          profile: string?          # replaces the slot's profile, and drops the slot's configuration, firmwareVersion and vpd
          configuration: string?    # replaces the slot's configuration
          firmwareVersion: string?
          vpd: {string: string}?    # merged into the slot's VPD keywords
```

```yaml
# Example: the Ethernet inventory from Story 1
rackGroups:
  - id: training
    count: 4
    profileRef: {name: gb300-nvl72}
    nicOverrides:
      - {slots: [nic0, nic1, nic2, nic3], configuration: eth-multiplane, firmwareVersion: "40.48.1102"}
```

#### Validation

The CRD checks field shapes. These rules need more than one field, so the loader checks them in file mode, and the controller checks them and reports failures in status in control-plane mode:

- **Decoding:** strict; an unknown field is an error.
- **Profile:** VPD scopes are `board`, `asic` or `boardPosition`, and a keyword is either a model value or an identity. Every function type has a driver. `rdmaOmit` and `rdmaEmpty` name only `fw_ver`, `hca_type`, `board_id` or `hw_rev`, and no attribute is in both. A `vfType` names another function type, on a function type with `totalVFs`. Configuration names are unique, and the default is one of them.
- **Configuration:** port and function IDs are unique. Technology and lane speed come from the lists above; width is positive and planes aren't negative. A function's type exists, its PCI device is named, and each device and function number is used once. The class is `0x` plus six hex digits. Every port has an owner, and at most one per PCI device. An RDMA device names a function and at least one port, and its ports are ports that function owns. A function has at most one RDMA device, since its identifiers come from the function's position.
- **Rack profile:** slot names, GPU slots and PCI addresses are unique. One board holds one NIC model. A slot's GPUs and profile exist. A per-board VPD identity needs a `board`, and a per-board-position one needs a `vpdPosition`. `systemImageGUID` is `distinct` or an RDMA device of the selected configuration. Root ports are unique and inside their root complex. Addresses are well-formed and inside their root complex. Every parent, of a GPU slot, a NIC device or a switch port, names a root port or a switch port of the node in the same root complex, and switch ports form no cycle. A NIC device without a parent sits on its root complex's root bus, so its address is on that bus and it has no switch ports, as the EFA device in `aws-c5n` shows. Bridges need the profile's `switchPort`. Every PCI device a configuration uses is placed.
- **Overrides:** overrides apply in order, and a later one wins per field; VPD keywords merge. An override that sets a new profile drops the slot's configuration, firmware version and VPD overrides, which belonged to the old model, unless the same override sets them. An override that names an unknown slot is an error. The rack-profile rules then run on the effective selection, so an override that selects a profile or configuration whose PCI devices the slot doesn't place is rejected.

#### Samples by feature

[Appendix B](#appendix-b-samples) holds the full samples. Each shows a part of the schema:

| Sample | Shows |
|---|---|
| [`cx8-c8280z`](#sample-cx8-c8280z), [`gb300-nvl72`](#sample-gb300-nvl72) | Two configurations of one ASIC; Data Direct in a GPU's domain; VPD by board, ASIC and board position; `systemImageGUID: distinct` |
| [`gb300-ethernet`](#sample-gb300-ethernet) | Selecting `eth-multiplane`; per-slot firmware and VPD overrides; `umadMode` |
| [`cx7-hhhl`](#sample-cx7-hhhl) | One card with two links in two domains |
| [`bf3-b3240`](#sample-bf3-b3240) | A SoC function without an RDMA device; a function type without a VPD identity |
| [`bf4-vr`](#sample-bf4-vr), [`vr-nvl72`](#sample-vr-nvl72) | Two host links sharing ports; the system image GUID taken from a named RDMA device |
| [`cx9-vr`](#sample-cx9-vr) | The VR NVL72 scale-out NIC |
| [`cx8-aws-vf`](#sample-cx8-aws-vf), [`aws-gb300`](#sample-aws-gb300) | A cloud VM's VFs and Data Direct functions, with no PF |
| [`efa-c5n`](#sample-efa-c5n), [`aws-c5n`](#sample-aws-c5n) | EFA: declared node type, absent and empty attributes; a device on the root bus |

### Generation rules

Generation has two halves. **Resolve** turns the declaration into a typed NIC state in which every value is decided. **Render** turns that state into surfaces; the node agent's simulators do it. Both halves read only declared values and branch only on port technology.

| Rule | When | Surfaces | Reads |
|---|---|---|---|
| G1 PCI | Every function of the selected configuration. SR-IOV attributes only when `totalVFs` is set, link attributes only when `pcieLink` is, upstream IDs only where they're declared | IDs, class, revision, link, driver, modalias, upstream IDs, SR-IOV totals | Function type, configuration class, placement |
| G2 VPD | Functions whose type has `vpd: true`; each identity keyword unless the type lists it in `vpdOmit` | VPD keywords | Profile keywords and identity scopes, suffixes, slot overrides, `vpdPosition` |
| G3 Bridges | Each bridge a device placement lists | The NIC's switch ports | Slot bridges, profile `switchPort` |
| G4 RDMA device | Each RDMA device of the selected configuration. Attributes in `rdmaOmit` are left out; those in `rdmaEmpty` are empty | Node type, node and system image GUIDs, firmware, board ID, HCA type, hardware revision | Function type, identity, slot `systemImageGUID` and overrides |
| G5 Names | All RDMA devices on the node, in PCI address order, numbered per prefix as the kernel numbers them per driver | RDMA device names, such as `mlx5_0` and `efa_0` | `rdmaNamePrefix` |
| G6 Rate | Each port of each RDMA device | Rate text, link layer | Width, lane speed, technology |
| G7 Ports | Each port of each RDMA device, by technology: `ib` gets its capability mask and a port GUID (the node GUID on port 1, its own on each later port), with GID 0 from the port GUID; `roce` gets a GID from its netdev's MAC; `efa` gets LID 0 and one link-local GID with no netdev | LIDs, capability mask, P_Keys, GIDs | Port |
| G8 Netdevs | Each `roce` port. `ib` ports get netdevs only from G12; `efa` ports get none | Netdev names, MACs, one switch ID per NIC | Port, function, placement |
| G9 Device files | `uverbs` per RDMA device and one `rdma_cm` always; `umad` per port when `software.rdma.umad`; `issm` per `ib` port without planes, also only with `umad` | Device files and their modes | `software.rdma` |
| G10 Verbs | Each RDMA device | `ibv_devinfo` fields | As G4, G6 and G7 |
| G11 ethtool | Each netdev from G8 or G12 | Driver, firmware and board ID, bus address | Function type, identity, placement |
| G12 IPoIB | Each `ib` port, when `software.rdma.ipoib` | An IPoIB netdev: its name, from the PCI address, and its 20-byte address: queue pair number 0, as the capture shows, then GID 0, which ends in the port GUID | Port, RDMA device |
| G13 GPUs | Each GPU slot | GPU PCI records and their NIC association | GPU slots, and each NIC instance's `gpus` |

Rules that decide values (G1–G5, G13, and the values in G7, G8 and G12) belong to resolve; rules that format values (G6, G9–G11, and the rest of G12) belong to render. So the state names every netdev, Ethernet and IPoIB alike, with its address.

Identifiers are name-based (UUIDv5) hashes of coordinates, using the scheme GPU UUIDs already use (`internal/sgpu/rackrender/identity.go`), shared so NICs and GPUs have one identity function. Control-plane mode names a node by its inventory UID, rack group, rack index and node index. File mode names it by an empty inventory UID followed by the node name; no control-plane node has an empty inventory UID, so the two modes never produce the same name. Then comes where the object sits within the node: the slot, then the PCI device, function number and port number for values per function or port, or the board and board position for VPD identities. The profile's ID strings for functions, ports and RDMA devices never enter an identifier, so renaming them changes nothing; a configuration switch keeps a function's GUIDs and MACs when the function keeps its position, as on hardware. Each kind of identifier (GUID, MAC, 128-bit ID) hashes in its own domain, so a GUID and a MAC of the same object are unrelated, and MACs are locally administered.

### Node agent

NICs follow the GPU path through the node agent ([MEP-0003](../0003-node-agent/README.md)). The agent loop doesn't change.

```
ConfigMap ──FileSource.reload──▶ compileState ──▶ agent.State ──reconcileLoop──▶ Stage → Apply → Daemon.Run/Reload
                                   └─ compileNICs ─▶ State.NICs ─▶ pcibus, ib, new simulators
```

**Input.** The profile document the agent already reads. In file mode the chart appends the NIC profiles the slots name and the overrides from values, so `FileSource` watches one file as today:

```yaml
# The profile document in the node agent's ConfigMap, file mode
system:
  # … GPU profile, unchanged
nics:                      # from profiles/gb300.yaml
  rootPorts:
    - {address: "0009:00:00.0", rootComplex: pci0009:00, id: {vendorID: "10de", deviceID: "22b1"}}
    # …
  gpuParents:              # by GPU index; the GPU addresses stay in the GPU profile above
    1: "0009:05:00.0"
    # …
  slots:
    # …
nic_profiles:              # appended by the chart: the profiles the slots name
  - apiVersion: mokka.nvidia.com/v1alpha1
    kind: SNICProfile
    metadata: {name: cx8-c8280z}
    spec:
      # …
nic_overrides:             # appended by the chart from values
  - {slots: [nic0, nic1, nic2, nic3], configuration: eth-multiplane}
```

**Compile.** `compileNICs` joins `compileState` (`internal/agent/source/file.go`) and handles profiles with slots; `compileNetwork` stays for profiles that still use the `infiniband` block. Like `compileNetwork`, it parses the same bytes through its own schema, since the NVML engine never reads NICs. It validates them, applies the selection order, and resolves with the node's coordinates.

**Failing open.** An invalid NIC declaration neither takes NICs away from a running node nor blocks a new one. A node that resolved its NICs before keeps its last resolved NIC state; a node that never did starts without NICs. Either way its GPUs render as usual, and the error is logged, recorded as a Warning Event on the node agent's pod, with a Normal Event once the declaration resolves again, and shown in `/readyz` under `nics` while the probe stays OK. Fixing the declaration applies on the same pod, without a restart. Control-plane mode follows the same rule.

**State.** `State.NICs` takes over from `NodeShape.Network`, which remains only for the `infiniband` block and goes with it. Every value is resolved, so simulators never derive anything and a change is detected by comparing states. Nothing downstream reads the NIC YAML, so there is no raw copy of it.

```go
// NICs is a node's resolved NIC state. Every value is decided.
type NICs struct {
	RDMA      RDMA         // umad, ipoib, umadMode, driverVersion
	Instances []Instance   // one per slot: profile, configuration, model, firmware, board ID, board, GPUs, switch ID
	Bridges   []Bridge     // address, parent, root complex, IDs, slot
	Functions []Function   // address, IDs, class, revision, driver, placement, upstream IDs, NUMA node, SR-IOV and VF type, link, VPD
	Devices   []RDMADevice // name, function, node type, node and system image GUIDs, attributes, firmware, ports
	Netdevs   []Netdev     // Ethernet and IPoIB: name, function, port, MAC or IPoIB address
}
```

**Kernel modules.** `gpudriver` lists the NIC driver's modules when the node has RDMA devices, or when the legacy block enables InfiniBand. `ib_umad` and `ib_ipoib` will follow `software.rdma`'s `umad` and `ipoib` when [#1358](https://github.com/NVIDIA/k8s-test-infra/issues/1358) adds them, so the module list and the device files agree.

**Simulators.**

| Simulator | Change |
|---|---|
| `pcibus` | Renders NIC functions and bridges into the same `/sys/bus/pci` tree as the GPUs; GPUs, NIC functions and switch ports form one tree, each under its declared parent. Its NFD feature file gains the NIC vendors |
| `ib` | Renders `class/infiniband`, `infiniband_mad`, `infiniband_verbs`, `/dev/infiniband` and RoCE netdevs from `State.NICs`, and from `NetworkShape` for profiles that still use the `infiniband` block. `mock-ib` reloads when the NIC state changes, as today |
| Driver reactions (new) | Emulates the driver's response to the operator actions Mokka can see, starting with `sriov_numvfs` writes (MEP-0011). Switchdev mode and SMI devices go through devlink and RDMA netlink, which Mokka can't answer yet. A daemon, since operators act after staging. The behavior belongs to a driver, so it lives in a module per declared `driver`, starting with `mlx5_core`; it isn't described by declaration fields. Its state is runtime only; a configuration change clears it |
| Netdevs (new, if needed) | IPoIB netdevs and `ethtool` answers, if they don't fit in `ib`. `ethtool` uses ioctl and netlink, so it needs a shim like the IB tools |

Today an HCA's PCI function exists only in `ib`'s own tree. A Data Direct function has to sit under its GPU's root complex, so all PCI functions move to one tree. How the `libmockfs` and `libibmocksys` path redirections share that tree is part of the implementation.

A new state from a profile edit or a configuration change runs `Stage` again: the functions of a changed NIC are removed and created in the new layout.

### Control plane

| Area | Change |
|---|---|
| Watches | A new `SNICProfile` informer and RBAC (`get`, `list`, `watch`). NIC profile events re-route inventories as `SGPURackProfile` events do. Slots are part of the rack profile's spec, so they already change its revision; `nicOverrides` counts as a rendering input |
| Rack rendering | The rack records the revision of each `SNICProfile` its slots name, next to the rack profile's, and renders again when one changes. It validates the NIC references and the cross-field rules. No NIC state is stored in `SGPURack`: it is deterministic |
| Per-node view | When a node agent asks for its state, the control plane resolves the node's NICs with the same code file mode uses, from the pinned revisions only |
| Status | A missing or invalid `SNICProfile` becomes a condition on the inventory and rack. Nothing is blocked: a node keeps its last applied NIC state, and a node that never had one starts without NICs, as in file mode |
| Unchanged | Allocation, capacity, Node projection and runtime policies |

```yaml
# SGPURack spec
profileRef: {name: gb300-nvl72, uid: …, generation: 3, revision: …}   # existing
nicProfileRefs:                                                         # new
  - {name: cx8-c8280z, uid: …, generation: 1, revision: …}
  - {name: bf3-b3240, uid: …, generation: 1, revision: …}
```

Delivery of per-node state to the agent is MEP-0001's and isn't built yet: the agent's control-plane source is a stub ([#614](https://github.com/NVIDIA/k8s-test-infra/issues/614)). Control-plane NIC state arrives with it.

### Helm chart

```
deployments/mokka-crds/helm/mokka-crds/templates/
└── mokka.nvidia.com_snicprofiles.yaml   new CRD, generated
deployments/nvml-mock/helm/nvml-mock/
├── nics/                                one SNICProfile per NIC model
│   ├── cx8-c8280z.yaml
│   └── …
├── profiles/
│   └── gb300.yaml                       GPU profile; NIC slots replace the infiniband block
└── templates/controlplane/
    └── snicprofiles.yaml                built-in objects, when controlPlane.enabled
```

The control-plane template renders one object per file and checks that its name matches the file name:

```yaml
{{- if .Values.controlPlane.enabled }}
{{- range $path, $_ := .Files.Glob "nics/*.yaml" }}
{{- $p := $.Files.Get $path | fromYaml }}
{{- $name := base $path | trimSuffix ".yaml" }}
{{- if ne $p.metadata.name $name }}{{ fail (printf "%s: metadata.name must be %s" $path $name) }}{{ end }}
{{- $_ := set $p.metadata "labels" (dict "mokka.nvidia.com/profile-source" "builtin") }}
---
{{ toYaml $p }}
{{- end }}
{{- end }}
```

The CRDs come from `mokka-crds`, installed first as today. The objects belong to nvml-mock because Helm can't create objects of a kind whose CRD the same release installs.

File-mode values:

```yaml
nics:
  overrides:                       # the file-mode nicOverrides
    - slots: [nic0, nic1, nic2, nic3]
      configuration: eth-multiplane
  customProfiles:                  # SNICProfiles in the same schema as the shipped files
    - apiVersion: mokka.nvidia.com/v1alpha1
      kind: SNICProfile
      metadata: {name: cx7-oem}
      spec:
        # …
  slots: []                        # replaces the GPU profile's slots, root ports and GPU parents together, for a platform it doesn't describe
  rootPorts: []                    # the root ports those slots' devices name
  gpuParents: {}                   # each GPU's parent by index, such as {0: "0008:05:00.0"}
```

The chart fails the render on mistakes it can see: an override without `slots`, or naming an unknown slot or configuration; a custom profile without `apiVersion`, or reusing a shipped profile's name.

To record those Events, the chart gives the node agent a Role that only allows creating Events, and mounts an API token only in the node-agent container.

### Test plan

- **Schema.** Every shipped profile decodes strictly into the Go type and passes validation. Every shipped profile also passes the generated CRD schema, and every invalid case for a rule the CRD can enforce (field types, enums, patterns, required fields) fails both, so file mode and the API server can't drift apart. Rules that span fields or objects, such as a slot naming a profile that exists, can't live in a CRD; file mode and the controller run them from one validation package, and a test gives both the same cases. The NIC slots, GPU slots and root ports in each GPU profile equal those of the matching rack profile, so the capture checks cover the addresses file mode uses.
- **Declaration level.** For each capture, the generator's output is compared with the capture through `diff nic`; every difference must match a cause. Deliberate breakages, such as swapping two NICs' Data Direct functions, dropping planes or putting both of a card's ports on one link, must each be caught.
- **Determinism.** Generating twice gives identical output, removing one NIC leaves the others' identifiers unchanged, and renaming every function, port and RDMA device ID in a profile leaves every identifier unchanged.
- **Configuration switch.** Changing only a slot's configuration changes its functions and nothing in its placement or Data Direct pairing.
- **Rendered level.** The simulators stage a node into a temporary host root, the capture scripts read it, and `diff nic` compares the result with the same capture and causes. No cluster is needed. The `collect network` scripts read `/sys` and `/dev` directly today; a root-directory option for a staged tree is in #1268.
- **Both modes.** File mode and control-plane mode give the same `State.NICs` for the same declaration and node coordinates.
- **Consumer level.** Real tools find and use the simulated NICs in a Mokka pod: `lspci`, `ibv_devinfo`, `ethtool`, `nvidia-smi topo -m`, and later `rdma link`, `ip link` and the network operator's components. The declaration level shows the model is right and the rendered level that the files are there; only this level shows applications can use them.

### Implementation order

1. API types, CRD, chart profile files, validation.
2. Node agent in file mode: `compileNICs`, `State.NICs`, `pcibus` and `ib` changes, the rendered-level check. Until `ib` renders from `State.NICs` ([#1355](https://github.com/NVIDIA/k8s-test-infra/issues/1355)), a compatibility shape derived from it keeps today's `ib` rendering working.
3. Control plane: watches, pins, status; the per-node view once delivery exists.
4. Driver reactions for the operations Mokka can see, starting with VFs (MEP-0011). Switchdev mode and SMI devices wait for devlink and RDMA netlink support.
5. Captures and profiles for the remaining platforms, then removal of the `infiniband` block, `compileNetwork` and `NetworkShape`.

## Open Questions

The first is a tooling choice still to make. The others depend on topics this MEP doesn't settle: how a node attaches to a fabric (MEP-0015), and how driver reactions work. They're recorded so the design leaves room for them; the answers belong in follow-up changes to this MEP or in their own proposals.

### Comparing a declaration with a capture

`diff nic` compares two capture directories, and the generator's output has to reach it in one of two ways:
- the generator writes a capture directory in the layout `collect network` writes, so `diff nic` runs unchanged on both sides;
- `diff nic` also reads `nic-surfaces` output for one side, so the generator writes that normalized list.

The first tests the capture layout end to end; the second keeps the generator independent of the per-source files. Undecided.

### Reconciling boot, runtime and network state

NIC surfaces come from three inputs, each with its own owner:

| Input | Owner | Examples |
|---|---|---|
| Boot state | The declaration: profiles, slots, overrides | PFs, RDMA devices, ports |
| Driver runtime state | Driver reactions to operator actions | VFs, representors, SMI devices |
| Network state | The fabric (MEP-0015) | Port state, LIDs, partitions |

Settled so far: a configuration change is a firmware reset and clears the runtime state. Open: how the three inputs combine into what the simulators render, and what survives a restage. A test case for any answer: a NIC with one PF and four InfiniBand planes; the network operator creates an SMI device, once Mokka can answer RDMA netlink; the fabric reports plane 2 down; the node agent reconciles. The SMI device should remain, and its second port should show the failure.

A likely direction is one effective NIC state, resolved from the three inputs, that the simulators consume. This MEP doesn't commit to it.

### Fabric endpoints for MEP-0015

Settled: fabric endpoints come from a configuration's logical ports, not from PCI functions or RDMA devices. A BlueField-4 with two host links has four PFs and four RDMA devices but two endpoints, because both links share its two ports.

Open: how endpoints are named, whether a port with planes gives one endpoint per plane, how an endpoint's identity survives reconciliation, and how the state of each plane rolls up into the RDMA port.

### Reporting surface fidelity

A simulated file isn't always a working interface. A netdev under `/sys/class/net` doesn't appear in `ip link`, which asks the kernel over rtnetlink. For each surface (PCI sysfs, RDMA sysfs, network sysfs, rtnetlink, RDMA netlink, verbs, ethtool, NVML topology), Mokka should tell apart one that is:
- simulated;
- turned off on purpose;
- unsupported in this environment;
- not implemented.

Otherwise the declaration and rendered checks can pass while applications still can't find the NIC. Where this is declared and reported is open; it overlaps with the interface tiers in [MEP-0014](../0014-network-simulation/README.md).

## Drawbacks

- **Verbose declarations.** Each simulated mode is a full configuration, and each slot repeats root ports and bridges.
- **A new CRD** to install, document and version.
- **Slots in two places** until built-in rack profiles exist.
- **Every new NIC model needs a capture** before it ships as built-in.

## Alternatives

| Alternative | Why not |
|---|---|
| Keep the `infiniband` block, extended | It counts NICs per GPU and has one shape per node; it can't express several NIC models, NICs across PCIe domains, or a mode change |
| A NIC catalog where the link type drives the layout | The generator would derive PFs and classes from link type and planes, which is ConnectX knowledge in Mokka |
| Firmware parameters (`mlxconfig`) as input | The same parameters mean different layouts per technology; interpreting them makes the generator device-specific |
| Profiles compiled into the controller, kept in sync with the chart by a CI check | A second copy and a drift check, where one chart file serves both modes |
| NIC state stored in `SGPURack` | It is deterministic from the declaration and coordinates, so storing it adds writes and nothing else |
| NIC profiles in a separately mounted file | `FileSource` would watch and hash another file; appending to the one profile document needs no change |
| NIC PCI functions only in the InfiniBand tree | A Data Direct function couldn't sit under its GPU's root complex, and `lspci`-style consumers wouldn't see NICs |
| One placement per function instead of per PCI device | Function counts change with the configuration, so placement would change with every mode switch |

## Appendix A: Design validation

A prototype implements the schema, the validation, the generator, and the declaration-level and determinism checks above. It compares the samples in [Appendix B](#appendix-b-samples) with five captures:

| Capture | Checks | Passed | Differences, all with a cause |
|---|---|---|---|
| VR NVL72 | 792 | 666 | 122 blind spots of an older capture format, 4 by design |
| GB300 InfiniBand | 935 | 669 | 266 by design: SMI devices, partitions, IPoIB MTU, kernel modules, rendered attributes |
| GB300 Ethernet, at a cloud provider | 1,715 | 936 | 779 by design: a VF, its representor, switchdev with a multiport eswitch, MTU, rendered attributes |
| AWS GB300 instance | 385 | 123 | 242 blind spots of a trimmed listing, 20 by design (VF netdevs moved into pods, an IPv4-mapped GID) |
| AWS EFA (`c5n.9xlarge`) | 58 | 52 | 3 by design: host kernel and architecture, udev directories. 3 capture blind spots: attributes the driver doesn't create, which the converted capture recorded as empty |

No difference is a schema gap or a rule bug. Fifteen deliberate breakages are each caught. The by-design differences are surfaces the declaration leaves out on purpose. Of those, the VF can come from Mokka's driver emulation (MEP-0011), but the SMI devices, switchdev and representors need devlink and RDMA netlink, so a simulated node can't produce them yet; they're known gaps, not planned work. The AWS sources predate `collect network` and were converted into the prototype's format.

These counts come from the prototype's comparison tool and its single-file captures. [#1353](https://github.com/NVIDIA/k8s-test-infra/pull/1353)'s `diff nic` reads capture directories and makes one check per attribute either side has, so an object one side lacks fails once per attribute; its totals aren't comparable with these. It also adds two relations: whose node GUID a system image GUID is, and whether a BlueField's VPD `VU` starts with its serial number.

Not covered yet:
- GB200's ConnectX-7 on InfiniBand, and an InfiniBand cluster without SMI devices: no captures.
- `collect network` captures of the AWS instances.
- Node coordinates in identifiers: the prototype hashes declaration coordinates only, so it models one node.
- `nicOverrides`, the rendered level and control-plane mode, which need the implementation.

## Appendix B: Samples

The prototype's samples. Rack profiles hold only the fields this MEP adds or reads; `rack`, the GPU model and the other existing fields are left out. Captures under `tests/hardware-captures/` come with [#1353](https://github.com/NVIDIA/k8s-test-infra/pull/1353).

- NIC profiles: [`cx8-c8280z`](#sample-cx8-c8280z), [`cx9-vr`](#sample-cx9-vr), [`bf3-b3240`](#sample-bf3-b3240), [`bf4-vr`](#sample-bf4-vr), [`cx7-hhhl`](#sample-cx7-hhhl), [`cx8-aws-vf`](#sample-cx8-aws-vf), [`efa-c5n`](#sample-efa-c5n)
- Rack profiles: [`gb300-nvl72`](#sample-gb300-nvl72), [`vr-nvl72`](#sample-vr-nvl72), [`gb300-ethernet`](#sample-gb300-ethernet), [`aws-gb300`](#sample-aws-gb300), [`aws-c5n`](#sample-aws-c5n)

### NIC profiles

<a id="sample-cx8-c8280z"></a>
#### `cx8-c8280z`

One ConnectX-8 ASIC of the dual-ASIC C8280Z mezzanine board in GB300 trays.

```yaml
kind: SNICProfile
metadata:
  name: cx8-c8280z
spec:
  identity:
    model: ConnectX-8
    hcaType: MT4131
    boardID: MT_0000001513
    firmwareVersion: "40.47.3576"
    partNumber: 900-9X86E-00CX-ST0
    vendorOUI: "0x02c9"
  sources:
    - tests/hardware-captures/gb300-ib/node-a
    - tests/hardware-captures/gb300-roce/node-a
  switchPort: {vendorID: "15b3", deviceID: "197c"}
  vpd:
    keywords:
      name: NVIDIA Dual ConnectX-8 SuperNIC C8280Z Mezzanine Board for GB300 systems, Crypto Enabled, Secure Boot Enabled, Partner cooled.
      EC: A3
      V2: 900-9X86E-00CX-ST0
      VA: MLX:MN=MLNX:CSKU=V2:UUID=V3:PCI=V0:MODL=C8280Z
      V0: PCIeGen6 x48
    # Serial number per board, V3 UUID per ASIC (it pairs a network function
    # with its Data Direct function), VU per board position.
    identities: {SN: board, V3: asic, VU: boardPosition}
  functionTypes:
    network:
      vendorID: "15b3"
      deviceID: "1023"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "0208"
      revision: "0x00"
      pcieLink: {speed: 64.0 GT/s PCIe, width: 16}
      driver: mlx5_core
      rdmaNamePrefix: mlx5_
      totalVFs: 16
      vfType: vf
      vpd: true
      vpdSuffixes: {VU: "MLNXS0D0F{function}"}
    dataDirect:
      vendorID: "15b3"
      deviceID: "2100"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "0208"
      revision: "0x00"
      pcieLink: {speed: 64.0 GT/s PCIe, width: 16}
      driver: mlx5_ib
      vpd: true
      vpdSuffixes: {VU: MLNXDDIS0D0F0}
    # A VF that cluster software enables on a network function, as the
    # Ethernet capture shows on PF0. It reports its current link as Unknown and
    # its maximum as 64.0 GT/s x16, which pcieLink can't express, so no link is
    # declared.
    vf:
      vendorID: "15b3"
      deviceID: "101e"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "0208"
      revision: "0x00"
      driver: mlx5_core
      rdmaNamePrefix: mlx5_
  defaultConfiguration: ib-xdr-4plane
  configurations:
    # NUM_OF_PF=1, LINK_TYPE_P1=IB(1), NUM_OF_PLANES_P1=4, MODULE_SPLIT_M0[0..3]=1.
    - name: ib-xdr-4plane
      ports:
        - {id: p0, technology: ib, width: 4, laneSpeed: XDR, planes: 4, capMask: "0xa751ec48"}
      functions:
        - {id: pf0, type: network, pciDevice: network, function: 0, class: "0x020700", ports: [p0]}
        - {id: dd0, type: dataDirect, pciDevice: dataDirect, function: 0, class: "0x080100"}
      rdmaDevices:
        - {id: rdma0, function: pf0, ports: [p0]}
    # Ethernet multi-plane: four PFs (NUM_OF_PF=4), one 200G port each. As
    # captured on a cloud provider's nodes, read without mlxconfig. Each PF has
    # its own RDMA device until cluster software switches the eswitch to
    # switchdev with a multiport eswitch, which shares PF0's device.
    - name: eth-multiplane
      ports:
        - {id: p0, technology: roce, width: 2, laneSpeed: NDR, planes: 0}
        - {id: p1, technology: roce, width: 2, laneSpeed: NDR, planes: 0}
        - {id: p2, technology: roce, width: 2, laneSpeed: NDR, planes: 0}
        - {id: p3, technology: roce, width: 2, laneSpeed: NDR, planes: 0}
      functions:
        - {id: pf0, type: network, pciDevice: network, function: 0, class: "0x020000", ports: [p0]}
        - {id: pf1, type: network, pciDevice: network, function: 1, class: "0x020000", ports: [p1]}
        - {id: pf2, type: network, pciDevice: network, function: 2, class: "0x020000", ports: [p2]}
        - {id: pf3, type: network, pciDevice: network, function: 3, class: "0x020000", ports: [p3]}
        - {id: dd0, type: dataDirect, pciDevice: dataDirect, function: 0, class: "0x080100"}
      rdmaDevices:
        - {id: rdma0, function: pf0, ports: [p0]}
        - {id: rdma1, function: pf1, ports: [p1]}
        - {id: rdma2, function: pf2, ports: [p2]}
        - {id: rdma3, function: pf3, ports: [p3]}
```

<a id="sample-cx9-vr"></a>
#### `cx9-vr`

ConnectX-9 as fitted in a VR NVL72 compute tray.

```yaml
kind: SNICProfile
metadata:
  name: cx9-vr
spec:
  identity:
    model: ConnectX-9
    hcaType: MT4133
    boardID: MT_0000001658
    firmwareVersion: "82.48.4222"
    partNumber: 900-9X9BE-00PB-TS5EC
    vendorOUI: "0x02c9"
  sources:
    - tests/hardware-captures/vr-nvl72/node-a
  switchPort: {vendorID: "15b3", deviceID: "197c"}
  functionTypes:
    network:
      vendorID: "15b3"
      deviceID: "1025"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "0671"
      driver: mlx5_core
      rdmaNamePrefix: mlx5_
      totalVFs: 1
      vpd: true
  defaultConfiguration: eth-1x200
  configurations:
    # Firmware settings not read; the layout is what the capture shows.
    - name: eth-1x200
      ports:
        - {id: p0, technology: roce, width: 1, laneSpeed: XDR, planes: 0}
      functions:
        - {id: pf0, type: network, pciDevice: network, function: 0, class: "0x020000", ports: [p0]}
      rdmaDevices:
        - {id: rdma0, function: pf0, ports: [p0]}
```

<a id="sample-bf3-b3240"></a>
#### `bf3-b3240`

BlueField-3 B3240 DPU as a front-end NIC, two Ethernet ports, as in GB300 trays.

```yaml
kind: SNICProfile
metadata:
  name: bf3-b3240
spec:
  identity:
    model: BlueField-3
    hcaType: MT41692
    boardID: MT_0000001188
    firmwareVersion: "32.47.3576"
    partNumber: 900-9D3B6-00CN-PA0
    hwRev: "0x1"
    vendorOUI: "0x02c9"
  sources:
    - tests/hardware-captures/gb300-ib/node-a
  vpd:
    keywords:
      name: BlueField-3 B3240 P-Series FHHL DPU for Cold Aisle, 400GbE / NDR IB, Dual-port QSFP, PCIe Gen5.0 x16 with x16 PCIe extension option, 16 Arm cores, 32GB on-board DDR, integrated BMC
      EC: A5
      V2: 900-9D3B6-00CN-PA0
      VA: MLX:MN=MLNX:CSKU=V2:UUID=V3:PCI=V0:MODL=D3B6
      V0: PCIeGen5 x16
    identities: {SN: asic, V3: asic, VU: asic}
  functionTypes:
    network:
      vendorID: "15b3"
      deviceID: "a2dc"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "0041"
      revision: "0x01"
      pcieLink: {speed: 32.0 GT/s PCIe, width: 16}
      driver: mlx5_core
      rdmaNamePrefix: mlx5_
      totalVFs: 16
      vpd: true
      vpdSuffixes: {VU: "MLNXS0D0F{function}"}
    socManagement:
      vendorID: "15b3"
      deviceID: "c2d5"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "0041"
      revision: "0x01"
      pcieLink: {speed: 32.0 GT/s PCIe, width: 16}
      driver: vfio-pci
      vpd: true
      vpdOmit: [VU]
  defaultConfiguration: eth-2x400
  configurations:
    - name: eth-2x400
      ports:
        - {id: p0, technology: roce, width: 4, laneSpeed: NDR, planes: 0}
        - {id: p1, technology: roce, width: 4, laneSpeed: NDR, planes: 0}
      functions:
        - {id: pf0, type: network, pciDevice: host, function: 0, class: "0x020000", ports: [p0]}
        - {id: pf1, type: network, pciDevice: host, function: 1, class: "0x020000", ports: [p1]}
        - {id: soc, type: socManagement, pciDevice: host, function: 2, class: "0x080100"}
      rdmaDevices:
        - {id: rdma0, function: pf0, ports: [p0]}
        - {id: rdma1, function: pf1, ports: [p1]}
```

<a id="sample-bf4-vr"></a>
#### `bf4-vr`

BlueField-4 in a VR NVL72 tray: one ASIC with two Ethernet ports and two PCIe host links, each link showing a PF for each port.

```yaml
kind: SNICProfile
metadata:
  name: bf4-vr
spec:
  identity:
    model: BlueField-4
    hcaType: MT41695
    boardID: MT_0000001875
    firmwareVersion: "82.48.4238"
    partNumber: 900-9D4A4-00CB-TS6EC
    vendorOUI: "0x02c9"
  sources:
    - tests/hardware-captures/vr-nvl72/node-a
  functionTypes:
    network:
      vendorID: "15b3"
      deviceID: "a2df"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "2000"
      driver: mlx5_core
      rdmaNamePrefix: mlx5_
      totalVFs: 46
      vpd: true
  defaultConfiguration: eth-2x400-2host
  configurations:
    - name: eth-2x400-2host
      ports:
        - {id: p0, technology: roce, width: 4, laneSpeed: NDR, planes: 0}
        - {id: p1, technology: roce, width: 4, laneSpeed: NDR, planes: 0}
      functions:
        - {id: a0, type: network, pciDevice: hostA, function: 0, class: "0x020000", ports: [p0]}
        - {id: a1, type: network, pciDevice: hostA, function: 1, class: "0x020000", ports: [p1]}
        - {id: b0, type: network, pciDevice: hostB, function: 0, class: "0x020000", ports: [p0]}
        - {id: b1, type: network, pciDevice: hostB, function: 1, class: "0x020000", ports: [p1]}
      rdmaDevices:
        - {id: rdma-a0, function: a0, ports: [p0]}
        - {id: rdma-a1, function: a1, ports: [p1]}
        - {id: rdma-b0, function: b0, ports: [p0]}
        - {id: rdma-b1, function: b1, ports: [p1]}
```

<a id="sample-cx7-hhhl"></a>
#### `cx7-hhhl`

ConnectX-7 HHHL dual-port card used as a front-end NIC, its two x8 links in two PCIe domains, each link carrying one port.

```yaml
kind: SNICProfile
metadata:
  name: cx7-hhhl
spec:
  identity:
    model: ConnectX-7
    hcaType: MT4129
    boardID: ORC0000000014
    firmwareVersion: "28.47.1026"
    partNumber: "8219495"
    vendorOUI: "0x02c9"
  sources:
    - tests/hardware-captures/gb300-roce/node-a
  vpd:
    keywords:
      name: ConnectX-7 HHHL adapter Card, 200GbE (default mode) / NDR200 IB, Dual-port QSFP112, PCIe 5.0 x16, Crypto and Secure Boot, Short Bracket
      EC: "03"
      V2: "8219495"
      VA: MLX:MN=MLNX:CSKU=V2:UUID=V3:PCI=V0:MODL=CX755106A
      V0: PCIeGen5 x16
    identities: {SN: board, V3: asic, VU: boardPosition}
  functionTypes:
    network:
      vendorID: "15b3"
      deviceID: "1021"
      subsystemVendorID: "15b3"
      subsystemDeviceID: "1103"
      revision: "0x00"
      pcieLink: {speed: 32.0 GT/s PCIe, width: 8}
      driver: mlx5_core
      rdmaNamePrefix: mlx5_
      totalVFs: 252
      vpd: true
      vpdSuffixes: {VU: MLNXS0D0F0}
  defaultConfiguration: eth-2x200-2host
  configurations:
    - name: eth-2x200-2host
      ports:
        - {id: p0, technology: roce, width: 4, laneSpeed: HDR, planes: 0}
        - {id: p1, technology: roce, width: 4, laneSpeed: HDR, planes: 0}
      functions:
        - {id: a0, type: network, pciDevice: hostA, function: 0, class: "0x020000", ports: [p0]}
        - {id: b0, type: network, pciDevice: hostB, function: 0, class: "0x020000", ports: [p1]}
      rdmaDevices:
        - {id: rdma-a0, function: a0, ports: [p0]}
        - {id: rdma-b0, function: b0, ports: [p1]}
```

<a id="sample-cx8-aws-vf"></a>
#### `cx8-aws-vf`

ConnectX-8 as an AWS p6e-gb300r instance shows it: two RoCE virtual functions the hypervisor assigns, and a Data Direct function, with no PF visible.

```yaml
kind: SNICProfile
metadata:
  name: cx8-aws-vf
spec:
  identity:
    model: ConnectX-8
    hcaType: MT4126
    firmwareVersion: "40.47.1088"
    partNumber: 900-9X86E-B4CX-ST0
    vendorOUI: "0x02c9"
  sources:
    - Trimmed capture of an AWS p6e-gb300r.36xlarge instance (see internal/nic/conformance/testdata/converted)
  vpd:
    identities: {VU: asic}
  functionTypes:
    vf:
      vendorID: "15b3"
      deviceID: "101e"
      driver: mlx5_core
      rdmaNamePrefix: mlx5_
      vpd: true
      vpdOmit: [VU]
    dataDirect:
      vendorID: "15b3"
      deviceID: "2100"
      driver: mlx5_ib
      vpd: true
      vpdSuffixes: {VU: MLNXDDIS0D0F0}
  defaultConfiguration: roce-2x400-vf
  configurations:
    - name: roce-2x400-vf
      ports:
        - {id: p0, technology: roce, width: 4, laneSpeed: NDR, planes: 0}
        - {id: p1, technology: roce, width: 4, laneSpeed: NDR, planes: 0}
      functions:
        - {id: vf0, type: vf, pciDevice: vfA, function: 0, class: "0x020000", ports: [p0]}
        - {id: vf1, type: vf, pciDevice: vfB, function: 0, class: "0x020000", ports: [p1]}
        - {id: dd0, type: dataDirect, pciDevice: dataDirect, function: 0, class: "0x080100"}
      rdmaDevices:
        - {id: rdma0, function: vf0, ports: [p0]}
        - {id: rdma1, function: vf1, ports: [p1]}
```

<a id="sample-efa-c5n"></a>
#### `efa-c5n`

AWS EFA adapter as a c5n.9xlarge instance shows it: one EFA function on the root bus, next to a separate ENA function that has the netdev.

```yaml
kind: SNICProfile
metadata:
  name: efa-c5n
spec:
  identity:
    model: EFA
    firmwareVersion: "0.0.0.0"
    hwRev: "0xEFA0"
    vendorOUI: "0x1d0f"
  sources:
    - EFA capture on an AWS c5n.9xlarge instance (see internal/nic/conformance/testdata/converted)
  functionTypes:
    efa:
      vendorID: "1d0f"
      deviceID: "efa0"
      subsystemVendorID: "1d0f"
      subsystemDeviceID: "efa0"
      revision: "0x00"
      pcieLink: {speed: Unknown, width: 0, maxWidth: 255}
      driver: efa
      rdmaNamePrefix: efa_
      rdmaNodeType: "7: unspecified"
      # The efa driver doesn't create hca_type, board_id or hw_rev, and
      # leaves fw_ver empty; ibv_devinfo still reports firmware 0.0.0.0 and
      # hw_ver 0xEFA0.
      rdmaOmit: [hca_type, board_id, hw_rev]
      rdmaEmpty: [fw_ver]
  defaultConfiguration: efa-1x50
  configurations:
    - name: efa-1x50
      ports:
        - {id: p0, technology: efa, width: 1, laneSpeed: HDR, planes: 0, capMask: "0x00000000", lmc: 1}
      functions:
        - {id: efa0, type: efa, pciDevice: efa, function: 0, class: "0x020000", ports: [p0]}
      rdmaDevices:
        - {id: rdma0, function: efa0, ports: [p0]}
```

### Rack profiles

<a id="sample-gb300-nvl72"></a>
#### `gb300-nvl72`

NIC slots of one GB300 NVL72 tray (tests/hardware-captures/gb300-ib/node-a): four ConnectX-8 ASICs on two dual-ASIC boards, each with a Data Direct function in a GPU's PCIe domain. Only the NIC-related fields of the rack profile, plus the BlueField-3 front-end NIC.

The capture records each bridge's IDs and the IDs above each function, not the bridges' parents: the parents below are inferred from the layout the VR NVL72 capture shows in full.

```yaml
kind: SGPURackProfile
metadata:
  name: gb300-nvl72
spec:
  software:
    rdma: {umad: true, ipoib: true, driverVersion: 25.10-2.4.1}
  node:
    gpus:
      pci: {vendorID: "10de", deviceID: "31c2"}
    topology:
      rootPorts:
        - {address: "0000:00:00.0", rootComplex: pci0000:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0002:00:00.0", rootComplex: pci0002:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0008:00:00.0", rootComplex: pci0008:00, id: {vendorID: "10de", deviceID: "22b1"}}
        - {address: "0009:00:00.0", rootComplex: pci0009:00, id: {vendorID: "10de", deviceID: "22b1"}}
        - {address: "0010:00:00.0", rootComplex: pci0010:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0012:00:00.0", rootComplex: pci0012:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0016:00:00.0", rootComplex: pci0016:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0018:00:00.0", rootComplex: pci0018:00, id: {vendorID: "10de", deviceID: "22b1"}}
        - {address: "0019:00:00.0", rootComplex: pci0019:00, id: {vendorID: "10de", deviceID: "22b1"}}
      gpuSlots:
        - {index: 0, rootComplex: "pci0008:00", pciAddress: "0008:06:00.0", parent: "0008:05:00.0", numaNode: 0}
        - {index: 1, rootComplex: "pci0009:00", pciAddress: "0009:06:00.0", parent: "0009:05:00.0", numaNode: 0}
        - {index: 2, rootComplex: "pci0018:00", pciAddress: "0018:06:00.0", parent: "0018:05:00.0", numaNode: 1}
        - {index: 3, rootComplex: "pci0019:00", pciAddress: "0019:06:00.0", parent: "0019:05:00.0", numaNode: 1}
      nics:
        - slot: nic0
          profile: cx8-c8280z
          board: 0
          gpus: [1]
          devices:
            network:
              rootComplex: pci0000:00
              bridges:
                - {address: "0000:01:00.0", parent: "0000:00:00.0"}
                - {address: "0000:02:00.0", parent: "0000:01:00.0"}
                - {address: "0000:02:03.0", parent: "0000:01:00.0"}
                - {address: "0000:04:00.0", parent: "0000:02:03.0"}
                - {address: "0000:05:08.0", parent: "0000:04:00.0"}
                - {address: "0000:05:0c.0", parent: "0000:04:00.0"}
              address: "0000:03:00"
              parent: "0000:02:00.0"
              numaNode: 0
              vpdPosition: "-Prime"
            dataDirect:
              rootComplex: pci0009:00
              bridges:
                - {address: "0009:01:00.0", parent: "0009:00:00.0"}
                - {address: "0009:02:00.0", parent: "0009:01:00.0"}
                - {address: "0009:02:01.0", parent: "0009:01:00.0"}
                - {address: "0009:04:00.0", parent: "0009:02:01.0"}
                - {address: "0009:05:00.0", parent: "0009:04:00.0"}
              address: "0009:03:00"
              parent: "0009:02:00.0"
              numaNode: 0
              vpdPosition: "-Aux[1]"
        - slot: nic1
          profile: cx8-c8280z
          board: 0
          systemImageGUID: distinct
          gpus: [0]
          devices:
            network:
              rootComplex: pci0002:00
              bridges:
                - {address: "0002:01:00.0", parent: "0002:00:00.0"}
                - {address: "0002:02:00.0", parent: "0002:01:00.0"}
                - {address: "0002:02:01.0", parent: "0002:01:00.0"}
                - {address: "0002:04:00.0", parent: "0002:02:01.0"}
                - {address: "0002:05:08.0", parent: "0002:04:00.0"}
                - {address: "0002:05:0c.0", parent: "0002:04:00.0"}
              address: "0002:03:00"
              parent: "0002:02:00.0"
              numaNode: 0
              vpdPosition: "-Aux[1]"
            dataDirect:
              rootComplex: pci0008:00
              bridges:
                - {address: "0008:01:00.0", parent: "0008:00:00.0"}
                - {address: "0008:02:00.0", parent: "0008:01:00.0"}
                - {address: "0008:02:03.0", parent: "0008:01:00.0"}
                - {address: "0008:04:00.0", parent: "0008:02:03.0"}
                - {address: "0008:05:00.0", parent: "0008:04:00.0"}
              address: "0008:03:00"
              parent: "0008:02:00.0"
              numaNode: 0
              vpdPosition: "-Prime"
        - slot: nic2
          profile: cx8-c8280z
          board: 1
          gpus: [3]
          devices:
            network:
              rootComplex: pci0010:00
              bridges:
                - {address: "0010:01:00.0", parent: "0010:00:00.0"}
                - {address: "0010:02:00.0", parent: "0010:01:00.0"}
                - {address: "0010:02:03.0", parent: "0010:01:00.0"}
                - {address: "0010:04:00.0", parent: "0010:02:03.0"}
                - {address: "0010:05:08.0", parent: "0010:04:00.0"}
                - {address: "0010:05:0c.0", parent: "0010:04:00.0"}
              address: "0010:03:00"
              parent: "0010:02:00.0"
              numaNode: 1
              vpdPosition: "-Prime"
            dataDirect:
              rootComplex: pci0019:00
              bridges:
                - {address: "0019:01:00.0", parent: "0019:00:00.0"}
                - {address: "0019:02:00.0", parent: "0019:01:00.0"}
                - {address: "0019:02:01.0", parent: "0019:01:00.0"}
                - {address: "0019:04:00.0", parent: "0019:02:01.0"}
                - {address: "0019:05:00.0", parent: "0019:04:00.0"}
              address: "0019:03:00"
              parent: "0019:02:00.0"
              numaNode: 1
              vpdPosition: "-Aux[1]"
        - slot: nic3
          profile: cx8-c8280z
          board: 1
          systemImageGUID: distinct
          gpus: [2]
          devices:
            network:
              rootComplex: pci0012:00
              bridges:
                - {address: "0012:01:00.0", parent: "0012:00:00.0"}
                - {address: "0012:02:00.0", parent: "0012:01:00.0"}
                - {address: "0012:02:01.0", parent: "0012:01:00.0"}
                - {address: "0012:04:00.0", parent: "0012:02:01.0"}
                - {address: "0012:05:08.0", parent: "0012:04:00.0"}
                - {address: "0012:05:0c.0", parent: "0012:04:00.0"}
              address: "0012:03:00"
              parent: "0012:02:00.0"
              numaNode: 1
              vpdPosition: "-Aux[1]"
            dataDirect:
              rootComplex: pci0018:00
              bridges:
                - {address: "0018:01:00.0", parent: "0018:00:00.0"}
                - {address: "0018:02:00.0", parent: "0018:01:00.0"}
                - {address: "0018:02:03.0", parent: "0018:01:00.0"}
                - {address: "0018:04:00.0", parent: "0018:02:03.0"}
                - {address: "0018:05:00.0", parent: "0018:04:00.0"}
              address: "0018:03:00"
              parent: "0018:02:00.0"
              numaNode: 1
              vpdPosition: "-Prime"
        - slot: frontend0
          profile: bf3-b3240
          devices:
            host:
              rootComplex: pci0016:00
              address: "0016:01:00"
              parent: "0016:00:00.0"
              numaNode: 1
```

<a id="sample-vr-nvl72"></a>
#### `vr-nvl72`

NIC slots of one VR NVL72 compute tray (tests/hardware-captures/vr-nvl72/node-a). Only the NIC-related fields of the rack profile, including the BlueField-4.

```yaml
kind: SGPURackProfile
metadata:
  name: vr-nvl72
spec:
  software:
    rdma: {umad: false, ipoib: false}
  node:
    gpus:
      pci: {vendorID: "10de", deviceID: "3041"}
    topology:
      rootPorts:
        - {address: "0001:00:00.0", rootComplex: pci0001:00, id: {vendorID: "10de", deviceID: "2f95"}}
        - {address: "0002:80:00.0", rootComplex: pci0002:80}
        - {address: "0002:c0:00.0", rootComplex: pci0002:c0}
        - {address: "0003:00:00.0", rootComplex: pci0003:00, id: {vendorID: "10de", deviceID: "2f95"}}
        - {address: "0004:00:00.0", rootComplex: pci0004:00, id: {vendorID: "10de", deviceID: "2f95"}}
        - {address: "0005:40:00.0", rootComplex: pci0005:40, id: {vendorID: "10de", deviceID: "2f96"}}
        - {address: "0006:00:00.0", rootComplex: pci0006:00, id: {vendorID: "10de", deviceID: "2f95"}}
        - {address: "0009:00:00.0", rootComplex: pci0009:00, id: {vendorID: "10de", deviceID: "2f95"}}
        - {address: "000a:80:00.0", rootComplex: pci000a:80}
        - {address: "000a:e0:00.0", rootComplex: pci000a:e0}
        - {address: "000b:00:00.0", rootComplex: pci000b:00, id: {vendorID: "10de", deviceID: "2f95"}}
        - {address: "000c:00:00.0", rootComplex: pci000c:00, id: {vendorID: "10de", deviceID: "2f95"}}
        - {address: "000d:40:00.0", rootComplex: pci000d:40, id: {vendorID: "10de", deviceID: "2f96"}}
        - {address: "000e:00:00.0", rootComplex: pci000e:00, id: {vendorID: "10de", deviceID: "2f95"}}
      gpuSlots:
        - {index: 0, rootComplex: "pci0002:80", pciAddress: "0002:81:00.0", parent: "0002:80:00.0", numaNode: 0}
        - {index: 1, rootComplex: "pci0002:c0", pciAddress: "0002:c1:00.0", parent: "0002:c0:00.0", numaNode: 0}
        - {index: 2, rootComplex: "pci000a:80", pciAddress: "000a:81:00.0", parent: "000a:80:00.0", numaNode: 1}
        - {index: 3, rootComplex: "pci000a:e0", pciAddress: "000a:e1:00.0", parent: "000a:e0:00.0", numaNode: 1}
      nics:
        - slot: nic0
          profile: cx9-vr
          gpus: [0, 1]
          devices:
            network:
              rootComplex: pci0001:00
              # The NIC's own PCIe switch; an NVMe drive sits behind 05:04.0.
              bridges:
                - {address: "0001:01:00.0", parent: "0001:00:00.0"}
                - {address: "0001:02:00.0", parent: "0001:01:00.0"}
                - {address: "0001:02:01.0", parent: "0001:01:00.0"}
                - {address: "0001:04:00.0", parent: "0001:02:01.0"}
                - {address: "0001:05:04.0", parent: "0001:04:00.0"}
              address: "0001:03:00"
              parent: "0001:02:00.0"
              numaNode: 0
        - slot: nic1
          profile: cx9-vr
          gpus: [0, 1]
          devices:
            network:
              rootComplex: pci0003:00
              address: "0003:01:00"
              parent: "0003:00:00.0"
              numaNode: 0
        - slot: nic2
          profile: cx9-vr
          gpus: [0, 1]
          devices:
            network:
              rootComplex: pci0004:00
              # The NIC's own PCIe switch; an NVMe drive sits behind 05:04.0.
              bridges:
                - {address: "0004:01:00.0", parent: "0004:00:00.0"}
                - {address: "0004:02:00.0", parent: "0004:01:00.0"}
                - {address: "0004:02:01.0", parent: "0004:01:00.0"}
                - {address: "0004:04:00.0", parent: "0004:02:01.0"}
                - {address: "0004:05:04.0", parent: "0004:04:00.0"}
              address: "0004:03:00"
              parent: "0004:02:00.0"
              numaNode: 0
        - slot: nic3
          profile: cx9-vr
          gpus: [0, 1]
          devices:
            network:
              rootComplex: pci0006:00
              address: "0006:01:00"
              parent: "0006:00:00.0"
              numaNode: 0
        - slot: nic4
          profile: cx9-vr
          gpus: [2, 3]
          devices:
            network:
              rootComplex: pci0009:00
              # The NIC's own PCIe switch; an NVMe drive sits behind 05:04.0.
              bridges:
                - {address: "0009:01:00.0", parent: "0009:00:00.0"}
                - {address: "0009:02:00.0", parent: "0009:01:00.0"}
                - {address: "0009:02:01.0", parent: "0009:01:00.0"}
                - {address: "0009:04:00.0", parent: "0009:02:01.0"}
                - {address: "0009:05:04.0", parent: "0009:04:00.0"}
              address: "0009:03:00"
              parent: "0009:02:00.0"
              numaNode: 1
        - slot: nic5
          profile: cx9-vr
          gpus: [2, 3]
          devices:
            network:
              rootComplex: pci000b:00
              address: "000b:01:00"
              parent: "000b:00:00.0"
              numaNode: 1
        - slot: nic6
          profile: cx9-vr
          gpus: [2, 3]
          devices:
            network:
              rootComplex: pci000c:00
              # The NIC's own PCIe switch; an NVMe drive sits behind 05:04.0.
              bridges:
                - {address: "000c:01:00.0", parent: "000c:00:00.0"}
                - {address: "000c:02:00.0", parent: "000c:01:00.0"}
                - {address: "000c:02:01.0", parent: "000c:01:00.0"}
                - {address: "000c:04:00.0", parent: "000c:02:01.0"}
                - {address: "000c:05:04.0", parent: "000c:04:00.0"}
              address: "000c:03:00"
              parent: "000c:02:00.0"
              numaNode: 1
        - slot: nic7
          profile: cx9-vr
          gpus: [2, 3]
          devices:
            network:
              rootComplex: pci000e:00
              address: "000e:01:00"
              parent: "000e:00:00.0"
              numaNode: 1
        - slot: frontend0
          profile: bf4-vr
          # One ASIC; its system image GUID is the node GUID of host link B's p0 PF.
          systemImageGUID: rdma-b0
          devices:
            hostA:
              rootComplex: pci0005:40
              address: "0005:41:00"
              parent: "0005:40:00.0"
              numaNode: 0
            hostB:
              rootComplex: pci000d:40
              address: "000d:41:00"
              parent: "000d:40:00.0"
              numaNode: 1
```

<a id="sample-gb300-ethernet"></a>
#### `gb300-ethernet`

NIC slots of one GB300 NVL72 tray at a cloud provider (tests/hardware-captures/gb300-roce/node-a): the same ConnectX-8 boards as gb300-nvl72 in their Ethernet multi-plane configuration, on newer firmware, and a ConnectX-7 front-end card.

```yaml
kind: SGPURackProfile
metadata:
  name: gb300-ethernet
spec:
  software:
    rdma: {umad: true, ipoib: true, umadMode: "666", driverVersion: 26.01-1.0.0}
  node:
    gpus:
      pci: {vendorID: "10de", deviceID: "31c2"}
    topology:
      rootPorts:
        - {address: "0000:00:00.0", rootComplex: pci0000:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0002:00:00.0", rootComplex: pci0002:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0007:00:00.0", rootComplex: pci0007:00, id: {vendorID: "10de", deviceID: "22b8"}}
        - {address: "0008:00:00.0", rootComplex: pci0008:00, id: {vendorID: "10de", deviceID: "22b1"}}
        - {address: "0009:00:00.0", rootComplex: pci0009:00, id: {vendorID: "10de", deviceID: "22b1"}}
        - {address: "0010:00:00.0", rootComplex: pci0010:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0012:00:00.0", rootComplex: pci0012:00, id: {vendorID: "10de", deviceID: "22b2"}}
        - {address: "0017:00:00.0", rootComplex: pci0017:00, id: {vendorID: "10de", deviceID: "22b8"}}
        - {address: "0018:00:00.0", rootComplex: pci0018:00, id: {vendorID: "10de", deviceID: "22b1"}}
        - {address: "0019:00:00.0", rootComplex: pci0019:00, id: {vendorID: "10de", deviceID: "22b1"}}
      gpuSlots:
        - {index: 0, rootComplex: "pci0008:00", pciAddress: "0008:06:00.0", parent: "0008:05:00.0", numaNode: 0}
        - {index: 1, rootComplex: "pci0009:00", pciAddress: "0009:06:00.0", parent: "0009:05:00.0", numaNode: 0}
        - {index: 2, rootComplex: "pci0018:00", pciAddress: "0018:06:00.0", parent: "0018:05:00.0", numaNode: 1}
        - {index: 3, rootComplex: "pci0019:00", pciAddress: "0019:06:00.0", parent: "0019:05:00.0", numaNode: 1}
      nics:
        - slot: nic0
          profile: cx8-c8280z
          configuration: eth-multiplane
          firmwareVersion: "40.48.1102"
          board: 0
          gpus: [1]
          devices:
            network:
              rootComplex: pci0000:00
              bridges:
                - {address: "0000:01:00.0", parent: "0000:00:00.0"}
                - {address: "0000:02:00.0", parent: "0000:01:00.0"}
                - {address: "0000:02:03.0", parent: "0000:01:00.0"}
                - {address: "0000:04:00.0", parent: "0000:02:03.0"}
                - {address: "0000:05:08.0", parent: "0000:04:00.0"}
                - {address: "0000:05:0c.0", parent: "0000:04:00.0"}
              address: "0000:03:00"
              parent: "0000:02:00.0"
              numaNode: 0
              vpdPosition: "-Prime"
            dataDirect:
              rootComplex: pci0009:00
              bridges:
                - {address: "0009:01:00.0", parent: "0009:00:00.0"}
                - {address: "0009:02:00.0", parent: "0009:01:00.0"}
                - {address: "0009:02:01.0", parent: "0009:01:00.0"}
                - {address: "0009:04:00.0", parent: "0009:02:01.0"}
                - {address: "0009:05:00.0", parent: "0009:04:00.0"}
              address: "0009:03:00"
              parent: "0009:02:00.0"
              numaNode: 0
              vpdPosition: "-Aux[1]"
        - slot: nic1
          profile: cx8-c8280z
          configuration: eth-multiplane
          firmwareVersion: "40.48.1102"
          board: 0
          systemImageGUID: distinct
          gpus: [0]
          devices:
            network:
              rootComplex: pci0002:00
              bridges:
                - {address: "0002:01:00.0", parent: "0002:00:00.0"}
                - {address: "0002:02:00.0", parent: "0002:01:00.0"}
                - {address: "0002:02:01.0", parent: "0002:01:00.0"}
                - {address: "0002:04:00.0", parent: "0002:02:01.0"}
                - {address: "0002:05:08.0", parent: "0002:04:00.0"}
                - {address: "0002:05:0c.0", parent: "0002:04:00.0"}
              address: "0002:03:00"
              parent: "0002:02:00.0"
              numaNode: 0
              vpdPosition: "-Aux[1]"
            dataDirect:
              rootComplex: pci0008:00
              bridges:
                - {address: "0008:01:00.0", parent: "0008:00:00.0"}
                - {address: "0008:02:00.0", parent: "0008:01:00.0"}
                - {address: "0008:02:03.0", parent: "0008:01:00.0"}
                - {address: "0008:04:00.0", parent: "0008:02:03.0"}
                - {address: "0008:05:00.0", parent: "0008:04:00.0"}
              address: "0008:03:00"
              parent: "0008:02:00.0"
              numaNode: 0
              vpdPosition: "-Prime"
        - slot: nic2
          profile: cx8-c8280z
          configuration: eth-multiplane
          firmwareVersion: "40.48.1102"
          vpd: {EC: A5}
          board: 1
          gpus: [3]
          devices:
            network:
              rootComplex: pci0010:00
              bridges:
                - {address: "0010:01:00.0", parent: "0010:00:00.0"}
                - {address: "0010:02:00.0", parent: "0010:01:00.0"}
                - {address: "0010:02:03.0", parent: "0010:01:00.0"}
                - {address: "0010:04:00.0", parent: "0010:02:03.0"}
                - {address: "0010:05:08.0", parent: "0010:04:00.0"}
                - {address: "0010:05:0c.0", parent: "0010:04:00.0"}
              address: "0010:03:00"
              parent: "0010:02:00.0"
              numaNode: 1
              vpdPosition: "-Prime"
            dataDirect:
              rootComplex: pci0019:00
              bridges:
                - {address: "0019:01:00.0", parent: "0019:00:00.0"}
                - {address: "0019:02:00.0", parent: "0019:01:00.0"}
                - {address: "0019:02:01.0", parent: "0019:01:00.0"}
                - {address: "0019:04:00.0", parent: "0019:02:01.0"}
                - {address: "0019:05:00.0", parent: "0019:04:00.0"}
              address: "0019:03:00"
              parent: "0019:02:00.0"
              numaNode: 1
              vpdPosition: "-Aux[1]"
        - slot: nic3
          profile: cx8-c8280z
          configuration: eth-multiplane
          firmwareVersion: "40.48.1102"
          vpd: {EC: A5}
          board: 1
          systemImageGUID: distinct
          gpus: [2]
          devices:
            network:
              rootComplex: pci0012:00
              bridges:
                - {address: "0012:01:00.0", parent: "0012:00:00.0"}
                - {address: "0012:02:00.0", parent: "0012:01:00.0"}
                - {address: "0012:02:01.0", parent: "0012:01:00.0"}
                - {address: "0012:04:00.0", parent: "0012:02:01.0"}
                - {address: "0012:05:08.0", parent: "0012:04:00.0"}
                - {address: "0012:05:0c.0", parent: "0012:04:00.0"}
              address: "0012:03:00"
              parent: "0012:02:00.0"
              numaNode: 1
              vpdPosition: "-Aux[1]"
            dataDirect:
              rootComplex: pci0018:00
              bridges:
                - {address: "0018:01:00.0", parent: "0018:00:00.0"}
                - {address: "0018:02:00.0", parent: "0018:01:00.0"}
                - {address: "0018:02:03.0", parent: "0018:01:00.0"}
                - {address: "0018:04:00.0", parent: "0018:02:03.0"}
                - {address: "0018:05:00.0", parent: "0018:04:00.0"}
              address: "0018:03:00"
              parent: "0018:02:00.0"
              numaNode: 1
              vpdPosition: "-Prime"
        - slot: frontend0
          profile: cx7-hhhl
          board: 2
          devices:
            hostA:
              rootComplex: pci0007:00
              address: "0007:01:00"
              parent: "0007:00:00.0"
              numaNode: 0
              vpdPosition: "-Prime"
            hostB:
              rootComplex: pci0017:00
              address: "0017:01:00"
              parent: "0017:00:00.0"
              numaNode: 1
              vpdPosition: "-Aux[1]"
```

<a id="sample-aws-gb300"></a>
#### `aws-gb300`

NIC slots of one AWS p6e-gb300r.36xlarge instance. The guest sees two ConnectX-8 VFs and a Data Direct function per GPU, no PFs. Which VFs a Data Direct function serves isn't visible from inside the instance, so the slots group them by NUMA node and PCIe adjacency. The bridges above each function aren't ConnectX bridges and their IDs aren't recorded.

```yaml
kind: SGPURackProfile
metadata:
  name: aws-gb300
spec:
  software:
    rdma: {umad: true, ipoib: true}
  node:
    gpus:
      pci: {vendorID: "10de", deviceID: "31c2"}
    topology:
      rootPorts:
        - {address: "0000:98:00.0", rootComplex: pci0000:98}
        - {address: "0000:9e:00.0", rootComplex: pci0000:9e}
        - {address: "0000:a3:00.0", rootComplex: pci0000:a3}
        - {address: "0000:a9:00.0", rootComplex: pci0000:a9}
        - {address: "0000:b5:00.0", rootComplex: pci0000:b5}
        - {address: "0000:bb:00.0", rootComplex: pci0000:bb}
        - {address: "0000:c0:00.0", rootComplex: pci0000:c0}
        - {address: "0000:c6:00.0", rootComplex: pci0000:c6}
      gpuSlots:
        - {index: 0, rootComplex: "pci0000:98", pciAddress: "0000:9d:00.0", numaNode: 0}
        - {index: 1, rootComplex: "pci0000:a3", pciAddress: "0000:a8:00.0", numaNode: 0}
        - {index: 2, rootComplex: "pci0000:b5", pciAddress: "0000:ba:00.0", numaNode: 1}
        - {index: 3, rootComplex: "pci0000:c0", pciAddress: "0000:c5:00.0", numaNode: 1}
      nics:
        - slot: aws0
          profile: cx8-aws-vf
          gpus: [0]
          devices:
            vfA: {rootComplex: pci0000:9e, address: "0000:a1:00", parent: "0000:9e:00.0", numaNode: 0}
            vfB: {rootComplex: pci0000:9e, address: "0000:a2:00", parent: "0000:9e:00.0", numaNode: 0}
            dataDirect: {rootComplex: pci0000:98, address: "0000:9c:00", parent: "0000:98:00.0", numaNode: 0}
        - slot: aws1
          profile: cx8-aws-vf
          gpus: [1]
          devices:
            vfA: {rootComplex: pci0000:a9, address: "0000:ac:00", parent: "0000:a9:00.0", numaNode: 0}
            vfB: {rootComplex: pci0000:a9, address: "0000:ad:00", parent: "0000:a9:00.0", numaNode: 0}
            dataDirect: {rootComplex: pci0000:a3, address: "0000:a7:00", parent: "0000:a3:00.0", numaNode: 0}
        - slot: aws2
          profile: cx8-aws-vf
          gpus: [2]
          devices:
            vfA: {rootComplex: pci0000:bb, address: "0000:be:00", parent: "0000:bb:00.0", numaNode: 1}
            vfB: {rootComplex: pci0000:bb, address: "0000:bf:00", parent: "0000:bb:00.0", numaNode: 1}
            dataDirect: {rootComplex: pci0000:b5, address: "0000:b9:00", parent: "0000:b5:00.0", numaNode: 1}
        - slot: aws3
          profile: cx8-aws-vf
          gpus: [3]
          devices:
            vfA: {rootComplex: pci0000:c6, address: "0000:c9:00", parent: "0000:c6:00.0", numaNode: 1}
            vfB: {rootComplex: pci0000:c6, address: "0000:ca:00", parent: "0000:c6:00.0", numaNode: 1}
            dataDirect: {rootComplex: pci0000:c0, address: "0000:c4:00", parent: "0000:c0:00.0", numaNode: 1}
```

<a id="sample-aws-c5n"></a>
#### `aws-c5n`

NIC slots of one AWS c5n.9xlarge instance: one EFA adapter on the root bus.

```yaml
kind: SGPURackProfile
metadata:
  name: aws-c5n
spec:
  software:
    rdma: {umad: false, ipoib: false}
  node:
    topology:
      nics:
        - slot: efa0
          profile: efa-c5n
          devices:
            efa: {rootComplex: pci0000:00, address: "0000:00:1f", numaNode: -1}
```
