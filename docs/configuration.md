# Configuration Reference

Complete guide to Mock NVML configuration options.

## Configuration Methods

Mock NVML supports two configuration methods:

| Method | Use Case | Flexibility |
|--------|----------|-------------|
| **YAML File** | Full control, per-device settings | High |
| **Environment Variables** | Simple scenarios, CI/CD | Low |

YAML configuration takes precedence when `MOCK_NVML_CONFIG` is set.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MOCK_NVML_CONFIG` | Path to YAML configuration file | (none) |
| `MOCK_NVML_NUM_DEVICES` | Number of GPUs to simulate | 8 |
| `MOCK_NVML_DRIVER_VERSION` | NVIDIA driver version string | 550.163.01 |
| `MOCK_NVML_DEBUG` | Enable debug logging (any value) | (disabled) |

**Example**:

```bash
export MOCK_NVML_NUM_DEVICES=4
export MOCK_NVML_DRIVER_VERSION=550.163.01
LD_LIBRARY_PATH=. nvidia-smi
```

## YAML Configuration Structure

```yaml
version: "1.0"                    # Config format version (required)

system:                           # System-level settings
  driver_version: "550.163.01"    # Required
  nvml_version: "12.550.163.01"
  cuda_version: "12.4"
  cuda_version_major: 12
  cuda_version_minor: 4

device_defaults:                  # Default settings for all devices
  name: "NVIDIA A100-SXM4-40GB"
  # ... (see Device Properties below)

devices:                          # Per-device overrides
  - index: 0
    uuid: "GPU-12345678-1234-1234-1234-123456780000"
    pci:
      bus_id: "0000:07:00.0"
  - index: 1
    uuid: "GPU-12345678-1234-1234-1234-123456780001"
    pci:
      bus_id: "0000:0F:00.0"
  # ...

nvlink:                           # Optional NVLink configuration
  version: 4
  links_per_gpu: 18
```

## Device Properties

### Basic Identification

```yaml
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"      # GPU name shown in nvidia-smi
  brand: "nvidia"                     # nvidia, tesla, quadro, geforce
  serial: "1234567890123"             # Serial number
  board_part_number: "900-21001-0000-000"
  vbios_version: "92.00.45.00.03"
```

### Architecture

```yaml
device_defaults:
  architecture: "ampere"              # kepler, maxwell, pascal, volta, 
                                      # turing, ampere, ada, hopper
  compute_capability:
    major: 8
    minor: 0
  num_gpu_cores: 6912
```

### Memory

```yaml
device_defaults:
  memory:
    total_bytes: 42949672960          # 40 GiB
    reserved_bytes: 0
    free_bytes: 42949672960
    used_bytes: 0
  
  bar1_memory:
    total_bytes: 68719476736          # 64 GiB
    free_bytes: 68719476736
    used_bytes: 0
```

### PCI/PCIe

```yaml
device_defaults:
  pci:
    device_id: 0x20B010DE             # A100 device ID
    subsystem_id: 0x134710DE
    bus_id: "0000:07:00.0"        # Usually per-device
  
  pcie:
    max_link_gen: 4
    current_link_gen: 4
    max_link_width: 16
    current_link_width: 16
    replay_counter: 0
    tx_throughput_kbps: 0
    rx_throughput_kbps: 0
```

### Platform identity (rack location)

Where the node's boards sit in a rack, which `nvidia-smi -q` renders as its
`Platform Info` block. Rack-scale fault correlation reads it to turn a GPU fault
into a physical location. Only `gb200` and `gb300` ship it; on every other
profile the whole block reads `N/A`, which is the correct reading for a board
whose platform reports no location.

```yaml
device_defaults:
  platform:
    chassis_serial_number: "1822725100200"  # serial of the chassis (the rack)
    slot_number: 21                 # absolute physical slot, switch trays included
    tray_index: 11                  # position among compute trays only
    host_id: 1                      # the OS domain within the tray — this node
    peer_type: "switch_connected"    # or "direct_connected"

devices:
  - index: 0
    platform:
      module_id: 1                  # ID of this GPU within the node
  - index: 1
    platform:
      module_id: 2
