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
#include <pthread.h>
#include <stdbool.h>
#include <string.h>
#include <stdio.h>
#include "../include/nvml.h"

// Global state tracking
static int g_nvml_ref_count = 0;  // Reference counting instead of bool
static pthread_mutex_t g_init_mutex = PTHREAD_MUTEX_INITIALIZER;

// Initialize NVML
// This function implements reference counting as per real NVML behavior.
// Multiple calls increment a counter, and the library remains initialized
// until nvmlShutdown is called an equal number of times.
nvmlReturn_t DECLDIR nvmlInit_v2(void) {
    pthread_mutex_lock(&g_init_mutex);
    g_nvml_ref_count++;
    pthread_mutex_unlock(&g_init_mutex);
    
    return NVML_SUCCESS;
}

// Initialize NVML with flags
nvmlReturn_t DECLDIR nvmlInitWithFlags(unsigned int flags) {
    // For mock, we ignore flags but still initialize
    (void)flags; // Suppress unused parameter warning
    return nvmlInit_v2();
}

// Shutdown NVML
// Decrements the reference count. The library is only considered
// uninitialized when the count reaches zero.
nvmlReturn_t DECLDIR nvmlShutdown(void) {
    pthread_mutex_lock(&g_init_mutex);
    
    if (g_nvml_ref_count <= 0) {
        pthread_mutex_unlock(&g_init_mutex);
        return NVML_ERROR_UNINITIALIZED;
    }
    
    g_nvml_ref_count--;
    pthread_mutex_unlock(&g_init_mutex);
    
    return NVML_SUCCESS;
}

// Convert error code to string
const DECLDIR char* nvmlErrorString(nvmlReturn_t result) {
    switch (result) {
        case NVML_SUCCESS:
            return "Success";
        case NVML_ERROR_UNINITIALIZED:
            return "NVML was not first initialized with nvmlInit()";
        case NVML_ERROR_INVALID_ARGUMENT:
            return "A supplied argument is invalid";
        case NVML_ERROR_NOT_SUPPORTED:
            return "The requested operation is not available on target device";
        case NVML_ERROR_NO_PERMISSION:
            return "The current user does not have permission";
        case NVML_ERROR_ALREADY_INITIALIZED:
            return "Multiple initializations are now allowed";
        case NVML_ERROR_NOT_FOUND:
            return "A query to find an object was unsuccessful";
        case NVML_ERROR_INSUFFICIENT_SIZE:
            return "An input argument is not large enough";
        case NVML_ERROR_INSUFFICIENT_POWER:
            return "A device's external power cables are not properly attached";
        case NVML_ERROR_DRIVER_NOT_LOADED:
            return "NVIDIA driver is not loaded";
        case NVML_ERROR_TIMEOUT:
            return "User provided timeout passed";
        case NVML_ERROR_IRQ_ISSUE:
            return "NVIDIA Kernel detected an interrupt issue with a GPU";
        case NVML_ERROR_LIBRARY_NOT_FOUND:
            return "NVML Shared Library couldn't be found or loaded";
        case NVML_ERROR_FUNCTION_NOT_FOUND:
            return "Local version of NVML doesn't implement this function";
        case NVML_ERROR_CORRUPTED_INFOROM:
            return "infoROM is corrupted";
        case NVML_ERROR_GPU_IS_LOST:
            return "The GPU has fallen off the bus or has otherwise become inaccessible";
        case NVML_ERROR_RESET_REQUIRED:
            return "The GPU requires a reset before it can be used again";
        case NVML_ERROR_OPERATING_SYSTEM:
            return "The GPU control device has been blocked by the operating system/cgroups";
        case NVML_ERROR_LIB_RM_VERSION_MISMATCH:
            return "RM detects a driver/library version mismatch";
        case NVML_ERROR_IN_USE:
            return "An operation cannot be performed because the GPU is currently in use";
        case NVML_ERROR_MEMORY:
            return "Insufficient memory";
        case NVML_ERROR_NO_DATA:
            return "No data";
        case NVML_ERROR_VGPU_ECC_NOT_SUPPORTED:
            return "The requested vgpu operation is not available on target device";
        case NVML_ERROR_INSUFFICIENT_RESOURCES:
            return "Ran out of critical resources, other than memory";
        case NVML_ERROR_UNKNOWN:
        default:
            return "Unknown error";
    }
}

// Internal helper to check if NVML is initialized
bool nvml_is_initialized(void) {
    bool initialized;
    pthread_mutex_lock(&g_init_mutex);
    initialized = (g_nvml_ref_count > 0);
    pthread_mutex_unlock(&g_init_mutex);
    return initialized;
}

// Legacy compatibility
nvmlReturn_t DECLDIR nvmlInit(void) {
    return nvmlInit_v2();
}
