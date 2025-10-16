# NVML Symbols Analysis for Mock libnvidia-ml.so

## Overview

This document contains the analysis of NVML symbols required for implementing a mock libnvidia-ml.so library that supports NVIDIA Device Plugin and DRA Driver operations.

## Core NVML Functions Required

### 1. Initialization and Cleanup

```c
nvmlReturn_t nvmlInit_v2(void);                    // Initialize NVML library
nvmlReturn_t nvmlInitWithFlags(unsigned int flags); // Initialize with flags (e.g., NVML_INIT_FLAG_NO_GPUS)
nvmlReturn_t nvmlShutdown(void);                   // Cleanup NVML
const char* nvmlErrorString(nvmlReturn_t result);  // Convert error codes to strings
```

### 2. System-Level Queries

```c
nvmlReturn_t nvmlSystemGetDriverVersion(char *version, unsigned int length);     // Get driver version (e.g., "550.54.15")
nvmlReturn_t nvmlSystemGetNVMLVersion(char *version, unsigned int length);       // Get NVML version (e.g., "12.550.54")
nvmlReturn_t nvmlSystemGetCudaDriverVersion(int *cudaDriverVersion);             // Get CUDA driver version
nvmlReturn_t nvmlSystemGetCudaDriverVersion_v2(int *cudaDriverVersion);
nvmlReturn_t nvmlSystemGetProcessName(unsigned int pid, char *name, unsigned int length); // Get process name by PID
```

### 3. Device Enumeration

```c
nvmlReturn_t nvmlDeviceGetCount_v2(unsigned int *deviceCount);                          // Get total GPU count (8 for dgxa100)
nvmlReturn_t nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t *device);   // Get device handle by index (0-7)
nvmlReturn_t nvmlDeviceGetHandleByUUID(const char *uuid, nvmlDevice_t *device);         // Get device handle by UUID string
nvmlReturn_t nvmlDeviceGetHandleByPciBusId_v2(const char *pciBusId, nvmlDevice_t *device); // Get device handle by PCI bus ID
```

### 4. Device Information

```c
nvmlReturn_t nvmlDeviceGetName(nvmlDevice_t device, char *name, unsigned int length);           // Get device name ("NVIDIA A100-SXM4-40GB")
nvmlReturn_t nvmlDeviceGetUUID(nvmlDevice_t device, char *uuid, unsigned int length);           // Get device UUID
nvmlReturn_t nvmlDeviceGetPciInfo_v3(nvmlDevice_t device, nvmlPciInfo_t *pci);                // Get PCI information
nvmlReturn_t nvmlDeviceGetMinorNumber(nvmlDevice_t device, unsigned int *minorNumber);          // Get device minor number (0-7)
nvmlReturn_t nvmlDeviceGetIndex(nvmlDevice_t device, unsigned int *index);                     // Get device index
nvmlReturn_t nvmlDeviceGetSerial(nvmlDevice_t device, char *serial, unsigned int length);      // Get device serial number
nvmlReturn_t nvmlDeviceGetBrand(nvmlDevice_t device, nvmlBrandType_t *type);                  // Get device brand type
```

### 5. Memory Information

```c
nvmlReturn_t nvmlDeviceGetMemoryInfo(nvmlDevice_t device, nvmlMemory_t *memory);        // Get memory usage (total/free/used)
nvmlReturn_t nvmlDeviceGetMemoryInfo_v2(nvmlDevice_t device, nvmlMemory_v2_t *memory);  // Extended memory info with reserved
nvmlReturn_t nvmlDeviceGetBAR1MemoryInfo(nvmlDevice_t device, nvmlBAR1Memory_t *bar1Memory); // BAR1 memory info
```

### 6. MIG (Multi-Instance GPU) Functions

```c
nvmlReturn_t nvmlDeviceGetMigMode(nvmlDevice_t device, unsigned int *currentMode, unsigned int *pendingMode); // Get MIG mode status
nvmlReturn_t nvmlDeviceGetMaxMigDeviceCount(nvmlDevice_t device, unsigned int *count);                        // Max MIG devices (0 for A100 mock)
nvmlReturn_t nvmlDeviceGetGpuInstances(nvmlDevice_t device, unsigned int profileId, nvmlGpuInstance_t *instances, unsigned int *count); // Get GPU instances (not supported)
nvmlReturn_t nvmlDeviceGetAttributes_v2(nvmlDevice_t device, nvmlDeviceAttributes_t *attributes);             // Device attributes including MIG
```

### 7. Process Information

```c
nvmlReturn_t nvmlDeviceGetComputeRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount, nvmlProcessInfo_v2_t *infos);
nvmlReturn_t nvmlDeviceGetGraphicsRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount, nvmlProcessInfo_v2_t *infos);
nvmlReturn_t nvmlDeviceGetMPSComputeRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount, nvmlProcessInfo_v2_t *infos);
```

### 8. Additional Device Properties

