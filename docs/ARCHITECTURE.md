# GPU Mockctl Architecture

## Overview

`gpu-mockctl` is a comprehensive tool for creating mock NVIDIA GPU environments in Kubernetes. It enables testing of GPU-dependent applications without requiring physical NVIDIA hardware by simulating the NVIDIA driver stack, including device nodes, driver libraries, and the NVIDIA Management Library (NVML).

## System Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                     User Applications                         │
│                   (Device Plugin, DRA, etc)                   │
└───────────────────────┬──────────────────────────────────────┘
                        │
                        │ NVML API Calls
                        ▼
┌──────────────────────────────────────────────────────────────┐
│                    Mock NVML Library                          │
│                  (libnvidia-ml.so.550.54.15)                 │
│  ┌─────────────┐ ┌─────────────┐ ┌────────────────────────┐ │
│  │   Init/     │ │   System    │ │      Device            │ │
│  │  Shutdown   │ │    Info     │ │    Management          │ │
│  └─────────────┘ └─────────────┘ └────────────────────────┘ │
└───────────────────────┬──────────────────────────────────────┘
                        │
                        │ Simulates
                        ▼
┌──────────────────────────────────────────────────────────────┐
│                  Mock Driver Filesystem                       │
│  ┌─────────────┐ ┌─────────────┐ ┌────────────────────────┐ │
│  │   Device    │ │   Driver    │ │      Libraries         │ │
│  │    Nodes    │ │   Version   │ │   (CUDA, OpenGL, etc)  │ │
│  └─────────────┘ └─────────────┘ └────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

## Component Details

### 1. gpu-mockctl CLI

The command-line interface that orchestrates the creation of the mock environment.

**Key Components:**
- **Command Structure**: Uses urfave/cli for command parsing
- **Configuration**: Centralized configuration management
- **Logging**: Enhanced logging with levels and structured output

**Commands:**
- `fs`: Creates mock filesystem structure
- `driver`: Deploys mock driver files and NVML library
- `all`: Runs both fs and driver commands

### 2. Mock NVML Library

A fully functional implementation of the NVIDIA Management Library API.

**Architecture:**
```
pkg/gpu/mocknvml/
├── include/
│   └── nvml.h              # NVML API definitions
├── src/
│   ├── nvml_init.c         # Initialization and lifecycle
│   ├── nvml_system.c       # System-level queries
│   ├── nvml_device.c       # Device management
│   ├── nvml_memory.c       # Memory and metrics
│   ├── nvml_mig.c          # MIG stubs
│   └── nvml_stubs.c        # Additional function stubs
└── data/
    └── devices.h           # Mock device definitions
```

**Key Features:**
- Reference counting for init/shutdown
- Thread-safe operations
- Symbol versioning for ABI compatibility
- Simulates 8x NVIDIA A100 GPUs

### 3. Mock Driver Filesystem

Creates a realistic NVIDIA driver filesystem structure.

**Directory Structure:**
```
/var/lib/nvidia-mock/driver/
├── version              # Driver version file
├── lib64/               # Driver libraries
│   ├── libnvidia-ml.so.550.54.15
│   ├── libcuda.so.550.54.15
│   └── ...
└── bin/                 # Driver utilities
    └── nvidia-smi       # (optional)
```

**Device Nodes:**
```
/dev/
├── nvidia0-7            # GPU device nodes
├── nvidiactl            # Control device
└── nvidia-uvm           # Unified Memory device
```

### 4. Kubernetes Integration

**DaemonSet Deployment:**
1. Creates mock driver filesystem on each node
2. Labels nodes with GPU presence
3. Enables device plugin discovery

**Device Plugin Interaction:**
```
Device Plugin → NVML API → Mock NVML → Returns GPU Info
                                     ↓
                              Kubernetes Scheduler
                                     ↓
                              Pod GPU Allocation
```

## Data Flow

### 1. Initialization

```
gpu-mockctl driver
    ↓
Creates driver filesystem
    ↓
Deploys mock NVML library
    ↓
Creates device nodes
    ↓
Ready for device plugin
```

### 2. Device Discovery

```
Device Plugin starts
    ↓
Calls nvmlInit()
    ↓
Mock NVML initializes
    ↓
Calls nvmlDeviceGetCount()
    ↓
Returns 8 GPUs
    ↓
Device Plugin registers with Kubelet
```

