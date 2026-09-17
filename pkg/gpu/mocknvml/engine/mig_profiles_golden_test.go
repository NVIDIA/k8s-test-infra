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
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/gpus"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// migGoldenBoard is one shipped GPU profile, named the way the YAML names it.
type migGoldenBoard struct {
	profile     string // the YAML basename
	deviceName  string
	memoryBytes uint64
}

// migGoldenBoards are the five MIG-capable profiles the chart ships. The
// device names and memory sizes are copied from
// deployments/nvml-mock/helm/nvml-mock/profiles/<profile>.yaml.
var migGoldenBoards = []migGoldenBoard{
	{profile: "a100", deviceName: "NVIDIA A100-SXM4-40GB", memoryBytes: 42949672960},
	{profile: "h100", deviceName: "NVIDIA H100 80GB HBM3", memoryBytes: 85899345920},
	{profile: "b200", deviceName: "NVIDIA B200", memoryBytes: 206158430208},
	{profile: "gb200", deviceName: "NVIDIA GB200", memoryBytes: 206158430208},
	{profile: "gb300", deviceName: "NVIDIA GB300 NVL", memoryBytes: 309237645312},
}

// migGoldenRow is the part of a profile that must survive the move to YAML
// unchanged. Engine counts are deliberately absent: go-nvml's tables disagree
// with NVIDIA's published ones, and are corrected separately.
type migGoldenRow struct {
	Name          string
	SliceCount    uint32
	InstanceCount uint32
	MemorySizeMB  uint64
	Placements    []string
}

