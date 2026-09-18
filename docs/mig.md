# MIG

Everything the `mig:` blocks of a profile mean: what makes a board
MIG-capable, the partition table that says what it can be carved into, where
that table lives, and the two ways to declare a layout.

This is a reference. For installing a partitioned node and scheduling a pod
onto a slice, see the [MIG partitioning guide](guides/mig/README.md); for the
rest of a profile, [Configuration](configuration.md).

## What makes a board MIG-capable

```yaml
device_defaults:
  mig:
    mode_current: "disabled"
    mode_pending: "disabled"
    max_gpu_instances: 7
```

Those three keys are the whole of `device_defaults.mig` — they are properties
of the silicon, and they are small. The board's partition table,
`supported_profiles`, is several hundred rows of geometry and lives in
[a document of its own](#declaring-the-profile-table). A per-device `mig:`
block, which declares a layout rather than a capability, is described under
[declaring a layout by count](#declaring-a-layout-by-count); it inherits the
board's table and instance ceiling, so a device names only its mode and its
partitions.

No shipped profile declares a partitioning. `max_gpu_instances` is what the
board can do; how it is carved is a deployment choice, which under the chart is
`gpu.mig.gpuInstances` — required whenever `gpu.mig.enabled` is set, on every
board. Boards with no `mig` block at all — `t4`, `l40s` — are not MIG-capable,
and NVML answers `NVML_ERROR_NOT_SUPPORTED` for them as real hardware does.

## Declaring the profile table

`supported_profiles` is the board's MIG profile table: the rows
`nvidia-smi mig -lgip` prints. It is declared in YAML rather than resolved in
Go from the device name, so teaching the mock a new board is a YAML edit. The
table is a document of its own, not part of the profile — see
[where the table lives](#where-the-table-lives) for how the engine finds it.
Each row describes its partition completely — the slices it spans, the id
the board publishes for it, the slots it may occupy and the compute instances
it offers — and the engine reports those values rather than deriving them from
each other. One piece of geometry is still synthesized; see
[compute-instance placements](#compute-instance-placements) below. The whole
of `h100`'s table document, with one full row of its seven:

```yaml
version: 1               # decoded, and nothing reads it yet
supported_profiles:
  - name: "1g.10gb"
    nvml_profile: "1_SLICE"
    slices: 1
    profile_id: 19
    instances: 7
    memory_mb: 10240
    multiprocessors: 16
    copy_engines: 1
    decoders: 1
    jpeg: 1
    placements:            # memory units, not compute slices
      - {start: 0, size: 1}
      - {start: 1, size: 1}
      - {start: 2, size: 1}
      - {start: 3, size: 1}
      - {start: 4, size: 1}
      - {start: 5, size: 1}
      - {start: 6, size: 1}
    compute_instances:     # the row's `mig -lcip` listing
      - nvml_profile: "1_SLICE"
        slices: 1
        instances: 1
        multiprocessors: 16
        shared_copy_engines: 1
        decoders: 1
        jpeg: 1
      - nvml_profile: "1_SLICE_REV1"
        slices: 1
        instances: 1
        multiprocessors: 16
        shared_copy_engines: 1
        decoders: 1
        jpeg: 1
```

`supported_profiles` sits at the root of the table document, and the engine
attaches it to `device_defaults.mig.supported_profiles` before validation runs,
so a row is checked identically wherever it was written.

| Field | Meaning |
|---|---|
| `name` | What the listing prints and the cluster spells, e.g. `1g.10gb`, `1g.10gb+me`. Required, and refused if two rows share one — go-nvlib derives the `nvidia.com/mig-<name>` resource name from it |
| `nvml_profile` | NVML's profile enum suffix — `1_SLICE`, `1_SLICE_REV1`, `2_SLICE`, `7_SLICE` — which every lookup keys on. An unknown suffix, or one declared twice, is refused at load |
| `slices` | How many compute slices the partition spans. The enum carries the same width, and a row where the two disagree is refused, so this is a second reading of one fact rather than a second source for it. Omitted takes the enum's width |
| `profile_id` | The id the board publishes for this profile: the `ID` column of `mig -lgip`, and what `mig -cgi <id>` takes. It is not the NVML enum — an A100's `1g.5gb` binds `1_SLICE` and publishes `19`. Two rows publishing one id are refused, which is also what catches a row that omits the key, since an omitted `profile_id` publishes `0` |
| `instances` | How many of this profile the board offers at once. Bounded at load by how many partitions of that width fit the board: a 2-slice profile fits a 7-slice board three times, not seven |
| `memory_mb` | The partition's framebuffer. Checked at load against the size `name` advertises, loosely — a real allocation runs short of its name, by more on the wider profiles |
| `placements` | The slots on the board this partition may occupy, in memory units. Required — see below |
| `compute_instances` | The row's `nvidia-smi mig -lcip` listing. Required — see below |
| `multiprocessors`, `copy_engines`, `decoders`, `encoders`, `jpeg`, `ofa` | Engine counts the listing reports. Omitted is zero |

`placements` are measured in **memory units, not compute slices**, and this is
the single easiest thing to get wrong on a new board. A board has as many
memory units as the next power of two at or above its `max_gpu_instances` —
eight for a 7-slice board, four for a 4-slice one. So a `3g` partition holding
half the board's memory occupies four of eight units, and the full-board `7g`
is one `{start: 0, size: 8}` rather than a size of 7. A `1g` row on a 7-slice
board is seven starts of size 1, and a 1-slice row holding two eighths of the
memory — `h100`'s `1g.20gb` — is four starts of size 2.

Load refuses a `size` that is not a power of two, since the units divide the
board exactly and every start is aligned to its own size; that is what lets a
`1g`, a `2g` and a `3g` placement coexist without a partial overlap, and it
rules out a size of zero. It also refuses a `start` plus `size` that runs past
the board's memory units, and two placements sharing a `start`, which would
advertise one slot twice.

`compute_instances` is the row's `mig -lcip` listing. A GPU instance does not
offer every width that fits inside it — a 7-slice instance offers `3c` and `4c`
and then jumps to `7c` — so the listing is declared per row rather than
enumerated from the width.

| Field | Meaning |
|---|---|
| `nvml_profile` | NVML's compute instance profile enum suffix, e.g. `1_SLICE`. Unknown, or declared twice within one row, is refused |
| `slices` | Cross-checked against the enum the same way the GPU instance's `slices` is, and additionally bounded by the width of the GPU instance it sits in |
| `instances` | How many of this compute instance fit the GPU instance: three `2c` in a `7g`, not seven |
| `multiprocessors` | This compute instance's own share of the GPU instance's SMs |
| `shared_copy_engines`, `decoders`, `encoders`, `jpeg`, `ofa` | NVML's `Shared*` counts. Every compute instance inside a GPU instance sees all of its fixed-function engines, so these repeat the GPU instance's counts |

### Compute-instance placements

One piece of geometry is not declared and is still synthesized: the
compute-slice offsets a compute instance may occupy *inside* its GPU instance,
which `nvmlGpuInstanceGetComputeInstancePossiblePlacements` reports. A
`compute_instances` entry has no field for them, so the engine lays them out
from the entry itself — `instances` placements of `slices` slices each, end to
end — which is the layout every board NVIDIA publishes a listing for follows.

The consequence for a new board: a board whose compute-instance placements are
not uniform end to end cannot be expressed in YAML yet. The GPU instance's own
`placements` are unaffected — those are declared, in memory units, as above.

Declaring the geometry costs about 400 lines of YAML per board, and a
contributor adding a board writes all of it. That is a deliberate trade of
verbosity for expressiveness, not a simplification: an algorithm can only
produce the geometry it was taught, so a board whose layout does not match one
cannot be expressed in YAML at all — and the derivations that used to fill
these fields in Go were where this area's defects concentrated.

Transcribe rows from the Supported MIG Profiles tables of NVIDIA's MIG user
guide, which is where every shipped profile's rows come from — each names its
table in a YAML comment. NVIDIA publishes placements only as diagrams, so those
are transcribed from the layout the diagrams show.

## Where the table lives

The table is a sibling document of the profile, not a section of it. The five
MIG-capable boards ship theirs twice, once per config tree:

| | file |
|---|---|
| chart profiles | `deployments/nvml-mock/helm/nvml-mock/profiles/mig/<board>.yaml` |
| standalone configs | `pkg/gpu/mocknvml/configs/mock-nvml-config-<board>.mig.yaml` |

The engine resolves the table at load time, in this order:

1. **`MOCK_MIG_PROFILES_CONFIG`.** An explicit path, and what the chart sets.
2. **A sibling derived from the config path**, replacing the extension:
   `config.yaml` → `config.mig.yaml`. This is what makes a local run and the
   standalone configs work with no environment set at all, and why five boards'
   tables share one directory without colliding.

Under the chart nothing needs setting. The selected board's table renders into
a ConfigMap of its own, `<fullname>-mig-profiles`, keyed `mig-profiles.yaml`
and mounted read-only at `/etc/nvml-mock/mig`; the container gets
`MOCK_MIG_PROFILES_CONFIG=/etc/nvml-mock/mig/mig-profiles.yaml`. The chart's
`mig/` layout is not a sibling of the profile it belongs to, so the env var is
the only thing that finds it there. A table edit rolls the DaemonSet through
its own `checksum/mig-profiles` annotation.

### Consumer pods get the table without any environment

A pod that loads the mock library — the device plugin above all — reads the
config the [node agent](tools/node-agent.md) stages under the mock root,
and carries no environment naming a table. The agent therefore writes the table
beside each config it stages, as `config.mig.yaml`, so those processes resolve
it by rule 2 above. Nothing has to be mounted into the consumer.

This is why a board's table has to reach the agent and not only the library:
a consumer whose config had no table beside it would see a board that cannot
partition, and the device plugin would publish whole GPUs on a node where NVML
reports partitions.

!!! warning "A table declared inline *and* externally is refused"

    A config that keeps `supported_profiles` under `device_defaults.mig` while
    a table also resolves fails to load, naming both sources. This is an
    ambiguity rather than a precedence question: whichever table lost would be
    one somebody authored and the mock silently ignored, and nothing a consumer
    sees through NVML says which file is in force.

    For an externally-authored config written against the old single-file
    layout, that is the migration instruction: **move** the table into a
    sibling document, do not copy it. Nothing the chart ships declares a table
    inline, so no chart install can reach this.

### A board with no resolvable table is not MIG-capable

An absent table is not a load error. The board simply has no partition table,
which is already what a board declaring no profiles means — `t4` and `l40s`
have always been that board — so NVML answers `NVML_ERROR_NOT_SUPPORTED` for
every MIG call on it, and the rest of the config loads and serves normally.
The only trace is one line at debug level:

```text
[CONFIG] MIG profiles: no table at /etc/nvml-mock/mig/mig-profiles.yaml, board is not MIG-capable
```

This is deliberate: the thing that can go missing is a Kubernetes mount, and
taking a whole board's config down because one mount did not arrive would be
worse than a board that reports no MIG. The cost is that a board which
*should* partition and a board which genuinely cannot look identical from
outside.

So if a board that should partition does not — `nvidia-smi mig` declining on
it, `NVML_ERROR_NOT_SUPPORTED` reaching a consumer — the table is the first
thing to check, and the debug line is how. Every process loads the engine for
itself, so run one with debug logging on:

```bash
kubectl exec ds/nvml-mock -- env MOCK_NVML_DEBUG=1 \
  nvidia-smi mig -lgip 2>&1 | grep 'MIG profiles'
```

The line prints the exact path that was tried, which says whether the env var,
the mount or the file name is wrong. No line at all means no path resolved:
neither `MOCK_MIG_PROFILES_CONFIG` nor a config path was set.

Everything else is an operator authoring mistake and does fail the load, with
an error naming the file: unreadable, unparseable, a table file declaring no
rows, a row failing the validation above, or the inline-and-external clash.

## Declaring a layout by count

`gpu_instances` asks for a number of identical instances. Name each profile
either by name or by the id the board publishes for it:

```yaml
devices:
  - index: 0
    mig:
      mode_current: "enabled"
      gpu_instances:
        - profile: "3g.20gb"   # by name
          count: 2
        - profile_id: 19       # by id: 1g.5gb on an A100
          count: 1
```

| Field | Meaning |
|---|---|
| `profile` | Profile name as the cluster spells it, e.g. `1g.10gb`, `1g.5gb+me` |
| `profile_id` | Raw id instead of a name. Exactly one of the two |
| `count` | How many identical instances. Defaults to 1 |
| `compute_instances` | Compute slices inside each GPU instance, same `profile`/`profile_id`/`count` shape. Defaults to one spanning the whole GPU instance, which is what `nvidia-mig-parted` creates |

`profile_id` is the id in the `ID` column of `nvidia-smi mig -lgip`, so it can
be copied straight off that listing. Which numbers appear there is whatever the
board's own rows declare as their `profile_id`: on `a100` and `h100` those are
the ids NVIDIA publishes, where `19` is a 1-slice partition and NVML's profile
enum for the same partition is a different number entirely. NVIDIA publishes no
`mig -lgip` listing for Blackwell, so `b200`, `gb200` and `gb300` declare each
row's own NVML enum instead — `19` names nothing there and `0` is the 1-slice
profile rather than the whole board. Name partitions by `profile` on those
boards, and take any id from the mock's own listing rather than from a real
board's.

## Declaring a layout explicitly

`instances` states exactly which GPU instances exist, with the ids they were
created under. A count cannot express a layout with a hole in it — delete
instance 1 of three and the survivors are 0 and 2, which `count: 2` would
reload as 0 and 1 — so this is the form a runtime mutation records, and the
form to use when an instance needs a fixed id:

```yaml
device_defaults:
  mig:
    mode_current: "enabled"
    max_gpu_instances: 7
    instances:
      - id: 0
        profile: "1g.10gb"
        compute_instances:
          - id: 0
            profile: "1c"
      - id: 2                  # 1 is deliberately absent
        profile: "1g.10gb"
        placement_start: 2     # optional; omitted takes the first free slot
```

Fixed ids are what let a partition be deleted by id from a process that did not
create it, and what keeps `nvidia-smi -L` reporting the same MIG UUIDs across
processes. A changed layout is applied by difference: only the missing instances
are created and only the superfluous ones destroyed, so a consumer already
holding handles follows a repartition instead of losing them.

An empty `instances: []` is a MIG-enabled board with every instance deleted,
which is distinct from omitting the key; the same holds for a GPU instance's
`compute_instances`, since deleting the last compute instance is a state
hardware has.

