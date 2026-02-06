# Development Guide

Contributing to and extending Mock NVML.

## Prerequisites

- Go 1.23+ with CGo enabled
- GCC toolchain
- Docker (for cross-platform builds)
- golangci-lint (for linting)

## Project Structure

```
pkg/gpu/mocknvml/
├── bridge/                    # CGo bridge layer (hand-written + generated)
│   ├── cgo_types.go           # Shared CGo type definitions
│   ├── helpers.go             # Helper functions + main() + go:generate
│   ├── init.go                # nvmlInit_v2, nvmlShutdown, etc.
│   ├── device.go              # Device handle functions
│   ├── system.go              # System functions
│   ├── internal.go            # Internal export table (nvidia-smi)
│   └── stubs_generated.go     # Auto-generated stubs (~375 functions)
├── engine/
│   ├── config.go              # Configuration loading
│   ├── config_types.go        # YAML struct definitions
│   ├── device.go              # ConfigurableDevice implementation
│   ├── engine.go              # Singleton engine
│   ├── handles.go             # C↔Go handle mapping
│   ├── utils.go               # Debug utilities
│   └── *_test.go              # Unit tests
├── configs/
│   ├── mock-nvml-config-a100.yaml
│   └── mock-nvml-config-gb200.yaml
├── Dockerfile
├── Makefile
└── README.md

cmd/generate-bridge/
└── main.go                    # Stub generator (generates stubs_generated.go)

tests/mocknvml/
├── main.go                    # Integration test
├── Dockerfile
├── Makefile
└── README.md

docs/mocknvml/
├── README.md
├── quickstart.md
├── architecture.md
├── configuration.md
├── examples.md
├── development.md
└── troubleshooting.md
```

## Building

### Local Build

```bash
cd pkg/gpu/mocknvml
make
```

### Docker Build (Cross-Platform)

```bash
make docker-build
```

### Build with Custom Version

```bash
LIB_VERSION=560.35.03 make
```

### Clean Build

```bash
make clean
make
```

## Running Tests

### Unit Tests

```bash
cd pkg/gpu/mocknvml/engine
go test -v -race ./...
```

### With Coverage

```bash
go test -v -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

### Integration Test

```bash
cd tests/mocknvml
make test
```

## Adding New NVML Functions

### Step 1: Identify the Function

Find the function signature in `go-nvml`:

```go
// In github.com/NVIDIA/go-nvml/pkg/nvml
type Device interface {
    GetNewFunction() (ReturnType, Return)
}
```

### Step 2: Implement in ConfigurableDevice

Add the method to `pkg/gpu/mocknvml/engine/device.go`:

```go
// GetNewFunction returns the new function value
func (d *ConfigurableDevice) GetNewFunction() (ReturnType, nvml.Return) {
    // Check if config provides this value
    if d.config != nil && d.config.NewProperty != nil {
        value := d.config.NewProperty.Value
        debugLog("[NVML] nvmlDeviceGetNewFunction -> %v\n", value)
        return value, nvml.SUCCESS
    }
    
    // No config = not supported
    debugLog("[NVML] nvmlDeviceGetNewFunction -> NOT_SUPPORTED\n")
    return ReturnType{}, nvml.ERROR_NOT_SUPPORTED
}
```

### Step 3: Add Config Types (if needed)

Add to `pkg/gpu/mocknvml/engine/config_types.go`:

```go
// NewPropertyConfig defines the new property configuration
type NewPropertyConfig struct {
    Value     int    `json:"value,omitempty"`
    Enabled   bool   `json:"enabled,omitempty"`
}
```

Add field to `DeviceConfig`:

```go
type DeviceConfig struct {
    // ... existing fields ...
    NewProperty *NewPropertyConfig `json:"new_property,omitempty"`
}
```

### Step 4: Add Bridge Implementation

Add the C-exported function to the appropriate bridge file (e.g., `bridge/device.go`
for device functions, or create a new file for a new category):

```go
//export nvmlDeviceGetNewFunction
func nvmlDeviceGetNewFunction(nvmlDevice unsafe.Pointer, result unsafe.Pointer) C.nvmlReturn_t {
    if result == nil {
        return C.NVML_ERROR_INVALID_ARGUMENT
    }
    dev := engine.GetEngine().LookupDevice(uintptr(nvmlDevice))
    value, ret := dev.GetNewFunction()
    if ret == nvml.SUCCESS {
        *(*C.int)(result) = C.int(value)
    }
    return toReturn(ret)
}
```

### Step 5: Regenerate Stubs

The generator automatically detects your new implementation and removes its stub:

```bash
cd pkg/gpu/mocknvml/bridge
go generate
```

Or from repo root:
```bash
go generate ./pkg/gpu/mocknvml/bridge/...
```

### Step 6: Test

```bash
# Unit test
go test -v ./pkg/gpu/mocknvml/engine/...

