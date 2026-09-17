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
	"strconv"
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

const (
	a100_40GiB = 42949672960
	h100_80GiB = 85899345920
)

// TestMigProfileName pins the profile-name spelling against go-nvlib's
// algorithm (pkg/nvlib/device/mig_profile.go). The names are not cosmetic:
// they are what the device plugin publishes as nvidia.com/mig-<name>, and
// they are what a YAML profile's mig.gpu_instances[].profile is matched
// against, so a divergence here silently mismatches config and cluster state.
func TestMigProfileName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		giProfileID  int
		ciProfileID  int
		migMemoryMB  uint64
		deviceMemory uint64
		want         string
	}{
		// A100 40GB: a whole-GPU compute instance drops the "Nc." prefix.
		{"a100 1 slice", nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 4864, a100_40GiB, "1g.5gb"},
		{"a100 2 slice", nvml.GPU_INSTANCE_PROFILE_2_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE, 9856, a100_40GiB, "2g.10gb"},
		{"a100 3 slice", nvml.GPU_INSTANCE_PROFILE_3_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_3_SLICE, 19968, a100_40GiB, "3g.20gb"},
		{"a100 7 slice", nvml.GPU_INSTANCE_PROFILE_7_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE, 40192, a100_40GiB, "7g.40gb"},

		// A compute instance narrower than its GPU instance keeps the prefix.
		{"a100 1c in 3g", nvml.GPU_INSTANCE_PROFILE_3_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 19968, a100_40GiB, "1c.3g.20gb"},
		{"a100 2c in 7g", nvml.GPU_INSTANCE_PROFILE_7_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE, 40192, a100_40GiB, "2c.7g.40gb"},

		// Revision profiles carry attribute suffixes.
		{"a100 1 slice rev1", nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 4864, a100_40GiB, "1g.5gb+me"},
		{"a100 1 slice rev2", nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 9856, a100_40GiB, "1g.10gb"},

		{"h100 1 slice", nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 10240, h100_80GiB, "1g.10gb"},
		{"h100 7 slice", nvml.GPU_INSTANCE_PROFILE_7_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE, 81920, h100_80GiB, "7g.80gb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := migProfileName("", tt.giProfileID, tt.ciProfileID, tt.migMemoryMB, tt.deviceMemory)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMigProfileName_RejectsUnknownProfileIDs(t *testing.T) {
	t.Parallel()

	_, err := migProfileName("", nvml.GPU_INSTANCE_PROFILE_COUNT, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 4864, a100_40GiB)
	require.Error(t, err, "an out-of-range GPU instance profile has no slice count")

	_, err = migProfileName("", nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_COUNT, 4864, a100_40GiB)
	require.Error(t, err, "an out-of-range compute instance profile has no slice count")

	// A declared name must not buy a profile past the same checks. Both slice
	// counts are still needed to decide whether the name takes the "Nc."
	// prefix, so an unresolvable ID is an error whatever the board declares —
	// returning the declared string for a profile that cannot exist would
	// advertise a partition nothing can create.
	_, err = migProfileName("1g.5gb", nvml.GPU_INSTANCE_PROFILE_COUNT, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 4864, a100_40GiB)
	require.Error(t, err, "a declared name does not excuse an out-of-range GPU instance profile")

	_, err = migProfileName("1g.5gb", nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_COUNT, 4864, a100_40GiB)
	require.Error(t, err, "a declared name does not excuse an out-of-range compute instance profile")
}

// a30SupportedProfiles is a 4-slice board's declared table, transcribed from
// go-nvml's A30 24GB. It is here for the reported-ID tests: the A30 is the one
// shipped geometry that is not seven slices wide, and its numbering is a
// different table rather than the 7-slice one narrowed.
func a30SupportedProfiles() []MIGProfileSpec {
	return []MIGProfileSpec{
		{Name: "1g.6gb", NVMLProfile: "1_SLICE", Instances: 4, MemoryMB: 5836, Multiprocessors: 14, CopyEngines: 1},
		{Name: "1g.6gb+me", NVMLProfile: "1_SLICE_REV1", Instances: 1, MemoryMB: 5836, Multiprocessors: 14, CopyEngines: 1, Decoders: 1, JPEG: 1, OFA: 1},
		{Name: "2g.12gb", NVMLProfile: "2_SLICE", Instances: 2, MemoryMB: 11672, Multiprocessors: 28, CopyEngines: 2, Decoders: 2},
		{Name: "2g.12gb+me", NVMLProfile: "2_SLICE_REV1", Instances: 1, MemoryMB: 11672, Multiprocessors: 28, CopyEngines: 2, Decoders: 2, JPEG: 1, OFA: 1},
		{Name: "4g.24gb", NVMLProfile: "4_SLICE", Instances: 1, MemoryMB: 23344, Multiprocessors: 56, CopyEngines: 4, Decoders: 4, JPEG: 1, OFA: 1},
	}
}

// TestMIGProfilesFromConfig_ReportsHardwareProfileIDs pins the profile IDs
// against the `nvidia-smi mig -lgip` listings in NVIDIA's MIG user guide.
//
// These are what `nvidia-smi mig -cgi <id>` takes, so reporting go-nvml's enum
// instead — which is what its tables carry — makes the mock create a different
// partition than the same command creates on hardware. The ends invert: ID 0 is
// the whole board on hardware and one seventh of it under the enum.
//
// The table is the listing the board declares through mig.profile_ids. A
// board that declares none reports its enums, which is covered separately.
func TestMIGProfilesFromConfig_ReportsHardwareProfileIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		migCfg *MIGConfig
		want   map[int]int
	}{
		{"a 7-slice board", &MIGConfig{MaxGPUInstances: 7, ProfileIDs: migProfileIDs7Slice, SupportedProfiles: a100SupportedProfiles()}, map[int]int{
			nvml.GPU_INSTANCE_PROFILE_1_SLICE:      19,
			nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: 20,
			nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: 15,
			nvml.GPU_INSTANCE_PROFILE_2_SLICE:      14,
			nvml.GPU_INSTANCE_PROFILE_3_SLICE:      9,
			nvml.GPU_INSTANCE_PROFILE_4_SLICE:      5,
			nvml.GPU_INSTANCE_PROFILE_7_SLICE:      0,
		}},
		// The A30 has its own numbering, and it is not the 7-slice one
		// narrowed: its +me profiles are 21 and 6 where an A100's are 20 and
		// 15. All five are what an A30's -lgip listing prints.
		{"a 4-slice board", &MIGConfig{MaxGPUInstances: 4, ProfileIDs: migProfileIDs4Slice, SupportedProfiles: a30SupportedProfiles()}, map[int]int{
			nvml.GPU_INSTANCE_PROFILE_1_SLICE:      14,
			nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: 21, // 1g.6gb+me
			nvml.GPU_INSTANCE_PROFILE_2_SLICE:      5,
			nvml.GPU_INSTANCE_PROFILE_2_SLICE_REV1: 6, // 2g.12gb+me
			nvml.GPU_INSTANCE_PROFILE_4_SLICE:      0,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ids, ok := migProfilesFromConfig(tt.migCfg, a100_40GiB)
			require.True(t, ok)

			for profileEnum, wantID := range tt.want {
				require.Equal(t, wantID, ids.reported(profileEnum),
					"profile enum %d should report ID %d", profileEnum, wantID)
				// The reverse direction is what nvmlDeviceCreateGpuInstance
				// needs, and a one-way table would let create silently pick
				// the profile whose enum happens to equal the ID.
				gotEnum, ok := ids.enumOf(wantID)
				require.True(t, ok, "reported ID %d should resolve to a profile", wantID)
				require.Equal(t, profileEnum, gotEnum,
					"reported ID %d should resolve back to profile enum %d", wantID, profileEnum)
			}
		})
	}
}

