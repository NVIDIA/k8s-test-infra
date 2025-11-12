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

#ifndef __MOCK_DEVICES_H__
#define __MOCK_DEVICES_H__

#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include "../include/nvml.h"

// Mock device information structure
typedef struct {
    char uuid[NVML_DEVICE_UUID_V2_BUFFER_SIZE];
    char name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
    char pci_bus_id[NVML_DEVICE_PCI_BUS_ID_BUFFER_V2_SIZE];
    char pci_bus_id_legacy[NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE];
    char serial[NVML_DEVICE_SERIAL_BUFFER_SIZE];
    unsigned int pci_domain;
    unsigned int pci_bus;
    unsigned int pci_device;
    unsigned int pci_device_id;
    unsigned int pci_subsystem_id;
    unsigned int pci_base_class;
    unsigned int pci_sub_class;
    unsigned long long memory_total;
    unsigned long long memory_free;
    unsigned long long memory_used;
    unsigned int minor_number;
    nvmlBrandType_t brand;
    unsigned int persistence_mode;
    unsigned int display_mode;
    unsigned int display_active;
    unsigned int temperature;      // in Celsius
    unsigned int power_usage;      // in milliwatts
    unsigned int power_limit;      // in milliwatts
    unsigned int clock_graphics;   // in MHz
    unsigned int clock_sm;         // in MHz
    unsigned int clock_memory;     // in MHz
    int cuda_compute_capability_major;
    int cuda_compute_capability_minor;
} mock_device_info_t;

