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

// migProfilesFromConfig builds the board's MIG profile tables from the rows its
// YAML profile declares: the GPU-instance and compute-instance tables, the
// profile IDs the board reports, and whether it is MIG-capable at all.
//
// The tables are assembled rather than looked up in go-nvml so that teaching
// the mock a new board is a YAML edit. Placements and reported profile IDs are
// derived from the declared slice counts, because NVIDIA publishes the first
// only as diagrams and the second not at all for Blackwell.
//
// The ID table travels with the profile tables rather than being looked up
// separately, so the two cannot disagree about which geometry this board has.
//
// A board declaring no rows is reported as having no tables rather than empty
// ones, which is what lets GetMigMode answer ERROR_NOT_SUPPORTED — how a
// consumer detects a board that cannot partition.
func migProfilesFromConfig(migCfg *MIGConfig, deviceMemoryBytes uint64) (gpus.MIGProfileConfig, migProfileIDs, bool) {
	if migCfg == nil || len(migCfg.SupportedProfiles) == 0 {
		return gpus.MIGProfileConfig{}, nil, false
	}

	boardSlices := migCfg.MaxGPUInstances
	profiles := gpus.MIGProfileConfig{
		GpuInstanceProfiles:       map[int]nvml.GpuInstanceProfileInfo{},
		GpuInstancePlacements:     map[int][]nvml.GpuInstancePlacement{},
		ComputeInstanceProfiles:   map[int]map[int]nvml.ComputeInstanceProfileInfo{},
		ComputeInstancePlacements: map[int]map[int][]nvml.ComputeInstancePlacement{},
	}

	for _, spec := range migCfg.SupportedProfiles {
		profileEnum, ok := gpuInstanceProfileEnum(spec.NVMLProfile)
		if !ok {
			// Refused at config load; unreachable for a validated config.
			continue
		}

		// The enum is the single source of the slice count; the YAML does not
		// restate it. Validation has already refused any row whose enum is
		// unknown, so this cannot fail for a validated config.
		sliceCount, ok := gpuInstanceSliceCount(profileEnum)
		if !ok {
			continue
		}

		profiles.GpuInstanceProfiles[profileEnum] = nvml.GpuInstanceProfileInfo{
			Id:                  uint32(profileEnum),
			SliceCount:          uint32(sliceCount),
			InstanceCount:       uint32(spec.Instances),
			MultiprocessorCount: uint32(spec.Multiprocessors),
			CopyEngineCount:     uint32(spec.CopyEngines),
			DecoderCount:        uint32(spec.Decoders),
			EncoderCount:        uint32(spec.Encoders),
			JpegCount:           uint32(spec.JPEG),
			OfaCount:            uint32(spec.OFA),
			MemorySizeMB:        spec.MemoryMB,
		}
		profiles.GpuInstancePlacements[profileEnum] = derivePlacements(
			sliceCount, boardSlices, spec.MemoryMB, deviceMemoryBytes/oneMiB)
		ciProfiles := deriveComputeInstanceProfiles(spec, sliceCount)
		profiles.ComputeInstanceProfiles[profileEnum] = ciProfiles
		profiles.ComputeInstancePlacements[profileEnum] = computeInstancePlacementSlots(ciProfiles)
	}

	return profiles, profileIDsForBoard(boardSlices), true
}

