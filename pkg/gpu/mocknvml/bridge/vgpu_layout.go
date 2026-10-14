// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Sizes of the vGPU C types, exposed so a pure-Go test can pin them against
// go-nvml's Go structs; test files in this package cannot use cgo.

package main

/*
#include "nvml_types.h"
*/
import "C"
import "unsafe"

func vgpuLicenseInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlVgpuLicenseInfo_t{})
}

func gridLicensableFeatureSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGridLicensableFeature_t{})
}

func gridLicensableFeaturesSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGridLicensableFeatures_t{})
}

func vgpuInstanceSizeForTest() uintptr {
	var id C.nvmlVgpuInstance_t
	return unsafe.Sizeof(id)
}
