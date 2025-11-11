/*
 * Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/*
 * Simple test program to validate the Go-based mock NVML bridge.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// NVML types and constants
typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS 0
#define NVML_DEVICE_NAME_BUFFER_SIZE 64
#define NVML_DEVICE_UUID_BUFFER_SIZE 80
#define NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE 80
#define NVML_SYSTEM_NVML_VERSION_BUFFER_SIZE 80

// NVML function declarations
extern nvmlReturn_t nvmlInit_v2(void);
extern nvmlReturn_t nvmlShutdown(void);
extern nvmlReturn_t nvmlDeviceGetCount_v2(unsigned int *deviceCount);
extern nvmlReturn_t nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t *device);
extern nvmlReturn_t nvmlDeviceGetName(nvmlDevice_t device, char *name, unsigned int length);
extern nvmlReturn_t nvmlDeviceGetUUID(nvmlDevice_t device, char *uuid, unsigned int length);
extern nvmlReturn_t nvmlDeviceGetMinorNumber(nvmlDevice_t device, unsigned int *minorNumber);
extern nvmlReturn_t nvmlDeviceGetIndex(nvmlDevice_t device, unsigned int *index);
extern nvmlReturn_t nvmlSystemGetDriverVersion(char *version, unsigned int length);
extern nvmlReturn_t nvmlSystemGetNVMLVersion(char *version, unsigned int length);
extern nvmlReturn_t nvmlSystemGetCudaDriverVersion(int *cudaDriverVersion);
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

int main(void) {
    printf("=== Go-Based Mock NVML Bridge Test ===\n\n");

    // Test 1: Initialize NVML
    printf("Test 1: nvmlInit_v2()\n");
    CHECK_NVML(nvmlInit_v2());
    printf("  ✓ NVML initialized successfully\n\n");

    // Test 2: Get device count
    unsigned int deviceCount = 0;
    printf("Test 2: nvmlDeviceGetCount_v2()\n");
    CHECK_NVML(nvmlDeviceGetCount_v2(&deviceCount));
    printf("  ✓ Device count: %u\n\n", deviceCount);

    // Test 3: Get system information
    char driverVersion[NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE];
    char nvmlVersion[NVML_SYSTEM_NVML_VERSION_BUFFER_SIZE];
    int cudaVersion = 0;

    printf("Test 3: System Information\n");
    CHECK_NVML(nvmlSystemGetDriverVersion(driverVersion, sizeof(driverVersion)));
    printf("  ✓ Driver version: %s\n", driverVersion);
    
    CHECK_NVML(nvmlSystemGetNVMLVersion(nvmlVersion, sizeof(nvmlVersion)));
    printf("  ✓ NVML version: %s\n", nvmlVersion);
    
    CHECK_NVML(nvmlSystemGetCudaDriverVersion(&cudaVersion));
    printf("  ✓ CUDA driver version: %d\n\n", cudaVersion);

    // Test 4: Enumerate devices and get properties
    printf("Test 4: Device Enumeration and Properties\n");
    for (unsigned int i = 0; i < deviceCount && i < 3; i++) {
        nvmlDevice_t device;
        char name[NVML_DEVICE_NAME_BUFFER_SIZE];
        char uuid[NVML_DEVICE_UUID_BUFFER_SIZE];
        unsigned int minorNumber = 0;
        unsigned int index = 0;

        printf("  Device %u:\n", i);
        
        CHECK_NVML(nvmlDeviceGetHandleByIndex_v2(i, &device));
        printf("    ✓ Got device handle\n");
        
        CHECK_NVML(nvmlDeviceGetName(device, name, sizeof(name)));
        printf("    ✓ Name: %s\n", name);
        
        CHECK_NVML(nvmlDeviceGetUUID(device, uuid, sizeof(uuid)));
        printf("    ✓ UUID: %s\n", uuid);
        
        CHECK_NVML(nvmlDeviceGetMinorNumber(device, &minorNumber));
        printf("    ✓ Minor number: %u\n", minorNumber);
        
        CHECK_NVML(nvmlDeviceGetIndex(device, &index));
        printf("    ✓ Index: %u\n", index);
        
        printf("\n");
    }

    // Test 5: Reference counting (multiple init/shutdown)
    printf("Test 5: Reference Counting\n");
    CHECK_NVML(nvmlInit_v2());
    printf("  ✓ Second init succeeded (ref count: 2)\n");
    
    CHECK_NVML(nvmlShutdown());
    printf("  ✓ First shutdown succeeded (ref count: 1)\n");
    
    // Should still be able to query
    CHECK_NVML(nvmlDeviceGetCount_v2(&deviceCount));
    printf("  ✓ Still able to query (count: %u)\n", deviceCount);
    
    CHECK_NVML(nvmlShutdown());
    printf("  ✓ Second shutdown succeeded (ref count: 0)\n\n");

    printf("=== All Tests Passed! ===\n");
    return 0;
}