// deriveComputeInstanceProfiles gives a GPU instance of sliceCount slices the
// compute instances hardware offers inside it, each counted by how many fit
// and given its share of the GPU instance's multiprocessors.
//
// The engine counts are not divided between the compute instances the way the
// multiprocessors are: a GPU instance's decoders, JPEG and OFA engines are
// shared by every compute instance inside it, which is what NVML's "Shared"
// naming means and what a MIG device reports as its own attributes.
func deriveComputeInstanceProfiles(spec MIGProfileSpec, sliceCount int) map[int]nvml.ComputeInstanceProfileInfo {
	// A GPU instance's multiprocessors are handed out in whole slices, so an
	// SM count that does not divide the slices leaves the remainder in no
	// compute instance — which is what hardware does with an odd SM. No
	// published profile has an indivisible count, so this never truncates for
	// a shipped board; a YAML declaring one gets the faithful answer.
	perSlice := 0
	if sliceCount > 0 {
		perSlice = spec.Multiprocessors / sliceCount
	}

	ciProfiles := map[int]nvml.ComputeInstanceProfileInfo{}
	for _, ciEnum := range computeInstanceProfilesOffered(sliceCount) {
		ciSlices, ok := computeInstanceSliceCount(ciEnum)
		if !ok {
			// Unreachable: the tables below hold only profiles
			// computeInstanceSliceCount maps, which a guard test asserts.
			continue
		}
		ciProfiles[ciEnum] = nvml.ComputeInstanceProfileInfo{
			Id:                    uint32(ciEnum),
			SliceCount:            uint32(ciSlices),
			InstanceCount:         uint32(sliceCount / ciSlices),
			MultiprocessorCount:   uint32(perSlice * ciSlices),
			SharedCopyEngineCount: uint32(spec.CopyEngines),
			SharedDecoderCount:    uint32(spec.Decoders),
			SharedEncoderCount:    uint32(spec.Encoders),
			SharedJpegCount:       uint32(spec.JPEG),
			SharedOfaCount:        uint32(spec.OFA),
		}
	}
	return ciProfiles
}

// offeredComputeInstanceProfiles is the compute-instance listing of a GPU
// instance, keyed by how many slices that GPU instance spans. It is
// transcribed from what `nvidia-smi mig -lcip` prints, which go-nvml's A100
// table also carries.
//
// A GPU instance does not offer every width that would fit inside it, so this
// cannot be a loop over 1..S. A 4-slice instance offers 1c, 2c and 4c but not
// 3c; a 7-slice instance offers 3c and 4c but stops there and jumps to 7c. It
// also offers the media-extension 1-slice compute instance alongside the plain
// one, on every GPU instance.
//
// Getting this wrong is not cosmetic. go-nvlib derives the
// nvidia.com/mig-<name> resource names a cluster publishes from these values,
// so an invented 6-slice compute instance under a 7g partition publishes
// 6c.7g.40gb — a resource name no real cluster has.
var offeredComputeInstanceProfiles = map[int][]int{
	1: {
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1,
	},
	2: {
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1,
		nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE,
	},
	3: {
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1,
		nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_3_SLICE,
	},
	4: {
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1,
		nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_4_SLICE,
	},
	7: {
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1,
		nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_3_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_4_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE,
	},
}

// computeInstanceProfilesOffered answers the listing for a GPU instance of the
// given width.
//
// A width NVIDIA publishes no listing for — 6 and 8 slices, which NVML has
// enums for and no shipped board declares — gets the narrowest compute
// instance and one spanning the whole GPU instance. Those two are the least
// any GPU instance offers, and stopping there is the most that can be claimed
// without guessing: an intermediate width invented here would be advertised,
// created, and then named as a partition no cluster has.
func computeInstanceProfilesOffered(giSlices int) []int {
	if offered, ok := offeredComputeInstanceProfiles[giSlices]; ok {
		return offered
	}
	spanning, ok := computeInstanceProfileForSliceCount(giSlices)
	if !ok {
		return nil
	}
	return []int{
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1,
		spanning,
	}
}

// computeInstancePlacementSlots registers a placement list for every compute
// instance profile the GPU instance offers.
//
// The lists are empty on purpose. NVML answers "this profile fits nowhere" and
// "this profile is not offered here" through different returns, and the mock
// server's distinction between them is whether the profile has an entry at all
// — so the entry has to exist. computeInstancePlacements then lays the offsets
// out from the profile's own shape, which is the path go-nvml's A100 table
// already takes and the one every board's compute instances are placed by.
func computeInstancePlacementSlots(
	ciProfiles map[int]nvml.ComputeInstanceProfileInfo,
) map[int][]nvml.ComputeInstancePlacement {
	slots := make(map[int][]nvml.ComputeInstancePlacement, len(ciProfiles))
	for ciEnum := range ciProfiles {
		slots[ciEnum] = []nvml.ComputeInstancePlacement{}
	}
	return slots
}

