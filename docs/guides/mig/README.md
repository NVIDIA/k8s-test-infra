# MIG Partitioning

Multi-Instance GPU (MIG) splits one physical GPU into several isolated
partitions. Mokka simulates that split at the driver level, so `nvidia-smi mig`
behaves as it does against a real driver and the NVIDIA device plugin runs
unmodified. No A100, H100 or B200 is involved.

There are two different things people mean by "partition a board", and they are
not interchangeable.

| | At install time | At runtime |
|---|---|---|
| Driven by | chart values | `nvidia-smi mig` |
| Changes | what the node **advertises** and what NVML reports | what NVML reports |
| Pods can be scheduled onto slices | yes | no |
| Good for | testing schedulers, the device plugin, anything that allocates | testing consumers that read NVML, such as DCGM and GFD |

!!! warning "The layout a node boots with is the layout that is allocatable"

    The device plugin allocates against the driver capability surface, which
    Mokka stages once when the `nvml-mock` pod starts. Repartitioning at
    runtime moves the NVML view underneath it but not that surface, so a
    partition created with `nvidia-smi` can be seen and cannot be allocated.
    Decide the layout at install time if you intend to schedule anything.

## Partition at install time

Every MIG install names its own layout. `gpu.mig.enabled=true` on its own is
refused, because how a board is carved is a deployment choice rather than a
property of the silicon — and a capable board left MIG-enabled with no layout
publishes no GPU resource at all.

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  --set gpu.profile=h100 \
  --set gpu.count=2 \
  --set gpu.mig.enabled=true \
  --set 'gpu.mig.gpuInstances[0].profile=1g.10gb' \
  --set 'gpu.mig.gpuInstances[0].count=7' \
  --wait
