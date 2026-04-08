# Mock NVML Library

A configurable mock implementation of NVIDIA's NVML (NVIDIA Management Library)
for testing GPU-dependent software without physical NVIDIA hardware.

## Key Features

- **nvidia-smi compatible**: Works with the real `nvidia-smi` binary
- **YAML-based configuration**: Full control over GPU profiles (A100, GB200, custom)
- **Zero-config default**: Simulates DGX A100 system (8 GPUs) out of the box
- **89 NVML functions**: Comprehensive API coverage for nvidia-smi compatibility
- **Auto-generated bridge**: Scalable CGo bridge generated from `go-nvml`
- **Docker build support**: Build Linux binaries on macOS
- **Thread-safe**: Proper synchronization for concurrent access
- **Well-tested**: Comprehensive unit tests + integration test

## Quick Start

```bash
# Build (requires Linux with Go and GCC)
cd pkg/gpu/mocknvml
make

# Test all 3 scenarios:

# 1. Default (8x Mock A100, no config)
LD_LIBRARY_PATH=. nvidia-smi

# 2. A100 profile (8x A100-SXM4-40GB, 40GB, 400W)
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml nvidia-smi

# 3. GB200 profile (8x GB200 NVL, 192GB, 1000W)
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-gb200.yaml nvidia-smi
```

### Docker Build (Cross-platform)

```bash
cd pkg/gpu/mocknvml
make docker-build
```

Builds the library inside a Docker container, producing Linux-compatible
binaries even on macOS.

### Build Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `LIB_VERSION` | Library version (appears in filename) | 550.163.01 |
| `GOLANG_VERSION` | Go version for Docker builds | 1.25.0 |

This produces:
- `libnvidia-ml.so.<version>` - The actual library
- `libnvidia-ml.so.1` - Symlink (soname)
- `libnvidia-ml.so` - Symlink (linker name)

**Note:** The `LIB_VERSION` should match the `driver_version` in your YAML
config for consistency.

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MOCK_NVML_CONFIG` | Path to YAML configuration file | (none - uses defaults) |
| `MOCK_NVML_NUM_DEVICES` | Number of GPUs to simulate (if no YAML, max 8) | 8 |
| `MOCK_NVML_DRIVER_VERSION` | Driver version string (if no YAML) | 550.163.01 |
| `MOCK_NVML_DEBUG` | Enable debug logging to stderr | (disabled) |

> **Note:** The maximum number of simulated GPUs is 8. See [Limitations](#limitations)
> for details.

### YAML Configuration

YAML configs allow full control over GPU properties. See `configs/` for examples:

- `mock-nvml-config-a100.yaml` - DGX A100 (8x A100-SXM4-40GB)
- `mock-nvml-config-h100.yaml` - HGX H100 (8x H100 80GB HBM3)
- `mock-nvml-config-b200.yaml` - B200 (8x B200, 192 GiB HBM3e)
- `mock-nvml-config-gb200.yaml` - GB200 NVL (8x GB200 with 192 GiB HBM3e)
- `mock-nvml-config-l40s.yaml` - L40S (8x L40S, 48 GiB)
- `mock-nvml-config-t4.yaml` - T4 (8x T4, 16 GiB)

#### Configuration Structure

```yaml
version: "1.0"

system:
  driver_version: "550.163.01"
  nvml_version: "12.550.163.01"
  cuda_version: "12.4"
  cuda_version_major: 12
  cuda_version_minor: 4

device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  architecture: "ampere"
  memory:
    total_bytes: 42949672960      # 40 GiB
  power:
    default_limit_mw: 400000      # 400W
    current_draw_mw: 72000        # 72W idle
  thermal:
    temperature_gpu_c: 33
  # ... see full examples in configs/

devices:
  - index: 0
    uuid: "GPU-12345678-1234-1234-1234-123456780000"
    pci:
      bus_id: "00000000:07:00.0"
  - index: 1
    uuid: "GPU-12345678-1234-1234-1234-123456780001"
    pci:
      bus_id: "00000000:0F:00.0"
  # ... define each GPU
```

### Debugging

Enable verbose logging to troubleshoot issues:

```bash
LD_LIBRARY_PATH=. MOCK_NVML_DEBUG=1 nvidia-smi