// profileIDsForBoard picks the reported-ID table by the board's slice count.
//
// The numbering follows the partition's fraction of the board rather than the
// board's capacity, so every 7-slice datacenter board shares one table and
// every 4-slice board another. Selecting on slice count rather than on the
// device name is what lets a new board arrive as YAML.
func profileIDsForBoard(boardSlices int) migProfileIDs {
	switch boardSlices {
	case 7:
		return sevenSliceProfileIDs
	case 4:
		return fourSliceProfileIDs
	}
	// A width with no transcribed listing keeps go-nvml's numbering, which
	// reports every profile under its own enum: a guessed ID reads as correct
	// and then partitions the board wrongly.
	return nil
}

// declaredProfileNames maps each declared NVML profile enum to the name its
// YAML row gives it, which is the board's own spelling of that partition.
//
// It is built once per device and held on migState, because the five places
// that name a profile have the board's tables in hand but not its config.
// An enum the board does not declare is absent, which leaves migProfileName
// computing the name from the memory fraction.
func declaredProfileNames(migCfg *MIGConfig) map[int]string {
	if migCfg == nil {
		return nil
	}

	names := make(map[int]string, len(migCfg.SupportedProfiles))
	for _, spec := range migCfg.SupportedProfiles {
		if profileEnum, ok := gpuInstanceProfileEnum(spec.NVMLProfile); ok {
			names[profileEnum] = spec.Name
		}
	}
	return names
}

const (
	oneMiB = 1024 * 1024
	oneGiB = 1024 * oneMiB
)

// gpuInstanceProfileEnums maps the name a YAML profile uses to NVML's GPU
// instance profile enum. The names are NVML's own constant suffixes, so a
// reader comparing the YAML against nvml.h or the MIG guide sees the same
// spelling.
var gpuInstanceProfileEnums = map[string]int{
	"1_SLICE":        nvml.GPU_INSTANCE_PROFILE_1_SLICE,
	"1_SLICE_REV1":   nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1,
	"1_SLICE_REV2":   nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2,
	"1_SLICE_GFX":    nvml.GPU_INSTANCE_PROFILE_1_SLICE_GFX,
	"1_SLICE_NO_ME":  nvml.GPU_INSTANCE_PROFILE_1_SLICE_NO_ME,
	"1_SLICE_ALL_ME": nvml.GPU_INSTANCE_PROFILE_1_SLICE_ALL_ME,
	"2_SLICE":        nvml.GPU_INSTANCE_PROFILE_2_SLICE,
	"2_SLICE_REV1":   nvml.GPU_INSTANCE_PROFILE_2_SLICE_REV1,
	"2_SLICE_GFX":    nvml.GPU_INSTANCE_PROFILE_2_SLICE_GFX,
	"2_SLICE_NO_ME":  nvml.GPU_INSTANCE_PROFILE_2_SLICE_NO_ME,
	"2_SLICE_ALL_ME": nvml.GPU_INSTANCE_PROFILE_2_SLICE_ALL_ME,
	"3_SLICE":        nvml.GPU_INSTANCE_PROFILE_3_SLICE,
	"3_SLICE_GFX":    nvml.GPU_INSTANCE_PROFILE_3_SLICE_GFX,
	"4_SLICE":        nvml.GPU_INSTANCE_PROFILE_4_SLICE,
	"4_SLICE_GFX":    nvml.GPU_INSTANCE_PROFILE_4_SLICE_GFX,
	"6_SLICE":        nvml.GPU_INSTANCE_PROFILE_6_SLICE,
	"7_SLICE":        nvml.GPU_INSTANCE_PROFILE_7_SLICE,
	"8_SLICE":        nvml.GPU_INSTANCE_PROFILE_8_SLICE,
}

// gpuInstanceProfileEnum resolves a YAML profile name to its NVML enum.
//
// The enum is meaningless unless ok: an unknown name returns 0, which is
// GPU_INSTANCE_PROFILE_1_SLICE, so a caller dropping the second result binds
// every name it cannot resolve to a valid 1-slice partition rather than to
// something visibly wrong.
func gpuInstanceProfileEnum(name string) (int, bool) {
	profileEnum, ok := gpuInstanceProfileEnums[name]
	return profileEnum, ok
}