```

That is two boards of seven slices each, so the node carries fourteen
partitions.

Filling a board with its smallest slice gives every partition the same profile
name, which is what the device plugin's `migStrategy=single` requires:

| Profile | Smallest slice | Instances per board |
|---|---|---|
| `a100` | `1g.5gb` | 7 |
| `h100` | `1g.10gb` | 7 |
| `b200` | `1g.23gb` | 7 |
| `gb200` | `1g.23gb` | 7 |
| `gb300` | `1g.35gb` | 7 |

`l40s` and `t4` cannot partition. The chart refuses MIG on them, and NVML
answers `NVML_ERROR_NOT_SUPPORTED` exactly as it does on that hardware.

The full table each board offers, not just its smallest slice, is declared in
YAML — each row carrying the partition's slices, published profile id,
placements and compute instances. It is a document of its own, which the chart
mounts from a ConfigMap of its own; the schema and how the engine finds the
table are in the
[`mig:` reference](../../mig.md#declaring-the-profile-table).
A board whose table does not arrive comes up
[not MIG-capable](../../mig.md#a-board-with-no-resolvable-table-is-not-mig-capable),
which is the first thing to check if MIG unexpectedly declines on a capable
board.

Mixed layouts and fixed instance ids are described in
[MIG](../../mig.md).

## Look at the partitions

```bash
POD=$(kubectl -n mokka get pod -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')

kubectl -n mokka exec "$POD" -- nvidia-smi -L
kubectl -n mokka exec "$POD" -- nvidia-smi mig -lgi
```

`nvidia-smi -L` names each partition with its profile and its own `MIG-…`
UUID — the identity a workload is eventually given, rather than the parent
board's. `mig -lgi` lists the GPU instances behind those partitions, and
`mig -lgip` reports how many of each profile the board still has free.

## Change the layout at runtime

`nvidia-smi` drives this, not `nvml-mock-ctl`. The scope is one node, so run it
in the `nvml-mock` pod on the node you want to change and repeat per pod to
change several.

A board installed without a layout starts with MIG off, so enabling comes
first. Nothing below needs a partitioned install.

```bash
SMI="kubectl -n mokka exec $POD -- nvidia-smi"

$SMI -i 0 -mig 1                   # make GPU 0 MIG-capable; no partitions yet
$SMI mig -i 0 -cgi 1g.10gb -C      # carve one slice, with its compute instance
$SMI mig -lgi                      # read it back from a separate process
$SMI mig -i 0 -dci -gi 0 -ci 0     # compute instance first
$SMI mig -i 0 -dgi -gi 0
$SMI -i 0 -mig 0                   # back to a whole GPU
```

Four things about that sequence are worth knowing before you adapt it.

`-C` is not optional in practice. Without it the GPU instance exists in
`mig -lgi` but has no compute instance, so it produces no MIG device in
`nvidia-smi -L` and nothing can use it.

Deletion runs inward. NVML refuses to destroy a GPU instance that still holds a
compute instance, reporting `NVML_ERROR_IN_USE`, so the compute instance goes
first. This is the order `nvidia-mig-parted` and the MIG user guide already
use.

`-i` restricts a command rather than selecting for it. Omit it and the command
applies to every GPU on the node, which on an eight-board node means
`mig -dgi -gi 0` destroys eight partitions rather than one.

Every mutation is recorded, so it outlives the `nvidia-smi` process that made
it and reaches other consumers on the node within one TTL. That recording is
what makes a partition visible to a *separate* `nvidia-smi`, to DCGM and to GFD
at all, since every process gets its own engine. A mutation that cannot be
recorded fails with `NVML_ERROR_NO_PERMISSION` rather than succeeding in one
process only — which is what the driver reports when the same call is made
without the permissions it needs.

`nvml-mock-ctl status` shows the recorded layout and `nvml-mock-ctl reset`
clears it. There is deliberately no `nvml-mock-ctl mig`: it would be a second,
non-standard spelling of an interface `nvidia-smi` already covers, and a second
writer of the same state.

## Schedule a pod onto a slice

A partitioned node advertises one resource per slice instead of one per board,
so the install above turns two GPUs into fourteen allocatable ones:

```bash
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPUS:.status.allocatable.nvidia\.com/gpu'
```

That shape change is what matters above the plugin: the same
`nvidia.com/gpu: 1` request now buys a seventh of an H100, and the pod receives
a partition's `MIG-…` UUID in `NVIDIA_VISIBLE_DEVICES` along with two
`/dev/nvidia-caps` nodes — one for its GPU instance, one for its compute
instance — which is what a MIG-aware runtime reads to open the partition.

Getting there needs the NVIDIA device plugin deployed as in
[NVIDIA Device Plugin](../device-plugin.md), with two changes:

- `--mig-strategy=single`, which publishes every partition as
  `nvidia.com/gpu`. It rejects a node whose partitions do not all carry one
  profile, which is why the layout fills each board with a single slice size.
- The plugin resolves MIG capabilities through
  `/proc/driver/nvidia-caps/mig-minors`, at a hardcoded absolute path, while it
  builds its device map — so a plugin that cannot read that file advertises
  nothing rather than falling back to whole GPUs. A volume mount cannot deliver
  it, because runc refuses any mount targeted inside `/proc`. What works is
  letting a privileged container bind-mount Mokka's staged copy over its own
  `/proc/driver`.

A working DaemonSet doing both, including the wait for the node agent to finish
staging, is kept under test at `tests/e2e/go/assets/device-plugin-mock-mig.yaml`.

## How slices are named

A slice is named for the share of *its own* board it holds, and every shipped
profile declares the capacity of the product NVIDIA publishes its names for —
180 GiB for `b200`, 186 for `gb200`, 278 for `gb300` — so the names the mock
reports are the names those boards report.

Take the names a board accepts from `nvidia-smi mig -lgip` rather than from the
MIG user guide, which has no GB200 or GB300 table. On Blackwell, name
partitions by profile rather than profile id: NVIDIA publishes no profile ids
for those boards, so the mock reports NVML's enum instead and the ids will not
match a real board's listing.

## Limits

A runtime repartition changes the NVML view only. `/dev/nvidia-caps` and the
`mig-minors` table are staged once when the `nvml-mock` pod starts, so the
device plugin cannot allocate what a repartition produces — including a layout
identical to the installed one, since rebuilding it draws fresh GPU-instance
ids. Clearing the override restores the layout NVML reports but not the ids it
reports them under, so only restarting the `nvml-mock` pod returns the node to
an allocatable state.

There is no CUDA, so a MIG slice schedules and admits a pod but runs no kernels
on it.

## Troubleshooting

**`helm install` fails saying `gpu.mig.gpuInstances` declares no partitions.**
`gpu.mig.enabled=true` was set without a layout. Name one.

**`helm install` fails saying the profile is not a MIG-capable board.** The
profile declares no `mig.max_gpu_instances`; `l40s` and `t4` cannot partition.

**`nvidia-smi mig` answers `No MIG-enabled devices found`.** MIG is off on
every GPU, which is how a board installs without a layout. Enable it first with
`nvidia-smi -i 0 -mig 1`. Note that MIG state is per node: if the enable
succeeded and the next command disagrees, the two calls reached different pods.

**`mig -dgi` fails saying the GPU instance is in use.** It still holds a
compute instance. Destroy that first with `mig -dci -gi <id> -ci <id>`, as on
real hardware.

**Allocatable stays at zero after enabling MIG.** The plugin is running but
found no capability table, or found partitions it will not serve. Check that
the layout is uniform — `migStrategy=single` rejects a mix of profiles — then
read the plugin's logs.

**A partition `nvidia-smi` reports cannot be allocated.** It was created at
runtime; see [Limits](#limits). Restart the `nvml-mock` pod on that node.

## Related

| To read about | See |
|---|---|
| The `mig:` config schema, including per-device layouts and fixed instance ids | [MIG](../../mig.md) |
| Every chart value | [Installation](../../helm-chart.md) |
| Whole-GPU allocation, and the plugin without MIG | [NVIDIA Device Plugin](../device-plugin.md) |
| Changing temperature, power or health at runtime | [Runtime control](../../nvml-mock-ctl.md) |
