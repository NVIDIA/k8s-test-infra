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
#include <stdbool.h>
#include <stdint.h>
#include "../include/nvml.h"
#include "../data/devices.h"

// External function from nvml_init.c
extern bool nvml_is_initialized(void);

// Get device count
nvmlReturn_t DECLDIR nvmlDeviceGetCount_v2(unsigned int *deviceCount) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (deviceCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *deviceCount = 8; // dgxa100 has 8 GPUs
    return NVML_SUCCESS;
}

// Get device handle by index
nvmlReturn_t DECLDIR nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t *device) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (device == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (!is_valid_device_index(index)) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Use index + 1 as handle value (so handle values are 1-8)
    device->handle = (struct nvmlDevice_st*)(uintptr_t)(index + 1);
    
    return NVML_SUCCESS;
}

// Get device handle by UUID
nvmlReturn_t DECLDIR nvmlDeviceGetHandleByUUID(const char *uuid, nvmlDevice_t *device) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (uuid == NULL || device == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Search for matching UUID
    for (unsigned int i = 0; i < 8; i++) {
        if (strcmp(uuid, mock_devices[i].uuid) == 0) {
            device->handle = (struct nvmlDevice_st*)(uintptr_t)(i + 1);
            return NVML_SUCCESS;
        }
    }
    
    return NVML_ERROR_NOT_FOUND;
}

// Get device handle by PCI bus ID
nvmlReturn_t DECLDIR nvmlDeviceGetHandleByPciBusId_v2(const char *pciBusId, nvmlDevice_t *device) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (pciBusId == NULL || device == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Search for matching PCI bus ID (try both formats)
    for (unsigned int i = 0; i < 8; i++) {
        if (strcmp(pciBusId, mock_devices[i].pci_bus_id) == 0 ||
            strcmp(pciBusId, mock_devices[i].pci_bus_id_legacy) == 0) {
            device->handle = (struct nvmlDevice_st*)(uintptr_t)(i + 1);
            return NVML_SUCCESS;
        }
    }
    
    return NVML_ERROR_NOT_FOUND;
}

