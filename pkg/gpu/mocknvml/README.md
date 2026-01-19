# Mock NVML Library

A CGo-based mock implementation of NVIDIA's NVML (NVIDIA Management Library)
for testing GPU-enabled applications without physical GPUs.

## Overview

This library provides a drop-in replacement for `libnvidia-ml.so` that
simulates GPU devices and their properties. It's designed for testing
Kubernetes components like the NVIDIA device plugin in environments without
actual GPUs.

**Key Features:**
- **Zero-config default**: Simulates DGX A100 system (8 GPUs) out of the box
- **Full API coverage**: 396 NVML functions (16 implemented, 380 stubs)
- **Auto-generated bridge**: Scalable CGo bridge generated from `go-nvml`
- **Docker build support**: Build Linux binaries on macOS
- **Thread-safe**: Proper synchronization for concurrent access
- **Well-tested**: Comprehensive unit tests + integration test

## Quick Start

### Building the Library

#### Local Build (Linux)
```bash
cd pkg/gpu/mocknvml
make
```

This produces:
- `libnvidia-ml.so.550.54.15` (versioned library)
- `libnvidia-ml.so.1` (soname symlink)
- `libnvidia-ml.so` (linker symlink)

#### Docker Build (Cross-platform)
```bash
cd pkg/gpu/mocknvml
make docker-build
```

Builds the library inside a Docker container, producing Linux-compatible
binaries even on macOS.

### Using the Library

#### Option 1: Default Configuration (8 A100 GPUs)

```bash
# Set library path
export LD_LIBRARY_PATH=/path/to/pkg/gpu/mocknvml:$LD_LIBRARY_PATH

# Run your application
./your-gpu-application
```

#### Option 2: Custom Device Count

```bash
export LD_LIBRARY_PATH=/path/to/pkg/gpu/mocknvml:$LD_LIBRARY_PATH
export MOCK_NVML_NUM_DEVICES=4
export MOCK_NVML_DRIVER_VERSION=550.54.15

./your-gpu-application
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MOCK_NVML_NUM_DEVICES` | Number of GPU devices (max 8) | `8` |
| `MOCK_NVML_DRIVER_VERSION` | NVIDIA driver version string | `550.54.15` |

## Testing

### Integration Test

Run the integration test to verify the library works correctly:

```bash
cd tests/mocknvml
make test
```

This builds and runs a mini device plugin in Docker that exercises the mock
NVML library. See `tests/mocknvml/README.md` for details.

### Unit Tests

```bash
cd pkg/gpu/mocknvml/engine
go test -v -race ./...
```

## Supported NVML Functions

The mock library provides **full API coverage** with 396 NVML functions:

### Fully Implemented (16 functions)

#### Initialization & Lifecycle
- `nvmlInit` / `nvmlInit_v2` / `nvmlInitWithFlags`
- `nvmlShutdown`
- `nvmlErrorString`

#### System Information
- `nvmlSystemGetDriverVersion`

#### Device Enumeration
- `nvmlDeviceGetCount` / `nvmlDeviceGetCount_v2`
- `nvmlDeviceGetHandleByIndex` / `nvmlDeviceGetHandleByIndex_v2`
- `nvmlDeviceGetHandleByUUID`
- `nvmlDeviceGetHandleByPciBusId_v2` / `nvmlDeviceGetHandleByPciBusId_v1`

#### Device Information
- `nvmlDeviceGetName`
- `nvmlDeviceGetUUID`
- `nvmlDeviceGetPciInfo_v3`
- `nvmlDeviceGetMemoryInfo`

#### Process Information
- `nvmlDeviceGetComputeRunningProcesses_v3`
- `nvmlDeviceGetGraphicsRunningProcesses_v3`

### Stub Functions (380 functions)

All other NVML functions return `NVML_ERROR_NOT_SUPPORTED`. This provides
full API coverage for linking but allows gradual implementation of additional
functionality as needed.

## Architecture

