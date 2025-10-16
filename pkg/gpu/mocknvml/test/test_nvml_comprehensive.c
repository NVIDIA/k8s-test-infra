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
#include <assert.h>
#include <pthread.h>
#include <unistd.h>
#include "../include/nvml.h"

#define TEST_PASS(name) printf("✓ %s\n", name)
#define TEST_FAIL(name, msg) do { \
    printf("✗ %s: %s\n", name, msg); \
    g_test_failures++; \
} while(0)

#define ASSERT_EQ(actual, expected, test_name) do { \
    if ((actual) != (expected)) { \
        char msg[256]; \
        snprintf(msg, sizeof(msg), "Expected %d, got %d", (int)(expected), (int)(actual)); \
        TEST_FAIL(test_name, msg); \
        return; \
    } \
} while(0)

#define ASSERT_STR_EQ(actual, expected, test_name) do { \
    if (strcmp((actual), (expected)) != 0) { \
        char msg[256]; \
        snprintf(msg, sizeof(msg), "Expected '%s', got '%s'", (expected), (actual)); \
        TEST_FAIL(test_name, msg); \
        return; \
    } \
} while(0)

#define ASSERT_NOT_NULL(ptr, test_name) do { \
    if ((ptr) == NULL) { \
        TEST_FAIL(test_name, "Unexpected NULL pointer"); \
        return; \
    } \
} while(0)

static int g_test_failures = 0;

// Test initialization and shutdown
void test_init_shutdown() {
    const char* test_name = "test_init_shutdown";
    
    // Test normal init/shutdown
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlShutdown();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test shutdown without init
    ret = nvmlShutdown();
    ASSERT_EQ(ret, NVML_ERROR_UNINITIALIZED, test_name);
    
    TEST_PASS(test_name);
}

// Test reference counting
void test_reference_counting() {
    const char* test_name = "test_reference_counting";
    
    // Multiple inits should succeed
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Should need 3 shutdowns
    ret = nvmlShutdown();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlShutdown();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlShutdown();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Now should be uninitialized
    ret = nvmlShutdown();
    ASSERT_EQ(ret, NVML_ERROR_UNINITIALIZED, test_name);
    
    TEST_PASS(test_name);
}