# Example output:
# [CONFIG] Loaded YAML config: 8 devices, driver 550.163.01
# [ENGINE] Creating devices from YAML config
# [DEVICE 0] Created: name=NVIDIA A100-SXM4-40GB uuid=GPU-12345678-...
# [NVML] nvmlDeviceGetHandleByIndex(0)
# [NVML] nvmlDeviceGetTemperature(sensor=0) -> 33
```

## Supported nvidia-smi Commands

| Command | Description |
|---------|-------------|
| `nvidia-smi` | Default display (GPU table) |
| `nvidia-smi -L` | List GPUs with UUIDs |
| `nvidia-smi -q` | Full query (all details) |
| `nvidia-smi -q -d MEMORY` | Memory details |
| `nvidia-smi -q -d TEMPERATURE` | Temperature details |
| `nvidia-smi -q -d POWER` | Power details |
| `nvidia-smi -q -d CLOCK` | Clock details |
| `nvidia-smi -q -d ECC` | ECC details |
| `nvidia-smi -q -d UTILIZATION` | Utilization details |
| `nvidia-smi -q -d PCIE` | PCIe details |
| `nvidia-smi -x -q` | XML output (full query) |
| `nvidia-smi --query-gpu=... --format=csv` | CSV output |
| `nvidia-smi -i <index>` | Query specific GPU |

Example CSV query:

```bash
nvidia-smi --query-gpu=index,name,uuid,memory.total,power.draw,temperature.gpu --format=csv
```

## Example Output

### With A100 Config

```
$ MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml LD_LIBRARY_PATH=. nvidia-smi
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 550.163.01             Driver Version: 550.163.01     CUDA Version: 12.4     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
|=========================================+========================+======================|
|   0  NVIDIA A100-SXM4-40GB          On  |   00000000:07:00.0 Off |                    0 |
| N/A   33C    P0             72W /  400W |       0MiB /  40960MiB |      0%      Default |
...
```

### With GB200 Config

```
$ MOCK_NVML_CONFIG=configs/mock-nvml-config-gb200.yaml LD_LIBRARY_PATH=. nvidia-smi
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 560.35.03              Driver Version: 560.35.03      CUDA Version: 12.6     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
|=========================================+========================+======================|
|   0  NVIDIA GB200 NVL               On  |   00000000:0A:00.0 Off |                    0 |
| N/A   36C    P0            145W / 1000W |       0MiB / 196608MiB |      0%      Default |
...
```

## Architecture

```
┌─────────────────────────────────────────┐
│         Your Application                 │
│    (e.g., k8s-device-plugin, nvidia-smi)│
└─────────────────┬───────────────────────┘
                  │ NVML C API
┌─────────────────▼───────────────────────┐
│      libnvidia-ml.so (Mock)             │
│                                          │
│  ┌────────────────────────────────────┐ │
│  │  Bridge Layer (CGo)                │ │
│  │  - Hand-written implementations    │ │
│  │  - Auto-generated stubs            │ │
│  │  - 400 C function exports          │ │
│  │  - Type conversions (C ↔ Go)       │ │
│  └────────────┬───────────────────────┘ │
│               │                          │
│  ┌────────────▼───────────────────────┐ │
│  │  Engine Layer (Go)                 │ │
│  │  - Singleton lifecycle mgmt        │ │
│  │  - Handle table (C ↔ Go mapping)   │ │
│  │  - YAML configuration loading      │ │
│  │  - MockServer delegation           │ │
│  └────────────┬───────────────────────┘ │
│               │                          │
│  ┌────────────▼───────────────────────┐ │
│  │  ConfigurableDevice                │ │
│  │  - YAML-driven GPU properties      │ │
│  │  - 89 NVML method implementations  │ │
│  │  - Wraps dgxa100.Device            │ │
│  └────────────┬───────────────────────┘ │
│               │                          │
│  ┌────────────▼───────────────────────┐ │
│  │  go-nvml Mock (dgxa100)            │ │
│  │  - DGX A100 simulation (8 GPUs)    │ │
│  │  - Base device properties          │ │
│  └────────────────────────────────────┘ │
└──────────────────────────────────────────┘
```

### Design Patterns

- **Singleton**: `Engine` uses singleton pattern for global state management
- **Decorator**: `ConfigurableDevice` extends `dgxa100.Device` with YAML config
- **Handle Table**: Maps C pointers to Go objects for CGo safety
- **Lazy Initialization**: Server created on first `Init()` call
- **Config Merging**: Device defaults + per-device overrides

## Project Structure

```
pkg/gpu/mocknvml/
├── bridge/                        # CGo bridge layer
│   ├── cgo_types.go               # Shared CGo type definitions
│   ├── helpers.go                 # Helper functions + main() + go:generate
│   ├── init.go                    # Init/shutdown functions
│   ├── device.go                  # Device handle functions
│   ├── events.go                  # Event set/wait functions
│   ├── system.go                  # System functions
│   ├── internal.go                # Internal export table (nvidia-smi)
│   ├── nvml_types.h               # C type definitions for CGo preamble
│   └── stubs_generated.go         # Auto-generated stubs (~289 functions)
├── engine/
│   ├── config.go                  # Configuration loading
│   ├── config_types.go            # YAML struct definitions
│   ├── device.go                  # ConfigurableDevice implementation
│   ├── engine.go                  # Main engine singleton
│   ├── handles.go                 # C-compatible handle management
│   ├── invalid_device.go          # Invalid device handle sentinel
│   ├── utils.go                   # Debug logging utilities
│   ├── version.go                 # NVML version responses
│   └── *_test.go                  # Unit tests
├── configs/
│   ├── mock-nvml-config-a100.yaml
│   ├── mock-nvml-config-b200.yaml
│   ├── mock-nvml-config-gb200.yaml
│   ├── mock-nvml-config-h100.yaml
│   ├── mock-nvml-config-l40s.yaml
│   └── mock-nvml-config-t4.yaml
├── Dockerfile                     # Docker build environment
├── Makefile                       # Build automation
└── README.md

