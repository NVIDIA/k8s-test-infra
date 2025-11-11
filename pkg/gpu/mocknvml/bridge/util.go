// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

/*
#include <stdlib.h>

typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS                0
#define NVML_ERROR_INVALID_ARGUMENT 2
#define NVML_ERROR_NOT_SUPPORTED    3

typedef enum {
    NVML_TEMPERATURE_GPU = 0
} nvmlTemperatureSensors_t;

typedef enum {
    NVML_CLOCK_GRAPHICS = 0,
    NVML_CLOCK_SM       = 1,
    NVML_CLOCK_MEM      = 2
} nvmlClockType_t;
*/
import "C"
import (
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetTemperature
func nvmlDeviceGetTemperature(device C.nvmlDevice_t, sensorType C.nvmlTemperatureSensors_t, temp *C.uint) C.nvmlReturn_t {
	if temp == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return static temperature (30°C)
	*temp = 30
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerUsage
func nvmlDeviceGetPowerUsage(device C.nvmlDevice_t, power *C.uint) C.nvmlReturn_t {
	if power == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return static power usage (250W = 250000 mW)
	*power = 250000
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerManagementLimit
func nvmlDeviceGetPowerManagementLimit(device C.nvmlDevice_t, limit *C.uint) C.nvmlReturn_t {
	if limit == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return static power limit (400W = 400000 mW for A100)
	*limit = 400000
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetClock
func nvmlDeviceGetClock(device C.nvmlDevice_t, clockType C.nvmlClockType_t, clockId C.int, clock *C.uint) C.nvmlReturn_t {
	if clock == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return static clock speeds based on type
	switch clockType {
	case C.NVML_CLOCK_GRAPHICS:
		*clock = 1410 // MHz
	case C.NVML_CLOCK_SM:
		*clock = 1410 // MHz
	case C.NVML_CLOCK_MEM:
		*clock = 1215 // MHz
	default:
		*clock = 0
	}

	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMaxClockInfo
func nvmlDeviceGetMaxClockInfo(device C.nvmlDevice_t, clockType C.nvmlClockType_t, clock *C.uint) C.nvmlReturn_t {
	if clock == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return max clock speeds based on type
	switch clockType {
	case C.NVML_CLOCK_GRAPHICS:
		*clock = 1410 // MHz
	case C.NVML_CLOCK_SM:
		*clock = 1410 // MHz
	case C.NVML_CLOCK_MEM:
		*clock = 1215 // MHz
	default:
		*clock = 0
	}

	return C.NVML_SUCCESS
}

//export nvmlDeviceGetFanSpeed
func nvmlDeviceGetFanSpeed(device C.nvmlDevice_t, speed *C.uint) C.nvmlReturn_t {
	if speed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// A100 doesn't have fans (passive cooling)
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlDeviceGetPerformanceState
func nvmlDeviceGetPerformanceState(device C.nvmlDevice_t, pState *C.int) C.nvmlReturn_t {
	if pState == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return P0 (maximum performance)
	*pState = 0
	return C.NVML_SUCCESS
}

