/*
 * Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
 *
 * Extended test program for Go-based mock NVML bridge covering Phase 2 functions.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// NVML types and constants
typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS 0
#define NVML_ERROR_NOT_SUPPORTED 3
#define NVML_DEVICE_NAME_BUFFER_SIZE 64
#define NVML_DEVICE_UUID_BUFFER_SIZE 80
#define NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE 32

typedef struct {
    unsigned long long total;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_t;

typedef struct {
    unsigned long long total;
    unsigned long long reserved;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_v2_t;

typedef struct {
    unsigned long long bar1Total;
    unsigned long long bar1Free;
    unsigned long long bar1Used;
} nvmlBAR1Memory_t;

typedef struct {
    char busIdLegacy[16];
    unsigned int domain;
    unsigned int bus;
    unsigned int device;
    unsigned int pciDeviceId;
    unsigned int pciSubSystemId;
    char busId[NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE];
} nvmlPciInfo_t;

typedef struct {
    unsigned int pid;
    unsigned long long usedGpuMemory;
    unsigned int gpuInstanceId;
    unsigned int computeInstanceId;
} nvmlProcessInfo_t;

// Function declarations
extern nvmlReturn_t nvmlInit_v2(void);
extern nvmlReturn_t nvmlShutdown(void);
extern nvmlReturn_t nvmlDeviceGetCount_v2(unsigned int *deviceCount);
extern nvmlReturn_t nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t *device);

// Memory functions
extern nvmlReturn_t nvmlDeviceGetMemoryInfo(nvmlDevice_t device, nvmlMemory_t *memory);
extern nvmlReturn_t nvmlDeviceGetMemoryInfo_v2(nvmlDevice_t device, nvmlMemory_v2_t *memory);
extern nvmlReturn_t nvmlDeviceGetBAR1MemoryInfo(nvmlDevice_t device, nvmlBAR1Memory_t *bar1Memory);

// PCI functions
extern nvmlReturn_t nvmlDeviceGetPciInfo_v3(nvmlDevice_t device, nvmlPciInfo_t *pci);
extern nvmlReturn_t nvmlDeviceGetCudaComputeCapability(nvmlDevice_t device, int *major, int *minor);
extern nvmlReturn_t nvmlDeviceGetBrand(nvmlDevice_t device, int *brandType);
extern nvmlReturn_t nvmlDeviceGetArchitecture(nvmlDevice_t device, int *arch);

// Process functions
extern nvmlReturn_t nvmlDeviceGetComputeRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount, nvmlProcessInfo_t *infos);
extern nvmlReturn_t nvmlDeviceGetGraphicsRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount, nvmlProcessInfo_t *infos);

// MIG functions
extern nvmlReturn_t nvmlDeviceGetMigMode(nvmlDevice_t device, unsigned int *currentMode, unsigned int *pendingMode);
extern nvmlReturn_t nvmlDeviceGetMaxMigDeviceCount(nvmlDevice_t device, unsigned int *count);

// Utility functions
extern nvmlReturn_t nvmlDeviceGetTemperature(nvmlDevice_t device, int sensorType, unsigned int *temp);
extern nvmlReturn_t nvmlDeviceGetPowerUsage(nvmlDevice_t device, unsigned int *power);
extern nvmlReturn_t nvmlDeviceGetPowerManagementLimit(nvmlDevice_t device, unsigned int *limit);
extern nvmlReturn_t nvmlDeviceGetClock(nvmlDevice_t device, int clockType, int clockId, unsigned int *clock);
extern nvmlReturn_t nvmlDeviceGetPerformanceState(nvmlDevice_t device, int *pState);

extern const char* nvmlErrorString(nvmlReturn_t result);

#define CHECK_NVML(call) \
    do { \
        nvmlReturn_t _ret = (call); \
        if (_ret != NVML_SUCCESS) { \
            fprintf(stderr, "NVML Error at %s:%d: %s (return code: %d)\n", \
                    __FILE__, __LINE__, nvmlErrorString(_ret), _ret); \
            exit(1); \
        } \
    } while(0)

#define CHECK_NVML_OPTIONAL(call, expected_err) \
    do { \
        nvmlReturn_t _ret = (call); \
        if (_ret != NVML_SUCCESS && _ret != expected_err) { \
            fprintf(stderr, "NVML Error at %s:%d: %s (return code: %d)\n", \
                    __FILE__, __LINE__, nvmlErrorString(_ret), _ret); \
            exit(1); \
        } \
    } while(0)

int main(void) {
    printf("=== Extended Go-Based Mock NVML Test ===\n\n");

    // Initialize
    CHECK_NVML(nvmlInit_v2());

    unsigned int deviceCount = 0;
    CHECK_NVML(nvmlDeviceGetCount_v2(&deviceCount));
    printf("Device count: %u\n\n", deviceCount);

    if (deviceCount == 0) {
        printf("No devices found!\n");
        return 1;
    }

    // Test first device
    nvmlDevice_t device;
    CHECK_NVML(nvmlDeviceGetHandleByIndex_v2(0, &device));
    printf("Testing device 0...\n\n");

    // Test 1: Memory Information
    printf("Test 1: Memory Information\n");
    nvmlMemory_t memory;
    CHECK_NVML(nvmlDeviceGetMemoryInfo(device, &memory));
    printf("  ✓ Total memory: %llu bytes (%.2f GB)\n", memory.total, memory.total / 1024.0 / 1024.0 / 1024.0);
    printf("  ✓ Free memory: %llu bytes\n", memory.free);
    printf("  ✓ Used memory: %llu bytes\n", memory.used);

    nvmlMemory_v2_t memory_v2;
    CHECK_NVML(nvmlDeviceGetMemoryInfo_v2(device, &memory_v2));
    printf("  ✓ Memory v2 - Total: %llu, Reserved: %llu\n", memory_v2.total, memory_v2.reserved);

    nvmlBAR1Memory_t bar1Memory;
    CHECK_NVML(nvmlDeviceGetBAR1MemoryInfo(device, &bar1Memory));
    printf("  ✓ BAR1 Total: %llu bytes\n\n", bar1Memory.bar1Total);

    // Test 2: PCI Information
    printf("Test 2: PCI Information\n");
    nvmlPciInfo_t pci;
    CHECK_NVML(nvmlDeviceGetPciInfo_v3(device, &pci));
    printf("  ✓ Bus ID: %s\n", pci.busId);
    printf("  ✓ Domain: %u, Bus: %u, Device: %u\n", pci.domain, pci.bus, pci.device);
    printf("  ✓ PCI Device ID: 0x%x\n", pci.pciDeviceId);

    int cudaMajor, cudaMinor;
    CHECK_NVML(nvmlDeviceGetCudaComputeCapability(device, &cudaMajor, &cudaMinor));
    printf("  ✓ CUDA Compute Capability: %d.%d\n", cudaMajor, cudaMinor);

    int brand;
    CHECK_NVML(nvmlDeviceGetBrand(device, &brand));
    printf("  ✓ Brand: %d\n", brand);

    int arch;
    CHECK_NVML(nvmlDeviceGetArchitecture(device, &arch));
    printf("  ✓ Architecture: %d\n\n", arch);

    // Test 3: Process Information
    printf("Test 3: Process Information\n");
    unsigned int procCount = 0;
    CHECK_NVML(nvmlDeviceGetComputeRunningProcesses_v3(device, &procCount, NULL));
    printf("  ✓ Compute processes: %u\n", procCount);

    procCount = 0;
    CHECK_NVML(nvmlDeviceGetGraphicsRunningProcesses_v3(device, &procCount, NULL));
    printf("  ✓ Graphics processes: %u\n\n", procCount);

    // Test 4: MIG (Should return NOT_SUPPORTED or 0)
    printf("Test 4: MIG Functions\n");
    unsigned int currentMode, pendingMode;
    nvmlReturn_t ret = nvmlDeviceGetMigMode(device, &currentMode, &pendingMode);
    if (ret == NVML_ERROR_NOT_SUPPORTED) {
        printf("  ✓ MIG not supported (as expected)\n");
    } else if (ret == NVML_SUCCESS) {
        printf("  ✓ MIG mode: current=%u, pending=%u\n", currentMode, pendingMode);
    }

    unsigned int maxMigCount = 0;
    CHECK_NVML(nvmlDeviceGetMaxMigDeviceCount(device, &maxMigCount));
    printf("  ✓ Max MIG device count: %u\n\n", maxMigCount);

    // Test 5: Utility Functions
    printf("Test 5: Utility Functions\n");
    unsigned int temp = 0;
    CHECK_NVML(nvmlDeviceGetTemperature(device, 0, &temp));
    printf("  ✓ Temperature: %u°C\n", temp);

    unsigned int power = 0;
    CHECK_NVML(nvmlDeviceGetPowerUsage(device, &power));
    printf("  ✓ Power usage: %u mW (%.2f W)\n", power, power / 1000.0);

    unsigned int limit = 0;
    CHECK_NVML(nvmlDeviceGetPowerManagementLimit(device, &limit));
    printf("  ✓ Power limit: %u mW (%.2f W)\n", limit, limit / 1000.0);

    unsigned int clock = 0;
    CHECK_NVML(nvmlDeviceGetClock(device, 0, 0, &clock)); // Graphics clock
    printf("  ✓ Graphics clock: %u MHz\n", clock);

    int pState = 0;
    CHECK_NVML(nvmlDeviceGetPerformanceState(device, &pState));
    printf("  ✓ Performance state: P%d\n\n", pState);

    // Cleanup
    CHECK_NVML(nvmlShutdown());

    printf("=== All Extended Tests Passed! ===\n");
    return 0;
}

