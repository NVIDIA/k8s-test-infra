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
	"sort"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/gpus"
	"github.com/stretchr/testify/require"
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
// unchanged. Engine counts are deliberately absent: NVIDIA's published tables
// disagree with go-nvml's, and correcting them is part of this change.
type migGoldenRow struct {
	Name          string
	SliceCount    uint32
	InstanceCount uint32
	MemorySizeMB  uint64
	Placements    []string
}

// migGoldenSnapshot renders the board's profile table in a form a diff can
// read, keyed by the NVML profile enum.
func migGoldenSnapshot(t *testing.T, b migGoldenBoard) map[int]migGoldenRow {
	t.Helper()

	profiles, _, supported := resolveMIGProfiles(b.deviceName, b.memoryBytes)
	require.True(t, supported, "%s must be MIG-capable", b.profile)

	snapshot := make(map[int]migGoldenRow, len(profiles.GpuInstanceProfiles))
	for profileEnum, info := range profiles.GpuInstanceProfiles {
		name, err := migProfileName("", profileEnum, ciProfileSpanningGI(t, profiles, profileEnum), info.MemorySizeMB, b.memoryBytes)
		require.NoError(t, err)

		placements := []string{}
		for _, p := range profiles.GpuInstancePlacements[int(info.Id)] {
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
func ciProfileSpanningGI(t *testing.T, profiles gpus.MIGProfileConfig, giProfileEnum int) int {
	t.Helper()

	widest, widestSlices := -1, uint32(0)
	for ciEnum, ci := range profiles.ComputeInstanceProfiles[giProfileEnum] {
		if ci.SliceCount >= widestSlices {
			widest, widestSlices = ciEnum, ci.SliceCount
		}
	}
	require.NotEqual(t, -1, widest, "GPU instance profile %d offers no compute instance", giProfileEnum)
	return widest
}

// migGoldenWant is what the boards report today, transcribed from this test's
// own output rather than from NVIDIA's tables. The two do not always agree —
// the Blackwell boards take their slice geometry from go-nvml's single B200
// table, so a GB300 names its full-board partition 7g.180gb — and this map
// records the behaviour a reader can observe, not the behaviour that is
// correct. Fixing the disagreements means changing these literals.
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
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.20gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 20480, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.20gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 20480, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.40gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 40960, Placements: []string{"0:3", "4:3"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.40gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 40960, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.80gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 81920, Placements: []string{"0:7"}},
	},
	"b200": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.24gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 23552, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.24gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 23552, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.48gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 46080, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.48gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 46080, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.96gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 92160, Placements: []string{"0:3", "4:3"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.96gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 92160, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.192gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 184320, Placements: []string{"0:7"}},
	},
	"gb200": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.24gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 23552, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.24gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 23552, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.48gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 46080, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.48gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 46080, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.96gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 92160, Placements: []string{"0:3", "4:3"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.96gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 92160, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.192gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 184320, Placements: []string{"0:7"}},
	},
	"gb300": {
		nvml.GPU_INSTANCE_PROFILE_1_SLICE:      {Name: "1g.36gb", SliceCount: 1, InstanceCount: 7, MemorySizeMB: 23552, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: {Name: "1g.36gb+me", SliceCount: 1, InstanceCount: 1, MemorySizeMB: 23552, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: {Name: "1g.72gb", SliceCount: 1, InstanceCount: 4, MemorySizeMB: 46080, Placements: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		nvml.GPU_INSTANCE_PROFILE_2_SLICE:      {Name: "2g.72gb", SliceCount: 2, InstanceCount: 3, MemorySizeMB: 46080, Placements: []string{"0:2", "2:2", "4:2"}},
		nvml.GPU_INSTANCE_PROFILE_3_SLICE:      {Name: "3g.108gb", SliceCount: 3, InstanceCount: 2, MemorySizeMB: 92160, Placements: []string{"0:3", "4:3"}},
		nvml.GPU_INSTANCE_PROFILE_4_SLICE:      {Name: "4g.108gb", SliceCount: 4, InstanceCount: 1, MemorySizeMB: 92160, Placements: []string{"0:4"}},
		nvml.GPU_INSTANCE_PROFILE_7_SLICE:      {Name: "7g.180gb", SliceCount: 7, InstanceCount: 1, MemorySizeMB: 184320, Placements: []string{"0:7"}},
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
