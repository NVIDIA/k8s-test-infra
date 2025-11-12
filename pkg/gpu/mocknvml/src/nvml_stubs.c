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

// Process information functions - return empty lists
nvmlReturn_t DECLDIR nvmlDeviceGetComputeRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount, 
                                                             nvmlProcessInfo_v2_t *infos) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (infoCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // No processes in mock
    *infoCount = 0;
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlDeviceGetGraphicsRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount,
                                                              nvmlProcessInfo_v2_t *infos) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (infoCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // No processes in mock
    *infoCount = 0;
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlDeviceGetMPSComputeRunningProcesses_v3(nvmlDevice_t device, unsigned int *infoCount,
                                                                nvmlProcessInfo_v2_t *infos) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (infoCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // No processes in mock
    *infoCount = 0;
    return NVML_SUCCESS;
}

// Unit functions - no units in mock
nvmlReturn_t DECLDIR nvmlUnitGetCount(unsigned int *unitCount) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (unitCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *unitCount = 0;
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlUnitGetHandleByIndex(unsigned int index, nvmlUnit_t *unit) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    // No units available
    return NVML_ERROR_INVALID_ARGUMENT;
}

// Get supported event types - return no events supported for mock
nvmlReturn_t DECLDIR nvmlDeviceGetSupportedEventTypes(nvmlDevice_t device, unsigned long long *eventTypes) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (!is_valid_device_handle(device)) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (eventTypes == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // No events supported in mock
    *eventTypes = 0;
    
    return NVML_SUCCESS;
}

// Register events for a device - mock accepts but doesn't actually monitor
nvmlReturn_t DECLDIR nvmlDeviceRegisterEvents(nvmlDevice_t device, unsigned long long eventTypes, nvmlEventSet_t set) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (!is_valid_device_handle(device)) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // In mock, we don't actually register any events but return success
    // if no events are requested (eventTypes == 0)
    if (eventTypes != 0) {
        // Real NVML would check if the requested events are supported
        return NVML_ERROR_NOT_SUPPORTED;
    }
    
    return NVML_SUCCESS;
}

// Event set functions
nvmlReturn_t DECLDIR nvmlEventSetCreate(nvmlEventSet_t *set) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (set == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Create a dummy event set handle
    // Just use a non-NULL value as handle
    set->handle = (void*)(uintptr_t)0xDEADBEEF;
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlEventSetWait_v2(nvmlEventSet_t set, nvmlEventData_t *data, unsigned int timeoutms) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (data == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Always timeout in mock
    return NVML_ERROR_TIMEOUT;
}

nvmlReturn_t DECLDIR nvmlEventSetFree(nvmlEventSet_t set) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    return NVML_SUCCESS;
}

// Device modification functions - accept but do nothing
nvmlReturn_t DECLDIR nvmlDeviceSetPersistenceMode(nvmlDevice_t device, nvmlEnableState_t mode) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (mode != NVML_FEATURE_DISABLED && mode != NVML_FEATURE_ENABLED) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Accept but don't actually change
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlDeviceSetComputeMode(nvmlDevice_t device, nvmlComputeMode_t mode) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // Accept but don't actually change
    return NVML_SUCCESS;
}

// Get NvLink state - for mock, report all links as active
nvmlReturn_t DECLDIR nvmlDeviceGetNvLinkState(nvmlDevice_t device, unsigned int link, nvmlEnableState_t *isActive) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (!is_valid_device_handle(device)) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (isActive == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // A100 supports NVLink, mock as active for all valid links
    // A100 typically has 12 NVLink connections
    if (link >= 12) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *isActive = NVML_FEATURE_ENABLED;
    
    return NVML_SUCCESS;
}

