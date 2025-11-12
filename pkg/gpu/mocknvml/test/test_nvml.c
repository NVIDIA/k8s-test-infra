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

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <nvml.h>

int main() {
    nvmlReturn_t result;
    unsigned int deviceCount;
    char version[NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE];
    char nvmlVersion[NVML_SYSTEM_NVML_VERSION_BUFFER_SIZE];
    int cudaVersion;
    
    printf("=== Mock NVML Library Test ===\n\n");
    
    // Initialize NVML
    printf("Initializing NVML...\n");
    result = nvmlInit_v2();
    if (result != NVML_SUCCESS) {
        printf("Failed to initialize NVML: %s\n", nvmlErrorString(result));
        return 1;
    }
    printf("✓ NVML initialized successfully\n\n");
    
    // Get driver version
    result = nvmlSystemGetDriverVersion(version, sizeof(version));
    if (result == NVML_SUCCESS) {
        printf("Driver Version: %s\n", version);
    } else {
        printf("Failed to get driver version: %s\n", nvmlErrorString(result));
    }
    
    // Get NVML version
    result = nvmlSystemGetNVMLVersion(nvmlVersion, sizeof(nvmlVersion));
    if (result == NVML_SUCCESS) {
        printf("NVML Version: %s\n", nvmlVersion);
    } else {
        printf("Failed to get NVML version: %s\n", nvmlErrorString(result));
    }
    
    // Get CUDA driver version
    result = nvmlSystemGetCudaDriverVersion(&cudaVersion);
    if (result == NVML_SUCCESS) {
        printf("CUDA Driver Version: %d.%d\n", cudaVersion / 1000, (cudaVersion % 1000) / 10);
    } else {
        printf("Failed to get CUDA driver version: %s\n", nvmlErrorString(result));
    }
    
    // Get device count
    result = nvmlDeviceGetCount_v2(&deviceCount);
    if (result == NVML_SUCCESS) {
        printf("Device Count: %u\n\n", deviceCount);
    } else {
        printf("Failed to get device count: %s\n", nvmlErrorString(result));
        nvmlShutdown();
        return 1;
    }
    
    // Enumerate devices
    for (unsigned int i = 0; i < deviceCount && i < 8; i++) {
        nvmlDevice_t device;
        char name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
        char uuid[NVML_DEVICE_UUID_V2_BUFFER_SIZE];
        nvmlPciInfo_t pci;
        unsigned int minorNumber;
        nvmlMemory_t memory;
        
        printf("=== Device %u ===\n", i);
        
        // Get device handle
        result = nvmlDeviceGetHandleByIndex_v2(i, &device);
        if (result != NVML_SUCCESS) {
            printf("Failed to get device handle: %s\n", nvmlErrorString(result));
            continue;
        }
        
        // Get device name
        result = nvmlDeviceGetName(device, name, sizeof(name));
        if (result == NVML_SUCCESS) {
            printf("Name: %s\n", name);
        }
        
        // Get device UUID
        result = nvmlDeviceGetUUID(device, uuid, sizeof(uuid));
        if (result == NVML_SUCCESS) {
            printf("UUID: %s\n", uuid);
        }
        
        // Get PCI info
        result = nvmlDeviceGetPciInfo_v3(device, &pci);
        if (result == NVML_SUCCESS) {
            printf("PCI Bus ID: %s\n", pci.busId);
            printf("PCI Device ID: 0x%08X\n", pci.pciDeviceId);
        }
        
        // Get minor number
        result = nvmlDeviceGetMinorNumber(device, &minorNumber);
        if (result == NVML_SUCCESS) {
            printf("Minor Number: %u\n", minorNumber);
        }
        
        // Get memory info
        result = nvmlDeviceGetMemoryInfo(device, &memory);
        if (result == NVML_SUCCESS) {
            printf("Memory Total: %.2f GB\n", memory.total / (1024.0 * 1024.0 * 1024.0));
            printf("Memory Free: %.2f GB\n", memory.free / (1024.0 * 1024.0 * 1024.0));
            printf("Memory Used: %.2f GB\n", memory.used / (1024.0 * 1024.0 * 1024.0));
        }
        
        // Test MIG support
        unsigned int maxMigDevices;
        result = nvmlDeviceGetMaxMigDeviceCount(device, &maxMigDevices);
        if (result == NVML_SUCCESS) {
            printf("Max MIG Devices: %u\n", maxMigDevices);
        } else if (result == NVML_ERROR_NOT_SUPPORTED) {
            printf("MIG: Not Supported\n");
        }
        
        printf("\n");
    }
    
    // Test process queries
    printf("=== Process Queries ===\n");
    if (deviceCount > 0) {
        nvmlDevice_t device;
        unsigned int infoCount = 10;
        nvmlProcessInfo_v2_t infos[10];
        
        result = nvmlDeviceGetHandleByIndex(0, &device);
        if (result == NVML_SUCCESS) {
            result = nvmlDeviceGetComputeRunningProcesses_v3(device, &infoCount, infos);
            if (result == NVML_SUCCESS) {
                printf("Compute Processes: %u\n", infoCount);
            } else {
                printf("Failed to get compute processes: %s\n", nvmlErrorString(result));
            }
        }
    }
    
    // Shutdown NVML
    printf("\nShutting down NVML...\n");
    result = nvmlShutdown();
    if (result == NVML_SUCCESS) {
        printf("✓ NVML shutdown successfully\n");
    } else {
        printf("Failed to shutdown NVML: %s\n", nvmlErrorString(result));
    }
    
    printf("\n=== Test Complete ===\n");
    return 0;
}
