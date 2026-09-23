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

func TestMigProfileName_RejectsUnknownProfileIDs(t *testing.T) {
	t.Parallel()

	// A declared name must not buy a profile past the slice-count checks. Both
	// counts are still needed to decide whether the name takes the "Nc."
	// prefix, so an unresolvable ID is an error whatever the board declares —
	// returning the declared string for a profile that cannot exist would
	// advertise a partition nothing can create.
	_, err := migProfileName("1g.5gb", nvml.GPU_INSTANCE_PROFILE_COUNT, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Error(t, err, "a declared name does not excuse an out-of-range GPU instance profile")

	_, err = migProfileName("1g.5gb", nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_COUNT)
	require.Error(t, err, "a declared name does not excuse an out-of-range compute instance profile")
}

// a30SupportedProfiles is a 4-slice board's declared table, transcribed from
// go-nvml's A30 24GB with the reported ids from the A30's own `mig -lgip`
// listing. It is here for the reported-ID tests: the A30 is the one geometry
// these tests reach for that is not seven slices wide, and its numbering is a
// different table rather than the 7-slice one narrowed — its +me profiles are
// 21 and 6 where an A100's are 20 and 15.
//
// The rows declare no placements or compute instances, which config load
// refuses. They do not need to: these tests read the id table alone, and no
// board ships this geometry for them to be checked against.
func a30SupportedProfiles() []MIGProfileSpec {
	return []MIGProfileSpec{
		{Name: "1g.6gb", NVMLProfile: "1_SLICE", ProfileID: 14, Instances: 4, MemoryMB: 5836, Multiprocessors: 14, CopyEngines: 1},
		{Name: "1g.6gb+me", NVMLProfile: "1_SLICE_REV1", ProfileID: 21, Instances: 1, MemoryMB: 5836, Multiprocessors: 14, CopyEngines: 1, Decoders: 1, JPEG: 1, OFA: 1},
		{Name: "2g.12gb", NVMLProfile: "2_SLICE", ProfileID: 5, Instances: 2, MemoryMB: 11672, Multiprocessors: 28, CopyEngines: 2, Decoders: 2},
		{Name: "2g.12gb+me", NVMLProfile: "2_SLICE_REV1", ProfileID: 6, Instances: 1, MemoryMB: 11672, Multiprocessors: 28, CopyEngines: 2, Decoders: 2, JPEG: 1, OFA: 1},
		{Name: "4g.24gb", NVMLProfile: "4_SLICE", ProfileID: 0, Instances: 1, MemoryMB: 23344, Multiprocessors: 56, CopyEngines: 4, Decoders: 4, JPEG: 1, OFA: 1},
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
// The table is built from the id each row declares. A board reports its enums
// by declaring them, which the Blackwell boards do and a test below reads out
// of the shipped profiles.
func TestMIGProfilesFromConfig_ReportsHardwareProfileIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		migCfg *MIGConfig
		want   map[int]int
	}{
		{"a 7-slice board", &MIGConfig{MaxGPUInstances: 7, SupportedProfiles: a100SupportedProfiles()}, map[int]int{
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
		{"a 4-slice board", &MIGConfig{MaxGPUInstances: 4, SupportedProfiles: a30SupportedProfiles()}, map[int]int{
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
		{"a 7-slice board", &MIGConfig{MaxGPUInstances: 7, SupportedProfiles: a100SupportedProfiles()}},
		{"a 4-slice board", &MIGConfig{MaxGPUInstances: 4, SupportedProfiles: a30SupportedProfiles()}},
		// A board NVIDIA publishes no listing for declares its NVML enums as
		// its ids, which have to resolve back just the same.
		{"a board that publishes its enums", &MIGConfig{
			MaxGPUInstances: 3,
			SupportedProfiles: []MIGProfileSpec{
				{Name: "1g.8gb", NVMLProfile: "1_SLICE", ProfileID: nvml.GPU_INSTANCE_PROFILE_1_SLICE, Instances: 3, MemoryMB: 8192, Multiprocessors: 15},
				{Name: "3g.24gb", NVMLProfile: "3_SLICE", ProfileID: nvml.GPU_INSTANCE_PROFILE_3_SLICE, Instances: 1, MemoryMB: 24576, Multiprocessors: 45},
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

// TestMIGProfilesFromConfig_ProfileIDsAreUniquePerBoard is the invariant a
// per-row id declaration has to hold. Two rows publishing one id collapse two
// partition sizes onto whatever `mig -cgi <id>` resolves to first.
//
// validateMIGSupportedProfiles refuses that at load, which is where a
// contributor meets it; this reads the assembled table instead, so a
// transcription that lost a row's id — and therefore reported the enum for it
// — fails here too.
func TestMIGProfilesFromConfig_ProfileIDsAreUniquePerBoard(t *testing.T) {
	t.Parallel()

	boards := []struct {
		name   string
		migCfg *MIGConfig
	}{
		{"a 7-slice board", &MIGConfig{MaxGPUInstances: 7, SupportedProfiles: a100SupportedProfiles()}},
		{"a 4-slice board", &MIGConfig{MaxGPUInstances: 4, SupportedProfiles: a30SupportedProfiles()}},
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

// TestMIGProfileIDs_ABoardWithNoTableReportsTheEnum documents what a board
// that declares no profiles at all answers. It is the only way to reach a nil
// table now that every declared row publishes an id, and the answers have to
// stay go-nvml's own numbering rather than nothing: a MIGConfig built in code
// reaches these two methods without going through a profile table.
func TestMIGProfileIDs_ABoardWithNoTableReportsTheEnum(t *testing.T) {
	t.Parallel()

	ids := migProfileIDs(nil)
	require.Equal(t, nvml.GPU_INSTANCE_PROFILE_1_SLICE,
		ids.reported(nvml.GPU_INSTANCE_PROFILE_1_SLICE))
	gotEnum, ok := ids.enumOf(nvml.GPU_INSTANCE_PROFILE_7_SLICE)
	require.True(t, ok, "a board with no ID table resolves every id to itself")
	require.Equal(t, nvml.GPU_INSTANCE_PROFILE_7_SLICE, gotEnum)
}

// TestMIGProfilesFromConfig_BlackwellPublishesNoProfileIDs reads the shipped
// Blackwell profiles rather than a synthetic config, because the defect it
// guards lives in the YAML: all three boards declare seven slices, and a
// numbering chosen by board width therefore handed them Hopper's IDs. Under
// those, `mig -cgi 0` partitions the whole board where these boards hand back
// a 1g, and `-cgi 4` is refused. NVIDIA publishes no Blackwell listing, and
// every one of these profiles ships with a comment saying the mock reports
// NVML's enum.
//
// Each row now declares its own profile_id, so the table is present and maps
// every enum to itself where it used to be absent and resolve to itself by
// default. What the board reports is the assertion; whether the table exists
// is not.
func TestMIGProfilesFromConfig_BlackwellPublishesNoProfileIDs(t *testing.T) {
	t.Parallel()

	for _, profile := range []string{"b200", "gb200", "gb300"} {
		t.Run(profile, func(t *testing.T) {
			t.Parallel()

			migCfg := migConfigOfShippedProfile(t, profile)
			profiles, ids, supported := migProfilesFromConfig(migCfg, a100_40GiB)
			require.True(t, supported)

			for profileEnum := range profiles.GpuInstanceProfiles {
				require.Equal(t, profileEnum, ids.reported(profileEnum),
					"profile enum %d must report itself, not a Hopper ID", profileEnum)
				gotEnum, ok := ids.enumOf(profileEnum)
				require.True(t, ok,
					"`mig -cgi %d` must still be accepted on %s", profileEnum, profile)
				require.Equal(t, profileEnum, gotEnum)
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

	// A board's own spelling is reported as declared, whatever fraction of the
	// board the partition's allocation actually works out to: the name comes
	// from NVIDIA's published table for the product, not from its capacity.
	name, err := migProfileName("1g.23gb",
		nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.NoError(t, err)
	require.Equal(t, "1g.23gb", name)
}

func TestMIGProfileName_ComposesTheComputeInstanceFormFromTheDeclaredName(t *testing.T) {
	t.Parallel()

	// A compute instance narrower than its GPU instance keeps the Nc. prefix,
	// which the declared name does not carry: it names the GPU instance only.
	name, err := migProfileName("3g.37gb",
		nvml.GPU_INSTANCE_PROFILE_3_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.NoError(t, err)
	require.Equal(t, "1c.3g.37gb", name)
}

func TestMIGProfileName_RefusesAProfileTheBoardDoesNotName(t *testing.T) {
	t.Parallel()

	// Validation requires a `name` of every row, so no validated board reaches
	// this. It is asserted rather than assumed because the alternative to an
	// error is a partition advertised under a name — and so a
	// nvidia.com/mig-<name> resource — that no NVIDIA table publishes.
	_, err := migProfileName("",
		nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Error(t, err)
}

func TestMIGProfilesFromConfig_ReportsTheDeclaredRows(t *testing.T) {
	t.Parallel()

	// An 80 GiB board, so a 10240 MB slice is an exact eighth of it.
	profiles, ids, supported := migProfilesFromConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: []MIGProfileSpec{
			{
				Name: "1g.10gb", NVMLProfile: "1_SLICE", ProfileID: 19,
				Instances: 7, MemoryMB: 10240,
				Multiprocessors: 16, CopyEngines: 1, Decoders: 1, JPEG: 1,
				Placements: []MIGPlacementSpec{
					{Start: 0, Size: 1}, {Start: 1, Size: 1}, {Start: 2, Size: 1},
					{Start: 3, Size: 1}, {Start: 4, Size: 1}, {Start: 5, Size: 1},
					{Start: 6, Size: 1},
				},
				ComputeInstances: []MIGComputeInstanceSpec{
					{NVMLProfile: "1_SLICE", Slices: 1, Instances: 1, Multiprocessors: 16, SharedCopyEngines: 1, Decoders: 1, JPEG: 1},
				},
			},
			{
				Name: "2g.20gb", NVMLProfile: "2_SLICE", ProfileID: 14,
				Instances: 3, MemoryMB: 20480,
				Multiprocessors: 32, CopyEngines: 2, Decoders: 2, JPEG: 2,
				Placements: []MIGPlacementSpec{
					{Start: 0, Size: 2}, {Start: 2, Size: 2}, {Start: 4, Size: 2},
				},
				ComputeInstances: []MIGComputeInstanceSpec{
					{NVMLProfile: "1_SLICE", Slices: 1, Instances: 2, Multiprocessors: 16, SharedCopyEngines: 2, Decoders: 2, JPEG: 2},
					{NVMLProfile: "1_SLICE_REV1", Slices: 1, Instances: 2, Multiprocessors: 16, SharedCopyEngines: 2, Decoders: 2, JPEG: 2},
					{NVMLProfile: "2_SLICE", Slices: 2, Instances: 1, Multiprocessors: 32, SharedCopyEngines: 2, Decoders: 2, JPEG: 2},
				},
			},
		},
	}, h100_80GiB)

	require.True(t, supported)
	require.Len(t, profiles.GpuInstanceProfiles, 2, "exactly the declared rows, nothing inherited")

	oneSlice := profiles.GpuInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_1_SLICE]
	require.EqualValues(t, 7, oneSlice.InstanceCount)
	require.EqualValues(t, 10240, oneSlice.MemorySizeMB)
	require.EqualValues(t, 1, oneSlice.JpegCount)
	require.Len(t, profiles.GpuInstancePlacements[nvml.GPU_INSTANCE_PROFILE_1_SLICE], 7)

	// The 2-slice row declares a 1-slice compute instance, its media-extension
	// revision, and one spanning both slices — the listing NVIDIA publishes
	// for a 2g partition.
	require.Len(t, profiles.ComputeInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_2_SLICE], 3)
	ci := profiles.ComputeInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_2_SLICE][nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE]
	require.EqualValues(t, 2, ci.InstanceCount, "two 1-slice compute instances fit a 2-slice GPU instance")
	require.EqualValues(t, 16, ci.MultiprocessorCount)
	// The engines of a GPU instance are shared by every compute instance in
	// it, rather than divided between them, which is how go-nvml's own tables
	// report them and what a MIG device's attributes are read from.
	require.EqualValues(t, 2, ci.SharedCopyEngineCount)
	require.EqualValues(t, 2, ci.SharedJpegCount)

	require.Equal(t, 19, ids.reported(nvml.GPU_INSTANCE_PROFILE_1_SLICE))
}

func TestMIGProfilesFromConfig_ABoardDeclaringNothingIsNotCapable(t *testing.T) {
	t.Parallel()

	_, _, supported := migProfilesFromConfig(&MIGConfig{MaxGPUInstances: 7}, h100_80GiB)
	require.False(t, supported, "max_gpu_instances alone must not make a board MIG-capable")

	_, _, supported = migProfilesFromConfig(nil, h100_80GiB)
	require.False(t, supported)
}

// TestMIGProfilesFromConfig_ARowDeclaringNoPlacementHasNowhereToSit pins what
// a row that declares nothing now gets: nothing. The geometry used to be
// derived from the board width, so this same row came back with seven
// placements and a full compute-instance listing on a 7-slice board.
//
// Config load refuses the document — validateMIGProfileSpec names both missing
// keys — so no YAML can reach this. migProfilesFromConfig is reachable
// directly, and what it must not do is invent the row's geometry back.
func TestMIGProfilesFromConfig_ARowDeclaringNoPlacementHasNowhereToSit(t *testing.T) {
	t.Parallel()

	migCfg := &MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
		},
	}
	require.ErrorContains(t, validateMIGConfig(migCfg), "placements is required")

	profiles, _, supported := migProfilesFromConfig(migCfg, h100_80GiB)
	require.True(t, supported)
	require.Empty(t, profiles.GpuInstancePlacements[nvml.GPU_INSTANCE_PROFILE_1_SLICE])
	require.Empty(t, profiles.ComputeInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_1_SLICE])
	// The compute-instance placement table follows the listing, so the
	// difference between "fits nowhere" and "not offered" is not invented for
	// a row that offers nothing.
	require.Empty(t, profiles.ComputeInstancePlacements[nvml.GPU_INSTANCE_PROFILE_1_SLICE])
}

// TestComputeInstanceProfileEnums_SpellEveryWidthTheyName is the guard
// validateMIGComputeInstances relies on when it treats an enum with no known
// slice count as unreachable.
//
// It also holds each name to the width it advertises. The name is what a
// profile YAML writes and the enum is what NVML sizes, so an entry bound to
// the wrong enum would let a row read as 3c while partitioning four slices —
// and go-nvlib names the nvidia.com/mig-<name> resource from the width, not
// from the spelling.
func TestComputeInstanceProfileEnums_SpellEveryWidthTheyName(t *testing.T) {
	t.Parallel()

	for name, ciEnum := range computeInstanceProfileEnums {
		slices, ok := computeInstanceSliceCount(ciEnum)
		require.True(t, ok, "compute instance profile %q (enum %d) has no slice count", name, ciEnum)
		require.True(t, strings.HasPrefix(name, strconv.Itoa(slices)+"_SLICE"),
			"compute instance profile %q is bound to an enum of %d slices", name, slices)
	}
}

// TestMIGProfilesFromConfig_TranscribesTheDeclaredGeometry asserts a row's own
// placements, compute instances and reported id reach the tables unchanged.
//
// Every declared value below is one no algorithm over the row's shape would
// produce: a single 1-unit placement at offset 3 where a 1g on a 7-slice board
// would be given seven, a compute-instance listing without the media-extension
// profile that every GPU instance offers, and an id NVIDIA publishes for no
// profile. That is the point of declaring them — a board whose geometry the
// derivation could not express can now be described.
func TestMIGProfilesFromConfig_TranscribesTheDeclaredGeometry(t *testing.T) {
	t.Parallel()

	const declaredID = 42
	migCfg := &MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: []MIGProfileSpec{{
			Name: "1g.10gb", NVMLProfile: "1_SLICE", Slices: 1, Instances: 7, MemoryMB: 10240,
			ProfileID:  declaredID,
			Placements: []MIGPlacementSpec{{Start: 3, Size: 1}},
			ComputeInstances: []MIGComputeInstanceSpec{{
				NVMLProfile: "1_SLICE", Slices: 1, Instances: 1,
				Multiprocessors: 16, SharedCopyEngines: 3,
				Decoders: 4, Encoders: 5, JPEG: 6, OFA: 7,
			}},
		}},
	}
	require.NoError(t, validateMIGConfig(migCfg))

	profiles, ids, supported := migProfilesFromConfig(migCfg, h100_80GiB)
	require.True(t, supported)

	const profileEnum = nvml.GPU_INSTANCE_PROFILE_1_SLICE
	require.Equal(t, []nvml.GpuInstancePlacement{{Start: 3, Size: 1}},
		profiles.GpuInstancePlacements[profileEnum])
	require.Equal(t, map[int]nvml.ComputeInstanceProfileInfo{
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE: {
			Id:                    nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE,
			SliceCount:            1,
			InstanceCount:         1,
			MultiprocessorCount:   16,
			SharedCopyEngineCount: 3,
			SharedDecoderCount:    4,
			SharedEncoderCount:    5,
			SharedJpegCount:       6,
			SharedOfaCount:        7,
		},
	}, profiles.ComputeInstanceProfiles[profileEnum])
	// The possible-placement table keys off the declared listing too, so a
	// compute instance the row does not declare has no entry rather than an
	// empty one — the difference between "fits nowhere" and "not offered".
	require.Equal(t, map[int][]nvml.ComputeInstancePlacement{
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE: {},
	}, profiles.ComputeInstancePlacements[profileEnum])
	require.Equal(t, declaredID, ids.reported(profileEnum))
	// profile_id is the id the board publishes, not the profile's NVML
	// identity: assigning it to Id would change what
	// nvmlDeviceGetGpuInstanceProfileInfo answers.
	require.Equal(t, uint32(profileEnum), profiles.GpuInstanceProfiles[profileEnum].Id)
}

// TestMIGProfilesFromConfig_EachRowPublishesItsOwnID pins that one row's id
// does not reach another. The ids came from a table per published listing
// before, so a board's rows shared one map; they are now per-row, and the
// table is rebuilt per device.
func TestMIGProfilesFromConfig_EachRowPublishesItsOwnID(t *testing.T) {
	t.Parallel()

	_, ids, supported := migProfilesFromConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", ProfileID: 42, Instances: 7, MemoryMB: 10240},
			{Name: "2g.20gb", NVMLProfile: "2_SLICE", ProfileID: 14, Instances: 3, MemoryMB: 20480},
		},
	}, h100_80GiB)

	require.True(t, supported)
	require.Equal(t, 42, ids.reported(nvml.GPU_INSTANCE_PROFILE_1_SLICE))
	require.Equal(t, 14, ids.reported(nvml.GPU_INSTANCE_PROFILE_2_SLICE))
	// A profile the board does not declare has no id, and falls back to its
	// enum rather than to another row's number.
	require.Equal(t, nvml.GPU_INSTANCE_PROFILE_7_SLICE,
		ids.reported(nvml.GPU_INSTANCE_PROFILE_7_SLICE))
}