# Integration test
make -C tests/mocknvml test
```

## Adding New GPU Profiles

### Step 1: Create YAML File

Create `pkg/gpu/mocknvml/configs/mock-nvml-config-newgpu.yaml`:

```yaml
version: "1.0"

system:
  driver_version: "560.35.03"
  nvml_version: "12.560.35.03"
  cuda_version: "12.6"
  cuda_version_major: 12
  cuda_version_minor: 6

device_defaults:
  name: "NVIDIA New GPU"
  architecture: "hopper"  # or appropriate arch
  # ... add all properties

devices:
  - index: 0
    uuid: "GPU-NEWGPU-0000"
    pci:
      bus_id: "00000000:07:00.0"
  # ... add all devices
```

### Step 2: Test the Profile

```bash
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-newgpu.yaml nvidia-smi
```

### Step 3: Verify All nvidia-smi Commands

```bash
# Basic display
nvidia-smi

# Full query
nvidia-smi -q

# XML output
nvidia-smi -x -q

# Specific queries
nvidia-smi -q -d MEMORY
nvidia-smi -q -d POWER
nvidia-smi -q -d TEMPERATURE
```

## Code Style

### Go Style

Follow standard Go conventions:

```bash
# Format code
gofmt -w .

# Run linter
golangci-lint run ./pkg/gpu/mocknvml/...
```

### Documentation

- Public functions must have doc comments
- Comments should be ≤80 characters per line
- Use `debugLog()` for debug output, not `fmt.Printf`

### Testing

- Every new function should have unit tests
- Test both success and error cases
- Use table-driven tests where appropriate

```go
func TestNewFunction(t *testing.T) {
    tests := []struct {
        name     string
        config   *DeviceConfig
        expected ReturnType
        wantErr  nvml.Return
    }{
        {
            name:     "with config",
            config:   &DeviceConfig{NewProperty: &NewPropertyConfig{Value: 42}},
            expected: 42,
            wantErr:  nvml.SUCCESS,
        },
        {
            name:     "without config",
            config:   nil,
            expected: 0,
            wantErr:  nvml.ERROR_NOT_SUPPORTED,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            dev := &ConfigurableDevice{config: tt.config}
            got, ret := dev.GetNewFunction()
            if ret != tt.wantErr {
                t.Errorf("GetNewFunction() ret = %v, want %v", ret, tt.wantErr)
            }
            if got != tt.expected {
                t.Errorf("GetNewFunction() = %v, want %v", got, tt.expected)
            }
        })
    }
}
```

## Debugging

### Enable Debug Logging

```bash
MOCK_NVML_DEBUG=1 LD_LIBRARY_PATH=. nvidia-smi
```

### Debug with GDB

```bash
# Build with debug symbols
CGO_CFLAGS="-g" go build -gcflags="all=-N -l" -buildmode=c-shared ...

# Attach GDB
gdb nvidia-smi
(gdb) set environment LD_LIBRARY_PATH .
(gdb) run
```

### Debug with Delve

```bash
# Run tests with debugger
dlv test ./pkg/gpu/mocknvml/engine/...
```

## Regenerating Stubs

The stub generator creates `stubs_generated.go` with stub implementations for
NVML functions that don't have hand-written implementations:

```bash
# From bridge directory (uses go:generate directive)
cd pkg/gpu/mocknvml/bridge
go generate

# Or from repo root
go generate ./pkg/gpu/mocknvml/bridge/...

# Or run generator directly
go run ./cmd/generate-bridge \
  -input vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go \
  -bridge pkg/gpu/mocknvml/bridge \
  -output pkg/gpu/mocknvml/bridge/stubs_generated.go
```

The generator:
1. Parses all NVML function names from `nvml.go` (~396 functions)
2. Scans `bridge/*.go` for existing `//export` directives
3. Generates stubs only for functions NOT already implemented
4. Outputs to `stubs_generated.go`

When you add a new implementation to a bridge file, regenerate stubs to
automatically remove the corresponding stub.

## Release Checklist

1. [ ] All tests pass: `go test -race ./...`
2. [ ] Linter passes: `golangci-lint run`
3. [ ] Integration test passes: `make -C tests/mocknvml test`
4. [ ] Documentation updated
5. [ ] YAML configs updated if needed
6. [ ] README updated with new features
7. [ ] Version bumped if applicable

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make changes with tests
4. Run linter and tests
5. Submit pull request

### Commit Message Format

```
type(scope): description

- detail 1
- detail 2

Co-authored-by: Name <email>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`
