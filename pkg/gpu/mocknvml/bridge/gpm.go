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

// Package main: GPM (GPU Performance Monitoring) bridge. DCGM's profiling
// module reads DCGM_FI_PROF_* on Hopper+ exclusively through this API:
// nvmlGpmSampleAlloc/Get twice ~100ms apart, then nvmlGpmMetricsGet over the
// pair. A sample handle is an 8-byte C allocation holding an engine registry
// key — cgo forbids storing Go pointers in C-visible memory, so the snapshot
// itself lives in the engine (see engine/gpm.go).

package main

/*
#include <stdlib.h>
#include "nvml_types.h"

// The sample handle cell. nvmlGpmSample_t wraps a single pointer; its .handle
// points at an 8-byte allocation carrying the engine registry key.
static nvmlGpmSample_t gpmKeyToSample(unsigned long long *cell) {
	nvmlGpmSample_t sample = { .handle = (struct nvmlGpmSample_st *)cell };
	return sample;
}
static int gpmSampleIsNull(nvmlGpmSample_t sample) {
	return sample.handle == NULL;
}
static void *gpmSampleHandle(nvmlGpmSample_t sample) {
	return sample.handle;
}
static unsigned long long gpmSampleToKey(nvmlGpmSample_t sample) {
	return *(unsigned long long *)sample.handle;
}

// Accessors for nvmlGpmMetricsGet_t: CGo cannot index the metrics array of a
// struct received by pointer without materializing the whole 333-entry value,
// so the per-entry reads/writes happen in C.
static unsigned int gpmMetricId(nvmlGpmMetricsGet_t *mg, unsigned int i) {
	return mg->metrics[i].metricId;
}
static void gpmSetMetric(nvmlGpmMetricsGet_t *mg, unsigned int i, nvmlReturn_t ret, double value) {
	mg->metrics[i].nvmlReturn = ret;
	mg->metrics[i].value = value;
	mg->metrics[i].metricInfo.shortName = NULL;
	mg->metrics[i].metricInfo.longName = NULL;
	mg->metrics[i].metricInfo.unit = NULL;
}
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlGpmSampleAlloc
func nvmlGpmSampleAlloc(gpmSample *C.nvmlGpmSample_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpmSampleAlloc"); !ok {
		return ret
	}
	if gpmSample == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	cell := (*C.ulonglong)(C.malloc(C.size_t(unsafe.Sizeof(C.ulonglong(0)))))
	if cell == nil {
		return C.NVML_ERROR_MEMORY
	}
	*cell = C.ulonglong(engine.GpmSampleAlloc())
	*gpmSample = C.gpmKeyToSample(cell)
	return C.NVML_SUCCESS
}

//export nvmlGpmSampleFree
func nvmlGpmSampleFree(gpmSample C.nvmlGpmSample_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpmSampleFree"); !ok {
		return ret
	}
	if C.gpmSampleIsNull(gpmSample) != 0 {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// A double free reads the already-freed cell before the registry check
	// below rejects the key — same UB contract as real NVML, whose opaque
	// sample is also a heap allocation the caller must free exactly once.
	if !engine.GpmSampleFree(uint64(C.gpmSampleToKey(gpmSample))) {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	C.free(C.gpmSampleHandle(gpmSample))
	return C.NVML_SUCCESS
}

//export nvmlGpmSampleGet
func nvmlGpmSampleGet(device C.nvmlDevice_t, gpmSample C.nvmlGpmSample_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpmSampleGet"); !ok {
		return ret
	}
	if C.gpmSampleIsNull(gpmSample) != 0 {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.GpmSnapshotInto(uint64(C.gpmSampleToKey(gpmSample))))
}

//export nvmlGpmMetricsGet
func nvmlGpmMetricsGet(metricsGet *C.nvmlGpmMetricsGet_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpmMetricsGet"); !ok {
		return ret
	}
	if metricsGet == nil || C.gpmSampleIsNull(metricsGet.sample1) != 0 || C.gpmSampleIsNull(metricsGet.sample2) != 0 {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if metricsGet.version != C.NVML_GPM_METRICS_GET_VERSION {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	n := int(metricsGet.numMetrics)
	if n <= 0 || n > C.NVML_GPM_METRIC_MAX {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	ids := make([]uint32, n)
	for i := 0; i < n; i++ {
		ids[i] = uint32(C.gpmMetricId(metricsGet, C.uint(i)))
	}
	key1 := uint64(C.gpmSampleToKey(metricsGet.sample1))
	key2 := uint64(C.gpmSampleToKey(metricsGet.sample2))

	values, rets, ret := engine.GpmMetricsGet(key1, key2, ids)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	for i := 0; i < n; i++ {
		C.gpmSetMetric(metricsGet, C.uint(i), toReturn(rets[i]), C.double(values[i]))
	}
	debugLog("[NVML] nvmlGpmMetricsGet(numMetrics=%d) -> filled\n", n)
	return C.NVML_SUCCESS
}

// nvmlGpmQueryIfStreamingEnabled / nvmlGpmSetStreamingEnabled intentionally
// stay auto-generated stubs: they were added in driver 555. Pre-555 profiles
// get FUNCTION_NOT_FOUND through the version registry; newer profiles get the
// stub's NOT_SUPPORTED until there is a real streaming implementation. DCGM
// 3.3.x never calls them.