// Test system info functions
void test_system_info() {
    const char* test_name = "test_system_info";
    char buffer[256];
    int cuda_version;
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test driver version
    ret = nvmlSystemGetDriverVersion(buffer, sizeof(buffer));
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_STR_EQ(buffer, "550.54.15", test_name);
    
    // Test NVML version
    ret = nvmlSystemGetNVMLVersion(buffer, sizeof(buffer));
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_STR_EQ(buffer, "12.550.54", test_name);
    
    // Test CUDA driver version
    ret = nvmlSystemGetCudaDriverVersion(&cuda_version);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(cuda_version, 12040, test_name);
    
    // Test with NULL pointers
    ret = nvmlSystemGetDriverVersion(NULL, 10);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    // Test with small buffer
    ret = nvmlSystemGetDriverVersion(buffer, 5);
    ASSERT_EQ(ret, NVML_ERROR_INSUFFICIENT_SIZE, test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Test device enumeration
void test_device_enumeration() {
    const char* test_name = "test_device_enumeration";
    unsigned int count;
    nvmlDevice_t device;
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test device count
    ret = nvmlDeviceGetCount(&count);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(count, 8, test_name);
    
    // Test getting device handles
    for (unsigned int i = 0; i < count; i++) {
        ret = nvmlDeviceGetHandleByIndex(i, &device);
        ASSERT_EQ(ret, NVML_SUCCESS, test_name);
        ASSERT_NOT_NULL(device, test_name);
    }
    
    // Test invalid index
    ret = nvmlDeviceGetHandleByIndex(count, &device);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    // Test NULL arguments
    ret = nvmlDeviceGetCount(NULL);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    ret = nvmlDeviceGetHandleByIndex(0, NULL);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Test device properties
void test_device_properties() {
    const char* test_name = "test_device_properties";
    nvmlDevice_t device;
    char name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
    char uuid[NVML_DEVICE_UUID_V2_BUFFER_SIZE];
    nvmlPciInfo_t pci_info;
    unsigned int minor;
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Get first device
    ret = nvmlDeviceGetHandleByIndex(0, &device);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test device name
    ret = nvmlDeviceGetName(device, name, sizeof(name));
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_STR_EQ(name, "NVIDIA A100-SXM4-40GB", test_name);
    
    // Test UUID
    ret = nvmlDeviceGetUUID(device, uuid, sizeof(uuid));
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_NOT_NULL(strstr(uuid, "GPU-"), test_name);
    
    // Test PCI info
    ret = nvmlDeviceGetPciInfo_v3(device, &pci_info);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(pci_info.domain, 0x0000, test_name);
    
    // Test minor number
    ret = nvmlDeviceGetMinorNumber(device, &minor);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(minor, 0, test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Test memory info
void test_memory_info() {
    const char* test_name = "test_memory_info";
    nvmlDevice_t device;
    nvmlMemory_t memory;
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlDeviceGetHandleByIndex(0, &device);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test memory info
    ret = nvmlDeviceGetMemoryInfo(device, &memory);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // A100 40GB = 42949672960 bytes
    ASSERT_EQ(memory.total, 42949672960ULL, test_name);
    ASSERT_EQ(memory.free, memory.total, test_name);
    ASSERT_EQ(memory.used, 0, test_name);
    
    // Test NULL argument
    ret = nvmlDeviceGetMemoryInfo(device, NULL);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Test CUDA compute capability
void test_cuda_capability() {
    const char* test_name = "test_cuda_capability";
    nvmlDevice_t device;
    int major, minor;
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlDeviceGetHandleByIndex(0, &device);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test compute capability
    ret = nvmlDeviceGetCudaComputeCapability(device, &major, &minor);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(major, 8, test_name);  // A100 is compute 8.0
    ASSERT_EQ(minor, 0, test_name);
    
    // Test NULL arguments
    ret = nvmlDeviceGetCudaComputeCapability(device, NULL, &minor);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    ret = nvmlDeviceGetCudaComputeCapability(device, &major, NULL);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Test process info
void test_process_info() {
    const char* test_name = "test_process_info";
    nvmlDevice_t device;
    nvmlProcessInfo_v2_t processes[10];
    unsigned int count;
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlDeviceGetHandleByIndex(0, &device);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test compute processes (should be empty)
    count = 10;
    ret = nvmlDeviceGetComputeRunningProcesses_v3(device, &count, processes);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(count, 0, test_name);
    
    // Test graphics processes (should be empty)
    count = 10;
    ret = nvmlDeviceGetGraphicsRunningProcesses_v3(device, &count, processes);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(count, 0, test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Test error strings
void test_error_strings() {
    const char* test_name = "test_error_strings";
    
    const char* str = nvmlErrorString(NVML_SUCCESS);
    ASSERT_STR_EQ(str, "Success", test_name);
    
    str = nvmlErrorString(NVML_ERROR_UNINITIALIZED);
    ASSERT_NOT_NULL(strstr(str, "not first initialized"), test_name);
    
    str = nvmlErrorString(NVML_ERROR_INVALID_ARGUMENT);
    ASSERT_STR_EQ(str, "Invalid argument", test_name);
    
    str = nvmlErrorString(999999);
    ASSERT_STR_EQ(str, "Unknown error", test_name);
    
    TEST_PASS(test_name);
}

// Test thread safety
void* thread_init_shutdown(void* arg) {
    int thread_id = *(int*)arg;
    
    for (int i = 0; i < 100; i++) {
        nvmlReturn_t ret = nvmlInit();
        if (ret != NVML_SUCCESS) {
            printf("Thread %d: Init failed at iteration %d\n", thread_id, i);
            return NULL;
        }
        
        // Do some work
        unsigned int count;
        nvmlDeviceGetCount(&count);
        
        ret = nvmlShutdown();
        if (ret != NVML_SUCCESS) {
            printf("Thread %d: Shutdown failed at iteration %d\n", thread_id, i);
            return NULL;
        }
    }
    
    return NULL;
}

void test_thread_safety() {
    const char* test_name = "test_thread_safety";
    const int num_threads = 10;
    pthread_t threads[num_threads];
    int thread_ids[num_threads];
    
    // Create threads
    for (int i = 0; i < num_threads; i++) {
        thread_ids[i] = i;
        if (pthread_create(&threads[i], NULL, thread_init_shutdown, &thread_ids[i]) != 0) {
            TEST_FAIL(test_name, "Failed to create thread");
            return;
        }
    }
    
    // Wait for threads
    for (int i = 0; i < num_threads; i++) {
        pthread_join(threads[i], NULL);
    }
    
    // Verify we're back to uninitialized state
    nvmlReturn_t ret = nvmlShutdown();
    ASSERT_EQ(ret, NVML_ERROR_UNINITIALIZED, test_name);
    
    TEST_PASS(test_name);
}

// Test uninitialized access
void test_uninitialized_access() {
    const char* test_name = "test_uninitialized_access";
    unsigned int count;
    nvmlDevice_t device;
    char buffer[256];
    
    // Ensure we're uninitialized
    nvmlShutdown();
    
    // All functions should return NVML_ERROR_UNINITIALIZED
    nvmlReturn_t ret = nvmlDeviceGetCount(&count);
    ASSERT_EQ(ret, NVML_ERROR_UNINITIALIZED, test_name);
    
    ret = nvmlDeviceGetHandleByIndex(0, &device);
    ASSERT_EQ(ret, NVML_ERROR_UNINITIALIZED, test_name);
    
    ret = nvmlSystemGetDriverVersion(buffer, sizeof(buffer));
    ASSERT_EQ(ret, NVML_ERROR_UNINITIALIZED, test_name);
    
    TEST_PASS(test_name);
}

// Test device handle validation
void test_device_handle_validation() {
    const char* test_name = "test_device_handle_validation";
    nvmlDevice_t device;
    nvmlDevice_t invalid_device;
    char name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Get valid device
    ret = nvmlDeviceGetHandleByIndex(0, &device);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Create invalid device handle
    invalid_device = (nvmlDevice_t)((uintptr_t)device + 100);
    
    // Test with invalid handle
    ret = nvmlDeviceGetName(invalid_device, name, sizeof(name));
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    // Test with NULL handle
    ret = nvmlDeviceGetName(NULL, name, sizeof(name));
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Test NVLink functionality
void test_nvlink() {
    const char* test_name = "test_nvlink";
    nvmlDevice_t device;
    nvmlEnableState_t state;
    nvmlPciInfo_t remote_pci;
    
    nvmlReturn_t ret = nvmlInit();
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    ret = nvmlDeviceGetHandleByIndex(0, &device);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    
    // Test NVLink state
    ret = nvmlDeviceGetNvLinkState(device, 0, &state);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_EQ(state, NVML_FEATURE_ENABLED, test_name);
    
    // Test invalid link
    ret = nvmlDeviceGetNvLinkState(device, 12, &state);
    ASSERT_EQ(ret, NVML_ERROR_INVALID_ARGUMENT, test_name);
    
    // Test remote PCI info
    ret = nvmlDeviceGetNvLinkRemotePciInfo_v2(device, 0, &remote_pci);
    ASSERT_EQ(ret, NVML_SUCCESS, test_name);
    ASSERT_NOT_NULL(strstr(remote_pci.busId, "0000:"), test_name);
    
    nvmlShutdown();
    TEST_PASS(test_name);
}

// Main test runner
int main() {
    printf("=== Mock NVML Unit Tests ===\n\n");
    
    // Run all tests
    test_init_shutdown();
    test_reference_counting();
    test_system_info();
    test_device_enumeration();
    test_device_properties();
    test_memory_info();
    test_cuda_capability();
    test_process_info();
    test_error_strings();
    test_thread_safety();
    test_uninitialized_access();
    test_device_handle_validation();
    test_nvlink();
    
    // Summary
    printf("\n=== Test Summary ===\n");
    if (g_test_failures == 0) {
        printf("All tests passed! ✓\n");
        return 0;
    } else {
        printf("%d tests failed ✗\n", g_test_failures);
        return 1;
    }
}