// migProfileIDs maps NVML's GPU instance profile enum to the profile ID the
// board reports for it.
//
// The two are different numbers on hardware, and NVML uses both: a caller
// enumerates profiles by the enum (nvmlDeviceGetGpuInstanceProfileInfo) but
// creates one by the reported ID (nvmlDeviceCreateGpuInstance). The reported
// ID encodes the partition's absolute share of the board, which is why a
// 4-slice A30's 1g — a quarter of its board — shares ID 14 with a 7-slice
// A100's 2g, also a quarter.
//
// go-nvml's tables set the reported ID to the enum, so without this the mock
// answers `nvidia-smi mig -cgi 0` with one seventh of the board where hardware
// hands over all of it, and `-cgi 19` with nothing at all.
type migProfileIDs map[int]int

// reported gives the ID the board publishes for a profile enum. An unmapped
// profile reports its enum, which is what go-nvml already did.
func (m migProfileIDs) reported(profileEnum int) int {
	if id, ok := m[profileEnum]; ok {
		return id
	}
	return profileEnum
}

// enumOf is reported in reverse, for the NVML calls that take a reported ID.
//
// The second result is false when the board publishes no profile under that ID,
// which is what refuses `nvidia-smi mig -cgi 3` on an A100: no profile there
// reports 3, and resolving it to the enum of the same value would hand back a
// 4-slice instance that hardware would never have created.
//
// A board with no ID table maps every ID to itself, keeping go-nvml's numbering
// end to end.
func (m migProfileIDs) enumOf(reportedID int) (int, bool) {
	if m == nil {
		return reportedID, true
	}
	for profileEnum, id := range m {
		if id == reportedID {
			return profileEnum, true
		}
	}
	return 0, false
}

// The IDs below are transcribed from the `nvidia-smi mig -lgip` listings in
// NVIDIA's MIG user guide. Boards whose listings are not verified are left
// reporting the enum rather than guessed at: a wrong ID reads as correct and
// then partitions the board wrongly, where the enum at least stays
// self-consistent with the placements and capacities reported alongside it.
var (
	// sevenSliceProfileIDs covers the 7-slice datacenter boards — A100, H100
	// and H200 — which share one numbering because it follows the partition's
	// fraction of the board rather than the board's own capacity.
	sevenSliceProfileIDs = migProfileIDs{
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      19,
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: 20, // the +me variant
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: 15, // 1g at double memory
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      14,
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      9,
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      5,
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      0,
	}

	// fourSliceProfileIDs covers the A30. Its plain profiles take the ID of the
	// A100 profile with the same slice count, but its +me variants do not
	// follow from that — an A30 lists them as 21 and 6 where an A100 lists 20
	// and 15 — so they are transcribed from the A30's own listing.
	//
	// Every profile the board publishes is mapped. A table that covers only
	// some leaves the rest advertised under their enum and rejected under it,
	// which is the one state worse than not mapping the board at all.
	fourSliceProfileIDs = migProfileIDs{
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      14,
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: 21, // 1g.6gb+me
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      5,
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_REV1: 6, // 2g.12gb+me
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      0,
	}
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
//
// declaredName is the board's own spelling of the GPU instance, and overrides
// the computation; empty means compute the name from the memory fraction.
func migProfileName(declaredName string, giProfileID, ciProfileID int, migMemorySizeMB, deviceMemoryBytes uint64) (string, error) {
	giSlices, ok := gpuInstanceSliceCount(giProfileID)
	if !ok {
		return "", fmt.Errorf("invalid GPU instance profile ID: %d", giProfileID)
	}
	ciSlices, ok := computeInstanceSliceCount(ciProfileID)
	if !ok {
		return "", fmt.Errorf("invalid compute instance profile ID: %d", ciProfileID)
	}

	// A declared name is the board's own spelling, taken from NVIDIA's
	// published table. It wins over the computed one because the computation
	// scales the memory fraction against the board's declared capacity, which
	// is not always the capacity of the product NVIDIA published the name for.
	//
	// It names the GPU instance, so a narrower compute instance still needs
	// the "Nc." prefix composing onto it.
	if declaredName != "" {
		if ciSlices == giSlices {
			return declaredName, nil
		}
		return fmt.Sprintf("%dc.%s", ciSlices, declaredName), nil
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
