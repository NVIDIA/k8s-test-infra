# Mock NVML Documentation

A configurable mock implementation of NVIDIA's NVML (NVIDIA Management Library)
for testing GPU-dependent software without physical NVIDIA hardware.

## Documentation Index

| Document | Description |
|----------|-------------|
| [Quick Start](quickstart.md) | Get up and running in 5 minutes |
| [Architecture](architecture.md) | System design and component overview |
| [Configuration Reference](configuration.md) | Complete YAML configuration guide |
| [Examples](examples.md) | Common usage patterns and scenarios |
| [Development Guide](development.md) | Contributing and extending the library |
| [Troubleshooting](troubleshooting.md) | Common issues and solutions |

## What is Mock NVML?

Mock NVML is a drop-in replacement for `libnvidia-ml.so` that:

- **Works with real nvidia-smi** - No modifications needed to the binary
- **Simulates any GPU** - A100, H100, B200, GB200, or custom profiles via YAML
- **Zero hardware required** - Test GPU workloads on any Linux system
- **Kubernetes-ready** - Test device plugins, operators, and schedulers

## Prerequisites

### nvidia-smi Binary

The mock library replaces `libnvidia-ml.so` but **does not include** the `nvidia-smi` binary itself. You need to obtain `nvidia-smi` through one of these methods:

| Method | Use Case | Command/Notes |
|--------|----------|---------------|
| **NVIDIA Driver Package** | Systems with driver installed | Already at `/usr/bin/nvidia-smi` |
| **Container Image** | CI/CD, Kubernetes | Extract from driver package into your image (CUDA base images do NOT include nvidia-smi) |
| **Standalone Extract** | Minimal environments | Extract from `.run` driver installer |
| **nvidia-container-toolkit** | Container testing | Provides nvidia-smi in container context |

**Note:** The mock library intercepts NVML calls from `nvidia-smi`. The binary itself must exist on your system or in your container image.

### For CI/CD (No Hardware)

```bash
# Option 1: Standalone build (obtain nvidia-smi separately)
FROM ubuntu:22.04
# Note: nvidia-smi must be obtained separately (e.g., from driver .run package or a driver image)
COPY libnvidia-ml.so.1 /usr/lib/x86_64-linux-gnu/
ENV LD_PRELOAD=/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1

# Option 2: Extract nvidia-smi from driver package
# Download driver .run file, extract nvidia-smi binary only
```

## Use Cases

### Testing Kubernetes GPU Components

```bash
# Test NVIDIA device plugin without GPUs
export LD_LIBRARY_PATH=/path/to/mocknvml
export MOCK_NVML_CONFIG=/path/to/a100-config.yaml
./k8s-device-plugin
```

> **Note:** `LD_LIBRARY_PATH` works for local nvidia-smi testing. Kubernetes consumers (device plugin, DRA driver) use `--nvidia-driver-root` path resolution and do not honor `LD_LIBRARY_PATH`.

### CI/CD Pipelines

```yaml
# GitHub Actions example
- name: Test GPU features
  env:
    LD_LIBRARY_PATH: ./pkg/gpu/mocknvml
    MOCK_NVML_CONFIG: ./configs/mock-nvml-config-a100.yaml
  run: |
    make docker-build -C pkg/gpu/mocknvml
    go test ./... -tags=gpu
```

### Local Development

```bash
# Simulate 8x A100 GPUs on your laptop
# Requires: nvidia-smi binary (see Prerequisites section)
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml nvidia-smi
```

**For systems without NVIDIA drivers:**
```bash
# Requires: nvidia-smi binary mounted or installed in the image
docker run --rm -v $(pwd):/mock \
  -e LD_PRELOAD=/mock/libnvidia-ml.so.1 \
  -e MOCK_NVML_CONFIG=/mock/configs/mock-nvml-config-a100.yaml \
  ubuntu:22.04 /path/to/nvidia-smi
```

> **Note:** `LD_PRELOAD` forces loading the mock even when a real NVML library is present. `LD_LIBRARY_PATH` is sufficient when no real library exists.

