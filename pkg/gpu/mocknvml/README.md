# Mock NVIDIA Management Library (NVML)

A production-ready mock implementation of the NVIDIA Management Library (NVML) designed for testing GPU-dependent applications in environments without physical NVIDIA GPUs.

## Overview

This library provides a fully functional mock of NVML that simulates an NVIDIA DGX A100 system with 8 GPUs. It implements all essential NVML functions required by the NVIDIA Device Plugin for Kubernetes, making it ideal for:

- CI/CD pipelines testing GPU workloads
- Development environments without GPU hardware
- Integration testing of GPU-aware applications
- Educational and demonstration purposes

## Key Features

- **Complete NVML API Coverage**: Implements 50+ NVML functions including initialization, device enumeration, memory queries, and process information
- **DGX A100 Simulation**: Accurately simulates 8x NVIDIA A100-SXM4-40GB GPUs with realistic properties
- **Reference Counting**: Properly implements NVML's reference counting behavior for init/shutdown
- **Thread-Safe**: All functions are protected with appropriate mutexes
- **Symbol Versioning**: Includes proper symbol versioning for ABI compatibility
- **Zero Dependencies**: Only requires standard C library and pthread

## Building

```bash
# Build the library
make

# Run tests
make test

# Show library information
make info
```

Build outputs:
- `build/lib64/libnvidia-ml.so.550.54.15` - Main library file
- `build/lib64/libnvidia-ml.so.1` - SONAME symlink
- `build/lib64/libnvidia-ml.so` - Development symlink

## Integration with gpu-mockctl

The mock NVML library is automatically included when creating a mock driver filesystem:

```bash
gpu-mockctl driver --driver-root /path/to/driver --with-compiled-nvml
```

This copies the library to the appropriate location in the mock driver filesystem.

## Simulated Hardware

The library simulates an NVIDIA DGX A100 system with the following specifications:

| Property | Value |
|----------|-------|
| GPU Model | NVIDIA A100-SXM4-40GB |
| Number of GPUs | 8 |
| Memory per GPU | 40 GB |
| CUDA Compute Capability | 8.0 |
| Driver Version | 550.54.15 |
| NVML Version | 12.550.54 |
| PCIe Generation | 4 |
| NVLink Support | Yes (12 links per GPU) |

## API Implementation Status

### ✅ Fully Implemented

**Initialization & Lifecycle**
- `nvmlInit()`, `nvmlInit_v2()`, `nvmlInitWithFlags()`
- `nvmlShutdown()`
- `nvmlErrorString()`

**System Information**
- `nvmlSystemGetDriverVersion()`
- `nvmlSystemGetNVMLVersion()`
- `nvmlSystemGetCudaDriverVersion()`

**Device Enumeration**
- `nvmlDeviceGetCount()`, `nvmlDeviceGetCount_v2()`
- `nvmlDeviceGetHandleByIndex()`, `nvmlDeviceGetHandleByIndex_v2()`
- `nvmlDeviceGetHandleByUUID()`
- `nvmlDeviceGetHandleByPciBusId()`, `nvmlDeviceGetHandleByPciBusId_v2()`

**Device Properties**
- `nvmlDeviceGetName()`
- `nvmlDeviceGetBrand()`
- `nvmlDeviceGetUUID()`
- `nvmlDeviceGetPciInfo()`, `nvmlDeviceGetPciInfo_v3()`
- `nvmlDeviceGetMinorNumber()`
- `nvmlDeviceGetCudaComputeCapability()`

**Memory Information**
- `nvmlDeviceGetMemoryInfo()`, `nvmlDeviceGetMemoryInfo_v2()`
- `nvmlDeviceGetBAR1MemoryInfo()`

**Process Information**
- `nvmlDeviceGetComputeRunningProcesses()`, `nvmlDeviceGetComputeRunningProcesses_v3()`
- `nvmlDeviceGetGraphicsRunningProcesses()`, `nvmlDeviceGetGraphicsRunningProcesses_v3()`

**Topology & Connectivity**
- `nvmlDeviceGetNvLinkState()`
- `nvmlDeviceGetNvLinkRemotePciInfo()`, `nvmlDeviceGetNvLinkRemotePciInfo_v2()`

### ⚠️ Returns Static/Default Values

- Temperature queries (returns 30°C)
- Power queries (returns 250W)
- Clock speeds (returns base clocks)
- Utilization (returns 0%)

### ❌ Not Supported (Returns NVML_ERROR_NOT_SUPPORTED)

- MIG (Multi-Instance GPU) operations
- vGPU operations
- Configuration changes (power limits, clocks, etc.)
- Event handling (beyond basic registration)

## Architecture

```
src/
├── nvml_init.c      # Initialization, shutdown, error handling
├── nvml_system.c    # System-level information queries
├── nvml_device.c    # Device enumeration and properties
├── nvml_memory.c    # Memory, temperature, power queries
├── nvml_mig.c       # MIG stubs (returns not supported)
└── nvml_stubs.c     # Additional function stubs

data/
└── devices.h        # Mock device data definitions

include/
└── nvml.h          # NVML API header (from go-nvml)
```

## Testing

The library includes a comprehensive test suite:

```bash
# Run all tests
make test

# Run specific test suites
make test-basic          # Basic API tests
make test-comprehensive  # Full test suite with thread safety
make test-valgrind      # Memory leak detection

# Run tests in Docker (if no local compiler)
./test/run_tests.sh

# Run tests with detailed output
make test-comprehensive CFLAGS="-DDEBUG"
```

### Test Coverage

The test suite covers:
- ✅ Initialization and shutdown with reference counting
- ✅ System information queries
- ✅ Device enumeration and properties
- ✅ Memory information
- ✅ CUDA compute capability
- ✅ Process information
- ✅ Error handling and invalid arguments
- ✅ Thread safety with concurrent access
- ✅ NVLink functionality
- ✅ Uninitialized access protection

### CI/CD Integration

Tests run automatically on:
- Pull requests that modify the library
- Pushes to main branch
- Manual workflow dispatch

See `.github/workflows/test-mocknvml.yml` for CI configuration.

## Thread Safety

All functions that access global state are protected with mutexes:
- Reference counting for init/shutdown
- Device handle validation
- Any operations that could race

## Limitations

This is a mock implementation intended for testing:
- No actual GPU hardware access
- Static/hardcoded values for dynamic metrics
- No real process monitoring
- No support for GPU configuration changes
- MIG functionality returns "not supported"

## Contributing

When adding new functions:
1. Check if it's required by consumers (device plugin, etc.)
2. Implement proper error checking and thread safety
3. Add appropriate mock data to `data/devices.h`
4. Update this README with the implementation status
5. Add test coverage

## License

Licensed under the Apache License, Version 2.0. See LICENSE in the repository root.