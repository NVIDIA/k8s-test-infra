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

import (
	"fmt"
	"math"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/gpus"
)

// resolveMIGProfiles returns the GPU-instance and compute-instance profile
// tables for the board a YAML profile describes, and whether the board is
// MIG-capable at all.
//
// Every mock device is built from the dgxa100 base regardless of its YAML
// profile, so without this the MIG tables would always be A100 40GB's — an
// H100 would advertise A100 slice memory. Matching on the configured device
// name keeps the tables tied to what the rest of NVML reports, and the memory
// size disambiguates the boards that ship in several capacities.
//
// go-nvml owns the tables themselves; duplicating them here would mean two
// sources of truth for slice counts and placements that must agree.
func resolveMIGProfiles(deviceName string, memoryBytes uint64) (gpus.MIGProfileConfig, bool) {
	name := strings.ToUpper(deviceName)

	switch {
	case strings.Contains(name, "A100"):
		// The 80GB boards double every slice's memory, so the capacity
		// decides the table. Anything at or above 64 GiB is an 80GB board.
		if memoryBytes >= 64*oneGiB {
			return gpus.A100_SXM4_80GB.MIGProfiles, true
		}
		return gpus.A100_SXM4_40GB.MIGProfiles, true
	case strings.Contains(name, "A30"):
		return gpus.A30_PCIE_24GB.MIGProfiles, true
	case strings.Contains(name, "H200"):
		return gpus.H200_SXM5_141GB.MIGProfiles, true
	case strings.Contains(name, "H100"), strings.Contains(name, "GH200"):
		return gpus.H100_SXM5_80GB.MIGProfiles, true
	case strings.Contains(name, "B200"), strings.Contains(name, "B300"),
		strings.Contains(name, "GB200"), strings.Contains(name, "GB300"):
		// go-nvml carries one Blackwell table; the GB trays use the same
		// partitioning as the SXM B200 they are built from.
		return gpus.B200_SXM5_180GB.MIGProfiles, true
	}

	// T4, L40S and anything unrecognised: not MIG-capable. Reporting this as
	// "no tables" rather than "empty tables" is what lets GetMigMode answer
	// ERROR_NOT_SUPPORTED, which is how consumers detect a non-MIG board.
	return gpus.MIGProfileConfig{}, false
}

const (
	oneMiB = 1024 * 1024
	oneGiB = 1024 * oneMiB
)

// migProfileName renders the canonical MIG profile name for a
// (GPU instance, compute instance) profile pair, e.g. "1g.5gb", "2c.7g.40gb"
// or "1g.5gb+me".
//
// This deliberately mirrors go-nvlib's NewMigProfile and MigProfileInfo.String
// (pkg/nvlib/device/mig_profile.go), because go-nvlib is what the device plugin
// uses to derive the nvidia.com/mig-<name> resource names from the values this
// mock reports. A name computed differently here would mean a YAML profile
// naming a partition that never matches the resource the cluster publishes.
func migProfileName(giProfileID, ciProfileID int, migMemorySizeMB, deviceMemoryBytes uint64) (string, error) {
	giSlices, ok := gpuInstanceSliceCount(giProfileID)
	if !ok {
		return "", fmt.Errorf("invalid GPU instance profile ID: %d", giProfileID)
	}
	ciSlices, ok := computeInstanceSliceCount(ciProfileID)
	if !ok {
		return "", fmt.Errorf("invalid compute instance profile ID: %d", ciProfileID)
	}

	gb := migMemorySizeGB(deviceMemoryBytes, migMemorySizeMB)

	var suffix string
	if attrs := gpuInstanceAttributes(giProfileID); len(attrs) > 0 {
		suffix = "+" + strings.Join(attrs, ",")
	} else if negAttrs := gpuInstanceNegAttributes(giProfileID); len(negAttrs) > 0 {
		suffix = "-" + strings.Join(negAttrs, ",")
	}

	// A compute instance spanning its whole GPU instance is spelled without
	// the redundant "Nc." prefix.
	if ciSlices == giSlices {
		return fmt.Sprintf("%dg.%dgb%s", giSlices, gb, suffix), nil
	}
	return fmt.Sprintf("%dc.%dg.%dgb%s", ciSlices, giSlices, gb, suffix), nil
}