// Get device name
nvmlReturn_t DECLDIR nvmlDeviceGetName(nvmlDevice_t device, char *name, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (name == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const char *device_name = mock_devices[index].name;
    
    if (length < strlen(device_name) + 1) {
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    strncpy(name, device_name, length);
    name[length - 1] = '\0';
    
    return NVML_SUCCESS;
}

// Get device UUID
nvmlReturn_t DECLDIR nvmlDeviceGetUUID(nvmlDevice_t device, char *uuid, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (uuid == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const char *device_uuid = mock_devices[index].uuid;
    
    if (length < strlen(device_uuid) + 1) {
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    strncpy(uuid, device_uuid, length);
    uuid[length - 1] = '\0';
    
    return NVML_SUCCESS;
}

// Get device PCI info
nvmlReturn_t DECLDIR nvmlDeviceGetPciInfo_v3(nvmlDevice_t device, nvmlPciInfo_t *pci) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (pci == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const mock_device_info_t *dev = &mock_devices[index];
    
    pci->domain = dev->pci_domain;
    pci->bus = dev->pci_bus;
    pci->device = dev->pci_device;
    pci->pciDeviceId = dev->pci_device_id;
    pci->pciSubSystemId = dev->pci_subsystem_id;
    
    strncpy(pci->busId, dev->pci_bus_id_legacy, sizeof(pci->busId));
    pci->busId[sizeof(pci->busId) - 1] = '\0';
    
    strncpy(pci->busIdLegacy, dev->pci_bus_id_legacy, sizeof(pci->busIdLegacy));
    pci->busIdLegacy[sizeof(pci->busIdLegacy) - 1] = '\0';
    
    return NVML_SUCCESS;
}

// Get device minor number
nvmlReturn_t DECLDIR nvmlDeviceGetMinorNumber(nvmlDevice_t device, unsigned int *minorNumber) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (minorNumber == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *minorNumber = mock_devices[index].minor_number;
    
    return NVML_SUCCESS;
}

// Get device index
nvmlReturn_t DECLDIR nvmlDeviceGetIndex(nvmlDevice_t device, unsigned int *index) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (index == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int dev_index = device_handle_to_index(device);
    if (dev_index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *index = dev_index;
    
    return NVML_SUCCESS;
}

// Get device serial
nvmlReturn_t DECLDIR nvmlDeviceGetSerial(nvmlDevice_t device, char *serial, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (serial == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const char *device_serial = mock_devices[index].serial;
    
    if (length < strlen(device_serial) + 1) {
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    strncpy(serial, device_serial, length);
    serial[length - 1] = '\0';
    
    return NVML_SUCCESS;
}

// Get device brand
nvmlReturn_t DECLDIR nvmlDeviceGetBrand(nvmlDevice_t device, nvmlBrandType_t *type) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (type == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *type = mock_devices[index].brand;
    
    return NVML_SUCCESS;
}

// Get device persistence mode
nvmlReturn_t DECLDIR nvmlDeviceGetPersistenceMode(nvmlDevice_t device, nvmlEnableState_t *mode) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (mode == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *mode = mock_devices[index].persistence_mode;
    
    return NVML_SUCCESS;
}

// Get device display mode
nvmlReturn_t DECLDIR nvmlDeviceGetDisplayMode(nvmlDevice_t device, nvmlEnableState_t *display) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (display == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *display = mock_devices[index].display_mode;
    
    return NVML_SUCCESS;
}

// Get device display active
nvmlReturn_t DECLDIR nvmlDeviceGetDisplayActive(nvmlDevice_t device, nvmlEnableState_t *isActive) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (isActive == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *isActive = mock_devices[index].display_active;
    
    return NVML_SUCCESS;
}

// Get device board part number
nvmlReturn_t DECLDIR nvmlDeviceGetBoardPartNumber(nvmlDevice_t device, char* partNumber, unsigned int length) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (partNumber == NULL || length == 0) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    const char *part = "699-21001-0000-000";
    
    if (length < strlen(part) + 1) {
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    strncpy(partNumber, part, length);
    partNumber[length - 1] = '\0';
    
    return NVML_SUCCESS;
}

// Get device attributes
nvmlReturn_t DECLDIR nvmlDeviceGetAttributes_v2(nvmlDevice_t device, nvmlDeviceAttributes_t *attributes) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (attributes == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Version field doesn't exist in the struct, skip validation
    
    // Fill in attributes
    attributes->multiprocessorCount = 108;  // A100 has 108 SMs
    attributes->sharedCopyEngineCount = 5;
    attributes->sharedDecoderCount = 0;
    attributes->sharedEncoderCount = 0;
    attributes->sharedJpegCount = 0;
    attributes->sharedOfaCount = 0;
    attributes->gpuInstanceSliceCount = 0;  // No MIG support in mock
    attributes->computeInstanceSliceCount = 0;
    attributes->memorySizeMB = 40960;  // 40GB
    
    return NVML_SUCCESS;
}

// Additional PCI info functions
nvmlReturn_t DECLDIR nvmlDeviceGetPciInfo_v2(nvmlDevice_t device, nvmlPciInfo_t *pci) {
    return nvmlDeviceGetPciInfo_v3(device, pci);
}

nvmlReturn_t DECLDIR nvmlDeviceGetPciInfo(nvmlDevice_t device, nvmlPciInfo_t *pci) {
    return nvmlDeviceGetPciInfo_v3(device, pci);
}

// Get CUDA compute capability
nvmlReturn_t DECLDIR nvmlDeviceGetCudaComputeCapability(nvmlDevice_t device, int *major, int *minor) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (!is_valid_device_handle(device)) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (major == NULL || minor == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int idx = device_handle_to_index(device);
    *major = mock_devices[idx].cuda_compute_capability_major;
    *minor = mock_devices[idx].cuda_compute_capability_minor;
    
    return NVML_SUCCESS;
}

// Legacy v1 functions
nvmlReturn_t DECLDIR nvmlDeviceGetCount(unsigned int *deviceCount) {
    return nvmlDeviceGetCount_v2(deviceCount);
}

nvmlReturn_t DECLDIR nvmlDeviceGetHandleByIndex(unsigned int index, nvmlDevice_t *device) {
    return nvmlDeviceGetHandleByIndex_v2(index, device);
}

nvmlReturn_t DECLDIR nvmlDeviceGetHandleByPciBusId(const char *pciBusId, nvmlDevice_t *device) {
    return nvmlDeviceGetHandleByPciBusId_v2(pciBusId, device);
}