```
┌─────────────────────────────────────────┐
│         Your Application                 │
│    (e.g., k8s-device-plugin)            │
└─────────────────┬───────────────────────┘
                  │ NVML C API
┌─────────────────▼───────────────────────┐
│      libnvidia-ml.so (Mock)             │
│                                          │
│  ┌────────────────────────────────────┐ │
│  │  Bridge Layer (CGo)                │ │
│  │  - Auto-generated from go-nvml     │ │
│  │  - 396 C function exports          │ │
│  │  - Type conversions (C ↔ Go)      │ │
│  └────────────┬───────────────────────┘ │
│               │                          │
│  ┌────────────▼───────────────────────┐ │
│  │  Engine Layer (Go)                 │ │
│  │  - Singleton lifecycle mgmt        │ │
│  │  - Handle table (C ↔ Go mapping)  │ │
│  │  - Configuration loading           │ │
│  │  - MockServer delegation           │ │
│  └────────────┬───────────────────────┘ │
│               │                          │
│  ┌────────────▼───────────────────────┐ │
│  │  MockServer (Decorator)            │ │
│  │  - Wraps dgxa100.Server            │ │
│  │  - Adds GetPciInfo()               │ │
│  │  - Adds process queries            │ │
│  └────────────┬───────────────────────┘ │
│               │                          │
│  ┌────────────▼───────────────────────┐ │
│  │  go-nvml Mock (dgxa100)            │ │
│  │  - DGX A100 simulation (8 GPUs)    │ │
│  │  - Device properties               │ │
│  └────────────────────────────────────┘ │
└──────────────────────────────────────────┘
```

### Design Patterns

- **Singleton**: `Engine` uses singleton pattern for global state management
- **Decorator**: `MockServer` extends `dgxa100.Server` without modification
- **Handle Table**: Maps C pointers to Go objects for CGo safety
- **Lazy Initialization**: Server created on first `Init()` call

## Development

### Project Structure

```
pkg/gpu/mocknvml/
├── bridge/
│   └── bridge_generated.go    # Auto-generated CGo bridge (396 functions)
├── engine/
│   ├── engine.go              # Lifecycle & handle management
│   ├── device.go              # MockServer & EnhancedDevice
│   ├── handles.go             # Handle table implementation
│   ├── config.go              # Configuration loading
│   └── *_test.go              # Unit tests
├── Dockerfile                 # Docker build environment
└── Makefile                   # Build automation

cmd/generate-bridge/
└── main.go                    # Bridge code generator

tests/mocknvml/
├── main.go                    # Integration test (mini device plugin)
├── Dockerfile                 # Test container
├── Makefile                   # Test automation
└── README.md                  # Test documentation
```

### Running Tests

#### Unit Tests
```bash
cd pkg/gpu/mocknvml/engine
go test -v -race -coverprofile=coverage.out ./...
```

#### Integration Test
```bash
cd tests/mocknvml
make test
```

### Regenerating the Bridge

The CGo bridge is auto-generated from the `go-nvml` interface:

```bash
go run ./cmd/generate-bridge
```

This parses `nvml.Interface` and generates 396 C function exports with proper
type conversions.

### Adding New NVML Function Implementations

1. Edit `cmd/generate-bridge/main.go`
2. Add a case in `getImplementation()` with your implementation
3. Regenerate: `go run ./cmd/generate-bridge`
4. Build and test: `make -C pkg/gpu/mocknvml && go test ./pkg/gpu/mocknvml/engine/...`

**Example:**
```go
case "nvmlDeviceGetTemperature":
    return `	dev := engine.GetEngine().LookupDevice(uintptr(nvmlDevice))
    if dev == nil {
        return C.NVML_ERROR_INVALID_ARGUMENT
    }
    temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
    if ret == nvml.SUCCESS {
        *(*C.uint)(temperature) = C.uint(temp)
    }
    return toReturn(ret)`
```

### Extending MockServer

To add functionality not present in `dgxa100.Device`, add methods to
`EnhancedDevice` in `pkg/gpu/mocknvml/engine/device.go`:

```go
func (d *EnhancedDevice) GetTemperature(sensor nvml.TemperatureSensors) (uint32, nvml.Return) {
    // Your implementation
    return 65, nvml.SUCCESS
}
```

## Limitations

- **Maximum 8 GPUs**: Limited by the underlying `dgxa100` mock implementation
- **Partial API implementation**: 16 of 396 functions fully implemented
- **Static device properties**: Device properties set at initialization
- **No MIG support**: Multi-Instance GPU features not implemented
- **No compute capability**: `GetCudaComputeCapability` returns
  `ERROR_NOT_SUPPORTED`

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

Apache License 2.0 - See LICENSE file for details.

## Related Projects

- [go-nvml](https://github.com/NVIDIA/go-nvml) - Official NVIDIA Go bindings
  for NVML
- [k8s-device-plugin](https://github.com/NVIDIA/k8s-device-plugin) - NVIDIA
  device plugin for Kubernetes
- [nvidia-container-toolkit](https://github.com/NVIDIA/nvidia-container-toolkit)
  - Container toolkit for GPU support