// migMemorySizeGB converts a slice's raw MiB allocation into the rounded GB
// figure the profile name carries. A 1-slice A100 40GB partition holds
// 4864 MiB, which the name reports as 5gb: the raw size is snapped to the
// nearest eighth of the board before being scaled back up, which is how the
// advertised sizes come out as round numbers.
func migMemorySizeGB(totalDeviceMemory, migMemorySizeMB uint64) uint64 {
	const fracDenominator = 8

	if totalDeviceMemory == 0 {
		return 0
	}
	fractionalGPUMem := (float64(migMemorySizeMB) * oneMiB) / float64(totalDeviceMemory)
	fractionalGPUMem = math.Ceil(fractionalGPUMem*fracDenominator) / fracDenominator
	totalMemGB := float64((totalDeviceMemory + oneGiB - 1) / oneGiB)
	return uint64(math.Round(fractionalGPUMem * totalMemGB))
}

// gpuInstanceSliceCount maps a GPU instance profile ID to the number of GPU
// slices it occupies. The revision and attribute variants of a profile occupy
// the same slice count as the base profile they revise.
func gpuInstanceSliceCount(giProfileID int) (int, bool) {
	switch giProfileID {
	case nvml.GPU_INSTANCE_PROFILE_1_SLICE,
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1,
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2,
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_GFX,
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_NO_ME,
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_ALL_ME:
		return 1, true
	case nvml.GPU_INSTANCE_PROFILE_2_SLICE,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_REV1,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_GFX,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_NO_ME,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_ALL_ME:
		return 2, true
	case nvml.GPU_INSTANCE_PROFILE_3_SLICE,
		nvml.GPU_INSTANCE_PROFILE_3_SLICE_GFX:
		return 3, true
	case nvml.GPU_INSTANCE_PROFILE_4_SLICE,
		nvml.GPU_INSTANCE_PROFILE_4_SLICE_GFX:
		return 4, true
	case nvml.GPU_INSTANCE_PROFILE_6_SLICE:
		return 6, true
	case nvml.GPU_INSTANCE_PROFILE_7_SLICE:
		return 7, true
	case nvml.GPU_INSTANCE_PROFILE_8_SLICE:
		return 8, true
	}
	return 0, false
}

// computeInstanceSliceCount maps a compute instance profile ID to its slice count.
func computeInstanceSliceCount(ciProfileID int) (int, bool) {
	switch ciProfileID {
	case nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1:
		return 1, true
	case nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE:
		return 2, true
	case nvml.COMPUTE_INSTANCE_PROFILE_3_SLICE:
		return 3, true
	case nvml.COMPUTE_INSTANCE_PROFILE_4_SLICE:
		return 4, true
	case nvml.COMPUTE_INSTANCE_PROFILE_6_SLICE:
		return 6, true
	case nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE_NVL:
		return 7, true
	case nvml.COMPUTE_INSTANCE_PROFILE_8_SLICE:
		return 8, true
	}
	return 0, false
}

// MIG profile name attributes, spelled as go-nvlib spells them.
const (
	migAttributeMediaExtensions    = "me"
	migAttributeMediaExtensionsAll = "me.all"
	migAttributeGraphics           = "gfx"
)

func gpuInstanceAttributes(giProfileID int) []string {
	switch giProfileID {
	case nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_REV1:
		return []string{migAttributeMediaExtensions}
	case nvml.GPU_INSTANCE_PROFILE_1_SLICE_ALL_ME,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_ALL_ME:
		return []string{migAttributeMediaExtensionsAll}
	case nvml.GPU_INSTANCE_PROFILE_1_SLICE_GFX,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_GFX,
		nvml.GPU_INSTANCE_PROFILE_4_SLICE_GFX:
		return []string{migAttributeGraphics}
	}
	return nil
}

func gpuInstanceNegAttributes(giProfileID int) []string {
	switch giProfileID {
	case nvml.GPU_INSTANCE_PROFILE_1_SLICE_NO_ME,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_NO_ME:
		return []string{migAttributeMediaExtensions}
	}
	return nil
}