// TestMIGProfilesFromConfig_EveryPublishedProfileResolvesBack is the invariant
// a per-geometry ID table has to hold: whatever a board advertises, it must
// accept.
//
// Pinning IDs one by one cannot enforce this, because the profiles a partial
// table forgets are exactly the ones such a test forgets too. Enumerating the
// board instead is what makes a half-mapped table fail here: the unmapped
// profile still enumerates, under its enum, and then `nvidia-smi mig -cgi
// <that id>` is refused for a profile the board just listed.
func TestMIGProfilesFromConfig_EveryPublishedProfileResolvesBack(t *testing.T) {
	t.Parallel()

	boards := []struct {
		name   string
		migCfg *MIGConfig
	}{
		{"a 7-slice board", &MIGConfig{MaxGPUInstances: 7, ProfileIDs: migProfileIDs7Slice, SupportedProfiles: a100SupportedProfiles()}},
		{"a 4-slice board", &MIGConfig{MaxGPUInstances: 4, ProfileIDs: migProfileIDs4Slice, SupportedProfiles: a30SupportedProfiles()}},
		// A board declaring no listing publishes every profile under its
		// enum, which has to resolve back just the same.
		{"a board that publishes no listing", &MIGConfig{
			MaxGPUInstances: 3,
			SupportedProfiles: []MIGProfileSpec{
				{Name: "1g.8gb", NVMLProfile: "1_SLICE", Instances: 3, MemoryMB: 8192, Multiprocessors: 15},
				{Name: "3g.24gb", NVMLProfile: "3_SLICE", Instances: 1, MemoryMB: 24576, Multiprocessors: 45},
			},
		}},
	}

	for _, board := range boards {
		t.Run(board.name, func(t *testing.T) {
			t.Parallel()
			profiles, ids, ok := migProfilesFromConfig(board.migCfg, a100_40GiB)
			require.True(t, ok)
			require.NotEmpty(t, profiles.GpuInstanceProfiles)

			for profileEnum := range profiles.GpuInstanceProfiles {
				id := ids.reported(profileEnum)
				gotEnum, resolved := ids.enumOf(id)
				require.True(t, resolved,
					"profile enum %d is advertised under ID %d, which must be accepted back",
					profileEnum, id)
				require.Equal(t, profileEnum, gotEnum,
					"ID %d is advertised for profile enum %d but resolves to %d",
					id, profileEnum, gotEnum)
			}
		})
	}
}