// migConfigOfShippedProfile reads a board's MIG block out of the profile the
// chart ships, through the same decode and validation the library performs at
// load. Reading the shipped file rather than a fixture is the point: these
// tests assert what a user of the chart gets.
func migConfigOfShippedProfile(t *testing.T, profile string) *MIGConfig {
	t.Helper()

	path := filepath.Join("../../../../deployments/nvml-mock/helm/nvml-mock/profiles", profile+".yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var cfg YAMLConfig
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.NoError(t, validateYAMLConfig(&cfg))

	return cfg.DeviceDefaults.MIG
}

// migGoldenSnapshot renders the board's profile table in a form a diff can
// read, keyed by the NVML profile enum.
func migGoldenSnapshot(t *testing.T, b migGoldenBoard) map[int]migGoldenRow {
	t.Helper()

	migCfg := migConfigOfShippedProfile(t, b.profile)
	profiles, _, supported := migProfilesFromConfig(migCfg, b.memoryBytes)
	require.True(t, supported, "%s must declare a MIG profile table", b.profile)

	snapshot := make(map[int]migGoldenRow, len(profiles.GpuInstanceProfiles))
	for profileEnum, info := range profiles.GpuInstanceProfiles {
		name, err := migProfileName(declaredProfileNames(migCfg)[profileEnum],
			profileEnum, ciProfileSpanningGI(t, profiles, profileEnum),
			info.MemorySizeMB, b.memoryBytes)
		require.NoError(t, err)

		// Keyed by the profile enum, not info.Id. They hold the same value
		// today, but the two are different things: one is NVML's profile
		// identity, the other is the id a board reports for it.
		placements := []string{}
		for _, p := range profiles.GpuInstancePlacements[profileEnum] {
			placements = append(placements, fmt.Sprintf("%d:%d", p.Start, p.Size))
		}
		sort.Strings(placements)

		snapshot[profileEnum] = migGoldenRow{
			Name:          name,
			SliceCount:    info.SliceCount,
			InstanceCount: info.InstanceCount,
			MemorySizeMB:  info.MemorySizeMB,
			Placements:    placements,
		}
	}
	return snapshot
}

// ciProfileSpanningGI returns the compute-instance profile that fills the whole
// GPU instance, which is the one whose name carries no "Nc." prefix.
//
// The widest is taken strictly, so a tie cannot be broken by map iteration
// order: two profiles of equal slice count render the same name today, which
// would make this pick arbitrarily and the test intermittent later.
func ciProfileSpanningGI(t *testing.T, profiles gpus.MIGProfileConfig, giProfileEnum int) int {
	t.Helper()

	widest, widestSlices := -1, uint32(0)
	for ciEnum, ci := range profiles.ComputeInstanceProfiles[giProfileEnum] {
		if ci.SliceCount > widestSlices {
			widest, widestSlices = ciEnum, ci.SliceCount
		}
	}
	require.NotEqual(t, -1, widest, "GPU instance profile %d offers no compute instance", giProfileEnum)
	return widest
}

// migGoldenWant is what the boards report, transcribed from this test's own
// output rather than from NVIDIA's tables: it records the behaviour a reader
// can observe, so a change that moves any of it has to be deliberate.
//
// The placement literals are identical across all five boards, which is the
// check that the derivation is right — placement geometry belongs to the
// seven-slice layout, not to a board's capacity.
var migGoldenWant = map[string]map[int]migGoldenRow{
	"a100": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.5gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 4864, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.5gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 4864, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.10gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 9856, Placements: []string{"0:2", "2:2", "4:2", "6:2"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.10gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 9856, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.20gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 19968, Placements: []string{"0:4", "4:4"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.20gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 19968, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.40gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 40192, Placements: []string{"0:8"}},
	},
	"h100": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.10gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 10240, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.10gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 10240, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.20gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 20480, Placements: []string{"0:2", "2:2", "4:2", "6:2"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.20gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 20480, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.40gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 40960, Placements: []string{"0:4", "4:4"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.40gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 40960, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.80gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 81920, Placements: []string{"0:8"}},
	},
	"b200": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.24gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 24576, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.24gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 24576, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.48gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 49152, Placements: []string{"0:2", "2:2", "4:2", "6:2"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.48gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 49152, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.96gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 98304, Placements: []string{"0:4", "4:4"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.96gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 98304, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.192gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 196608, Placements: []string{"0:8"}},
	},
	"gb200": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.24gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 24576, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.24gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 24576, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.48gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 49152, Placements: []string{"0:2", "2:2", "4:2", "6:2"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.48gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 49152, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.96gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 98304, Placements: []string{"0:4", "4:4"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.96gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 98304, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.192gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 196608, Placements: []string{"0:8"}},
	},
	"gb300": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.36gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 36864, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.36gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 36864, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.72gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 73728, Placements: []string{"0:2", "2:2", "4:2", "6:2"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.72gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 73728, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.144gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 147456, Placements: []string{"0:4", "4:4"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.144gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 147456, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.288gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 294912, Placements: []string{"0:8"}},
	},
}

// TestMIGProfiles_GoldenSnapshot pins the profile table of every shipped
// MIG-capable board. It is the regression net for moving this data into YAML:
// the YAML-driven path must reproduce these rows exactly.
func TestMIGProfiles_GoldenSnapshot(t *testing.T) {
	t.Parallel()

	for _, board := range migGoldenBoards {
		t.Run(board.profile, func(t *testing.T) {
			t.Parallel()

			want := migGoldenWant[board.profile]
			require.NotEmpty(t, want, "no golden rows recorded for %s", board.profile)
			require.Equal(t, want, migGoldenSnapshot(t, board))
		})
	}
}

// TestMIGProfiles_EngineCountsMatchThePublishedTables guards the correction
// this change makes. go-nvml's H100 table carried the A100's engine counts —
// 0 JPEG throughout, 5 NVDEC and 7 copy engines on 7g — where NVIDIA publishes
// 1 JPEG per slice and 7 NVDEC / 8 copy engines. A future edit that
// reintroduces the A100 numbers fails here.
func TestMIGProfiles_EngineCountsMatchThePublishedTables(t *testing.T) {
	t.Parallel()

	profiles, _, supported := migProfilesFromConfig(migConfigOfShippedProfile(t, "h100"), 85899345920)
	require.True(t, supported)

	sevenSlice := profiles.GpuInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_7_SLICE]
	require.EqualValues(t, 7, sevenSlice.DecoderCount, "Table 10 publishes 7 NVDECs for 7g.80gb")
	require.EqualValues(t, 7, sevenSlice.JpegCount, "Table 10 publishes 7 JPEG for 7g.80gb")
	require.EqualValues(t, 1, sevenSlice.OfaCount, "Table 10 publishes 1 OFA for 7g.80gb")
	require.EqualValues(t, 8, sevenSlice.CopyEngineCount, "Table 10 publishes 8 copy engines for 7g.80gb")

	oneSlice := profiles.GpuInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_1_SLICE]
	require.EqualValues(t, 1, oneSlice.JpegCount, "Table 10 publishes 1 JPEG per slice, not 0")
}

// TestMIGProfiles_AFullBoardPartitionSpansTheBoard guards the memory fix. The
// Blackwell profiles all resolved to the B200-180GB table while naming their
// slices against their own capacity, so gb300's 7g.288gb reported 180 GiB of a
// 288 GiB board. A full-board GPU instance has to account for the whole board,
// give or take the reservation real hardware also keeps back.
func TestMIGProfiles_AFullBoardPartitionSpansTheBoard(t *testing.T) {
	t.Parallel()

	for _, board := range migGoldenBoards {
		t.Run(board.profile, func(t *testing.T) {
			t.Parallel()

			profiles, _, supported := migProfilesFromConfig(
				migConfigOfShippedProfile(t, board.profile), board.memoryBytes)
			require.True(t, supported)

			fullBoard := profiles.GpuInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_7_SLICE]
			capacityMB := board.memoryBytes / oneMiB

			require.LessOrEqual(t, fullBoard.MemorySizeMB, capacityMB,
				"a partition cannot hold more than the board")
			// a100 sits at 98% — go-nvml allocates 40192 MB of 40 GiB, which
			// tracks real hardware holding some back. 95% is loose enough for
			// that and tight enough to catch 180 of 288.
			require.Greater(t, fullBoard.MemorySizeMB*100/capacityMB, uint64(95),
				"a full-board partition leaves too much of the board unreachable")
		})
	}
}