```

Only `module_id` varies between the GPUs of a node: NVML defines it as the ID of
the GPU within the node, while the other fields describe the node, which occupies
exactly one tray in one slot of one chassis. All six are settable per device
anyway, and a device override merges field by field, so a device declares just
its `module_id`.

`slot_number` counts switch trays and compute trays alike while `tray_index`
counts compute trays only, so on an NVL72 rack — 18 compute trays, 9 switch
trays — slot runs ahead of tray for a tray above the switch group.

`peer_type` says how the GPU reaches its NVLink peers: `switch_connected`
(through an NVSwitch tray) renders as `Switch Connected`, and anything else,
including an absent key, renders as `Direct Connected`.

As everywhere else in a device override, a zero is indistinguishable from an
unset key, so `module_id` numbers from 1.

Declaring the block also requires a driver new enough to have the API:
`nvmlDeviceGetPlatformInfo` arrived in 560, so a profile on an older
`system.driver_version` reports `N/A` regardless. The `GPU Fabric GUID` row of
the same block is not modelled and reads `0x0000000000000000`.

### Power

```yaml
device_defaults:
  power:
    management_supported: true
    management_mode: "enabled"
    default_limit_mw: 400000          # 400W
    enforced_limit_mw: 400000
    min_limit_mw: 100000              # 100W
    max_limit_mw: 400000              # 400W
    current_draw_mw: 72000            # 72W (idle)
    power_state: "P0"
```

### Thermal

```yaml
device_defaults:
  thermal:
    temperature_gpu_c: 33             # Current temperature
    temperature_memory_c: 31
    shutdown_threshold_c: 92
    slowdown_threshold_c: 87
    max_operating_c: 83
    target_temperature_c: 83
```

How these thresholds are reported depends on `device_defaults.architecture`,
as on real hardware. Ada and later expose them as signed T.Limit offsets
(`nvmlDeviceGetMarginTemperature` and the `NVML_FI_DEV_TEMPERATURE_*_TLIMIT`
field IDs), which `nvidia-smi -q` renders as the "T.Limit" rows. Pre-Ada
architectures report `NVML_ERROR_NOT_SUPPORTED` for both, so `nvidia-smi -q`
falls back to the absolute `GPU Shutdown / Slowdown / Max Operating Temp` rows.

### Fan

```yaml
device_defaults:
  fan:
    count: 0                          # 0 = liquid cooled (A100 SXM)
    speed_percent: "N/A"
    target_speed_percent: "N/A"
```

### Clocks

```yaml
device_defaults:
  clocks:
    graphics_current: 210             # MHz
    graphics_max: 1410
    graphics_app: 1410
    graphics_app_default: 1410
    sm_current: 210
    sm_max: 1410
    memory_current: 1215
    memory_max: 1215
    memory_app: 1215
    memory_app_default: 1215
    video_current: 585
    video_max: 1290
  
  clocks_throttle_reasons:
    gpu_idle: true
    applications_clocks_setting: false
    sw_power_cap: false
    hw_slowdown: false
    hw_thermal_slowdown: false
    hw_power_brake_slowdown: false
    sync_boost: false
    sw_thermal_slowdown: false
    display_clocks_setting: false

    # Cumulative time each cause has already cost the GPU, in microseconds.
    # The flags above answer "is it throttled now"; these answer "how long has
    # it been", which is what a slow workload is diagnosed from. Omit the block
    # for a GPU that has never been throttled: every counter then reports 0 us,
    # which is an answer, where the pre-#678 N/A was not.
    counters:
      sw_power_cap_us: 39595
      sync_boost_us: 0
      sw_thermal_slowdown_us: 0
      hw_thermal_slowdown_us: 0
      hw_power_brake_slowdown_us: 0