// TestMIGProfilesFromConfig_ProfileIDsAreUniquePerBoard guards the fallback. An
// unmapped profile reports its own enum, so a board that is only partly mapped
// could report one profile's enum as another's hardware ID and collapse the two
// onto one partition size.
//
// Only the boards declaring an ID table are listed. On a board without one
// every profile reports its own enum, so this would assert nothing beyond the
// map's keys being distinct — true of any map. What is worth pinning about
// those boards is that they report the enum at all, which is
// TestProfileIDsForListing_ABoardPublishingNoListingReportsTheEnum.
func TestMIGProfilesFromConfig_ProfileIDsAreUniquePerBoard(t *testing.T) {
	t.Parallel()

	boards := []struct {
		name   string
		migCfg *MIGConfig
	}{
		{"a 7-slice board", &MIGConfig{MaxGPUInstances: 7, ProfileIDs: migProfileIDs7Slice, SupportedProfiles: a100SupportedProfiles()}},
		{"a 4-slice board", &MIGConfig{MaxGPUInstances: 4, ProfileIDs: migProfileIDs4Slice, SupportedProfiles: a30SupportedProfiles()}},
	}

	for _, board := range boards {
		t.Run(board.name, func(t *testing.T) {
			t.Parallel()
			profiles, ids, ok := migProfilesFromConfig(board.migCfg, a100_40GiB)
			require.True(t, ok)

			seen := map[int]int{}
			for profileEnum := range profiles.GpuInstanceProfiles {
				id := ids.reported(profileEnum)
				other, dup := seen[id]
				require.False(t, dup,
					"profile enums %d and %d both report ID %d", other, profileEnum, id)
				seen[id] = profileEnum
			}
		})
	}
}

// TestProfileIDsForListing_ABoardPublishingNoListingReportsTheEnum documents
// what a board NVIDIA publishes no listing for gets, and that it is also what
// a board gets by saying nothing. Only the 7- and 4-slice listings have been
// transcribed from the MIG guide; anything else keeps NVML's own numbering
// rather than a guessed one, because a guessed ID reads as correct and then
// partitions the board wrongly.
func TestProfileIDsForListing_ABoardPublishingNoListingReportsTheEnum(t *testing.T) {
	t.Parallel()

	for _, listing := range []string{"", migProfileIDsNone} {
		ids, known := profileIDsForListing(listing)
		require.True(t, known, "%q must be an accepted listing", listing)
		require.Nil(t, ids)
		require.Equal(t, nvml.GPU_INSTANCE_PROFILE_1_SLICE,
			ids.reported(nvml.GPU_INSTANCE_PROFILE_1_SLICE))
		gotEnum, ok := ids.enumOf(nvml.GPU_INSTANCE_PROFILE_7_SLICE)
		require.True(t, ok, "a board with no ID table resolves every id to itself")
		require.Equal(t, nvml.GPU_INSTANCE_PROFILE_7_SLICE, gotEnum)
	}

	_, known := profileIDsForListing("6_slice")
	require.False(t, known, "a listing nobody transcribed is not silently accepted")
}

