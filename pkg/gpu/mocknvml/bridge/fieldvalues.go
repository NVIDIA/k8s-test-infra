// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

// Package main: nvmlDeviceGetFieldValues bridge. nvidia-smi enumerates the
// NVLink subcommands (`nvlink -s/-c/-e`) by first reading
// NVML_FI_DEV_NVLINK_LINK_COUNT via this API; while it was an unimplemented
// stub (NVML_ERROR_NOT_SUPPORTED) nvidia-smi saw zero NVLinks and printed
// nothing. This marshals the request array and fills the NVLink field set
// from the immutable NodeFabric (see engine/nvlink_fields.go).

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "nvml_types.h"

// nvmlValueType_t enumerators (subset) used to tag the union member written.
#define MOCK_NVML_VALUE_TYPE_DOUBLE             0
#define MOCK_NVML_VALUE_TYPE_UNSIGNED_INT       1
#define MOCK_NVML_VALUE_TYPE_UNSIGNED_LONG_LONG 3

// Accessors/mutators for nvmlFieldValue_t. CGo represents the nvmlValue_t
// union as an opaque byte array, so the union members are written here in C
// rather than from Go.
static unsigned int fvFieldId(nvmlFieldValue_t *fv)  { return fv->fieldId; }
static unsigned int fvScopeId(nvmlFieldValue_t *fv)  { return fv->scopeId; }
static void fvSetTimestamp(nvmlFieldValue_t *fv, long long ts) { fv->timestamp = ts; }
static void fvSetReturn(nvmlFieldValue_t *fv, nvmlReturn_t r)  { fv->nvmlReturn = r; }
static void fvSetUInt(nvmlFieldValue_t *fv, unsigned int v) {
	fv->valueType = MOCK_NVML_VALUE_TYPE_UNSIGNED_INT;
	fv->value.uiVal = v;
}
static void fvSetULL(nvmlFieldValue_t *fv, unsigned long long v) {
	fv->valueType = MOCK_NVML_VALUE_TYPE_UNSIGNED_LONG_LONG;
	fv->value.ullVal = v;
}
// fvSetDoubleBits writes the raw IEEE-754 bits into the shared union and tags
// the value as a double; nvidia-smi reads value.dVal back from the same bytes.
static void fvSetDoubleBits(nvmlFieldValue_t *fv, unsigned long long bits) {
	fv->valueType = MOCK_NVML_VALUE_TYPE_DOUBLE;
	fv->value.ullVal = bits;
}
*/
import "C"
import (
	"time"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetFieldValues
func nvmlDeviceGetFieldValues(device C.nvmlDevice_t, valuesCount C.int, values *C.nvmlFieldValue_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetFieldValues"); !ok {
		return ret
	}
	if values == nil || valuesCount <= 0 {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	fields := unsafe.Slice(values, int(valuesCount))
	ts := C.longlong(time.Now().UnixMicro())
	for i := range fields {
		fv := &fields[i]
		fieldID := uint32(C.fvFieldId(fv))
		scopeID := uint32(C.fvScopeId(fv))
		C.fvSetTimestamp(fv, ts)

		vt, val, ret := dev.GetNvLinkFieldValue(fieldID, scopeID)
		if ret != nvml.SUCCESS {
			C.fvSetReturn(fv, toReturn(ret))
			continue
		}
		switch vt {
		case engine.NVLinkFieldUint:
			C.fvSetUInt(fv, C.uint(val))
		case engine.NVLinkFieldUint64:
			C.fvSetULL(fv, C.ulonglong(val))
		case engine.NVLinkFieldDouble:
			C.fvSetDoubleBits(fv, C.ulonglong(val))
		default:
			C.fvSetReturn(fv, C.NVML_ERROR_NOT_SUPPORTED)
			continue
		}
		C.fvSetReturn(fv, C.NVML_SUCCESS)
	}
	debugLog("[NVML] nvmlDeviceGetFieldValues(count=%d) -> filled\n", int(valuesCount))
	return C.NVML_SUCCESS
}
