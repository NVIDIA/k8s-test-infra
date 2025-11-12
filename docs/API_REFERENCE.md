# GPU Mockctl API Reference

## Command Line Interface

### Global Options

```bash
gpu-mockctl [global options] command [command options] [arguments...]
```

#### Global Flags

| Flag | Type | Default | Description | Environment Variable |
|------|------|---------|-------------|---------------------|
| `--verbose, -v` | bool | false | Enable verbose logging (equivalent to --log-level=debug) | |
| `--log-level` | string | info | Set log level (trace, debug, info, warn, error) | `GPU_MOCKCTL_LOG_LEVEL` |
| `--log-format` | string | text | Set log format (text, json) | `GPU_MOCKCTL_LOG_FORMAT` |
| `--machine` | string | dgxa100 | Machine type to simulate | |
| `--help, -h` | bool | false | Show help | |
| `--version` | bool | false | Print version information | |

### Commands

#### `gpu-mockctl fs`

Creates mock GPU filesystem structure.

```bash
gpu-mockctl fs [options]
```

**Options:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--base-dir` | string | /var/lib/gpu-mockfs | Base directory for mock filesystem |
| `--driver-version` | string | 550.54.15 | NVIDIA driver version |
| `--cuda-version` | string | 12.4 | CUDA version |

**Example:**

```bash
# Create filesystem with default settings
gpu-mockctl fs

# Create with custom base directory
gpu-mockctl fs --base-dir /tmp/mock-gpu

# Enable debug logging
gpu-mockctl --log-level=debug fs
```

#### `gpu-mockctl driver`

Deploys mock NVIDIA driver files including libraries and device nodes.

```bash
gpu-mockctl driver [options]
```

**Options:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--driver-root` | string | /var/lib/nvidia-mock/driver | Root directory for driver files |
| `--with-compiled-nvml` | bool | false | Include compiled mock NVML library |

**Example:**

```bash
# Deploy driver with compiled NVML
gpu-mockctl driver --with-compiled-nvml

# Deploy to custom location
gpu-mockctl driver --driver-root /mnt/mock-driver

# Deploy with trace logging
gpu-mockctl --log-level=trace driver --with-compiled-nvml
```

#### `gpu-mockctl all`

Runs both filesystem and driver setup.

```bash
gpu-mockctl all [options]
```

**Options:**

Combines options from both `fs` and `driver` commands.

**Example:**

```bash
# Complete setup with defaults
gpu-mockctl all

# Complete setup with compiled NVML
gpu-mockctl all --with-compiled-nvml
```

#### `gpu-mockctl version`

Displays version information.

```bash
gpu-mockctl version
```

**Output Example:**

```
gpu-mockctl version 0.1.0
Git Commit: abc123def
Built: 2025-01-15T10:00:00Z
```

## Mock NVML Library API

The mock NVML library implements the NVIDIA Management Library API. Below are the key functions:

### Initialization Functions

#### `nvmlInit_v2()`

Initializes NVML. Uses reference counting - multiple calls increment counter.

```c
nvmlReturn_t nvmlInit_v2(void);
```

**Returns:**
- `NVML_SUCCESS` - Initialization successful
- `NVML_ERROR_DRIVER_NOT_LOADED` - Driver not loaded (never returned by mock)

#### `nvmlShutdown()`

Decrements reference count. Only fully shuts down when count reaches zero.

```c
nvmlReturn_t nvmlShutdown(void);
```

**Returns:**
- `NVML_SUCCESS` - Shutdown successful
- `NVML_ERROR_UNINITIALIZED` - NVML not initialized

### System Information Functions

#### `nvmlSystemGetDriverVersion()`

Returns mock driver version.

```c
nvmlReturn_t nvmlSystemGetDriverVersion(char *version, unsigned int length);
```

**Parameters:**
- `version` - Buffer to store version string
- `length` - Buffer size

**Returns:**
- `NVML_SUCCESS` - Success
- `NVML_ERROR_INSUFFICIENT_SIZE` - Buffer too small
- `NVML_ERROR_INVALID_ARGUMENT` - NULL pointer

**Mock Value:** "550.54.15"

#### `nvmlSystemGetNVMLVersion()`

Returns NVML library version.

```c
nvmlReturn_t nvmlSystemGetNVMLVersion(char *version, unsigned int length);
```

**Mock Value:** "12.550.54"

### Device Enumeration Functions

#### `nvmlDeviceGetCount_v2()`

Returns number of GPU devices.

```c
nvmlReturn_t nvmlDeviceGetCount_v2(unsigned int *deviceCount);
```

**Mock Value:** 8 devices

#### `nvmlDeviceGetHandleByIndex_v2()`

Gets device handle by index.

```c
nvmlReturn_t nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t *device);
```

**Parameters:**
- `index` - Device index (0-7)
- `device` - Pointer to store device handle

### Device Property Functions

#### `nvmlDeviceGetName()`

Returns GPU product name.

```c
nvmlReturn_t nvmlDeviceGetName(nvmlDevice_t device, char *name, unsigned int length);
```

**Mock Value:** "NVIDIA A100-SXM4-40GB"

#### `nvmlDeviceGetUUID()`

Returns globally unique GPU identifier.

```c
nvmlReturn_t nvmlDeviceGetUUID(nvmlDevice_t device, char *uuid, unsigned int length);
```

**Mock Values:**
- Device 0: "GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c"
- Device 1: "GPU-7e8ad30b-b5d9-cd98-3fcf-9b3e4d2ba6a0"
- ... (unique for each device)

