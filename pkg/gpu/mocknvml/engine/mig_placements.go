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

package engine

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// maxGPUInstanceSlices is the widest board this derivation will describe.
// NVML's widest GPU instance profile is GPU_INSTANCE_PROFILE_8_SLICE, so a
// board claiming more compute slices than that is a malformed profile
// document rather than a board the code has yet to learn about — there is no
// profile that could occupy the extra slices.
//
// It is a bound and not just a plausibility check: it is what keeps the
// doubling in nextPowerOfTwo and the narrowing to uint32 below in range for
// any slice count a profile document can contain.
const maxGPUInstanceSlices = 8

// derivePlacements returns the slice placements a GPU instance profile can
// occupy: where on the board a partition of sliceCount compute slices holding
// memoryMB of the board's capacityMB is allowed to sit.
//
// Placements are derived rather than declared because NVIDIA publishes them
// only as diagrams, and the geometry is regular enough not to need
// transcribing.
//
// The size of a placement is measured in memory units, not compute slices.
// A board has as many memory units as the next power of two at or above its
// compute-slice count — eight for a 7-slice datacenter board — so a 3g
// partition holding half the memory occupies four of eight units, and a 7g
// partition occupies all eight. Getting this wrong is what makes go-nvml's
// H100 table report a 7g placement of size 7.
//
// How many placements exist is capped from both directions: by how many spans
// of that size fit the memory, and by how many groups of sliceCount fit the
// compute slices. A 1g and a 2g profile of the same memory size differ only in
// the second cap.
func derivePlacements(sliceCount, boardSlices int, memoryMB, capacityMB uint64) []nvml.GpuInstancePlacement {
	placements := []nvml.GpuInstancePlacement{}
	if sliceCount <= 0 || boardSlices <= 0 || boardSlices > maxGPUInstanceSlices ||
		capacityMB == 0 || memoryMB == 0 {
		return placements
	}

	memoryUnits := nextPowerOfTwo(boardSlices)

	// Rounding up absorbs the memory a real board holds back: an A100 slice is
	// 4864 MB, 95% of an eighth, and still occupies a whole unit.
	units := (memoryMB*uint64(memoryUnits) + capacityMB - 1) / capacityMB

	// A partition that wants more memory than the board has cannot sit
	// anywhere. Rejecting it before the rounding leaves units no greater than
	// memoryUnits, which is what keeps the conversion below in range.
	if units > uint64(memoryUnits) {
		return placements
	}

	// Rounding to a power of two keeps every start aligned to its own size,
	// and divides the board's units exactly because that count is itself a
	// power of two.
	size := nextPowerOfTwo(int(units))

	count := min(memoryUnits/size, boardSlices/sliceCount)
	for i := range count {
		placements = append(placements, nvml.GpuInstancePlacement{
			Start: uint32(i * size),
			Size:  uint32(size),
		})
	}
	return placements
}

// nextPowerOfTwo returns the smallest power of two at or above n, and 1 for
// any n below 1.
//
// Callers must bound n. The result is reached by doubling up from 1, so for an
// n within a factor of two of the largest int the doubling overflows to
// negative and then to zero and the loop never ends. Computing it as
// 1 << bits.Len(uint(n-1)) overflows on the same inputs, so the bound has to
// come from the caller rather than from here.
func nextPowerOfTwo(n int) int {
	p := 1
	for p < n {
		p *= 2
	}
	return p
}
