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
#include <stdbool.h>
#include <stdint.h>
#include "../include/nvml.h"
#include "../data/devices.h"

// External function from nvml_init.c
extern bool nvml_is_initialized(void);

// Get MIG mode - A100 doesn't support MIG in our mock
nvmlReturn_t DECLDIR nvmlDeviceGetMigMode(nvmlDevice_t device, unsigned int *currentMode, unsigned int *pendingMode) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (currentMode == NULL || pendingMode == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // MIG not supported in our mock
    return NVML_ERROR_NOT_SUPPORTED;
}

// Get max MIG device count - return 0 for no MIG support
nvmlReturn_t DECLDIR nvmlDeviceGetMaxMigDeviceCount(nvmlDevice_t device, unsigned int *count) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (count == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *count = 0;  // No MIG devices in mock
    
    return NVML_SUCCESS;
}

// Get GPU instance possible placements
nvmlReturn_t DECLDIR nvmlDeviceGetGpuInstancePossiblePlacements_v2(nvmlDevice_t device, unsigned int profileId, 
                                                                   nvmlGpuInstancePlacement_t *placements, 
                                                                   unsigned int *count) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (count == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // MIG not supported
    *count = 0;
    return NVML_ERROR_NOT_SUPPORTED;
}

// Get GPU instances
nvmlReturn_t DECLDIR nvmlDeviceGetGpuInstances(nvmlDevice_t device, unsigned int profileId, 
                                               nvmlGpuInstance_t *instances, unsigned int *count) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (count == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // MIG not supported
    *count = 0;
    return NVML_ERROR_NOT_SUPPORTED;
}

// Create GPU instance
nvmlReturn_t DECLDIR nvmlDeviceCreateGpuInstance(nvmlDevice_t device, unsigned int profileId,
                                                 nvmlGpuInstance_t *gpuInstance) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // MIG not supported
    return NVML_ERROR_NOT_SUPPORTED;
}

// Destroy GPU instance
nvmlReturn_t DECLDIR nvmlGpuInstanceDestroy(nvmlGpuInstance_t gpuInstance) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    // MIG not supported
    return NVML_ERROR_NOT_SUPPORTED;
}

// Get compute instance info
nvmlReturn_t DECLDIR nvmlComputeInstanceGetInfo_v2(nvmlComputeInstance_t computeInstance,
                                                   nvmlComputeInstanceInfo_t *info) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    // MIG not supported
    return NVML_ERROR_NOT_SUPPORTED;
}

// Legacy compatibility
nvmlReturn_t DECLDIR nvmlDeviceGetGpuInstancePossiblePlacements(nvmlDevice_t device, unsigned int profileId,
                                                               nvmlGpuInstancePlacement_t *placements,
                                                               unsigned int *count) {
    return nvmlDeviceGetGpuInstancePossiblePlacements_v2(device, profileId, placements, count);
}

nvmlReturn_t DECLDIR nvmlComputeInstanceGetInfo(nvmlComputeInstance_t computeInstance,
                                               nvmlComputeInstanceInfo_t *info) {
    return nvmlComputeInstanceGetInfo_v2(computeInstance, info);
}
