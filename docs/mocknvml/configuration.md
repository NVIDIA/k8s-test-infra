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
export MOCK_NVML_DRIVER_VERSION=550.54.15
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
      bus_id: "00000000:07:00.0"
  - index: 1
    uuid: "GPU-12345678-1234-1234-1234-123456780001"
    pci:
      bus_id: "00000000:0F:00.0"
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
    bus_id: "00000000:07:00.0"        # Usually per-device
  
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

```yaml
device_defaults:
  processes:
    - pid: 1234
      type: "C"                       # C=compute, G=graphics
      name: "python"
      used_memory_mib: 1024
```

## Per-Device Overrides

Override any property for specific devices:

```yaml
devices:
  - index: 0
    uuid: "GPU-12345678-1234-1234-1234-123456780000"
    minor_number: 0
    pci:
      bus_id: "00000000:07:00.0"
    # Override thermal for this device only
    thermal:
      temperature_gpu_c: 35
  
  - index: 1
    uuid: "GPU-12345678-1234-1234-1234-123456780001"
    minor_number: 1
    pci:
      bus_id: "00000000:0F:00.0"
    thermal:
      temperature_gpu_c: 37
```

## NVLink Configuration

```yaml
nvlink:
  version: 4
  links_per_gpu: 18
  bandwidth_per_link_gbps: 25
  switch_support: true
  switch_count: 6
  c2c_enabled: false
  links:
    - link: 0
      state: "active"
      remote_device_type: "GPU"
      remote_pci_bus_id: "00000000:0F:00.0"
```

## Complete Example: A100 Profile

See `pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml` for a complete
A100 configuration with all 8 devices configured.

## Complete Example: GB200 Profile

See `pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml` for a Blackwell
GB200 configuration with 192 GiB HBM3e memory.

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
