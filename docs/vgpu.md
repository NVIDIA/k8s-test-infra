# vGPU

Everything the `virtualization.vgpu` block of a profile means: how a GPU
becomes a vGPU host or a vGPU guest, the fields each side takes, what NVML and
`nvidia-smi` report for them, and what is refused at load.

This is a reference. For installing a host or a guest node and reading it back
with `nvidia-smi`, see the [vGPU host and guest guide](guides/vgpu/README.md);
for the rest of a profile, [Configuration](configuration.md).

## What it simulates

NVIDIA vGPU splits a physical GPU into virtual GPUs that a hypervisor hands to
virtual machines (VMs). Two drivers are involved, and each sees something
different. The **host** is the hypervisor's physical GPU, running the vGPU
manager and the vGPU instances carved from it. The **guest** is the vGPU as
the VM sees it: a GPU that knows it is virtual, which host driver it runs on,
and whether it holds a vGPU software license. Mokka simulates either view at
the driver level, so `nvidia-smi` and go-nvml consumers read it as they would
on a real hypervisor or VM. No hypervisor or VM is involved.

## Choosing a side

`device_defaults.virtualization.mode` selects what NVML reports:

| `mode` | `nvmlDeviceGetVirtualizationMode` | What else changes |
|---|---|---|
| `none` | `NVML_GPU_VIRTUALIZATION_MODE_NONE` | Nothing: bare metal. Every shipped profile declares it |
| `passthrough` | `NVML_GPU_VIRTUALIZATION_MODE_PASSTHROUGH` | Nothing else |
| `vgpu` | `NVML_GPU_VIRTUALIZATION_MODE_VGPU` | With a [`guest` block](#guest): licensing, and the PCI capability GFD reads |
| `host_vgpu` | `NVML_GPU_VIRTUALIZATION_MODE_HOST_VGPU` | The [host](#host) types and running instances |

`nvidia-smi -q` prints the two vGPU modes as `VGPU` and `Host VGPU`. An
unrecognised mode reports `NONE` and ignores the `vgpu` block.

vGPU is configured in `device_defaults` only: a
[per-device override](configuration.md#per-device-overrides) does not merge
`virtualization`, so every GPU on the node starts on the same side. Only a
[runtime override](#runtime-overrides) can move one GPU to another.

### Under the chart

The chart sets the block from `gpu.vgpu`, so an install names neither the
NVML mode nor the profile key:

| `gpu.vgpu` | Default | Meaning |
|---|---|---|
| `mode` | `""` | `host` renders `mode: host_vgpu`, `guest` renders `mode: vgpu`, `""` leaves the profile's block untouched |
| `hostVgpuMode` | `sriov` | Host only. Becomes `host_vgpu_mode` |
| `types` | `[]` | Host only. The [types](#types), in the profile's snake_case form |
| `instances` | `[]` | Host only. The [running instances](#instances) |
| `guest` | `{}` | Guest only. The [guest block](#guest), in the profile's snake_case form |

`mode: host` passes only `hostVgpuMode`, `types` and `instances`; `mode: guest`
passes only `guest`. The chart ignores the other mode's fields rather than
refusing them. `mode: guest` with an empty `guest` renders `mode: vgpu` and no
`vgpu` block, which is the [mode-only guest](#a-guest-with-no-block). The chart
refuses to render `mode: host` with no `types` or with `gpu.mig.enabled`; every
other rule is the node agent's, which [validates](#validation) the rendered
block.

### Runtime overrides

[`nvml-mock-ctl set`](nvml-mock-ctl.md#set--set-arbitrary-fields) can change
`virtualization` on a running node, for one GPU or for all of them. An override
is not validated like the profile: only its field names and value types are
checked, not the [validation rules](#validation). A GPU set to `host_vgpu` with
no `vgpu` block, for example, reports `Host VGPU` as its mode and answers
`NVML_ERROR_NOT_SUPPORTED` to every vGPU call. The guest's PCI capability is
written by the node agent from the profile, so an override does not change it.

Toggling a guest's license works:

```bash
nvml-mock-ctl set --gpu 0 virtualization.vgpu.guest.license.state=unlicensed
```

The next `nvidia-smi -q` reports GPU 0 unlicensed.

## Host

```yaml
device_defaults:
  virtualization:
    mode: "host_vgpu"
    host_vgpu_mode: "sriov"           # sriov, non_sriov
    vgpu:
      types:
        - id: 474
          name: "NVIDIA A100-4C"
          framebuffer_mib: 4096
          max_instances: 10
          license: "NVIDIA-Virtual-Compute-Server,9.0"
      instances:
        - type: "NVIDIA A100-4C"
          count: 2
```

No profile ships a type list. Which types a board offers depends on the vGPU
software release, not on the silicon, so an install declares its own, taking
the IDs, names and sizes from that release's documentation.

`host_vgpu_mode` is required: `sriov` or `non_sriov`. `nvidia-smi -q` prints it
as `Host VGPU Mode`.

### Types

| Field | Default | Meaning |
|---|---|---|
| `id` | required | The vGPU type ID NVML hands out. Non-zero, unique within the list. A type ID names the same type on every GPU |
| `name` | required | What `nvidia-smi vgpu` prints, e.g. `NVIDIA A100-4C`. Unique within the list; `instances` refer to it |
| `framebuffer_mib` | required | Framebuffer per instance, non-zero |
| `max_instances` | required | How many instances of this type one GPU holds, `1`–`999` |
| `class` | `Compute` | The type's class string |
| `max_instances_per_vm` | `1` | Instances one VM may hold |
| `license` | `""` | The license string the type requires |
| `num_display_heads` | `1` | Display heads |
| `resolution` | `{width: 4096, height: 2160}` | Maximum resolution, reported for every display head |
| `frame_rate_limit` | `0` | Frame-rate limit |
| `gpu_instance_profile_id` | not MIG-backed (`0xFFFFFFFF`) | The MIG GPU instance profile behind the type |
| `device_id` | the board's `pci.device_id` | The PCI device and vendor ID the type presents to its guest, encoded as in the profile's `pci` block |
| `subsystem_id` | the board's `pci.subsystem_id` | The PCI subsystem and subsystem vendor ID the type presents to its guest |

The two PCI IDs are what `nvmlVgpuTypeGetDeviceID` reports. NVML reports them
per type rather than per board, so each can be set per type; one left unset
takes the board's.

### Instances

Each entry asks for `count` running instances of the named type. An omitted
count means one. Instances run from the moment the library loads; no NVML call
creates or destroys them, and only a [runtime override](#runtime-overrides) of
`instances` changes the set.

A GPU runs one vGPU type at a time, so every entry names the same type, and
their counts together are bounded by that type's `max_instances`. When the
profile sets `memory.total_bytes`, their framebuffer together must fit it.

### Host identities

An instance's ID is `(GPU index + 1) × 1000 + slot + 1`, where the slot counts
the GPU's instances from zero in declaration order: GPU 0's first instance is
`1001`, GPU 1's second is `2002`. The GPU index is the GPU's physical index on
the node, not the enumeration index a container sees: in a container given only
GPU 3, that GPU's first instance is still `4001`. The ID encodes its GPU, so it
is the same in every process.

Each instance's vGPU UUID and VM UUID are derived from its GPU's UUID and slot.
They are stable across restarts when the profile declares `devices[].uuid`,
which every shipped profile does.

Every instance runs in a VM of its own. The VM name is blank. The guest driver
version is `Not Available`, which is what NVML reports until a driver loads in
the VM, and no simulated VM loads one; `nvidia-smi` prints it as `N/A`. ECC
mode follows the board's
`ecc.mode_current`, and the GPU
instance ID is not MIG-backed, which `nvidia-smi` prints as `N/A`.

### Host behaviour

**Supported and creatable types.** `nvidia-smi vgpu -s` lists every declared
type. `nvidia-smi vgpu -c` lists what could still be created: every declared
type while no instance runs, then only the running type until the GPU holds
`max_instances` of it, then nothing.

**Idle instances.** No process runs in a simulated guest, so every instance
reports no framebuffer used, no encoder or frame buffer capture (FBC) sessions,
and accounting disabled. Each reports itself licensed, with a permanent
license.

**Not implemented, `N/A`.** These calls return `NVML_ERROR_NOT_SUPPORTED`, which
`nvidia-smi` prints as `N/A`: an instance's placement ID, mdev UUID, accounting
PIDs, utilization and guest PCI bus ID; a type's BAR1 info, capabilities,
placements and maximum instances per GPU instance; and the device's vGPU
capabilities, vGPU driver capabilities and heterogeneous mode.

**Errors.** A host-side vGPU call on a GPU that is not in `host_vgpu` mode
returns `NVML_ERROR_NOT_SUPPORTED`, as a bare-metal driver does, and
`nvidia-smi vgpu` prints `No supported devices in vGPU mode`. An unknown type
ID returns `NVML_ERROR_INVALID_ARGUMENT`. For an instance ID, NVML reserves
`NVML_ERROR_INVALID_ARGUMENT` for `0`; any other ID that matches no running
instance, including one on a GPU the caller cannot see, returns
`NVML_ERROR_NOT_FOUND`. The vGPU calls answer
from configuration and do not advance
[failure injection](helm-chart.md#failure-injection)'s call counter. A call that
takes a device handle or an instance ID reports the loss once failure injection
has made that GPU lost; a call that takes only a type ID answers from any other
GPU that declares the type.

**`nvidia-smi pmon` declines a host GPU**, printing `The feature is not
supported in this configuration` for each one, as it does on a hypervisor.

## Guest

```yaml
device_defaults:
  virtualization:
    mode: "vgpu"
    vgpu:
      guest:
        host_driver_version: "550.163.01"
        host_driver_branch: "r550_00"
        license:
          product: "NVIDIA Virtual Compute Server"
          state: "licensed"           # licensed, unlicensed
          expiry: "2027-01-01T00:00:00Z"
```

| Field | Default | Meaning |
|---|---|---|
| `host_driver_version` | required | The host's vGPU manager version. At most 10 bytes |
| `host_driver_branch` | required | The host driver's branch. At most 10 bytes |
| `license` | absent: unlicensed | The vGPU software license the guest holds |
| `license.product` | `""` | The licensed product name. At most 127 bytes |
| `license.state` | `licensed` | `licensed` or `unlicensed` |
| `license.expiry` | absent: permanent | When a licensed guest's license expires, as an RFC 3339 time |

The two host driver fields are limited to 10 bytes because that is the width of
the fields in the PCI capability that carries them.

What changes for a guest:

- `nvidia-smi -q` prints `Virtualization Mode : VGPU` and a
  `vGPU Software Licensed Product` section with the product and its
  `License Status`, such as `Licensed (Expiry: Permanent)`. An unlicensed guest
  reports the unlicensed state and no expiry.
- The node agent writes the vGPU vendor capability, holding the host driver
  version and branch, into each GPU's config space in its
  [PCI sysfs tree](helm-chart.md#pci-sysfs-in-containers). GPU Feature
  Discovery (GFD) reads it from there and labels the node
  `nvidia.com/vgpu.present`, `nvidia.com/vgpu.host-driver-version` and
  `nvidia.com/vgpu.host-driver-branch`.

Licensing is reported through `nvmlDeviceGetGridLicensableFeatures` (v4, which
the unversioned name resolves to); a GPU that is not a guest with a `guest`
block answers `NVML_ERROR_NOT_SUPPORTED`.

### A guest with no block

`mode: vgpu` with no `guest` block reports `Virtualization Mode : VGPU` and
nothing else: no PCI capability, and licensing answers
`NVML_ERROR_NOT_SUPPORTED`.

## Validation

The block is checked when the config loads, and each failure names the field:

- `host_vgpu` needs `host_vgpu_mode` set to `sriov` or `non_sriov`, and at
  least one type.
- Every type needs a non-zero `id`, a `name` and a non-zero `framebuffer_mib`,
  with `max_instances` between 1 and 999. No two types share an `id` or a
  `name`.
- Every instance names a declared type, and its `count` is not negative.
- All instances name the same type.
- The instances' total count does not exceed that type's `max_instances`.
- When the profile sets `memory.total_bytes`, the instances' total framebuffer
  does not exceed it.
- `guest` is refused under `host_vgpu`; `types` and `instances` are refused
  under `vgpu`.
- A host runs no GPU in MIG mode: `host_vgpu` is refused when
  `device_defaults.mig` or any per-device `mig` block sets `mode_current:
  enabled`, and `nvmlDeviceSetMigMode` refuses to enable MIG on a host with
  `NVML_ERROR_NOT_SUPPORTED`. A real host with MIG on runs only MIG-backed
  types, which are not simulated.
- A `guest` block needs `host_driver_version` and `host_driver_branch`, each at
  most 10 bytes.
- `license.state` is `licensed` or `unlicensed`, `license.product` is at most
  127 bytes, and `license.expiry` parses as RFC 3339.

Any other `mode` ignores the `vgpu` block, which is why the `host_vgpu_mode:
"N/A"` every shipped profile carries loads.

The node agent refuses an invalid block and does not become ready, logging the
field as a `state source error`. The library rejects the same config and falls
back to its built-in defaults with a warning.

!!! note "Not simulated"
    - The host's mdev and SR-IOV sysfs surfaces.
    - MIG-backed vGPU types, and so a host with MIG mode on.
    - Creating or destroying instances at runtime.
    - Shipped vGPU type catalogues: an install declares its types.
    - Charging the instances' framebuffer against the physical GPU's memory.
    - Scheduler, placement and heterogeneous-mode APIs.
    - `nvmlGetVgpuVersion` and `nvmlSetVgpuVersion`.
    - vGPU metadata and migration compatibility.
    - Every `Set*` call.
    - `nvmlDeviceGetGridLicensableFeatures` v1–v3.
    - KubeVirt or any other VM manager.
