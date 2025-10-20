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
#include <stdbool.h>
#include <stdint.h>
#include "../include/nvml.h"
#include "../data/devices.h"

// External function from nvml_init.c
extern bool nvml_is_initialized(void);

// Get device memory info
nvmlReturn_t DECLDIR nvmlDeviceGetMemoryInfo(nvmlDevice_t device, nvmlMemory_t *memory) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (memory == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const mock_device_info_t *dev = &mock_devices[index];
    
    memory->total = dev->memory_total;
    memory->free = dev->memory_free;
    memory->used = dev->memory_used;
    
    return NVML_SUCCESS;
}

// Get device memory info v2 (with reserved memory)
nvmlReturn_t DECLDIR nvmlDeviceGetMemoryInfo_v2(nvmlDevice_t device, nvmlMemory_v2_t *memory) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (memory == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Check version
    if (memory->version != nvmlMemory_v2 && memory->version != 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const mock_device_info_t *dev = &mock_devices[index];
    
    memory->version = nvmlMemory_v2;
    memory->total = dev->memory_total;
    memory->reserved = 0;  // No reserved memory in mock
    memory->free = dev->memory_free;
    memory->used = dev->memory_used;
    
    return NVML_SUCCESS;
}

// Get BAR1 memory info
nvmlReturn_t DECLDIR nvmlDeviceGetBAR1MemoryInfo(nvmlDevice_t device, nvmlBAR1Memory_t *bar1Memory) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (bar1Memory == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // A100 has 64GB BAR1
    bar1Memory->bar1Total = 68719476736ULL;  // 64GB
    bar1Memory->bar1Free = 68719476736ULL;   // All free in mock
    bar1Memory->bar1Used = 0ULL;
    
    return NVML_SUCCESS;
}

// Get device temperature
nvmlReturn_t DECLDIR nvmlDeviceGetTemperature(nvmlDevice_t device, nvmlTemperatureSensors_t sensorType, unsigned int *temp) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (temp == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (sensorType != NVML_TEMPERATURE_GPU) {
        return NVML_ERROR_NOT_SUPPORTED;
    }
    
    *temp = mock_devices[index].temperature;
    
    return NVML_SUCCESS;
}

// Get device power usage
nvmlReturn_t DECLDIR nvmlDeviceGetPowerUsage(nvmlDevice_t device, unsigned int *power) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (power == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *power = mock_devices[index].power_usage;
    
    return NVML_SUCCESS;
}

// Get device enforced power limit
nvmlReturn_t DECLDIR nvmlDeviceGetEnforcedPowerLimit(nvmlDevice_t device, unsigned int *limit) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (limit == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *limit = mock_devices[index].power_limit;
    
    return NVML_SUCCESS;
}

// Get device total energy consumption
nvmlReturn_t DECLDIR nvmlDeviceGetTotalEnergyConsumption(nvmlDevice_t device, unsigned long long *energy) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (energy == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Return some mock value in millijoules
    *energy = 1000000ULL * (index + 1);  // Different per device
    
    return NVML_SUCCESS;
}

// Get device clocks
nvmlReturn_t DECLDIR nvmlDeviceGetClock(nvmlDevice_t device, nvmlClockType_t clockType, nvmlClockId_t clockId, unsigned int *clockMHz) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (clockMHz == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const mock_device_info_t *dev = &mock_devices[index];
    
    switch (clockType) {
        case NVML_CLOCK_GRAPHICS:
            *clockMHz = dev->clock_graphics;
            break;
        case NVML_CLOCK_SM:
            *clockMHz = dev->clock_sm;
            break;
        case NVML_CLOCK_MEM:
            *clockMHz = dev->clock_memory;
            break;
        default:
            return NVML_ERROR_NOT_SUPPORTED;
    }
    
    return NVML_SUCCESS;
}

// Get device max clock info
nvmlReturn_t DECLDIR nvmlDeviceGetMaxClockInfo(nvmlDevice_t device, nvmlClockType_t type, unsigned int *clock) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (clock == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Return max clocks for A100
    switch (type) {
        case NVML_CLOCK_GRAPHICS:
        case NVML_CLOCK_SM:
            *clock = 1410;  // Max boost clock
            break;
        case NVML_CLOCK_MEM:
            *clock = 1593;  // HBM2e memory clock
            break;
        default:
            return NVML_ERROR_NOT_SUPPORTED;
    }
    
    return NVML_SUCCESS;
}

// Get device clock info (simplified version)
nvmlReturn_t DECLDIR nvmlDeviceGetClockInfo(nvmlDevice_t device, nvmlClockType_t type, unsigned int *clock) {
    return nvmlDeviceGetClock(device, type, NVML_CLOCK_ID_CURRENT, clock);
}