```

A per-device `clocks_throttle_reasons` block replaces the `device_defaults` one
wholesale rather than merging into it, as `memory`, `power`, `thermal` and
`clocks` already do. A `devices:` entry carrying only `counters` therefore drops
the inherited flags, so restate the ones that still apply — which is why the
`gpu_idle: true` above is repeated in each per-device block of
`tests/mocknvml/util-test-config.yaml`.

The counters appear under `Clocks Event Reasons Counters` in `nvidia-smi -q`,
and are readable through `nvmlDeviceGetFieldValues` (the path DCGM uses) and
`nvmlDeviceGetViolationStatus`. A device accrues further time on top of its
configured baseline for as long as the matching flag is set. Accrual is per
process, like the dynamic-metrics simulator: a long-lived consumer such as
dcgm-exporter watches the counter climb, while each `nvidia-smi` invocation
accrues only over its own lifetime and so reports essentially the baseline.

That per-process scope is why the flags and the counters have to be configured
to agree rather than agreeing by construction. A GPU thrown into a throttle
state at runtime carries no history: `nvidia-smi` renders the reason `Active`
beside a counter of a microsecond or two, which is the age of that `nvidia-smi`
process and not of the throttle. To describe a GPU that has been throttling for
a while, seed the counter alongside the flag — both land in the same override
document, so the second command keeps the first:

```bash
nvml-mock-ctl throttle --gpu 3 thermal
nvml-mock-ctl set --gpu 3 clocks_throttle_reasons.counters.hw_thermal_slowdown_us=39595
```

### Performance

```yaml
device_defaults:
  performance_state: "P0"             # P0-P15
  
  utilization:
    gpu: 0                            # 0-100%
    memory: 0
    encoder: 0
    decoder: 0
    jpeg: 0
    ofa: 0
```

### ECC

```yaml
device_defaults:
  ecc:
    mode_current: "enabled"
    mode_pending: "enabled"
    default_mode: "enabled"
    errors:
      volatile:
        single_bit:
          device_memory: 0
          l1_cache: 0
          l2_cache: 0
          register_file: 0
          texture_memory: 0
          total: 0
        double_bit:
          device_memory: 0
          total: 0
      aggregate:
        single_bit:
          total: 0
        double_bit:
          total: 0
```

#### SRAM ECC

Hardware counts on-die SRAM errors separately from the DRAM counters above and
reports them through their own API, so they are a sibling block rather than
another memory location under `errors`. Omitting the block reports zeros, which
is what a healthy ECC-enabled GPU does; with ECC off the counters are reported
unsupported (`N/A`), as on real hardware.

```yaml
device_defaults:
  ecc:
    sram:
      volatile:                         # reset when the driver reloads
        correctable: 0
        uncorrectable_parity: 0
        uncorrectable_secded: 0
      aggregate:                        # persisted in the InfoROM
        correctable: 0
        uncorrectable_parity: 0
        uncorrectable_secded: 0
      # Which unit reported the aggregate uncorrectable errors; nvidia-smi
      # renders it as "Aggregate Uncorrectable SRAM Sources".
      uncorrectable_sources:
        l2: 0
        sm: 0
        microcontroller: 0
        pcie: 0
        other: 0
      # Whether the accumulated errors passed the driver's threshold — the
      # signal that the GPU needs servicing rather than just a count going up.
      threshold_exceeded: false
```

Inject these at runtime with `nvml-mock-ctl sram-ecc` (see
[nvml-mock-ctl.md](nvml-mock-ctl.md)).

How `nvidia-smi` renders these counters depends on the profile's
`architecture`, mirroring real hardware: Ampere and later split the uncorrectable
count into `SRAM Uncorrectable Parity` and `SRAM Uncorrectable SEC-DED` and print
the source breakdown and threshold flag, while pre-Ampere (`t4`) prints one
combined `SRAM Uncorrectable` row and omits the rest. The configuration is the
same either way — only the presentation differs.

### Remapped rows

`availability_histogram` is how many memory banks still have spare rows to remap
future failures. Row remapping is Ampere and later, so leaving the block out —
as `t4` does — reports the histogram as unsupported.

```yaml
device_defaults:
  remapped_rows:
    correctable: 0
    uncorrectable: 0
    pending: false
    failure_occurred: false
    availability_histogram:
      max: 640                          # banks with full spare capacity
      high: 0
      partial: 0
      low: 0
      none: 0                           # banks with no capacity left
```

### Display

```yaml
device_defaults:
  display:
    mode: "disabled"
    active: "disabled"
