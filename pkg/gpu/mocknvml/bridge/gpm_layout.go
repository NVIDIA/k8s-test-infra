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

package main

/*
#include "nvml_types.h"
*/
import "C"
import "unsafe"

func gpmMetricMaxForTest() uintptr {
	return uintptr(C.NVML_GPM_METRIC_MAX)
}

func gpmSupportVersionForTest() uint32 {
	return uint32(C.NVML_GPM_SUPPORT_VERSION)
}

func gpmSampleSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpmSample_t{})
}

func gpmSampleHandleOffsetForTest() uintptr {
	var sample C.nvmlGpmSample_t
	return unsafe.Offsetof(sample.handle)
}

func gpmMetricSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpmMetric_t{})
}

func gpmMetricsGetSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpmMetricsGet_t{})
}

func gpmMetricsGetSample1OffsetForTest() uintptr {
	var metrics C.nvmlGpmMetricsGet_t
	return unsafe.Offsetof(metrics.sample1)
}

func gpmMetricsGetSample2OffsetForTest() uintptr {
	var metrics C.nvmlGpmMetricsGet_t
	return unsafe.Offsetof(metrics.sample2)
}

func gpmSupportSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpmSupport_t{})
}
