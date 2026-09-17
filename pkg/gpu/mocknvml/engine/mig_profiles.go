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

// migProfilesFromConfig transcribes the board's MIG profile tables out of the
// rows its YAML profile declares: the GPU-instance and compute-instance
// tables, the profile IDs the board reports, and whether it is MIG-capable at
// all.
//
// Nothing here is computed. A row declares the slices it spans, the id the
// board publishes for it, the slots it may occupy and the compute instances it
// offers, and config load refuses a row that leaves any of that out. The
// geometry used to be derived from the slice count, and a board whose geometry
// did not match the derivation could not be described in YAML at all — which
// is the class of defect this transcription removes.
//
// The ID table travels with the profile tables rather than being looked up
// separately, so the two cannot disagree about which geometry this board has.
//
// A board declaring no rows is reported as having no tables rather than empty
// ones, which is what lets GetMigMode answer ERROR_NOT_SUPPORTED — how a
// consumer detects a board that cannot partition.
//
// The board's memory capacity is no longer read: it scaled the derived
// placements against the board, and a declared placement needs no scaling. The
// parameter stays so that every caller keeps naming the device whose tables
// these are.
func migProfilesFromConfig(migCfg *MIGConfig, _ uint64) (gpus.MIGProfileConfig, migProfileIDs, bool) {
	if migCfg == nil || len(migCfg.SupportedProfiles) == 0 {
		return gpus.MIGProfileConfig{}, nil, false
	}

	profiles := gpus.MIGProfileConfig{
		GpuInstanceProfiles:       map[int]nvml.GpuInstanceProfileInfo{},
		GpuInstancePlacements:     map[int][]nvml.GpuInstancePlacement{},
		ComputeInstanceProfiles:   map[int]map[int]nvml.ComputeInstanceProfileInfo{},
		ComputeInstancePlacements: map[int]map[int][]nvml.ComputeInstancePlacement{},
	}
	ids := make(migProfileIDs, len(migCfg.SupportedProfiles))

	for _, spec := range migCfg.SupportedProfiles {
		profileEnum, ok := gpuInstanceProfileEnum(spec.NVMLProfile)
		if !ok {
			// Refused at config load; unreachable for a validated config.
			continue
		}

		// Validation has already refused any row whose enum is unknown, so
		// this cannot fail for a validated config, and any row whose declared
		// width disagrees with the enum — so the two are interchangeable and
		// the declared one is preferred where it exists.
		sliceCount, ok := gpuInstanceSliceCount(profileEnum)
		if !ok {
			continue
		}
		if spec.Slices != 0 {
			sliceCount = spec.Slices
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
		profiles.GpuInstancePlacements[profileEnum] = placementsOfSpec(spec)
		ciProfiles, ciPlacements := computeInstancesOfSpec(spec)
		profiles.ComputeInstanceProfiles[profileEnum] = ciProfiles
		profiles.ComputeInstancePlacements[profileEnum] = ciPlacements
		// Every row publishes an id, so the table covers the whole board. It
		// is built per device rather than shared between the boards of one
		// numbering, so a declared id cannot leak onto an unrelated board.
		ids[profileEnum] = spec.ProfileID
	}

	return profiles, ids, true
}

// placementsOfSpec transcribes the slots a row declares it may occupy.
// Validation refuses a row declaring none, so the result is never empty for a
// board that loaded.
func placementsOfSpec(spec MIGProfileSpec) []nvml.GpuInstancePlacement {
	placements := make([]nvml.GpuInstancePlacement, 0, len(spec.Placements))
	for _, p := range spec.Placements {
		placements = append(placements, nvml.GpuInstancePlacement{Start: p.Start, Size: p.Size})
	}
	return placements
}

// computeInstancesOfSpec transcribes the compute-instance listing a row
// declares, together with the possible-placement table that accompanies it.
//
// Id is the NVML enum, not anything the row declares. It is the profile's
// identity rather than an id a board publishes, and the two only coincide for
// compute instances.
//
// The placement lists are empty on purpose, and exist for exactly the profiles
// the row declares. NVML answers "this profile fits nowhere" and "this profile
// is not offered here" through different returns, and the mock server's
// distinction between them is whether the profile has an entry at all — so the
// entry has to exist. computeInstancePlacements then lays the offsets out from
// the profile's own shape, which is the path go-nvml's A100 table already
// takes and the one every board's compute instances are placed by.
func computeInstancesOfSpec(spec MIGProfileSpec) (
	map[int]nvml.ComputeInstanceProfileInfo,
	map[int][]nvml.ComputeInstancePlacement,
) {
	ciProfiles := make(map[int]nvml.ComputeInstanceProfileInfo, len(spec.ComputeInstances))
	ciPlacements := make(map[int][]nvml.ComputeInstancePlacement, len(spec.ComputeInstances))
	for _, ci := range spec.ComputeInstances {
		ciEnum, ok := computeInstanceProfileEnum(ci.NVMLProfile)
		if !ok {
			// Refused at config load; unreachable for a validated config.
			continue
		}
		ciSlices := ci.Slices
		if ciSlices == 0 {
			// The enum carries the same width, and validation refuses a row
			// where the declared one disagrees with it.
			ciSlices, _ = computeInstanceSliceCount(ciEnum)
		}
		ciProfiles[ciEnum] = nvml.ComputeInstanceProfileInfo{
			Id:                    uint32(ciEnum),
			SliceCount:            uint32(ciSlices),
			InstanceCount:         uint32(ci.Instances),
			MultiprocessorCount:   uint32(ci.Multiprocessors),
			SharedCopyEngineCount: uint32(ci.SharedCopyEngines),
			SharedDecoderCount:    uint32(ci.Decoders),
			SharedEncoderCount:    uint32(ci.Encoders),
			SharedJpegCount:       uint32(ci.JPEG),
			SharedOfaCount:        uint32(ci.OFA),
		}
		ciPlacements[ciEnum] = []nvml.ComputeInstancePlacement{}
	}
	return ciProfiles, ciPlacements
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

// computeInstanceProfileEnums maps the name a YAML profile uses to NVML's
// compute instance profile enum, spelled as NVML's own constant suffixes the
// same way gpuInstanceProfileEnums spells the GPU instance ones.
//
// It is a separate namespace: COMPUTE_INSTANCE_PROFILE_1_SLICE and
// GPU_INSTANCE_PROFILE_1_SLICE are both 0, and the two enums diverge above
// that, so a name resolved through the wrong table names a different width.
var computeInstanceProfileEnums = map[string]int{
	"1_SLICE":      nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
	"1_SLICE_REV1": nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE_REV1,
	"2_SLICE":      nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE,
	"3_SLICE":      nvml.COMPUTE_INSTANCE_PROFILE_3_SLICE,
	"4_SLICE":      nvml.COMPUTE_INSTANCE_PROFILE_4_SLICE,
	"6_SLICE":      nvml.COMPUTE_INSTANCE_PROFILE_6_SLICE,
	"7_SLICE":      nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE,
	"7_SLICE_NVL":  nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE_NVL,
	"8_SLICE":      nvml.COMPUTE_INSTANCE_PROFILE_8_SLICE,
}

// computeInstanceProfileEnum resolves a YAML compute-instance profile name to
// its NVML enum. As with gpuInstanceProfileEnum, the enum is meaningless
// unless ok.
func computeInstanceProfileEnum(name string) (int, bool) {
	ciEnum, ok := computeInstanceProfileEnums[name]
	return ciEnum, ok
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
// A nil table maps every ID to itself, keeping go-nvml's numbering end to end.
// Every board that declares a profile table has one entry per row, so nil only
// arises for a board that declares no profiles at all.
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