```

### Modes

```yaml
device_defaults:
  persistence_mode: "enabled"
  compute_mode: "default"             # default, exclusive_thread, 
                                      # prohibited, exclusive_process
```

### MIG

```yaml
device_defaults:
  mig:
    mode_current: "disabled"
    mode_pending: "disabled"
    max_gpu_instances: 7
```

### InfoROM

```yaml
device_defaults:
  inforom:
    image_version: "G500.0212.00.02"
    oem_object: "2.0"
    ecc_object: "6.16"
    pwr_object: "1.0"
```

### Accounting

```yaml
device_defaults:
  accounting:
    mode: "disabled"
    buffer_size: 4000
```

### Encoder/Decoder

```yaml
device_defaults:
  encoder_stats:
    session_count: 0
    average_fps: 0
    average_latency_us: 0
  
  fbc_stats:
    session_count: 0
    average_fps: 0
    average_latency_us: 0
```

### Processes

`processes` drive the running-process queries and `nvmlDeviceGetProcessUtilization`.
The utilization fields (`sm_util`/`mem_util`/`enc_util`/`dec_util`, all percent) are
reported for every process — compute and graphics/video alike.

They also show up in `nvidia-smi`: the default table's Processes box, `-q`, and
`--query-compute-apps`. Two caveats there — `nvidia-smi` labels every row `M+C+G`
regardless of `type`, because the call it enumerates processes through carries no
type field, and `nvidia-smi pmon` uses a different entry point that the mock does
not implement, so it lists nothing.

```yaml
device_defaults:
  processes:
    - pid: 1234
      type: "C"                       # C=compute, G=graphics
      name: "python"
      used_memory_mib: 1024
      sm_util: 75                     # SM (compute) utilization %
      mem_util: 40                    # memory-bandwidth utilization %
    - pid: 5678
      type: "G"
      name: "ffmpeg"
      used_memory_mib: 512
      enc_util: 60                    # encoder utilization %
      dec_util: 30                    # decoder utilization %
```

## Per-Device Overrides

Override any property for specific devices:

```yaml
devices:
  - index: 0
    uuid: "GPU-12345678-1234-1234-1234-123456780000"
    minor_number: 0
    pci:
      bus_id: "0000:07:00.0"
    # Override thermal for this device only
    thermal:
      temperature_gpu_c: 35
  
  - index: 1
    uuid: "GPU-12345678-1234-1234-1234-123456780001"
    minor_number: 1
    pci:
      bus_id: "0000:0F:00.0"
    thermal:
      temperature_gpu_c: 37
```

## NVLink Configuration

```yaml
nvlink:
  version: 4
  links_per_gpu: 18
  bandwidth_per_link_mbps: 26562
  c2c_enabled: false
  links:
    - link: 0
      state: "active"
      remote_device_type: "GPU"
      remote_pci_bus_id: "0000:0F:00.0"
```

`c2c_enabled` is node-level and drives the `GPU C2C Mode` row of `nvidia-smi -q`
on every attached GPU: `true` reports `Enabled`, while `false` or an absent key
reports `N/A` — never `Disabled`, because NVML answers
`NVML_ERROR_NOT_SUPPORTED`, the correct reading for a board with no NVLink-C2C
link to a host CPU. Only `gb200` and `gb300` enable it. This is the only key that
drives the row: `device_defaults.features.nvlink_c2c` is descriptive metadata and
is not read.

### NVLink error injection (per device)

A device can be given a rising NVLink DL error rate on its switch links, so its
uplinks report climbing replay/recovery/CRC errors — the GPU-side signal DCGM's
`DCGM_HEALTH_WATCH_NVLINK` reads (→ `DCGM_FR_NVLINK_*`). Set it in a device
override, or at runtime with [`nvml-mock-ctl nvlink-error`](nvml-mock-ctl.md#nvlink-error--inject-nvlink-dl-errors-on-switch-links):

```yaml
devices:
  - index: 0
    nvlink_error:
      rate: 250        # errors/second added on top of the healthy baseline
      links: [0, 1, 2] # optional; omit to inject on all active links
