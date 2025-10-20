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

#define NVML_NO_UNVERSIONED_FUNC_DEFS
#include <string.h>
#include <stdio.h>
#include <unistd.h>
#include <limits.h>
#include <stdbool.h>
#include <stdint.h>
#include "../include/nvml.h"

// External function from nvml_init.c
extern bool nvml_is_initialized(void);

// Mock values matching our dgxa100 configuration
#define MOCK_DRIVER_VERSION "550.54.15"
#define MOCK_NVML_VERSION "12.550.54"
#define MOCK_CUDA_DRIVER_VERSION 12040  // CUDA 12.4

// Get driver version
nvmlReturn_t DECLDIR nvmlSystemGetDriverVersion(char *version, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (version == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (length < strlen(MOCK_DRIVER_VERSION) + 1) {
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    strncpy(version, MOCK_DRIVER_VERSION, length);
    version[length - 1] = '\0';
    
    return NVML_SUCCESS;
}

// Get NVML version
nvmlReturn_t DECLDIR nvmlSystemGetNVMLVersion(char *version, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (version == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (length < strlen(MOCK_NVML_VERSION) + 1) {
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    strncpy(version, MOCK_NVML_VERSION, length);
    version[length - 1] = '\0';
    
    return NVML_SUCCESS;
}

// Get CUDA driver version
nvmlReturn_t DECLDIR nvmlSystemGetCudaDriverVersion(int *cudaDriverVersion) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (cudaDriverVersion == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *cudaDriverVersion = MOCK_CUDA_DRIVER_VERSION;
    
    return NVML_SUCCESS;
}

// Get CUDA driver version v2 (same as v1 for our mock)
nvmlReturn_t DECLDIR nvmlSystemGetCudaDriverVersion_v2(int *cudaDriverVersion) {
    return nvmlSystemGetCudaDriverVersion(cudaDriverVersion);
}

// Get process name by PID
nvmlReturn_t DECLDIR nvmlSystemGetProcessName(unsigned int pid, char *name, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (name == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Try to get real process name
    char proc_path[PATH_MAX];
    char exe_path[PATH_MAX];
    ssize_t len;
    
    snprintf(proc_path, sizeof(proc_path), "/proc/%u/exe", pid);
    len = readlink(proc_path, exe_path, sizeof(exe_path) - 1);
    
    if (len > 0) {
        exe_path[len] = '\0';
        
        // Extract basename
        char *basename = strrchr(exe_path, '/');
        if (basename) {
            basename++;
        } else {
            basename = exe_path;
        }
        
        if (strlen(basename) >= length) {
            return NVML_ERROR_INSUFFICIENT_SIZE;
        }
        
        strncpy(name, basename, length);
        name[length - 1] = '\0';
        
        return NVML_SUCCESS;
    }
    
    // Process not found
    return NVML_ERROR_NOT_FOUND;
}

// Get HIC version (not supported in mock)
nvmlReturn_t DECLDIR nvmlSystemGetHicVersion(unsigned int *hwbcCount, nvmlHwbcEntry_t *hwbcEntries) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (hwbcCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *hwbcCount = 0;
    return NVML_SUCCESS;
}

// Get topology GPU set (simplified mock implementation)
nvmlReturn_t DECLDIR nvmlSystemGetTopologyGpuSet(unsigned int cpuNumber, unsigned int *count, nvmlDevice_t *deviceArray) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (count == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // For mock, return all 8 GPUs for any CPU
    if (deviceArray == NULL) {
        *count = 8;
        return NVML_SUCCESS;
    }
    
    if (*count < 8) {
        *count = 8;
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    // Return device handles (index-based for mock)
    for (unsigned int i = 0; i < 8; i++) {
        deviceArray[i].handle = (struct nvmlDevice_st*)(uintptr_t)(i + 1);
    }
    
    *count = 8;
    return NVML_SUCCESS;
}

// Get driver branch info
nvmlReturn_t DECLDIR nvmlSystemGetDriverBranch(nvmlSystemDriverBranchInfo_t *branchInfo, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (branchInfo == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const char *branch = "r550_00";
    
    if (length < strlen(branch) + 1) {
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    strncpy((char*)branchInfo, branch, length);
    ((char*)branchInfo)[length - 1] = '\0';
    
    return NVML_SUCCESS;
}