// Get remote PCI info for NvLink connection
nvmlReturn_t DECLDIR nvmlDeviceGetNvLinkRemotePciInfo_v2(nvmlDevice_t device, unsigned int link, nvmlPciInfo_t *pci) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (!is_valid_device_handle(device)) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    if (pci == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // A100 typically has 12 NVLink connections
    if (link >= 12) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int idx = device_handle_to_index(device);
    
    // For mock, simulate NVLink connections in a ring topology
    // Each GPU is connected to the next GPU in the ring
    unsigned int remote_idx = (idx + link / 2 + 1) % 8;
    
    // Get remote device info
    const mock_device_info_t *remote_device = &mock_devices[remote_idx];
    
    // Fill in PCI info for the remote device
    strncpy(pci->busIdLegacy, remote_device->pci_bus_id_legacy, NVML_DEVICE_PCI_BUS_ID_BUFFER_V2_SIZE);
    pci->busIdLegacy[NVML_DEVICE_PCI_BUS_ID_BUFFER_V2_SIZE - 1] = '\0';
    
    strncpy(pci->busId, remote_device->pci_bus_id, NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE);
    pci->busId[NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE - 1] = '\0';
    
    pci->domain = remote_device->pci_domain;
    pci->bus = remote_device->pci_bus;
    pci->device = remote_device->pci_device;
    pci->pciDeviceId = remote_device->pci_device_id;
    pci->pciSubSystemId = remote_device->pci_subsystem_id;
    
    return NVML_SUCCESS;
}

// Legacy compatibility
nvmlReturn_t DECLDIR nvmlDeviceGetNvLinkRemotePciInfo(nvmlDevice_t device, unsigned int link, nvmlPciInfo_t *pci) {
    return nvmlDeviceGetNvLinkRemotePciInfo_v2(device, link, pci);
}

// Topology functions
nvmlReturn_t DECLDIR nvmlDeviceGetTopologyCommonAncestor(nvmlDevice_t device1, nvmlDevice_t device2,
                                                         nvmlGpuTopologyLevel_t *pathInfo) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (pathInfo == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index1 = device_handle_to_index(device1);
    unsigned int index2 = device_handle_to_index(device2);
    
    if (index1 == (unsigned int)-1 || index2 == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // All devices on same system in mock
    *pathInfo = NVML_TOPOLOGY_SYSTEM;
    
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlDeviceGetTopologyNearestGpus(nvmlDevice_t device, nvmlGpuTopologyLevel_t level,
                                                      unsigned int *count, nvmlDevice_t *deviceArray) {
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
    
    // Return all other GPUs
    if (deviceArray == NULL) {
        *count = 7;  // All except self
        return NVML_SUCCESS;
    }
    
    if (*count < 7) {
        *count = 7;
        return NVML_ERROR_INSUFFICIENT_SIZE;
    }
    
    // Fill with all other devices
    unsigned int j = 0;
    for (unsigned int i = 0; i < 8; i++) {
        if (i != index) {
            deviceArray[j].handle = (struct nvmlDevice_st*)(uintptr_t)(i + 1);
            j++;
        }
    }
    
    *count = 7;
    return NVML_SUCCESS;
}

// P2P capabilities
nvmlReturn_t DECLDIR nvmlDeviceGetP2PStatus(nvmlDevice_t device1, nvmlDevice_t device2, 
                                           nvmlGpuP2PCapsIndex_t p2pIndex, nvmlGpuP2PStatus_t *p2pStatus) {
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (p2pStatus == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index1 = device_handle_to_index(device1);
    unsigned int index2 = device_handle_to_index(device2);
    
    if (index1 == (unsigned int)-1 || index2 == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    // All GPUs can do P2P in mock
    *p2pStatus = NVML_P2P_STATUS_OK;
    
    return NVML_SUCCESS;
}

// Legacy compatibility
nvmlReturn_t DECLDIR nvmlDeviceGetComputeRunningProcesses(nvmlDevice_t device, unsigned int *infoCount,
                                                          nvmlProcessInfo_v1_t *infos) {
    UNUSED(infos);
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (infoCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *infoCount = 0;
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlDeviceGetComputeRunningProcesses_v2(nvmlDevice_t device, unsigned int *infoCount,
                                                             nvmlProcessInfo_v2_t *infos) {
    UNUSED(infos);
    if (!nvml_is_initialized()) {
        return NVML_ERROR_UNINITIALIZED;
    }
    
    if (infoCount == NULL) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    unsigned int index = device_handle_to_index(device);
    if (index == (unsigned int)-1) {
        return NVML_ERROR_INVALID_ARGUMENT;
    }
    
    *infoCount = 0;
    return NVML_SUCCESS;
}

nvmlReturn_t DECLDIR nvmlEventSetWait(nvmlEventSet_t set, nvmlEventData_t *data, unsigned int timeoutms) {
    return nvmlEventSetWait_v2(set, data, timeoutms);
}
