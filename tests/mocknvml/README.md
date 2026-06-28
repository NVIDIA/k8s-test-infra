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
- `nvmlDeviceGetComputeRunningProcesses_v3()` / `nvmlDeviceGetGraphicsRunningProcesses_v3()` - per-process GPU memory
- `nvmlDeviceGetProcessUtilization()` - per-process SM / memory / encoder / decoder utilization
- `nvmlShutdown()` - Shutdown NVML

It also runs a suite of **bridge edge-case tests** (`bridge_tests.go`): NVML return-code strings, index/UUID/PCI boundary lookups, and post-shutdown behavior.

## Configuration

The integration test runs against a profile config fixture, `util-test-config.yaml`, via `MOCK_NVML_CONFIG` (set by the Makefile). It is an A100 profile plus one compute process carrying `sm_util`, so the per-process utilization path is exercised.

Without a config file, the mock library falls back to environment variables:

- `MOCK_NVML_NUM_DEVICES` - Number of mock devices (default: 8)
- `MOCK_NVML_DRIVER_VERSION` - Mock driver version (default: 550.163.01)

## Expected Output

```
Initializing NVML...
✓ NVML initialized successfully
✓ Driver version: 550.163.01
✓ Found 8 GPU device(s)

Enumerating devices:
Device 0:
  Name: NVIDIA A100-SXM4-40GB
  UUID: GPU-12345678-1234-1234-1234-123456780000
  Memory: 40960 MB (Total) ...
... (additional devices)

Testing device lookup by UUID / PCI Bus ID...
✓ Successfully looked up device by UUID / PCI Bus ID

=== Bridge Edge-Case Tests ===
  PASS  errstr/SUCCESS
  PASS  boundary/uuid_invalid (correctly returned: ERROR_NOT_FOUND)
  ... (additional bridge cases)
=== Bridge Tests: 29 passed, 0 failed ===

✓ checkProcessUtilization: pid=4242 smUtil=75 memUtil=40

=================================
✓ All tests passed!

SUCCESS: Mock NVML library is working correctly!
```

(Device names/UUIDs come from the configured profile in `util-test-config.yaml`.)

## Known Limitations

- Some NVML functions return `ERROR_NOT_SUPPORTED` (289 auto-generated stubs (out of 400 total exports))

## Architecture

The test uses a multi-stage Docker build:

1. **lib-builder**: Builds the mock NVML library from Go source
2. **test-builder**: Builds the test binary
3. **runtime**: Debian slim image with the library and test binary

This ensures the library is built for Linux (ELF format) even when running on macOS.