cmd/generate-bridge/
├── main.go                        # Stub generator (--stats, --validate flags)
├── parser.go                      # nvml.h prototype parser
└── main_test.go                   # Generator tests

tests/mocknvml/
├── bridge_tests.go                # Bridge-level integration tests
├── main.go                        # Integration test (mini device plugin)
├── Dockerfile                     # Test container
├── Makefile                       # Test automation
└── README.md                      # Test documentation
```

## Testing

### Unit Tests

```bash
cd pkg/gpu/mocknvml/engine
go test -v -race -coverprofile=coverage.out ./...
```

### Integration Test

```bash
cd tests/mocknvml
make test
```

This builds and runs a mini device plugin in Docker that exercises the mock
NVML library.

## Supported NVML Functions

The mock library implements 89 NVML functions required by nvidia-smi:

- **Device enumeration**: `nvmlDeviceGetCount`, `nvmlDeviceGetHandleByIndex`
- **Device properties**: `nvmlDeviceGetName`, `nvmlDeviceGetUUID`, `nvmlDeviceGetMemoryInfo`
- **Thermal/Power**: `nvmlDeviceGetTemperature`, `nvmlDeviceGetPowerUsage`
- **Clocks**: `nvmlDeviceGetClockInfo`, `nvmlDeviceGetMaxClockInfo`
- **ECC**: `nvmlDeviceGetEccMode`, `nvmlDeviceGetTotalEccErrors`
- **PCIe**: `nvmlDeviceGetPciInfo`, `nvmlDeviceGetCurrPcieLinkGeneration`
- **MIG**: `nvmlDeviceGetMigMode`
- **Events**: `nvmlEventSetCreate`, `nvmlEventSetWait` (EventSetCreate returns `SUCCESS`; EventSetWait returns `TIMEOUT`)

All other NVML functions return `NVML_ERROR_NOT_SUPPORTED`, providing full API
coverage for linking.

## Regenerating Stubs

The stub generator creates stubs for NVML functions without hand-written implementations:

```bash
# From bridge directory
cd pkg/gpu/mocknvml/bridge
go generate

# Or from repo root
go run ./cmd/generate-bridge \
  -input vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go \
  -header vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h \
  -bridge pkg/gpu/mocknvml/bridge \
  -output pkg/gpu/mocknvml/bridge/stubs_generated.go
```

When adding new NVML function implementations, add them to the appropriate
bridge file (e.g., `device.go`) and regenerate stubs.

## Limitations

- **Maximum 8 GPUs**: The mock library supports a maximum of 8 simulated GPUs
  (`MaxDevices = 8`). This limit is enforced by the underlying `dgxa100` mock
  implementation and handle table. If your YAML config defines more than 8
  devices, only the first 8 will be created. This matches the typical DGX A100
  system configuration.
- **Read-only simulation**: No actual GPU operations
- **Static device properties**: Device properties set at initialization
- **Limited MIG support**: GetMigMode is implemented; MIG device enumeration returns `NOT_FOUND` (end-of-iteration signal)
- **Process list**: Always empty (configurable in YAML)

## Troubleshooting

### Library Not Found

```bash
# Verify library exists
ls -la pkg/gpu/mocknvml/libnvidia-ml.so*

# Check library dependencies
ldd pkg/gpu/mocknvml/libnvidia-ml.so

# Set library path
export LD_LIBRARY_PATH=$(pwd)/pkg/gpu/mocknvml:$LD_LIBRARY_PATH
```

### Symbol Not Found

```bash
# List exported symbols
nm -D pkg/gpu/mocknvml/libnvidia-ml.so | grep nvml
```

### Build Errors

```bash
# Clean and rebuild
make -C pkg/gpu/mocknvml clean
make -C pkg/gpu/mocknvml

# Regenerate bridge if needed
go run ./cmd/generate-bridge
```

## Contributing

When adding new features:
1. Follow NVIDIA Go coding patterns (see `go-nvml` for reference)
2. Add unit tests for new functionality
3. Update this README with new configuration options
4. Ensure Docker build still works
5. Run tests with race detection: `go test -race ./...`
6. Run linter: `golangci-lint run ./pkg/gpu/mocknvml/engine/...`

## License

Apache License 2.0 - See LICENSE file in repository root.

## Related Projects

- [go-nvml](https://github.com/NVIDIA/go-nvml) - Official NVIDIA Go bindings
  for NVML
- [k8s-device-plugin](https://github.com/NVIDIA/k8s-device-plugin) - NVIDIA
  device plugin for Kubernetes
- [nvidia-container-toolkit](https://github.com/NVIDIA/nvidia-container-toolkit)
  - Container toolkit for GPU support