```

The count accrues monotonically off the shared counter epoch (same model as
`nvlink.defaults.error_rate`); `rate: 0` (or omitting the block) is healthy. This
is deliberately *not* an "NVSwitch entity health" knob — DCGM's NVSwitch/SXID
health is NSCQ/kernel-log sourced and cannot be driven from a `libnvidia-ml.so`
mock.

## Fabric Health

`fabric:` describes the GPU's NVLink fabric attachment — the identity fields
(`cluster_uuid`, `clique_id`, `state`) plus the health `nvidia-smi -q` renders
under `Fabric` → `Health`. A `fabric:` block with no health keys means a healthy
fabric, so the shipped Grace-Blackwell profiles report `Summary: Healthy` and
`Bandwidth: Full` without configuring anything:

```yaml
device_defaults:
  fabric:
    cluster_uuid: "00000000-0000-0000-0000-000000000001"
    clique_id: 0
    state: "auto"      # couple registration to fake fabricmanager readiness
```

Fault a single condition by naming it. Every other condition stays healthy, so a
consumer sees exactly the fault you configured:

```yaml
devices:
  - index: 3
    fabric:
      health:
        route_unhealthy: true
```

| `health:` key | `nvidia-smi -q` row | values |
| ------------- | ------------------- | ------ |
| `degraded_bandwidth` | `Bandwidth` | `false` → `Full`, `true` → `Degraded` |
| `route_recovery` | `Route Recovery in progress` | `false` / `true` |
| `route_unhealthy` | `Route Unhealthy` | `false` / `true` |
| `access_timeout_recovery` | `Access Timeout Recovery` | `false` / `true` |
| `incorrect_configuration` | `Incorrect Configuration` | `none` (default), `no_partition`, `insufficient_nvlinks`, `incompatible_gpu_fw`, `invalid_location`, `incorrect_sysguid`, `incorrect_chassis_sn`, `gpu_state_invalid` |

### Health summary

The `Summary` row is derived from the conditions above, so an injected fault
moves it: all clear → `Healthy`, degraded bandwidth alone → `Limited Capacity`,
any other fault → `Unhealthy`. Pin it with `health_summary` when you need a
summary that disagrees with the conditions (a driver that reports a fault
without classifying it, say):

```yaml
fabric:
  health_summary: "limited_capacity"   # healthy | unhealthy | limited_capacity
                                       # | not_supported | auto (default)
```

`not_supported` reproduces the pre-#677 rendering: `nvidia-smi` treats an
unreported summary as "no health data" and prints `N/A` for the whole `Health`
block.

### Raw `health_mask`

`health_mask` sets the NVML v2/v3 health bitmask directly, for encodings the
`health:` keys cannot express. It replaces the derived mask wholesale, and the
summary is derived from it:

```yaml
fabric:
  health_mask: 0x1aa    # what `health:` with everything clear produces