// TestMIGProfilesFromConfig_BlackwellPublishesNoProfileIDs reads the shipped
// Blackwell profiles rather than a synthetic config, because the defect it
// guards lives in the YAML: all three boards declare seven slices, and a
// numbering chosen by board width therefore handed them Hopper's IDs. Under
// those, `mig -cgi 0` partitions the whole board where these boards hand back
// a 1g, and `-cgi 4` is refused. NVIDIA publishes no Blackwell listing, and
// every one of these profiles ships with a comment saying the mock reports
// NVML's enum.
func TestMIGProfilesFromConfig_BlackwellPublishesNoProfileIDs(t *testing.T) {
	t.Parallel()

	for _, profile := range []string{"b200", "gb200", "gb300"} {
		t.Run(profile, func(t *testing.T) {
			t.Parallel()

			migCfg := migConfigOfShippedProfile(t, profile)
			profiles, ids, supported := migProfilesFromConfig(migCfg, a100_40GiB)
			require.True(t, supported)
			require.Nil(t, ids, "%s must publish no profile ID listing", profile)

			for profileEnum := range profiles.GpuInstanceProfiles {
				require.Equal(t, profileEnum, ids.reported(profileEnum),
					"profile enum %d must report itself, not a Hopper ID", profileEnum)
			}
		})
	}
}

// gpuInstanceProfileEnums is transcribed by hand and its values are asserted
// nowhere else: validation consumes only the second result of
// gpuInstanceProfileEnum, so a transposition binding "3_SLICE" to the 4-slice
// enum compiles, and every board declaring that profile would then partition
// to the wrong width.
//
// The assertions bound the table without restating its 18 bindings. Counting
// against NVML's own total catches a profile left out, which no board could
// then declare, and distinctness catches two names bound to one enum. Neither
// catches a permutation — swapping "3_SLICE" and "4_SLICE" keeps all 18
// distinct at a count of 18, which is the shape a misaligned block edit
// produces — so each name's leading digit is checked against the span its enum
// actually occupies. That catches any permutation across slice counts, and
// pins the two hand-maintained enumerations of these profiles, this table and
// gpuInstanceSliceCount, against each other.
//
// A swap within one slice count stays invisible: "1_SLICE_GFX" and
// "1_SLICE_NO_ME" both span one slice, so nothing here separates them. Closing
// that would need either a restatement of the table or a map from name suffix
// to capability, which does not exist.
func TestGPUInstanceProfileEnums_BindEveryNVMLProfileExactlyOnce(t *testing.T) {
	t.Parallel()

	require.Len(t, gpuInstanceProfileEnums, nvml.GPU_INSTANCE_PROFILE_COUNT)

	names := make(map[int]string, len(gpuInstanceProfileEnums))
	for name, profileEnum := range gpuInstanceProfileEnums {
		require.NotContainsf(t, names, profileEnum,
			"%q and %q both bind NVML profile %d", name, names[profileEnum], profileEnum)
		names[profileEnum] = name

		wantSpan, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		require.NoErrorf(t, err, "%q does not begin with the slice count it names", name)

		gotSpan, ok := gpuInstanceSliceCount(profileEnum)
		require.Truef(t, ok, "%q binds NVML profile %d, which has no slice count", name, profileEnum)
		require.Equalf(t, wantSpan, gotSpan, "%q binds an enum spanning %d slices", name, gotSpan)
	}
}

func TestMIGProfileName_PrefersTheDeclaredName(t *testing.T) {
	t.Parallel()

	// No shipped board needs this now that each declares its real capacity,
	// which is the point: a board whose allocation does not land on a clean
	// fraction of what it declares still names its own partitions. Computing
	// 1g.23gb against a 192 GiB board would give 1g.24gb.
	name, err := migProfileName("1g.23gb",
		nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		23552, 206158430208)
	require.NoError(t, err)
	require.Equal(t, "1g.23gb", name)
}

func TestMIGProfileName_ComposesTheComputeInstanceFormFromTheDeclaredName(t *testing.T) {
	t.Parallel()

	// A compute instance narrower than its GPU instance keeps the Nc. prefix,
	// which the declared name does not carry: it names the GPU instance only.
	//
	// The declared memory deliberately disagrees with what the computation
	// would produce — 40960 MB of an 80 GiB board is half of it, which computes
	// to "3g.40gb" — so the expected name is reachable only by composing the
	// prefix onto the declared string. A fixture where the two agree passes
	// with the declared branch deleted.
	name, err := migProfileName("3g.37gb",
		nvml.GPU_INSTANCE_PROFILE_3_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		40960, h100_80GiB)
	require.NoError(t, err)
	require.Equal(t, "1c.3g.37gb", name)
}

func TestMIGProfileName_FallsBackToComputingWhenNothingIsDeclared(t *testing.T) {
	t.Parallel()

	name, err := migProfileName("",
		nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
		10240, h100_80GiB)
	require.NoError(t, err)
	require.Equal(t, "1g.10gb", name)
}