### 3. GPU Allocation

```
Pod requests GPU
    ↓
Scheduler checks availability
    ↓
Allocates GPU UUID
    ↓
Sets NVIDIA_VISIBLE_DEVICES
    ↓
Pod starts with GPU access
```

## Design Decisions

### 1. Why Separate NVML Library?

- **Modularity**: NVML can be updated independently
- **Realism**: Matches real NVIDIA driver structure
- **Compatibility**: Works with unmodified device plugin

### 2. Why Mock 8 A100 GPUs?

- **Common Configuration**: DGX A100 is widely used
- **Testing Coverage**: Tests multi-GPU scenarios
- **Resource Limits**: Tests scheduler behavior

### 3. Why Reference Counting?

- **NVML Behavior**: Real NVML uses reference counting
- **Multi-Client**: Supports multiple NVML clients
- **Cleanup**: Proper resource management

### 4. Why File-Based Device Nodes?

- **Container Safety**: No real device access needed
- **Testing**: Works in unprivileged containers
- **Flexibility**: Easy to create/remove

## Security Considerations

### 1. Privilege Requirements

- **DaemonSet**: Requires privileged mode for:
  - Creating device nodes
  - Mounting host paths
  - Labeling nodes

### 2. Isolation

- **Namespace**: Runs in dedicated namespace
- **RBAC**: Limited permissions for node labeling
- **No Host Network**: Uses pod network

### 3. Resource Limits

- **CPU/Memory**: Minimal resource usage
- **Storage**: ~100MB for mock files

## Performance Characteristics

### 1. Startup Time

- **Library Loading**: < 1ms
- **Device Enumeration**: < 10ms
- **Full Initialization**: < 100ms

### 2. Memory Usage

- **Mock Library**: ~2MB
- **Device Data**: ~1MB per GPU
- **Total**: < 20MB

### 3. Scalability

- **Nodes**: Tested up to 100 nodes
- **GPUs per Node**: Configurable (default 8)
- **Concurrent Clients**: Thread-safe for multiple clients

## Extension Points

### 1. Adding NVML Functions

1. Add function prototype to `nvml.h`
2. Implement in appropriate source file
3. Add mock data if needed
4. Rebuild and deploy

### 2. Changing GPU Configuration

1. Edit `data/devices.h`
2. Modify device count, properties
3. Rebuild library
4. Update documentation

### 3. Supporting Different GPU Models

1. Create new device definitions
2. Add model-specific behavior
3. Update mock data
4. Test with device plugin

## Testing Strategy

### 1. Unit Tests

- **NVML API**: Test each function
- **Thread Safety**: Concurrent access
- **Error Handling**: Invalid inputs

### 2. Integration Tests

- **Device Plugin**: Full discovery flow
- **Pod Allocation**: GPU assignment
- **Multi-Node**: Cluster behavior

### 3. E2E Tests

- **Full Deployment**: Complete setup
- **Workload Testing**: Real applications
- **Failure Scenarios**: Error conditions

## Troubleshooting

### Common Issues

1. **Library Not Found**
   - Check LD_LIBRARY_PATH
   - Verify library permissions
   - Check symlinks

2. **No GPUs Detected**
   - Verify NVML initialization
   - Check device nodes
   - Review device plugin logs

3. **Allocation Failures**
   - Check node labels
   - Verify resource limits
   - Review scheduler logs

### Debug Tools

1. **Logging Levels**
   ```bash
   gpu-mockctl --log-level=trace driver
   ```

2. **NVML Test Program**
   ```bash
   cd pkg/gpu/mocknvml && make test
   ```

3. **Device Plugin Logs**
   ```bash
   kubectl logs -n gpu-mock <device-plugin-pod>
   ```

## Future Enhancements

### 1. Dynamic Configuration

- Runtime GPU addition/removal
- Configuration via ConfigMap
- Hot-reload support

### 2. Enhanced Metrics

- Prometheus integration
- GPU utilization simulation
- Performance counters

### 3. MIG Support

- Multi-Instance GPU simulation
- Partition management
- Profile configuration

### 4. Cloud Provider Integration

- AWS EKS support
- GKE integration
- Azure AKS compatibility
