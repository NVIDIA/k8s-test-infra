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
| `integrations.fakeGpuOperator.enabled` | `false` | Create per-profile ConfigMaps for fake-gpu-operator discovery |
| `integrations.fakeGpuOperator.profileLabels` | `run.ai/gpu-profile: "true"` | Discovery labels on profile ConfigMaps |

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

### Not workload-aware — known gap

No reported value responds to Kubernetes scheduling. A pod holding an
`nvidia.com/gpu` claim does not move `memory.used_bytes` and does not appear in
`processes`; those values change only when something writes the config.

Read the *Simulated* section above carefully here: `utilization.gpu` is no
longer a constant 0, but it is **not** evidence of work either. It moves on
elapsed time. A busy-looking utilization on an idle node is exactly what that
block produces, by design.

Nothing in the deployment observes allocation today. The nvml-mock DaemonSet
stages the driver tree and then sleeps, and the optional NRI plugin subscribes
only to container **creation** and mounts the overlay read-only. Making memory
and `processes` allocation-aware is tracked in
[#506](https://github.com/NVIDIA/k8s-test-infra/issues/506).

What each container *can see* is already allocation-aware, and is a separate
thing from what the values say: the engine filters its visible GPU set by which
`/dev/nvidia*` nodes are present, so a pod allocated one GPU on an eight-GPU
node sees exactly one in `nvidia-smi -L`. Only the reported metrics are static.

To move these numbers today, drive them explicitly with `nvml-mock-ctl set`.

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