```

An explicit `health_mask: 0` means "the driver reported no health at all" and
renders the whole block as `N/A` — that is why the shipped profiles no longer
set it.

### `Partition Assigned`

The mock reports this field as `NOT_SUPPORTED`, which is what a real GB300 tray
in a healthy rack reports (the driver does not answer it). Whether the row
appears at all is up to the `nvidia-smi` build: 580.173.02 prints
`Partition Assigned : N/A`, while the 580.65.06 binary the mock image bundles has
no such label and omits the row for every mask value.

Fabric health can also be degraded and restored at runtime with
[`nvml-mock-ctl fabric-health`](nvml-mock-ctl.md#fabric-health--degrade-nvlink-fabric-health),
without restarting the consumer.

## Available GPU Profiles

Standalone configuration files are provided for each supported GPU model:

| File | GPU Model | Memory | Architecture |
|------|-----------|--------|--------------|
| `pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml` | NVIDIA A100-SXM4-40GB | 40 GiB | Ampere |
| `pkg/gpu/mocknvml/configs/mock-nvml-config-h100.yaml` | NVIDIA H100 80GB HBM3 | 80 GiB | Hopper |
| `pkg/gpu/mocknvml/configs/mock-nvml-config-b200.yaml` | NVIDIA B200 | 192 GiB | Blackwell |
| `pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml` | NVIDIA GB200 NVL | 192 GiB | Blackwell |
| `pkg/gpu/mocknvml/configs/mock-nvml-config-gb300.yaml` | NVIDIA GB300 NVL | 288 GiB | Blackwell Ultra |
| `pkg/gpu/mocknvml/configs/mock-nvml-config-l40s.yaml` | NVIDIA L40S | 48 GiB | Ada Lovelace |
| `pkg/gpu/mocknvml/configs/mock-nvml-config-t4.yaml` | NVIDIA T4 | 16 GiB | Turing |

Each file contains a complete configuration with all 8 devices configured.

## Integration Values

When deploying via Helm, additional values control integration with external projects:

| Key | Default | Description |
|-----|---------|-------------|
| `integrations.fakeGpuOperator.enabled` | `false` | Create per-profile ConfigMaps named `gpu-profile-<profile>`, keyed `profile.yaml`, in the shape fake-gpu-operator's loader reads |
| `integrations.fakeGpuOperator.targetNamespace` | `""` (release namespace) | Namespace for the profile ConfigMaps. Set to FGO's release namespace for FGO to find them; requires FGO's `builtinProfiles.enabled=false` |
| `integrations.fakeGpuOperator.profileLabels` | `run.ai/gpu-profile: "true"` | Extra labels on profile ConfigMaps. The contract labels are always emitted |

See [fake-gpu-operator integration](integrations/fake-gpu-operator.md) for setup details.

## Metric Fidelity

Every value the mock reports comes from configuration, from a clock-driven
simulator, or from a fixed derivation — never from real GPU work, because the
mock never executes a kernel. This section states which is which, so a dashboard
built against nvml-mock is read with the right expectations.

### Simulated — changes between calls

The driver is **elapsed time, not workload**: these numbers move on a completely
idle node, and they do not move any faster under load.

**Utilization is live in every shipped profile.** Each profile carries its own
`device_defaults.dynamic_metrics.utilization` block — `pattern: steady`, GPU
10–45%, memory 5–25% — so `nvmlDeviceGetUtilizationRates`, and therefore
`DCGM_FI_DEV_GPU_UTIL`, never reports a constant 0 on a default install. The
floor is deliberately above 0 so "utilization is reported" is a deterministic
assertion rather than a flaky one. Because every `DCGM_FI_PROF_*` activity
metric is a fixed fraction of `utilization.gpu` (see *Deliberately fixed*
below), the whole profiling surface is non-zero as a consequence.

Temperature and power stay **opt-in**: set `gpu.dynamicMetrics.enabled=true`
(Helm) or add `dynamic_metrics.temperature` / `dynamic_metrics.power` to the
config. Note that enabling the Helm overlay *replaces* the profile's
`dynamic_metrics` block wholesale with the chart baseline, whose utilization
default is `pattern: burst` across 0–100 — that band's idle phase does report
near 0. Pin `gpu.dynamicMetrics.utilization.*` if you need a floor.

| Metric | Config | Behaviour |
|--------|--------|-----------|
| `temperature.gpu` | `dynamic_metrics.temperature` | `base_c`, plus a sine ramp over `ramp_period_sec` bounded by `ramp_c`, plus uniform noise in ±`variance_c`; clamped to the thermal shutdown threshold when known |
| `power.draw` | `dynamic_metrics.power` | `base_mw` ± `variance_mw`, clamped to `min_limit_mw` / `max_limit_mw` when those are set |
| `utilization.gpu`, `utilization.memory` | `dynamic_metrics.utilization` | random within the configured band, shaped by `pattern` (`idle` / `busy` / `burst` / `steady`) |
| NVLink throughput counters | — | accrue deterministically from a process-independent epoch (`MOCK_NVML_EPOCH`, else `/proc/stat` btime), so they grow monotonically across separate `nvidia-smi` runs |

### Static — held until the config changes

Everything else resolved through the effective config: device memory
(`memory.total_bytes` / `free_bytes` / `used_bytes` / `reserved_bytes`), ECC mode
and counters, clocks, fan speed, performance state, power limits, `processes`,
and failure injection. Each holds its configured value until the profile changes
or `nvml-mock-ctl` writes a runtime override, which takes effect within one
override TTL — see [nvml-mock-ctl](nvml-mock-ctl.md).

### Deliberately fixed

**Profiling metrics (`DCGM_FI_PROF_*`).** DCGM reads these on Hopper+ through the
NVML GPM API. The mock derives every activity metric as a fixed fraction of
`utilization.gpu`:

| GPM metric | Fraction of `utilization.gpu` |
|------------|-------------------------------|
| SM utilization, graphics utilization | 1.00 |
| SM occupancy | 0.75 |
| Any-tensor activity | 0.50 |
| HMMA (FP16 tensor) | 0.40 |
| FP16 | 0.35 |
| FP32 | 0.25 |
| Integer | 0.10 |
| FP64, DMMA, IMMA | 0.05 |
| DFMA | 0.02 |

DRAM bandwidth activity mirrors `utilization.memory`, and PCIe / NVLink
throughput come from the counter snapshots. The fractions approximate the shape
of a tensor-dominated Hopper training step; they are **intentionally not** tied to
execution. SM occupancy and tensor-core activity are properties of kernels the
mock does not run, so deriving them from anything other than the configured
utilization would be fabrication rather than simulation. They stay fixed by
design and are not a gap to be closed.

**Identity and topology.** Device `name`, `architecture`, `brand`,
`compute_capability`, `uuid`, PCI `bus_id`, and the BAR1 aperture
(`bar1_memory`) are baked onto the device at construction and need a pod restart
to change.

### Allocation-aware — opt in

`memory.used_bytes` and `memory.free_bytes` follow Kubernetes GPU allocation
when `allocationWatcher.enabled=true`. A sidecar in the nvml-mock DaemonSet
polls the kubelet pod-resources API and writes the same runtime override file
`nvml-mock-ctl` writes, which the engine re-reads within one override TTL.
Scheduling a pod that claims a GPU moves that GPU's number; deleting the pod
returns it.

| Value | Behaviour |
|-------|-----------|
| `allocationWatcher.usedFractionPerClaim` | Share of the usable aperture (`total - reserved`) attributed per claim. Default `0.5` |
| Time-sliced GPUs | Each holding container is a separate claim; claims on one device add up and clamp at the aperture |
| Unclaimed GPUs | Report `used_bytes: 0` and the profile's idle `free_bytes` |

The reported bytes are **synthetic**. The mock runs no kernel and allocates no
device memory, so the number says a claim *exists* — not what a workload
touched. It reads per device: a claim on GPU 3 moves GPU 3 only.

The design is level-triggered. Each poll recomputes every device from the full
current allocation, so a missed event self-heals on the next tick rather than
leaving a GPU pinned at a stale value. A failed poll publishes nothing and keeps
the previous reading, because publishing zeros on a transient kubelet error
would report the whole node idle.

While the watcher runs it **owns** those two fields: an `nvml-mock-ctl set --gpu
N memory.used_bytes=…` is overwritten on the next poll. Every other field
`nvml-mock-ctl` writes is preserved — both writers take the same lock.

Off by default, because enabling it mounts the kubelet pod-resources socket
(read-only, node-local; no RBAC, since it is not the API server).

### Still not workload-aware — known gap

`processes` stays empty regardless of allocation, so `nvidia-smi` reports no
running processes even on a claimed GPU. Tracked in
[#506](https://github.com/NVIDIA/k8s-test-infra/issues/506) item 2.

Read the *Simulated* section carefully here too: `utilization.gpu` is no longer
a constant 0, but it is **not** evidence of work either. It moves on elapsed
time. A busy-looking utilization on an idle node is exactly what that block
produces, by design.

What each container *can see* has always been allocation-aware, and is a
separate thing from what the values say: the engine filters its visible GPU set
by which `/dev/nvidia*` nodes are present, so a pod allocated one GPU on an
eight-GPU node sees exactly one in `nvidia-smi -L`.

To move any of these explicitly, use `nvml-mock-ctl set`.

## Validation

The configuration is validated on load:

- `version` field is required
- `system.driver_version` is required
- Device indices must be unique
- Invalid YAML syntax causes fallback to defaults

Enable debug mode to see validation errors:

```bash
MOCK_NVML_DEBUG=1 LD_LIBRARY_PATH=. nvidia-smi
```
