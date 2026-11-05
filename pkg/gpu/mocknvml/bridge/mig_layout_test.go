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

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// go-nvml keeps the info structs that embed handles unexported, so their ABI
// is mirrored here. A handle is one pointer wide on both sides.
type (
	goNvmlHandleABI struct {
		Handle unsafe.Pointer
	}

	goNvmlGpuInstanceInfoABI struct {
		Device    goNvmlHandleABI
		Id        uint32
		ProfileId uint32
		Placement nvml.GpuInstancePlacement
	}

	goNvmlComputeInstanceInfoABI struct {
		Device      goNvmlHandleABI
		GpuInstance goNvmlHandleABI
		Id          uint32
		ProfileId   uint32
		Placement   nvml.ComputeInstancePlacement
	}
)

// TestMigStructLayouts_MatchGoNvmlABI is the load-bearing test for the MIG
// struct definitions in nvml_types.h. Callers allocate these buffers from
// go-nvml's Go structs and hand the bridge a pointer, so any disagreement in
// size or field offset means the bridge writes into the wrong field — or past
// the end of the allocation.
func TestMigStructLayouts_MatchGoNvmlABI(t *testing.T) {
	t.Parallel()

	require.Equal(t, 96, deviceNameV2BufferSizeForTest(),
		"the profile name buffer sizes the trailing array of every versioned MIG struct")

	t.Run("gpu instance", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, unsafe.Sizeof(nvml.GpuInstancePlacement{}), gpuInstancePlacementSizeForTest())
		require.Equal(t, unsafe.Sizeof(nvml.GpuInstanceProfileInfo{}), gpuInstanceProfileInfoSizeForTest())
		require.Equal(t, unsafe.Sizeof(nvml.GpuInstanceProfileInfo_v2{}), gpuInstanceProfileInfoV2SizeForTest())
		require.Equal(t, unsafe.Sizeof(nvml.GpuInstanceProfileInfo_v3{}), gpuInstanceProfileInfoV3SizeForTest())
		require.Equal(t, unsafe.Sizeof(goNvmlGpuInstanceInfoABI{}), gpuInstanceInfoSizeForTest())

		// The 64-bit memory size is where padding differences would first
		// show up, and the name and capabilities fields sit after it.
		require.Equal(t, unsafe.Offsetof(nvml.GpuInstanceProfileInfo{}.MemorySizeMB),
			gpuInstanceProfileInfoMemorySizeOffsetForTest())
		require.Equal(t, unsafe.Offsetof(nvml.GpuInstanceProfileInfo_v2{}.Name),
			gpuInstanceProfileInfoV2NameOffsetForTest())
		require.Equal(t, unsafe.Offsetof(nvml.GpuInstanceProfileInfo_v3{}.Capabilities),
			gpuInstanceProfileInfoV3CapabilitiesOffsetForTest())
		require.Equal(t, unsafe.Offsetof(goNvmlGpuInstanceInfoABI{}.Placement),
			gpuInstanceInfoPlacementOffsetForTest())
	})

	t.Run("compute instance", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, unsafe.Sizeof(nvml.ComputeInstancePlacement{}), computeInstancePlacementSizeForTest())
		require.Equal(t, unsafe.Sizeof(nvml.ComputeInstanceProfileInfo{}), computeInstanceProfileInfoSizeForTest())
		require.Equal(t, unsafe.Sizeof(nvml.ComputeInstanceProfileInfo_v2{}),
			computeInstanceProfileInfoV2SizeForTest())
		require.Equal(t, unsafe.Sizeof(nvml.ComputeInstanceProfileInfo_v3{}),
			computeInstanceProfileInfoV3SizeForTest())
		require.Equal(t, unsafe.Sizeof(goNvmlComputeInstanceInfoABI{}), computeInstanceInfoSizeForTest())

		require.Equal(t, unsafe.Offsetof(nvml.ComputeInstanceProfileInfo_v2{}.Name),
			computeInstanceProfileInfoV2NameOffsetForTest())
		require.Equal(t, unsafe.Offsetof(goNvmlComputeInstanceInfoABI{}.Placement),
			computeInstanceInfoPlacementOffsetForTest())
	})

	t.Run("device attributes", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, unsafe.Sizeof(nvml.DeviceAttributes{}), deviceAttributesSizeForTest())
		require.Equal(t, unsafe.Offsetof(nvml.DeviceAttributes{}.MemorySizeMB),
			deviceAttributesMemorySizeOffsetForTest())
	})
}

// TestMigProfileInfoV3Versions_MatchGoNvml pins the version tags the bridge
// dispatches on against the ones a caller builds with go-nvml's helper. A
// mismatch would silently fall through to the v2 layout, which for GPU
// instance profiles has a different field order — not just a missing field.
func TestMigProfileInfoV3Versions_MatchGoNvml(t *testing.T) {
	t.Parallel()

	require.Equal(t, nvml.STRUCT_VERSION(nvml.GpuInstanceProfileInfo_v3{}, 3),
		gpuInstanceProfileInfoV3Version)
	require.Equal(t, nvml.STRUCT_VERSION(nvml.ComputeInstanceProfileInfo_v3{}, 3),
		computeInstanceProfileInfoV3Version)
}
