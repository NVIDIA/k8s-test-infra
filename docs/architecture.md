# Architecture

The main idea of Mokka is to simulate very low-level system contracts, 
so that higher layer components work meaningfully without any modifications.

This is achieved via simulation of device status, PCI trees, driver footprints for GPU and networking.
The lower level we operate, the better here as we expand the number of real workflows that are executed (vs. being completely disabled or mocked away).

![Mokka General Architecture](./img/mokka-general-architecture.png)

Higher level applications are our consumers:
- platform components such as [nvidia-smi](https://docs.nvidia.com/deploy/nvidia-smi/index.html), [K8s DRA driver](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu), [NFD](https://github.com/kubernetes-sigs/node-feature-discovery), [GPU Operator](https://github.com/nvidia/gpu-operator), [Network Operator](https://github.com/Mellanox/network-operator), [Topograph](https://github.com/NVIDIA/topograph), etc.
- applications like [Slurm](https://github.com/SlinkyProject/slurm-operator), [NVSentinel](https://github.com/nvidia/nvsentinel), etc.

We don't try to mock a specific higher layer component or use case, but rather focus on the simulation of contracts between lower and higher layers.
This should help to support a wide range of higher layer applications that we don't know or have access to (for example, neocloud's proprietary AI infrastructure services).

Mokka provides a mock driver that looks like the real one to management tooling.
Same libraries and file footprints, no real kernel module, no GPU data path.
That is why nvidia-smi can run unmodified, and why module surfaces such as what `lsmod` reads belong in the same story.
GPU identity and behavior come from config profiles (different chips, counts, topology), with tools to change health and fault state at runtime.

## Contract surfaces

Mocking a single interface (for example NVML alone) can prove a GPU can be *allocated*.
Many K8s control-plane consumers read a broader evidence surface than allocation alone: NVML / nvidia-smi, PCI and sysfs trees, and kernel or driver footprints under `/proc` and `/sys` (including module state such as `lsmod`).

## Independent failure and attribution

Layers form a dependency stack, but each layer must be able to fail on its own while others stay healthy.
Which layer broke decides the owner, the remediation path, and the urgency.

For the mock, that means each simulated surface has to be controllable independently.
A GPU can show up in the PCI tree with no driver footprint; that is a normal failure mode, and the mock needs to reproduce it.
If surfaces can only be turned on together, the layers collapse into one failure and attribution cannot be tested.

## Delivery

Simulated file surfaces also have to be visible at the paths consumers already use.
Pointing a consumer at a substitute path with a flag only works when that flag exists, and it no longer tests that the consumer reads the real path.

`LD_PRELOAD` can rewrite libc calls for C tools such as `lspci` and `ibv_devinfo`.
It does not work for Go binaries: they make syscalls directly and never go through the preloaded library.
Most of the Kubernetes control plane is Go, so file surfaces need to be mounted into the container instead of intercepted.

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
│    │  │  - 400 C function exports (//export directives)                 │ │   │
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
│    │  │  - 89 NVML method implementations                               │ │   │
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

**File**: `engine/engine.go` (~400 lines)

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

**File**: `engine/handles.go` (~170 lines)

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

**Files**: `engine/config.go` (~350 lines), `engine/config_types.go` (418 lines)

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

**File**: `engine/device.go` (~1290 lines)

Implements 89 NVML methods by reading from YAML configuration.

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
│   ├── helpers.go             # Helper functions + main() + go:generate
│   ├── init.go                # nvmlInit_v2, nvmlShutdown, etc.
│   ├── device.go              # Device handle functions
│   ├── events.go              # Event set/wait functions
│   ├── system.go              # System functions
│   ├── internal.go            # Internal export table (nvidia-smi)
│   ├── nvml_types.h           # C type definitions for CGo preamble
│   └── stubs_generated.go     # Auto-generated stubs (~289 functions)
├── engine/
│   ├── config.go              # Config loading
│   ├── config_types.go        # YAML structs
│   ├── device.go              # ConfigurableDevice
│   ├── engine.go              # Singleton engine
│   ├── handles.go             # Handle table
│   ├── invalid_device.go      # Invalid device handle sentinel
│   ├── utils.go               # Debug logging
│   ├── version.go             # NVML version responses
│   └── *_test.go              # Unit tests
├── configs/
│   ├── mock-nvml-config-a100.yaml
│   ├── mock-nvml-config-b200.yaml
│   ├── mock-nvml-config-gb200.yaml
│   ├── mock-nvml-config-gb300.yaml
│   ├── mock-nvml-config-h100.yaml
│   ├── mock-nvml-config-l40s.yaml
│   └── mock-nvml-config-t4.yaml
├── Dockerfile
├── Makefile
└── README.md

cmd/generate-bridge/
├── main.go                    # Stub generator (--stats, --validate flags)
├── parser.go                  # nvml.h prototype parser
└── main_test.go               # Generator tests
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