#### `nvmlDeviceGetMemoryInfo()`

Returns GPU memory information.

```c
nvmlReturn_t nvmlDeviceGetMemoryInfo(nvmlDevice_t device, nvmlMemory_t *memory);
```

**Mock Values:**
- Total: 42949672960 bytes (40 GB)
- Free: 42949672960 bytes
- Used: 0 bytes

### Advanced Functions

#### `nvmlDeviceGetPciInfo_v3()`

Returns PCI information.

```c
nvmlReturn_t nvmlDeviceGetPciInfo_v3(nvmlDevice_t device, nvmlPciInfo_t *pci);
```

**Mock Values Example (Device 0):**
- Domain: 0x0000
- Bus: 0x07
- Device: 0x00
- PCI ID: 0000:07:00.0

#### `nvmlDeviceGetCudaComputeCapability()`

Returns CUDA compute capability.

```c
nvmlReturn_t nvmlDeviceGetCudaComputeCapability(nvmlDevice_t device, int *major, int *minor);
```

**Mock Values:**
- Major: 8
- Minor: 0

## Error Codes

| Code | Name | Description |
|------|------|-------------|
| 0 | `NVML_SUCCESS` | Operation successful |
| 1 | `NVML_ERROR_UNINITIALIZED` | NVML not initialized |
| 2 | `NVML_ERROR_INVALID_ARGUMENT` | Invalid argument |
| 3 | `NVML_ERROR_NOT_SUPPORTED` | Not supported |
| 4 | `NVML_ERROR_NO_PERMISSION` | Insufficient permissions |
| 7 | `NVML_ERROR_INSUFFICIENT_SIZE` | Buffer too small |
| 999 | `NVML_ERROR_UNKNOWN` | Unknown error |

## Configuration Files

### gpu-mockctl Configuration

Configuration can be provided via environment variables:

```bash
# Set log level
export GPU_MOCKCTL_LOG_LEVEL=debug

# Set log format
export GPU_MOCKCTL_LOG_FORMAT=json

# Run with environment configuration
gpu-mockctl driver --with-compiled-nvml
```

### Mock Device Configuration

Mock devices are defined in `pkg/gpu/mocknvml/data/devices.h`:

```c
typedef struct {
    char uuid[NVML_DEVICE_UUID_V2_BUFFER_SIZE];
    char name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
    unsigned int pci_domain;
    unsigned int pci_bus;
    unsigned int pci_device;
    // ... additional fields
} mock_device_info_t;

static const mock_device_info_t mock_devices[8] = {
    {
        .uuid = "GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c",
        .name = "NVIDIA A100-SXM4-40GB",
        // ... configuration
    },
    // ... 7 more devices
};
```

## Logging

### Log Levels

1. **TRACE**: Very detailed debugging information
2. **DEBUG**: Debugging information
3. **INFO**: General information (default)
4. **WARN**: Warning messages
5. **ERROR**: Error messages
6. **FATAL**: Fatal errors (causes exit)

### Log Formats

#### Text Format (Default)

```
2025-01-15T10:00:00.000Z [gpu-mockctl] INFO: Deploying mock driver files to: /var/lib/nvidia-mock/driver
```

#### JSON Format

```json
{
  "timestamp": "2025-01-15T10:00:00.000Z",
  "level": "INFO",
  "prefix": "gpu-mockctl",
  "message": "Deploying mock driver files to: /var/lib/nvidia-mock/driver",
  "count": 45
}
```

### Structured Logging

Use structured fields for better log analysis:

```go
log.WithField("path", "/dev/nvidia0").Debug("Creating device node")

log.WithFields(map[string]interface{}{
    "files": 45,
    "duration": "2.3s",
}).Info("Deployment complete")
```

## Integration Examples

### Kubernetes DaemonSet

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: nvidia-mock-driver
spec:
  template:
    spec:
      containers:
      - name: setup
        image: local/gpu-mockctl:dev
        command: ["/usr/local/bin/gpu-mockctl"]
        args:
          - "driver"
          - "--driver-root"
          - "/host/var/lib/nvidia-mock/driver"
          - "--with-compiled-nvml"
          - "--log-level=info"
        env:
        - name: GPU_MOCKCTL_LOG_FORMAT
          value: "json"
```

### Docker Container

```dockerfile
FROM local/gpu-mockctl:dev

# Run with debug logging
ENV GPU_MOCKCTL_LOG_LEVEL=debug

# Deploy mock driver
RUN gpu-mockctl driver --with-compiled-nvml

# Verify deployment
RUN gpu-mockctl --log-format=json version
```

### Shell Script

```bash
#!/bin/bash

# Enable trace logging for debugging
export GPU_MOCKCTL_LOG_LEVEL=trace

# Deploy mock environment
gpu-mockctl all --with-compiled-nvml

# Check deployment
if [ $? -eq 0 ]; then
    echo "Mock environment deployed successfully"
else
    echo "Deployment failed"
    exit 1
fi
```

## Best Practices

### 1. Logging

- Use appropriate log levels
- Enable debug logging for troubleshooting
- Use JSON format for log aggregation
- Include structured fields for context

### 2. Error Handling

- Check return codes
- Log errors with context
- Use appropriate error types
- Include file paths in errors

### 3. Deployment

- Always use `--with-compiled-nvml` for device plugin
- Set appropriate permissions on directories
- Verify device nodes after creation
- Monitor logs during deployment

### 4. Testing

- Test with different log levels
- Verify all NVML functions
- Test error conditions
- Validate with real device plugin
