# Mock NVML Integration Test

This directory contains an integration test for the mock NVML library (`pkg/gpu/mocknvml`).

## Overview

The test simulates a minimal device plugin that uses the NVML API to enumerate and query GPU devices. It runs in a Docker container with the mock `libnvidia-ml.so` library, demonstrating that the mock works without requiring actual NVIDIA GPUs or drivers.

## Running the Test

```bash
make test
```

This will:
1. Build the mock NVML library inside a Docker container (for Linux compatibility)
2. Build the test binary
3. Create a runtime container with the mock library installed
4. Run the test and display the results

## What It Tests

The integration test exercises the following NVML APIs:

- `nvmlInit_v2()` - Initialize NVML
- `nvmlSystemGetDriverVersion()` - Get driver version
- `nvmlDeviceGetCount_v2()` - Get number of devices
- `nvmlDeviceGetHandleByIndex_v2()` - Get device by index
- `nvmlDeviceGetName()` - Get device name
- `nvmlDeviceGetUUID()` - Get device UUID
- `nvmlDeviceGetPciInfo_v3()` - Get PCI information
- `nvmlDeviceGetMemoryInfo()` - Get memory information
- `nvmlDeviceGetCudaComputeCapability()` - Get compute capability
- `nvmlDeviceGetHandleByUUID()` - Get device by UUID
- `nvmlDeviceGetHandleByPciBusId_v2()` - Get device by PCI Bus ID
- `nvmlShutdown()` - Shutdown NVML

## Configuration

The mock library can be configured via environment variables:

- `MOCK_NVML_NUM_DEVICES` - Number of mock devices (default: 8, test uses: 4)
- `MOCK_NVML_DRIVER_VERSION` - Mock driver version (default: 550.54.15)

## Expected Output

```
Starting Mini Device Plugin Test
=================================
Initializing NVML...
✓ NVML initialized successfully
✓ Driver version: 550.54.15
✓ Found 4 GPU device(s)

Enumerating devices:

Device 0:
  Name: Mock NVIDIA A100-SXM4-40GB
  UUID: GPU-9b465350-deaa-456b-ba7c-2b0c95ae7f2b
  Memory: 40960 MB (Total), 0 MB (Free), 0 MB (Used)

... (additional devices)

Testing device lookup by UUID...
  Device 0 UUID: "GPU-543ae470-0879-4749-8bb8-e38ecacd1bb5"
  Device 1 UUID: "GPU-c13faba5-2b00-4949-b3f3-5ab360ac0250"
  Device 2 UUID: "GPU-810f0b8a-cc0a-43bb-b11a-099b0b7acd18"
  Device 3 UUID: "GPU-10ffca56-a94f-4825-87e2-e41fc4c23395"
Looking up device with UUID: "GPU-543ae470-0879-4749-8bb8-e38ecacd1bb5"
✓ Successfully looked up device by UUID: GPU-543ae470-0879-4749-8bb8-e38ecacd1bb5

Testing device lookup by PCI Bus ID...
✓ Successfully looked up device by PCI Bus ID: 0000:00:00.0

=================================
✓ All device plugin tests passed!

SUCCESS: Mock NVML library is working correctly!
```

## Known Limitations

- Some NVML functions return `ERROR_NOT_SUPPORTED` (378 out of 396 stubs)

## Architecture

The test uses a multi-stage Docker build:

1. **lib-builder**: Builds the mock NVML library from Go source
2. **test-builder**: Builds the test binary
3. **runtime**: Debian slim image with the library and test binary

This ensures the library is built for Linux (ELF format) even when running on macOS.