// Static mock device data matching dgxa100 configuration
static const mock_device_info_t mock_devices[8] = {
    {
        .uuid = "GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:00:00.0",
        .pci_bus_id_legacy = "0000:00:00.0",
        .serial = "1563221000001",
        .pci_domain = 0,
        .pci_bus = 0,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,  // A100 device ID
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,       // Display controller
        .pci_sub_class = 0x02,        // 3D controller
        .memory_total = 42949672960ULL, // 40GB
        .memory_free = 42949672960ULL,  // All free in mock
        .memory_used = 0ULL,
        .minor_number = 0,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,         // Enabled
        .display_mode = 0,             // Disabled
        .display_active = 0,           // Not active
        .temperature = 30,             // 30°C
        .power_usage = 100000,         // 100W
        .power_limit = 400000,         // 400W
        .clock_graphics = 1410,        // 1410 MHz
        .clock_sm = 1410,              // 1410 MHz
        .clock_memory = 1593,          // 1593 MHz
    },
    {
        .uuid = "GPU-b8ea3855-276c-c9cb-b366-c6fa655957c5",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:01:00.0",
        .pci_bus_id_legacy = "0000:01:00.0",
        .serial = "1563221000002",
        .pci_domain = 0,
        .pci_bus = 1,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,
        .pci_sub_class = 0x02,
        .memory_total = 42949672960ULL,
        .memory_free = 42949672960ULL,
        .memory_used = 0ULL,
        .minor_number = 1,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,
        .display_mode = 0,
        .display_active = 0,
        .temperature = 31,
        .power_usage = 100000,
        .power_limit = 400000,
        .clock_graphics = 1410,
        .clock_sm = 1410,
        .clock_memory = 1593,
        .cuda_compute_capability_major = 8,
        .cuda_compute_capability_minor = 0
    },
    {
        .uuid = "GPU-36da4373-4344-3b36-9951-6c7af0e8d7a0",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:02:00.0",
        .pci_bus_id_legacy = "0000:02:00.0",
        .serial = "1563221000003",
        .pci_domain = 0,
        .pci_bus = 2,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,
        .pci_sub_class = 0x02,
        .memory_total = 42949672960ULL,
        .memory_free = 42949672960ULL,
        .memory_used = 0ULL,
        .minor_number = 2,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,
        .display_mode = 0,
        .display_active = 0,
        .temperature = 32,
        .power_usage = 100000,
        .power_limit = 400000,
        .clock_graphics = 1410,
        .clock_sm = 1410,
        .clock_memory = 1593,
        .cuda_compute_capability_major = 8,
        .cuda_compute_capability_minor = 0
    },
    {
        .uuid = "GPU-3dc6c589-3bea-2eb8-263e-d7a5b2b3b1ba",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:03:00.0",
        .pci_bus_id_legacy = "0000:03:00.0",
        .serial = "1563221000004",
        .pci_domain = 0,
        .pci_bus = 3,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,
        .pci_sub_class = 0x02,
        .memory_total = 42949672960ULL,
        .memory_free = 42949672960ULL,
        .memory_used = 0ULL,
        .minor_number = 3,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,
        .display_mode = 0,
        .display_active = 0,
        .temperature = 33,
        .power_usage = 100000,
        .power_limit = 400000,
        .clock_graphics = 1410,
        .clock_sm = 1410,
        .clock_memory = 1593,
        .cuda_compute_capability_major = 8,
        .cuda_compute_capability_minor = 0
    },
    {
        .uuid = "GPU-7e8ad30b-b5d9-cd98-3fcf-9b3e4d2ba6a0",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:04:00.0",
        .pci_bus_id_legacy = "0000:04:00.0",
        .serial = "1563221000005",
        .pci_domain = 0,
        .pci_bus = 4,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,
        .pci_sub_class = 0x02,
        .memory_total = 42949672960ULL,
        .memory_free = 42949672960ULL,
        .memory_used = 0ULL,
        .minor_number = 4,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,
        .display_mode = 0,
        .display_active = 0,
        .temperature = 34,
        .power_usage = 100000,
        .power_limit = 400000,
        .clock_graphics = 1410,
        .clock_sm = 1410,
        .clock_memory = 1593,
        .cuda_compute_capability_major = 8,
        .cuda_compute_capability_minor = 0
    },
    {
        .uuid = "GPU-e81b08cb-3aa9-4add-d834-1d3f537ea20f",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:05:00.0",
        .pci_bus_id_legacy = "0000:05:00.0",
        .serial = "1563221000006",
        .pci_domain = 0,
        .pci_bus = 5,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,
        .pci_sub_class = 0x02,
        .memory_total = 42949672960ULL,
        .memory_free = 42949672960ULL,
        .memory_used = 0ULL,
        .minor_number = 5,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,
        .display_mode = 0,
        .display_active = 0,
        .temperature = 35,
        .power_usage = 100000,
        .power_limit = 400000,
        .clock_graphics = 1410,
        .clock_sm = 1410,
        .clock_memory = 1593,
        .cuda_compute_capability_major = 8,
        .cuda_compute_capability_minor = 0
    },
    {
        .uuid = "GPU-eca0e2dd-3d99-2271-10fd-1939fec48d42",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:06:00.0",
        .pci_bus_id_legacy = "0000:06:00.0",
        .serial = "1563221000007",
        .pci_domain = 0,
        .pci_bus = 6,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,
        .pci_sub_class = 0x02,
        .memory_total = 42949672960ULL,
        .memory_free = 42949672960ULL,
        .memory_used = 0ULL,
        .minor_number = 6,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,
        .display_mode = 0,
        .display_active = 0,
        .temperature = 36,
        .power_usage = 100000,
        .power_limit = 400000,
        .clock_graphics = 1410,
        .clock_sm = 1410,
        .clock_memory = 1593,
        .cuda_compute_capability_major = 8,
        .cuda_compute_capability_minor = 0
    },
    {
        .uuid = "GPU-c9dea5de-06db-44ff-c80f-ce1d407e77ba",
        .name = "NVIDIA A100-SXM4-40GB",
        .pci_bus_id = "00000000:07:00.0",
        .pci_bus_id_legacy = "0000:07:00.0",
        .serial = "1563221000008",
        .pci_domain = 0,
        .pci_bus = 7,
        .pci_device = 0,
        .pci_device_id = 0x20B010DE,
        .pci_subsystem_id = 0x134F10DE,
        .pci_base_class = 0x03,
        .pci_sub_class = 0x02,
        .memory_total = 42949672960ULL,
        .memory_free = 42949672960ULL,
        .memory_used = 0ULL,
        .minor_number = 7,
        .brand = NVML_BRAND_TESLA,
        .persistence_mode = 1,
        .display_mode = 0,
        .display_active = 0,
        .temperature = 37,
        .power_usage = 100000,
        .power_limit = 400000,
        .clock_graphics = 1410,
        .clock_sm = 1410,
        .clock_memory = 1593,
    }
};

// Helper functions
static inline bool is_valid_device_index(unsigned int index) {
    return index < 8;
}

static inline bool is_valid_device_handle(nvmlDevice_t device) {
    if (device.handle == NULL) {
        return false;
    }
    
    uintptr_t handle_value = (uintptr_t)device.handle;
    return (handle_value >= 1 && handle_value <= 8);
}

static inline unsigned int device_handle_to_index(nvmlDevice_t device) {
    if (!is_valid_device_handle(device)) {
        return (unsigned int)-1;
    }
    
    return (unsigned int)((uintptr_t)device.handle - 1);
}

// Macro to suppress unused parameter warnings
#define UNUSED(x) ((void)(x))

#endif // __MOCK_DEVICES_H__