func TestMIGProfilesFromConfig_ReportsTheDeclaredRows(t *testing.T) {
	t.Parallel()

	// An 80 GiB board, so a 10240 MB slice is an exact eighth of it.
	profiles, ids, supported := migProfilesFromConfig(&MIGConfig{
		MaxGPUInstances: 7,
		ProfileIDs:      migProfileIDs7Slice,
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240, Multiprocessors: 16, CopyEngines: 1, Decoders: 1, JPEG: 1},
			{Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 3, MemoryMB: 20480, Multiprocessors: 32, CopyEngines: 2, Decoders: 2, JPEG: 2},
		},
	}, h100_80GiB)

	require.True(t, supported)
	require.Len(t, profiles.GpuInstanceProfiles, 2, "exactly the declared rows, nothing inherited")

	oneSlice := profiles.GpuInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_1_SLICE]
	require.EqualValues(t, 7, oneSlice.InstanceCount)
	require.EqualValues(t, 10240, oneSlice.MemorySizeMB)
	require.EqualValues(t, 1, oneSlice.JpegCount)
	require.Len(t, profiles.GpuInstancePlacements[nvml.GPU_INSTANCE_PROFILE_1_SLICE], 7)

	// A 2-slice GPU instance offers a 1-slice compute instance, its
	// media-extension revision, and one spanning both slices.
	require.Len(t, profiles.ComputeInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_2_SLICE], 3)
	ci := profiles.ComputeInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_2_SLICE][nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE]
	require.EqualValues(t, 2, ci.InstanceCount, "two 1-slice compute instances fit a 2-slice GPU instance")
	require.EqualValues(t, 16, ci.MultiprocessorCount, "multiprocessors scale with the slice ratio")
	// The engines of a GPU instance are shared by every compute instance in
	// it, rather than divided between them, which is how go-nvml's own tables
	// report them and what a MIG device's attributes are read from.
	require.EqualValues(t, 2, ci.SharedCopyEngineCount)
	require.EqualValues(t, 2, ci.SharedJpegCount)

	// A board declaring the datacenter listing reports its IDs.
	require.Equal(t, 19, ids.reported(nvml.GPU_INSTANCE_PROFILE_1_SLICE))
}

func TestMIGProfilesFromConfig_ABoardDeclaringNothingIsNotCapable(t *testing.T) {
	t.Parallel()

	_, _, supported := migProfilesFromConfig(&MIGConfig{MaxGPUInstances: 7}, h100_80GiB)
	require.False(t, supported, "max_gpu_instances alone must not make a board MIG-capable")

	_, _, supported = migProfilesFromConfig(nil, h100_80GiB)
	require.False(t, supported)
}

// TestMIGProfilesFromConfig_DeclaredRowsWithoutABoardWidthGetNoPlacements
// pins the one state validation exists to refuse. A row needs a board to sit
// on: with no width, derivePlacements can offer nowhere to put it, and the
// device ceiling derived from those placements falls to zero. Config load
// rejects the document, so no YAML can reach this — but migProfilesFromConfig
// is reachable directly, and answering "capable, with nowhere to partition"
// silently is worse than answering it visibly.
func TestMIGProfilesFromConfig_DeclaredRowsWithoutABoardWidthGetNoPlacements(t *testing.T) {
	t.Parallel()

	migCfg := &MIGConfig{
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
		},
	}
	profiles, _, supported := migProfilesFromConfig(migCfg, h100_80GiB)

	require.True(t, supported)
	require.Empty(t, profiles.GpuInstancePlacements[nvml.GPU_INSTANCE_PROFILE_1_SLICE])
	require.Zero(t, resolveMaxGPUInstances(migCfg, supported, profiles))
}

// deriveComputeInstanceProfiles skips any offered profile it cannot size,
// which would silently drop a compute instance from a MIG device's listing.
// This asserts the skip is unreachable: every profile any GPU instance width
// offers — the transcribed listings and the fallback for widths NVIDIA
// publishes no listing for — is one computeInstanceSliceCount can size.
func TestComputeInstanceProfilesOffered_EveryOfferedProfileHasASliceCount(t *testing.T) {
	t.Parallel()

	for giSlices := 0; giSlices <= maxGPUInstanceSlices; giSlices++ {
		for _, ciEnum := range computeInstanceProfilesOffered(giSlices) {
			_, ok := computeInstanceSliceCount(ciEnum)
			require.True(t, ok,
				"GPU instance of %d slices offers compute instance profile %d, which has no slice count",
				giSlices, ciEnum)
		}
	}
}