```c
nvmlReturn_t nvmlDeviceGetClock(nvmlDevice_t device, nvmlClockType_t clockType, nvmlClockId_t clockId, unsigned int *clockMHz);
nvmlReturn_t nvmlDeviceGetMaxClockInfo(nvmlDevice_t device, nvmlClockType_t type, unsigned int *clock);
nvmlReturn_t nvmlDeviceGetTemperature(nvmlDevice_t device, nvmlTemperatureSensors_t sensorType, unsigned int *temp);
nvmlReturn_t nvmlDeviceGetPowerUsage(nvmlDevice_t device, unsigned int *power);
nvmlReturn_t nvmlDeviceGetEnforcedPowerLimit(nvmlDevice_t device, unsigned int *limit);
nvmlReturn_t nvmlDeviceGetTotalEnergyConsumption(nvmlDevice_t device, unsigned long long *energy);
nvmlReturn_t nvmlDeviceGetPersistenceMode(nvmlDevice_t device, nvmlEnableState_t *mode);
nvmlReturn_t nvmlDeviceGetDisplayMode(nvmlDevice_t device, nvmlEnableState_t *display);
nvmlReturn_t nvmlDeviceGetDisplayActive(nvmlDevice_t device, nvmlEnableState_t *isActive);
```

## Mock Data Structure Design

Based on our dgxa100 mock topology, each device should return:

### Device Properties (for devices 0-7)
- **Name**: "NVIDIA A100-SXM4-40GB"
- **UUID**: As defined in dgxa100 mock
  - Device 0: "GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c"
  - Device 1-7: Similar format
- **PCI Bus ID**: "0000:00:00.0" through "0000:07:00.0"
- **Memory**: 42949672960 bytes (40GB)
- **Minor Number**: 0-7
- **Driver Version**: "550.54.15"
- **NVML Version**: "12.550.54"
- **CUDA Driver Version**: 12040 (12.4)
- **MIG Mode**: Not supported (return NVML_ERROR_NOT_SUPPORTED)

## Symbol Versioning

The NVML library uses versioned symbols. We need to implement both the versioned functions and their unversioned aliases:

```c
// Actual implementation
nvmlReturn_t nvmlInit_v2(void);

// Unversioned alias (unless NVML_NO_UNVERSIONED_FUNC_DEFS is defined)
#ifndef NVML_NO_UNVERSIONED_FUNC_DEFS
    #define nvmlInit nvmlInit_v2
#endif
```

## Error Codes

We need to return appropriate NVML error codes:

```c
#define NVML_SUCCESS                    0   // The operation was successful
#define NVML_ERROR_UNINITIALIZED        1   // NVML was not first initialized with nvmlInit()
#define NVML_ERROR_INVALID_ARGUMENT     2   // A supplied argument is invalid
#define NVML_ERROR_NOT_SUPPORTED        3   // The requested operation is not available on target device
#define NVML_ERROR_NO_PERMISSION        4   // The current user does not have permission
#define NVML_ERROR_ALREADY_INITIALIZED  5   // Deprecated: Multiple initializations are now allowed
#define NVML_ERROR_NOT_FOUND            6   // A query to find an object was unsuccessful
#define NVML_ERROR_INSUFFICIENT_SIZE    7   // An input argument is not large enough
#define NVML_ERROR_INSUFFICIENT_POWER   8   // A device's external power cables are not properly attached
#define NVML_ERROR_DRIVER_NOT_LOADED    9   // NVIDIA driver is not loaded
#define NVML_ERROR_TIMEOUT              10  // User provided timeout passed
#define NVML_ERROR_IRQ_ISSUE            11  // NVIDIA Kernel detected an interrupt issue with a GPU
#define NVML_ERROR_LIBRARY_NOT_FOUND    12  // NVML Shared Library couldn't be found or loaded
#define NVML_ERROR_FUNCTION_NOT_FOUND   13  // Local version of NVML doesn't implement this function
#define NVML_ERROR_CORRUPTED_INFOROM    14  // infoROM is corrupted
#define NVML_ERROR_GPU_IS_LOST          15  // The GPU has fallen off the bus or has otherwise become inaccessible
#define NVML_ERROR_RESET_REQUIRED       16  // The GPU requires a reset before it can be used again
#define NVML_ERROR_OPERATING_SYSTEM     17  // The GPU control device has been blocked by the operating system/cgroups
#define NVML_ERROR_LIB_RM_VERSION_MISMATCH 18  // RM detects a driver/library version mismatch
#define NVML_ERROR_IN_USE               19  // An operation cannot be performed because the GPU is currently in use
#define NVML_ERROR_MEMORY               20  // Insufficient memory
#define NVML_ERROR_NO_DATA              21  // No data
#define NVML_ERROR_VGPU_ECC_NOT_SUPPORTED 22  // The requested vgpu operation is not available on target device
#define NVML_ERROR_INSUFFICIENT_RESOURCES 23  // Ran out of critical resources, other than memory
#define NVML_ERROR_UNKNOWN              999 // An internal driver error occurred
```

## Implementation Requirements

The mock libnvidia-ml.so needs to:

1. **Maintain state for initialization** - Track whether nvmlInit has been called
2. **Return consistent mock data** - Match dgxa100 topology exactly
3. **Handle error cases appropriately** - Return correct error codes for invalid inputs
4. **Support both versioned and unversioned function names** - Via preprocessor macros
5. **Be thread-safe where NVML guarantees it** - Use appropriate synchronization

## Estimated Function Count

Approximately 50-60 core functions need to be implemented to support Device Plugin and DRA functionality. Additional stub functions may be needed for completeness, but can return NVML_ERROR_NOT_SUPPORTED.

## Testing Requirements

The mock implementation should be tested with:
1. Direct C test programs using dlopen/dlsym
2. go-nvml bindings test programs
3. Integration tests with actual Device Plugin and DRA Driver

## Notes

- The mock should focus on read-only query functions
- Write/configuration functions can return success without side effects
- Process query functions can return empty lists
- Performance/monitoring functions can return static values

