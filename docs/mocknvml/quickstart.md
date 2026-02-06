# Quick Start Guide

Get Mock NVML running in 5 minutes.

## Prerequisites

- Linux (x86_64 or arm64)
- Go 1.23+ with CGo
- GCC toolchain (`build-essential` on Debian/Ubuntu)
- `nvidia-smi` binary (from NVIDIA driver or CUDA toolkit)

## Option 1: Local Build (Linux)

### Step 1: Build the Library

```bash
cd pkg/gpu/mocknvml
make
```

This creates:
```
libnvidia-ml.so.550.163.01  # Versioned library
libnvidia-ml.so.1           # Soname symlink
libnvidia-ml.so             # Linker symlink
```

### Step 2: Run with Default Configuration

```bash
# 8x Mock A100 GPUs with default settings
LD_LIBRARY_PATH=. nvidia-smi
```

### Step 3: Run with YAML Configuration

```bash
# A100 profile (40GB, 400W)
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml nvidia-smi

# GB200 profile (192GB, 1000W)
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-gb200.yaml nvidia-smi
```

## Option 2: Docker Build (Cross-Platform)

Build Linux binaries from macOS or other platforms:

```bash
cd pkg/gpu/mocknvml
make docker-build
```

## Verification

### Basic Check

```bash
LD_LIBRARY_PATH=. nvidia-smi -L
```

Expected output:
```
GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780000)
GPU 1: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780001)
...
```

### Full Query

```bash
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml nvidia-smi -q
```

### XML Output

```bash
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml nvidia-smi -x -q
```

### CSV Query

```bash
LD_LIBRARY_PATH=. nvidia-smi --query-gpu=index,name,uuid,memory.total --format=csv
```

## Debug Mode

Enable verbose logging to see NVML function calls:

```bash
LD_LIBRARY_PATH=. MOCK_NVML_DEBUG=1 nvidia-smi
```

Output includes:
```
[CONFIG] Loaded YAML config: 8 devices, driver 550.163.01
[ENGINE] Creating devices from YAML config
[DEVICE 0] Created: name=NVIDIA A100-SXM4-40GB uuid=GPU-12345678-...
[NVML] nvmlDeviceGetHandleByIndex(0)
[NVML] nvmlDeviceGetTemperature(sensor=0) -> 33
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MOCK_NVML_CONFIG` | Path to YAML config file | (none) |
| `MOCK_NVML_NUM_DEVICES` | Number of GPUs (without YAML) | 8 |
| `MOCK_NVML_DRIVER_VERSION` | Driver version (without YAML) | 550.163.01 |
| `MOCK_NVML_DEBUG` | Enable debug logging | (disabled) |

## Next Steps

- [Configuration Reference](configuration.md) - Customize GPU properties
- [Examples](examples.md) - Common usage patterns
- [Architecture](architecture.md) - Understand how it works
