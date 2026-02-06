# Architecture Guide

Deep dive into Mock NVML's design and implementation.

## System Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              USER SPACE                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│    ┌──────────────────┐         ┌─────────────────────────────────────┐     │
│    │   nvidia-smi     │         │   Your Application                  │     │
│    │   (real binary)  │         │   (k8s-device-plugin, dcgm, etc)    │     │
│    └────────┬─────────┘         └──────────────┬──────────────────────┘     │
│             │                                   │                            │
│             │ dlopen("libnvidia-ml.so")         │                            │
│             ▼                                   ▼                            │
│    ┌────────────────────────────────────────────────────────────────────┐   │
│    │                     libnvidia-ml.so (MOCK)                          │   │
│    │                                                                      │   │
│    │  ┌────────────────────────────────────────────────────────────────┐ │   │
│    │  │                   CGo Bridge Layer                              │ │   │
│    │  │  - 396 C function exports (//export directives)                 │ │   │
│    │  │  - C struct definitions (nvmlPciInfo_t, nvmlMemory_t, etc)      │ │   │
│    │  │  - Type conversions (C ↔ Go)                                    │ │   │
│    │  └────────────────────────────────────────────────────────────────┘ │   │
│    │                               │                                      │   │
│    │  ┌────────────────────────────▼───────────────────────────────────┐ │   │
│    │  │                     Engine Layer                                │ │   │
│    │  │  - Singleton lifecycle management                               │ │   │
│    │  │  - Configuration loading (YAML or env vars)                     │ │   │
│    │  │  - Handle table (C pointer ↔ Go object mapping)                 │ │   │
│    │  └────────────────────────────────────────────────────────────────┘ │   │
│    │                               │                                      │   │
│    │  ┌────────────────────────────▼───────────────────────────────────┐ │   │
│    │  │                  ConfigurableDevice                             │ │   │
│    │  │  - 50+ NVML method implementations                              │ │   │
│    │  │  - YAML-driven property values                                  │ │   │
│    │  │  - Wraps dgxa100.Device (go-nvml mock)                          │ │   │
│    │  └────────────────────────────────────────────────────────────────┘ │   │
│    └────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Component Details

### 1. CGo Bridge Layer

**Directory**: `bridge/` (multiple files with IDE support)

The bridge exposes NVML functions as C symbols that applications can dynamically load.
The bridge is organized into hand-written implementation files plus auto-generated stubs:

| File | Purpose |
|------|---------|
| `cgo_types.go` | Shared CGo type definitions (C structs, constants) |
| `helpers.go` | Helper functions (`toReturn`, `goStringToC`, `stubReturn`) + `main()` |
| `init.go` | Initialization: `nvmlInit_v2`, `nvmlShutdown`, etc. |
| `device.go` | Device handles: `nvmlDeviceGetCount`, `GetHandleByIndex`, `GetName`, etc. |
| `system.go` | System functions: `nvmlSystemGetDriverVersion`, `GetCudaDriverVersion`, etc. |
| `internal.go` | Internal export table for nvidia-smi compatibility |
| `stubs_generated.go` | Auto-generated stubs for unimplemented functions |

```go
//export nvmlDeviceGetTemperature
func nvmlDeviceGetTemperature(device C.nvmlDevice_t, sensorType C.nvmlTemperatureSensors_t,
                               temp *C.uint) C.nvmlReturn_t {
    // 1. Look up Go device from C handle
    dev := engine.GetEngine().LookupConfigurableDevice(uintptr(device))
    if dev == nil {
        return C.NVML_ERROR_INVALID_ARGUMENT
    }

    // 2. Call Go implementation
    temperature, ret := dev.GetTemperature(nvml.TemperatureSensors(sensorType))

    // 3. Convert result to C types
    *temp = C.uint(temperature)
    return toReturn(ret)
}
```

**C Type Definitions** (CGo preamble):

```c
typedef struct nvmlPciInfo_st {
    char busIdLegacy[16];
    unsigned int domain;
    unsigned int bus;
    unsigned int device;
    unsigned int pciDeviceId;
    unsigned int pciSubSystemId;
    char busId[32];
} nvmlPciInfo_t;

typedef struct nvmlMemory_st {
    unsigned long long total;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_t;
```

### 2. Engine Layer

**File**: `engine/engine.go` (388 lines)

The Engine is the central coordinator, managing:

- **Lifecycle**: Init/Shutdown reference counting
- **Configuration**: Loading from YAML or environment
- **Handle mapping**: Translating C pointers to Go objects

```go
type Engine struct {
    server    *MockServer      // Device provider
    config    *Config          // Loaded configuration
    handles   *HandleTable     // C↔Go handle mapping
    initCount int              // Reference count
    mu        sync.RWMutex     // Thread safety
}
```

**Singleton Pattern**:

```go
var (
    engineInstance *Engine
    engineOnce     sync.Once
)

func GetEngine() *Engine {
    engineOnce.Do(func() {
        engineInstance = NewEngine(nil)
    })
    return engineInstance
}
```

### 3. Handle Table

**File**: `engine/handles.go` (126 lines)

**Problem**: CGo doesn't allow passing Go pointers with nested Go pointers to C code. When nvidia-smi receives a device handle, it expects to dereference it.

**Solution**: Allocate real C memory blocks that nvidia-smi can safely access.

```c
// C structure that nvidia-smi can dereference
typedef struct {
    unsigned int magic;      // 0x4E564D4C ("NVML")
    unsigned int index;      // Device index
    void* reserved[4];       // Space nvidia-smi might read
} HandleBlock;
```

```go
func (ht *HandleTable) Register(dev nvml.Device) uintptr {
    // Allocate C memory block
    cHandle := C.allocHandle(C.uint(deviceIndex))
    handle := uintptr(unsafe.Pointer(cHandle))
    
    // Store bidirectional mapping
    ht.devices[handle] = dev
    ht.reverse[dev] = handle
    
    return handle
}
```

### 4. Configuration System

**Files**: `engine/config.go` (243 lines), `engine/config_types.go` (418 lines)

#### Configuration Hierarchy

```yaml
YAMLConfig:
  ├── SystemConfig          # Driver version, CUDA version
  ├── DeviceDefaults        # Default properties for all devices
  └── Devices[]             # Per-device overrides
        ├── index: 0
        │   └── (overrides)
        ├── index: 1
        │   └── (overrides)
        └── ...
```

#### Merge Algorithm

```go
func (c *Config) GetDeviceConfig(index int) *DeviceConfig {
    // Start with defaults
    merged := c.YAMLConfig.DeviceDefaults
    
    // Apply per-device overrides
    for _, override := range c.YAMLConfig.Devices {
        if override.Index == index {
            mergeDeviceOverride(&merged, &override)
            break
        }
    }
    
    return &merged
}
```

### 5. ConfigurableDevice

**File**: `engine/device.go` (1008 lines)

Implements 50+ NVML methods by reading from YAML configuration.

```go
type ConfigurableDevice struct {
    *dgxa100.Device           // Base device (embedded)
    config      *DeviceConfig // YAML configuration
    index       int
    minorNumber int
    bar1Memory  nvml.BAR1Memory  // Cached
    pciInfo     nvml.PciInfo     // Cached
}
```

**Method Implementation Pattern**:

```go
func (d *ConfigurableDevice) GetTemperature(sensor nvml.TemperatureSensors) (uint32, nvml.Return) {
    // Check if config provides value
    if d.config != nil && d.config.Thermal != nil {
        return uint32(d.config.Thermal.TemperatureGPU_C), nvml.SUCCESS
    }
    // No config = not supported
    return 0, nvml.ERROR_NOT_SUPPORTED
}
```

## Data Flow

### Initialization Sequence

```
nvidia-smi                Engine                    Config
    │                        │                         │
    │  nvmlInit_v2()         │                         │
    │───────────────────────►│                         │
    │                        │  LoadConfig()           │
    │                        │────────────────────────►│
    │                        │                         │
    │                        │     ┌───────────────────┤
    │                        │     │ YAML exists?      │
    │                        │     └─────────┬─────────┘
    │                        │               │
    │                        │     YES: Parse YAML
    │                        │     NO: Use env vars
    │                        │               │
    │                        │◄──────────────┘
    │                        │
    │                        │  createServer()
    │                        │  - Create dgxa100.Server
    │                        │  - Create ConfigurableDevices
    │                        │  - Apply system config
    │                        │
    │◄───────────────────────│  NVML_SUCCESS
```

### Query Flow

```
nvidia-smi                Bridge              Engine           Device
    │                        │                   │                │
    │ GetTemperature(dev,0,&t)                   │                │
    │───────────────────────►│                   │                │
    │                        │ LookupDevice(dev) │                │
    │                        │──────────────────►│                │
    │                        │                   │ Lookup(handle) │
    │                        │◄──────────────────│                │
    │                        │                   │                │
    │                        │ GetTemperature(0) │                │
    │                        │───────────────────┼───────────────►│
    │                        │                   │                │
    │                        │                   │  config.Thermal│
    │                        │                   │  .TempGPU_C    │
    │                        │◄──────────────────┼────────────────│
    │                        │                   │    33, SUCCESS │
    │◄───────────────────────│                   │                │
    │        temp=33         │                   │                │
```

## Design Patterns

| Pattern | Component | Purpose |
|---------|-----------|---------|
| **Singleton** | Engine | Single lifecycle manager |
| **Decorator** | ConfigurableDevice wraps dgxa100.Device | Extend without modifying |
| **Strategy** | createDevicesFromYAML vs createDefaultDevices | Runtime behavior selection |
| **Handle Table** | HandleTable | Safe C↔Go pointer translation |
| **Config Merge** | mergeDeviceOverride | Defaults + overrides |

## File Structure

```
pkg/gpu/mocknvml/
├── bridge/
│   ├── cgo_types.go           # Shared CGo type definitions
│   ├── helpers.go             # Helper functions + main()
│   ├── init.go                # nvmlInit_v2, nvmlShutdown, etc.
│   ├── device.go              # Device handle functions
│   ├── system.go              # System functions
│   ├── internal.go            # Internal export table (nvidia-smi)
│   └── stubs_generated.go     # Auto-generated stubs (~375 functions)
├── engine/
│   ├── config.go              # Config loading
│   ├── config_types.go        # YAML structs
│   ├── device.go              # ConfigurableDevice
│   ├── engine.go              # Singleton engine
│   ├── handles.go             # Handle table
│   └── utils.go               # Debug logging
├── configs/
│   ├── mock-nvml-config-a100.yaml
│   └── mock-nvml-config-gb200.yaml
├── Dockerfile
├── Makefile
└── README.md

cmd/generate-bridge/
└── main.go                    # Stub generator
```

## Thread Safety

All public Engine methods are protected by `sync.RWMutex`:

- **Read operations** (`DeviceGetCount`, `LookupDevice`): Use `RLock`
- **Write operations** (`Init`, `Shutdown`, `DeviceGetHandleByIndex`): Use `Lock`

The HandleTable also has its own mutex for independent locking.

## Memory Management

### C Memory (Handles)

- Allocated via `calloc()` in CGo
- Freed on `Engine.Shutdown()` via `HandleTable.Clear()`
- Each handle is ~40 bytes

### Error String Cache

- C strings for `nvmlErrorString` are cached permanently
- Matches real NVML behavior (static strings)
- Prevents memory leaks from repeated allocations

## Extending the Library

See [Development Guide](development.md) for:

- Adding new NVML function implementations
- Creating custom GPU profiles
- Regenerating the bridge code
