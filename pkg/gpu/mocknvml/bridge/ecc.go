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

// Package main provides the NVML SRAM ECC and row-remap availability
// functions. Both were generated stubs, which made every SRAM row of
// `nvidia-smi -q` and the bank availability histogram report N/A — a consumer
// could not tell a healthy GPU from one whose SRAM the mock simply refused to
// describe. See issue NVIDIA/k8s-test-infra#641.
package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// sramEccErrorStatusVersion is the tag a caller must stamp into
// nvmlEccSramErrorStatus_t.version, encoding both the struct version and its
// size the way the upstream NVML_STRUCT_VERSION macro does. Requiring an exact
// match is what keeps the writes below inside the caller's allocation: a
// caller built against a differently sized layout gets
// ARGUMENT_VERSION_MISMATCH rather than a buffer overrun.
func sramEccErrorStatusVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlEccSramErrorStatus_t{}), 1)
}

//export nvmlDeviceGetSramEccErrorStatus
func nvmlDeviceGetSramEccErrorStatus(device C.nvmlDevice_t, status *C.nvmlEccSramErrorStatus_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetSramEccErrorStatus"); !ok {
		return ret
	}
	if status == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(status.version) != sramEccErrorStatusVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	handle := unsafe.Pointer(device.handle)
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	sram, ret := dev.GetSramEccErrorStatus()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	status.aggregateUncParity = C.ulonglong(sram.AggregateUncParity)
	status.aggregateUncSecDed = C.ulonglong(sram.AggregateUncSecDed)
	status.aggregateCor = C.ulonglong(sram.AggregateCor)
	status.volatileUncParity = C.ulonglong(sram.VolatileUncParity)
	status.volatileUncSecDed = C.ulonglong(sram.VolatileUncSecDed)
	status.volatileCor = C.ulonglong(sram.VolatileCor)
	status.aggregateUncBucketL2 = C.ulonglong(sram.AggregateUncBucketL2)
	status.aggregateUncBucketSm = C.ulonglong(sram.AggregateUncBucketSm)
	status.aggregateUncBucketPcie = C.ulonglong(sram.AggregateUncBucketPcie)
	status.aggregateUncBucketMcu = C.ulonglong(sram.AggregateUncBucketMcu)
	status.aggregateUncBucketOther = C.ulonglong(sram.AggregateUncBucketOther)
	status.bThresholdExceeded = C.uint(sram.BThresholdExceeded)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetRowRemapperHistogram
func nvmlDeviceGetRowRemapperHistogram(device C.nvmlDevice_t, values *C.nvmlRowRemapperHistogramValues_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetRowRemapperHistogram"); !ok {
		return ret
	}
	if values == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := unsafe.Pointer(device.handle)
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	histogram, ret := dev.GetRowRemapperHistogram()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	values.max = C.uint(histogram.Max)
	values.high = C.uint(histogram.High)
	values.partial = C.uint(histogram.Partial)
	values.low = C.uint(histogram.Low)
	values.none = C.uint(histogram.None)
	return C.NVML_SUCCESS
}
