# mokka-capture

Captures a real GPU node, scrubs the capture so it can be committed, writes a
mock profile from it, and compares it with what Mokka serves. It runs on your machine and reaches the
nodes through `kubectl`. The procedure is in
[Capturing a real node](../contributing/capturing-a-real-node.md).

```bash
go build -o bin/mokka-capture ./cmd/mokka-capture
```

| Command | Does |
| --- | --- |
| [`collect`](#collect) | Captures nodes and the cluster through `kubectl` |
| [`scrub`](#scrub) | Replaces identifiers in a capture, or checks that none are left |
| [`profile`](#profile) | Writes a mock NVML profile from a scrubbed capture |
| [`diff smi`](#diff-smi) | Compares two `nvidia-smi -q -x` documents |
| [`diff nvml`](#diff-nvml) | Compares two NVML recordings |
| [`dump`](#dump) | Records the NVML library on the loader path; `collect` runs it in the driver container |

`mokka-capture <command> --help` lists a command's flags and arguments. A flag
takes one dash or two (`-out` or `--out`) and goes after its command's name,
before or after the arguments; `--` ends the flags.

## collect

```bash
mokka-capture collect [flags] driver|host|network|all <node>...
mokka-capture collect [flags] cluster
mokka-capture collect [flags] build-dump
```

| Command | Runs | Takes |
| --- | --- | --- |
| `driver <node>...` | in the GPU Operator driver container, or with `-mock` a stand-in pod | `nvidia-smi`, the NVML dump, `/proc/driver/nvidia` |
| `host <node>...` | in a temporary privileged pod | PCI, NUMA, device files, NICs, CDI specs |
| `network <node>...` | in the same pod | only the NIC, interface and RDMA surfaces, plus the cluster's network objects |
| `cluster` | on your machine | node labels, ResourceSlices, `ClusterPolicy`, DCGM metrics |
| `all <node>...` | all of the above | `driver` and `host` for each node, then `cluster` |
| `build-dump` | Docker, on your machine | a Linux `mokka-capture` for the NVML dump (below) |

Every command runs in the cluster and every output is written on your machine,
one file per command under `-out`/`<node>/` or `-out`/`cluster/`. A command that
fails does not stop the run: its error goes to `errors.log` beside the output.
The cluster connection is checked before each node, since a dropped connection
leaves empty files that look like answers; the run ends by listing any empty
files.

The host pod is named `capture-<node>-<pid>` and is deleted when the command
finishes, is interrupted, or fails. It is privileged, mounts the node's root
filesystem, and installs `pciutils`, `rdma-core` and other tools with `apt`.

The NVML dump has to run inside the driver container, which holds the driver's
`libnvidia-ml.so.1`. `build-dump` builds `mokka-capture` for Linux in a `golang`
container and writes `-out/bin/mokka-capture-linux-<arch>`, for the
architecture of the machine running Docker (go-nvml loads the library at run
time, so the binary is linked dynamically and built natively). `collect` copies
the file given with `-dump-binary` into the driver container, runs
`mokka-capture dump` there, and removes it. Without `-dump-binary` the dump is
skipped.

### A Mokka node

`-mock` captures a Mokka node the same way, so the result can be compared with
a real capture using [`diff smi`](#diff-smi) and [`diff nvml`](#diff-nvml):

```bash
mokka-capture collect -mock -dump-binary <linux binary> -out mock all <node>
mokka-capture diff smi real/<node>/smi-q.xml mock/<node>/smi-q.xml
```

Two things differ from a real node, and `-mock` handles both:

- **No driver container.** Mokka's GPU Operator mode runs no driver, so
  `driver` finds no driver pod. With `-mock`, `collect` starts a stand-in pod,
  `capture-driver-<node>-<pid>`, and runs the driver commands there. It has the
  host pod's spec (privileged, host PID namespace), installs nothing, and is
  deleted like the host pod.
- **The mock is opt-in.** The mock NVML library, `nvidia-smi`,
  `/proc/driver/nvidia`, the GPUs in PCI sysfs and the InfiniBand devices reach
  a container only when Mokka's NRI plugin injects them, which it does for
  containers that ask with its annotations. With `-mock`, the stand-in and the
  host pod carry `nvml-mock.nvidia.com/devices`, `nvml-mock.nvidia.com/infiniband`
  and `nvml-mock.nvidia.com/imex-channels` set to `"true"`. Without them, the
  host pod sees the Kind node underneath, with no GPUs.

The MIG table is staged elsewhere too. Mokka writes `mig-minors` under its
driver root (`/opt/nvml-mock/driver/proc/driver/nvidia-caps/` in the stand-in),
where nvidia-container-toolkit reads it, and leaves `/proc/driver/nvidia-caps`
alone, so with `-mock` the `proc-*-minors.txt` files are read from there.

Four files come out empty for reasons outside the mock. The mock's library and
`nvidia-smi` live under `/opt/nvml-mock`, so `lib-listing.txt` and
`bin-listing.txt` find nothing. The stand-in's image has no `nm` or `modinfo`, so
`nvml-symbols.txt` and `modinfo-nvidia.txt` are empty too. Any other empty file
is a gap in the mock.

| Flag | Default | Description |
| --- | --- | --- |
| `-out` | `./capture` | Output directory |
| `-mock` | off | The nodes are Mokka nodes: run the driver commands in a stand-in pod and opt both pods into the mock |
| `-dump-binary` | none | Linux `mokka-capture` to run `dump` with in the driver container |
| `-operator-namespace` | `gpu-operator` | GPU Operator namespace |
| `-driver-container` | `nvidia-driver-ctr` | Driver container name |
| `-toolkit-container` | `nvidia-container-toolkit-ctr` | Toolkit container name |
| `-toolkit-ctk` | `/usr/local/nvidia/toolkit/nvidia-ctk` | `nvidia-ctk` in the toolkit container |
| `-host-pod-namespace` | `default` | Namespace for the host pod |
| `-host-pod-image` | `ubuntu:24.04` pinned by digest | Image for the host pod; pin it by digest |

`kubectl` uses its own configuration (`KUBECONFIG`, the current context).

## scrub

```bash
mokka-capture scrub -out scrubbed \
  -replace worker-7=node-a -replace worker-9=node-b \
  -seed raw/worker-7/smi-L.txt -seed raw/worker-9/smi-L.txt \
  raw/worker-7 raw/worker-9 raw/cluster
mokka-capture scrub -check qx-<node>.xml
```

Copies a directory of captures, replacing everything that names a particular
machine with a placeholder. The output directory must be empty and outside
every source directory.

### What it replaces

| Value | Placeholder |
| --- | --- |
| GPU UUID, MIG UUID | `GPU-0000000N-0000-0000-0000-00000000000N`, `MIG-…` |
| Other UUIDs (fabric cluster, Kubernetes object UIDs) | `0000000N-0000-0000-0000-00000000000N` |
| 32-digit machine IDs, and the unit fields of PCI vital product data | 32 hex digits ending in `N` |
| Serial numbers: `<serial>`, `<chassis_serial_number>`, `Serial Number` lines, VPD `[SN]` | `1650000000NNN` and `1820000000NNN` |
| PDIs, fabric GUIDs and other 16-digit hex values (except the masks below) | `0x000000000000000N` |
| Colon-separated GUIDs (`cc30:8903:00cb:be0e`) | `0000:0000:0000:000N`, the same `N` as the `0x` form of that GUID |
| IPv6 addresses in any notation (`fd00::12`, `::ffff:10.0.0.7`), such as node addresses and RDMA GIDs | `fe80:0000:…:000N` |
| MAC addresses, and `enx<mac>` interface names | `02:00:00:00:00:0N` |
| IPv4 addresses | `192.0.2.N` |
| Anything given with `-replace old=new` | `new` |

Rules that follow from keeping the data useful:

- **One placeholder per distinct value, across every file and directory in the
  run.** A GPU keeps the same placeholder in `nvidia-smi`, the NVML dump and the
  CDI specs. An NVML dump numbers its own GPUs from 0 when it is recorded; the
  scrubber pairs its devices with the GPUs in the same directory's `smi-q.xml` by
  PCI bus ID and renames the dump's UUID, serial, chassis serial, PDI and fabric
  GUID to match.
- **`-seed` numbers GPUs in index order.** Give it the `nvidia-smi -L` output of
  each node, in node order.
- **A serial number is scrubbed everywhere once it is found in a context that
  names it.** The same value in an NVML dump or a kernel log line carries nothing
  around it that says "serial".
- **Absent values stay.** Empty, `N/A` and all-zero values are not rewritten.
- **Things that look like identifiers but are not stay.** Version numbers
  (`99.00.16.55`), netmasks, the unspecified, loopback and broadcast addresses,
  16-digit hex values below 2^32 or all ones in their low bits, such as clock
  event and timestamp masks (no ID is either), kernel virtual addresses
  (`0xffff…`) and the link-local prefix `fe80::` of a GID; IPv6 loopback,
  multicast and the unspecified address; and colon-separated text that is not an
  address on its own: times, `std::vector`, or `a::b`, which has no digit.
- **File and directory names are scrubbed too**, so `-replace worker-7=node-a`
  renames a `worker-7/` directory.
- **Running it over its own output changes nothing.**

Binary files, `errors.log` and shared objects (`*.so`, `*.so.*`) are skipped and
named on standard error; override the list with `-exclude`.

### Reviewing the result

The scrub is a best effort, not a guarantee. `-report <file>` writes a table of
every substitution, with the original and its placeholder, for an audit:

```bash
cut -f2 report.tsv | sort -u | while read -r v; do
  [ ${#v} -ge 8 ] && grep -rqF -- "$v" scrubbed && echo "LEAK: $v"
done
```

The report contains the originals, so never commit it. Names a pattern cannot
recognise (tenant, site and user names, internal domains) are for you to find
with `grep` and to add as `-replace` values.

`-check FILE...` reads `nvidia-smi -q -x` files and exits `1` if a `serial`,
`chassis_serial_number`, `uuid`, `clusterUuid`, `pdi` or `gpu_fabric_guid`
element still holds a value that is neither absent nor one of the placeholders
the [hardware captures](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/go/assertions/nvidiasmi/testdata/hardware/README.md)
use. It applies the same patterns as `TestHardwareCapturesAreScrubbed`.

## profile

```bash
mokka-capture profile [-o file] [-base profile] [-explain] <capture node directory>
```

Writes a mock NVML profile from one node of a committed capture, such as
`tests/hardware-captures/vr-nvl72/node-a`, so a profile is regenerated from
evidence instead of typed. The format is that of
`pkg/gpu/mocknvml/configs/mock-nvml-config-<name>.yaml`; the fields are in
[Configuration](../configuration.md).

Every value the profile gets carries a comment naming where it came from, so
the profile can be checked against the capture line by line:

```yaml
inforom:
  ecc_object: "7.16"              # GetInforomVersion(1)
  # pwr_object: left out, GetInforomVersion(2) = ERROR_NOT_SUPPORTED
memory:
  total_bytes: 300442189824       # GetMemoryInfo_v2.Total
pci:
  device_id: 0x304110DE           # GetPciInfo.PciDeviceId = 809570526
thermal:
  temperature_gpu_c: 34           # GetTemperature(0) on GPU 0, idle reading; GPUs read 34 33 33 34
  max_operating_c: 90             # tlimit.reference_c - tlimit.slowdown_c = 88 - (-2); GetTemperatureThreshold(3) reads 90 on every GPU
  tlimit:
    reference_c: 88               # GetTemperatureThreshold(1) on GPU 0
    slowdown_c: -2                # GetFieldValues(id=194) on GPU 0
```

A name such as `GetClockInfo(1)` is the dump's entry for that getter called with
argument `1`; `.Field` is a field of its answer and `[n]` an element. When the
comment shows `= value`, the capture's raw answer differs from what the profile
key takes, for example an enum (`GetBrand = 14` is `"nvidia"`). A key the driver
does not answer is left out, with a comment giving the return code.

### Where each value comes from

The mapping is one file, `internal/capture/profile/rules.go`. Each line names a
profile key and its source; the verb says how GPUs that disagree are handled:

| Kind | Examples | Value written |
| --- | --- | --- |
| Property of the board | limits, maximum clocks, PCI IDs, ECC mode, MIG table | From the capture. Once in `device_defaults` when every GPU agrees, otherwise under each `devices[]` entry. The engine replaces a `devices[]` block such as `memory` or `power` whole, so that block repeats the values the GPUs share. GPUs that disagree on a key the engine reads only from `device_defaults`, such as `pcie`, stop the generator |
| Derived | thermal thresholds, the largest MIG instance count | Computed from several capture values; the comment shows the working on each GPU |
| Reading | temperature, power draw, current clocks and PCIe link, utilisation, and counts of faults: ECC and SRAM errors, PCIe replays, retired pages, remapped rows | The capture's idle reading, never `-base`'s: GPU 0's for the temperature, the lower median for the rest when GPUs differ, so one faulty GPU neither stops the generator nor gives the mock its errors |
| Rest of a total | free memory, free BAR1 | The total less the reserved and used values written, so they add up; the comment gives the capture's reading beside it. Worked out per GPU: on a GPU that reserves more than its siblings, a used reading it has no room for is lowered to what it has |
| Identifier | UUID, serial, chassis serial, fabric cluster UUID | From `-base`; otherwise the placeholder the scrubbed capture holds |
| Host CPU | `cpu` | Written only when the GPUs reach the CPU over NVLink-C2C, so the CPU is part of the board: the family and cores per socket from `host-lscpu.txt`, `memory_gb` from the first NUMA node with CPUs in `host-numactl.txt`, as other values are GPU 0's |
| Not in a capture | `features`, `dynamic_metrics`, NVSwitches, NIC GUID prefix | From `-base`; otherwise left out with a comment |

The NVML dump supplies most values. The other files fill what it cannot record:

| File | Gives |
| --- | --- |
| `smi-q.xml` | the supported clock table (the getters take a buffer size), NVLink-C2C, target temperature |
| `smi-nvlink-s.txt` | per-link NVLink bandwidth |
| `smi-mig-lgip.txt`, `smi-mig-lgipp.txt` | MIG profile names and placements |
| `host-dmi.txt` | the system product name |
| `host-lscpu.txt`, `host-numactl.txt` | the CPU block |
| `host-ib-sysfs.txt`, `host-ibstat.txt` | the NIC block |
| `rdma-sysfs.txt`, `pci-nic-functions.txt` | each RDMA device's PCI function, and which functions are SR-IOV virtual functions |
| `host-pci-hierarchy.txt`, `host-pci-nvidia.txt` | PCIe root complexes and each GPU's NUMA node |

The thermal values are GPU 0's readings, so they add up in the mock. The
T.Limit view is read, and the absolute thresholds follow from it:

| Key | Value |
| --- | --- |
| `tlimit.reference_c` | `GetTemperatureThreshold(1)`, the driver's SLOWDOWN threshold |
| `tlimit.shutdown_c`, `slowdown_c`, `max_operating_c` | T.Limit fields 193, 194 and 196 |
| `shutdown_threshold_c` | `tlimit.reference_c - tlimit.shutdown_c` |
| `slowdown_threshold_c` | `tlimit.reference_c` |
| `max_operating_c` | `tlimit.reference_c - tlimit.slowdown_c` |
| `temperature_gpu_c` | `GetTemperature(0)` |

The mock answers `GetMarginTemperature` as `tlimit.reference_c` minus the
temperature, and a note under `tlimit` compares that with GPU 0's reading. On
the captured tray the two differ by a degree: the driver's temperature plus
margin does not come to its own reference. The pairing is the driver's: it
answers SLOWDOWN with the T.Limit reference, where field 196 reads 0, and
GPU_MAX with the hardware-slowdown point. Each threshold's comment also gives
the driver's `GetTemperatureThreshold` answer for comparison. A board without
the T.Limit fields keeps the driver's thresholds.

A few keys follow the profile's meaning rather than NVML's encoding:
`nvlink.version` is the NVLink generation (NVML reports its enum, `8` for
NVLink 6.0), `power.supported_power_mizer_modes` names the modes set in
`GetPowerMizerMode_v1`'s mask (`7` is `adaptive`, `prefer_maximum_performance`
and `auto`), and a profile models one NIC type, so the NIC block describes the
type most devices are and its comment lists the rest. Only physical functions
count: an SR-IOV virtual function, which sysfs links to its parent with
`physfn`, is a slice of a NIC already counted. Two types tied for the most
devices stop the generator rather than one being picked by name, and so does a
tie in any other NIC value. With MIG disabled, the
capture lists no compute instance profiles; each MIG table row assumes one
compute instance spanning its GPU instance.

### Output

| Flag | Default | Description |
| --- | --- | --- |
| `-o` | stdout | Write the profile to this file and the MIG table beside it as `<name>.mig.yaml`, where the mock looks for it. When the capture has no MIG table, a `<name>.mig.yaml` an earlier run left is removed. Without `-o` only the profile is written |
| `-base` | none | Profile of the same GPU to take identifiers and mock-only blocks from. A base whose `device_defaults.pci.device_id` is not the capture's, or that has none, is refused |
| `-explain` | off | Print a table of every key with its value, rule, source and each GPU's raw answer, instead of the profile |

The output depends only on the capture and `-base`, so regenerating gives the
same bytes. `internal/capture/profile/testdata/` holds the profile generated
from each committed capture; the tests fail when a rule change alters it, check
that it loads in the mock engine, and check that every key is one the engine
reads. `go test ./internal/capture/profile -update` rewrites it.

## diff smi

```bash
mokka-capture diff smi [-ignore regexp] [-max n] real.xml mock.xml
```

Compares two `nvidia-smi -q -x` documents element by element: a real node's
capture against the output of the mock for the same profile. Each document is
flattened to path=value pairs for its leaf elements. Every element is indexed
among its same-named siblings in document order, so the first GPU is
`nvidia_smi_log[0]/gpu[0]`. The report says:

- how many of the real document's paths exist in the mock;
- how many of those carry the same value;
- the paths only one side has, and the values that differ;
- every path whose value is not compared, with both values, so a fidelity
  score says exactly what it leaves out.

A value that is not a property of the board is not compared, since a mock of
the captured node cannot be expected to reproduce it:

| Left out | Elements |
| --- | --- |
| Readings that move with load | `gpu_temp`, `gpu_temp_tlimit`, `memory_temp`, `utilization`, power draw (`power_draw`, `average_power_draw`, `instant_power_draw`), `power_state`, `performance_state`, the clocks in use (`clocks`, `dla_cur_engine_clock`), `graphics_volt`, `fan_speed`, memory `used` and `free`, clock event reasons and their counters, PCIe `tx_util` and `rx_util`, processes |
| Readings that move with time | `timestamp`, `time_run`, `inforom_bbx_flush` |
| Identifiers | `uuid`, `serial`, `pdi`, `gpu_fabric_guid`, `clusterUuid`, the clique IDs (`cliqueId`, `clique_id`), `minor_number`, the PCI bus location |

Everything else is compared, including the temperature thresholds and T.Limit
offsets, `max_clocks` and the supported clocks. An element name is matched
whole, so `clocks` does not hide `max_clocks`.

Only readings are left out, not the lack of one. When either side answers
`N/A`, `Not Supported`, `Insufficient Permissions`, `Requested functionality has
been deprecated` or `Unknown Error`, the two answers are compared like any
value: a mock that reads a temperature where the real GPU answers `N/A` counts
as a difference, and `N/A` on both sides counts as a match.

`-ignore` replaces the set with a regexp over paths; `-max` (default `40`)
limits the entries listed per kind. Pass `-max 100000` to see every value left
out of the score.

## diff nvml

```bash
mokka-capture diff nvml [-ignore regexp] [-max n] real.json mock.json
```

Example output:

```text
calls compared:        412
return code matches:   398 of 412 (96.6%)
value matches:         301 of 340 (88.5%) (of 412 calls answered SUCCESS on both sides, excluding 72 whose value is a reading or an identifier)
only on real device:   3
only on mock device:   0

...

not compared, a reading or an identifier (88):
  device 0  GetBBXTimeData_v1                        real={"TimeRun":7058642} mock={"TimeRun":45182}
  device 0  GetMemoryInfo_v2.Free                    real=299103551488 mock=299103158272
  ...
```

The report lists the calls whose return code differs, the values that differ,
the calls only one side has, and last, every value left out of the value rate:
whole answers, and the parts of compared answers that are readings (such as
`GetMemoryInfo_v2.Free` above). A fidelity score taken from it therefore says
exactly what it does not cover.

Return codes are compared for every call, so a real and a mock GPU must agree on
whether each getter is supported. A value is compared only where both sides
answered `SUCCESS`; a call both sides answer with the same error counts as a
return-code match and nothing more. A value that is not a property of the board
is excluded from the value rate and counted separately:

| Left out | Calls |
| --- | --- |
| Readings that move with load | temperatures, power draw and energy, utilisation (including `GetDynamicPstatesInfo`), the clocks in use (`GetClockInfo`, `GetClock` with clock ID 0, `GetCurrentClockFreqs`), clock event reasons, fan speed, processes, PCIe and NVLink throughput and error counters, ECC, retired-page and remapped-row counts |
| Readings that move with time | `GetBBXTimeData` (how long the GPU has run), `GetLastBBXFlushTime`, `GetSamples`, `GetViolationStatus` |
| What the host decides | `GetCpuAffinity`, `GetCpuAffinityWithinScope`: a Kind node has its own CPUs |
| Identifiers | UUID, serial, PDI, platform and fabric information |

Names are matched whole: `GetClockInfo` is excluded and `GetMaxClockInfo` is
compared. Some answers mix readings with values that describe the board; only
the readings are left out of those:

- `GetMemoryInfo` and `GetMemoryInfo_v2`: `Free` and `Used`. `Total` and the
  driver's `Reserved` are compared. The same holds for `GetBAR1MemoryInfo` and
  `GetConfComputeProtectedMemoryUsage`.
- `GetThermalSettings`: each sensor's `CurrentTemp`.
- Every answer's `Version`, the struct version the call carries back, which
  belongs to the API and not to the GPU.
- `GetFieldValues`: the fields that are counters, energy, power draw or
  temperatures. The fields that describe the board (ECC mode, NVLink count,
  speed and version, power limits, T.Limit offsets, capabilities) are compared.
  Field IDs follow go-nvml's header, which drivers from 580.95 on use; older
  drivers number the fields from 251 to 273 differently.

`-ignore <regexp>` replaces all of these. It is matched against whole call names
and against the parts of an answer, named `Call.Field` for a struct field and
`Call[i]` for an array element, as in `GetMemoryInfo_v2.Free` or
`GetThermalSettings(0).Sensor[0].CurrentTemp`. `-max <n>` sets the number of
entries listed per kind (`-max 100000` lists every value left out); a listed
difference shows only the parts that were compared. Devices pair by index, which holds when both sides expose the same
GPUs in the same order. Gzipped recordings (`.json.gz`) are read directly, which
is how a committed capture is stored.

## dump

```bash
mokka-capture dump > nvml-dump.json
```

Initialises whichever `libnvidia-ml.so.1` the dynamic loader finds, then calls
the getters (`Get*` and `Is*`) on every device and, at process level, the
driver, NVML and CUDA version getters and the device count. It finds the device
getters by reflection over go-nvml's device handle, so a getter added in a newer
go-nvml is picked up without editing the tool. Nothing that changes state is
called. "Every getter" is bounded by the list below: what the dump could not
call is named in `skipped`.

- A getter with no arguments is recorded as `Name`.
- A getter whose arguments are all integers or enums is called over a fixed range
  of values and recorded as `Name(a,b)`: clock types, NVLink indexes 0 to 39, ECC
  counter kinds, temperature sensors and so on. A value past the end of an enum
  answers `ERROR_INVALID_ARGUMENT`, which is recorded too, so the dump also shows
  where each range ends.
- `GetFieldValues` is probed one field ID at a time (0 to 399), at scope 0 only,
  recorded as `GetFieldValues(id=N)` with the per-field return code. Per-link
  field values (scope IDs for each NVLink) are not probed. The IDs a driver
  defines differ between releases, so this is where a driver-branch change shows.
- A getter with any other kind of argument (a struct, a handle, a string) is
  listed under `skipped` and not called, so the coverage of a dump is visible in
  the dump.

Each call is recorded with its NVML return code, as a fixed name such as
`ERROR_NOT_SUPPORTED`, and its value. A getter the library answers with
`ERROR_NOT_SUPPORTED` is recorded as such: on real hardware that is an answer
the mock has to give too.

A real node's identifiers are redacted as the dump is written: GPU UUIDs, serial
numbers, chassis serials, PDIs, IB and rack GUIDs and the fabric cluster UUID are
replaced by placeholders, one per distinct value. NVML returns most of these as C
byte arrays and integers, which no text scrubber recognises, so the redaction
goes by the name of the field or getter and not by the shape of the value. The
dump is written one device at a time, so a driver crash on a later device keeps
the earlier ones (the file is then truncated JSON; the devices written are
intact). A new field that names an identifier is covered; one that does not is
not, so a reviewer still reads a dump before it is committed.

To dump the mock, run `collect -mock` with the same `-dump-binary` (see
[A Mokka node](#a-mokka-node)).

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | The command completed, or printed its help. `collect` succeeds even when single commands on a node fail; those are in `errors.log` |
| `1` | The cluster was unreachable, a file could not be read or written, the output directory is not empty, NVML failed to initialise, `scrub -check` found an identifier, or `profile` found GPUs that disagree on a key with no per-device form |
| `2` | Bad usage: an unknown command or flag, a missing or extra argument, or an invalid flag value. The reason and the command's help go to standard error |
| `130` | `collect` was interrupted; the pods it started are deleted first |
