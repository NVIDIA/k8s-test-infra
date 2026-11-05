// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// The vGPU exports write these structs straight into buffers callers
// allocate from go-nvml's Go types, so a size mismatch writes past the
// caller's allocation.
func TestVgpuStructLayouts_MatchGoNvml(t *testing.T) {
	t.Parallel()
	require.Equal(t, unsafe.Sizeof(nvml.VgpuLicenseInfo{}), vgpuLicenseInfoSizeForTest(),
		"nvmlVgpuLicenseInfo_t no longer matches go-nvml — ABI drift")
	require.Equal(t, unsafe.Sizeof(nvml.GridLicensableFeature{}), gridLicensableFeatureSizeForTest(),
		"nvmlGridLicensableFeature_t no longer matches go-nvml — ABI drift")
	require.Equal(t, unsafe.Sizeof(nvml.GridLicensableFeatures{}), gridLicensableFeaturesSizeForTest(),
		"nvmlGridLicensableFeatures_t no longer matches go-nvml — ABI drift")
	require.Equal(t, unsafe.Sizeof(nvml.FBCStats{}), fbcStatsSizeForTest(),
		"nvmlFBCStats_t no longer matches go-nvml — ABI drift")
}

// Upstream NVML hands out vGPU instances as plain IDs. An opaque pointer
// typedef would make every instance-scoped export read a 64-bit argument the
// caller passed as 32 bits.
func TestVgpuInstanceIsAPlainID(t *testing.T) {
	t.Parallel()
	require.Equal(t, uintptr(4), vgpuInstanceSizeForTest())
}