## Quick Example

```bash
# 1. Build the library
cd pkg/gpu/mocknvml
make

# 2. Run nvidia-smi with mock library
LD_LIBRARY_PATH=. nvidia-smi

# Output:
# +-----------------------------------------------------------------------------------------+
# | NVIDIA-SMI 550.163.01             Driver Version: 550.163.01     CUDA Version: 12.4     |
# |-----------------------------------------+------------------------+----------------------+
# | GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
# |=========================================+========================+======================|
# |   0  NVIDIA A100-SXM4-40GB          On  |   00000000:07:00.0 Off |                    0 |
# ...
```

## Supported Features

| Category | Functions | Status | Notes |
|----------|-----------|--------|-------|
| Initialization | `nvmlInit`, `nvmlShutdown` | ✅ Full | |
| System Info | `SystemGetDriverVersion`, `SystemGetCudaDriverVersion` | ✅ Full | |
| Device Enumeration | `GetCount`, `GetHandleByIndex/UUID/PCI` | ✅ Full | |
| Device Info | `GetName`, `GetUUID`, `GetMemoryInfo`, `GetMemoryBusWidth` | ✅ Full | |
| Thermal | `GetTemperature`, `GetTemperatureThreshold` | ✅ Full | |
| Power | `GetPowerUsage`, `GetPowerManagementLimit`, `GetTotalEnergyConsumption` | ✅ Full | |
| Clocks | `GetClockInfo`, `GetMaxClockInfo`, `GetAutoBoostedClocksEnabled` | ✅ Full | |
| ECC | `GetEccMode`, `GetDefaultEccMode`, `GetTotalEccErrors`, `GetDetailedEccErrors` | ✅ Full | |
| PCIe | `GetPciInfo`, `GetCurrPcieLinkGeneration/Width` | ✅ Full | |
| Utilization | `GetUtilizationRates` | ✅ Full | |
| Topology | `GetTopologyCommonAncestor`, `GetTopologyNearestGpus` | ✅ Full | |
| NVLink | `GetNvLinkState`, `GetNvLinkVersion`, `GetNvLinkCapability` | ✅ Full | |
| Persistence | `GetPersistenceMode`, `SetPersistenceMode` | ✅ Full | |
| Process | `GetComputeRunningProcesses`, `GetGraphicsRunningProcesses` | ✅ Full | Returns empty list |
| MIG | `GetMigMode` | ✅ Basic | Returns disabled; profile info functions return `NOT_SUPPORTED` |
| GSP Firmware | `GetGspFirmwareVersion`, `GetGspFirmwareMode` | ✅ Full | |
| nvidia-smi | `-q`, `-x -q`, default display, CSV queries | ✅ Full | Real binary, mock data |
| Other | 289 additional functions | ⚠️ Returns `NOT_SUPPORTED` | See note below |

### Stub Function Behavior

The mock library implements ~111 NVML functions with configurable behavior.
The remaining ~289 functions return `NVML_ERROR_NOT_SUPPORTED` (error code 3). This is distinct from:
- `NVML_SUCCESS` (0) - Operation succeeded
- `NVML_ERROR_NOT_FOUND` (6) - Device/object not found

**Impact:** Code calling unimplemented functions will receive an error. Most NVML consumers (including `nvidia-smi`) handle `NOT_SUPPORTED` gracefully by displaying "N/A" or omitting the field.

**Debugging:** Set `MOCK_NVML_DEBUG=1` to log when stub functions are called:
```bash
MOCK_NVML_DEBUG=1 LD_LIBRARY_PATH=. nvidia-smi 2>&1 | grep "NOT IMPLEMENTED"
```

## Requirements

- **Go 1.25+** with CGo enabled
- **Linux** (x86_64 or arm64)
- **GCC toolchain** for building
- **Docker** (optional, for cross-platform builds)

## License

Apache License 2.0 - See [LICENSE](../../LICENSE) file.
