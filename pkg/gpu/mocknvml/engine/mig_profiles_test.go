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
			got, err := migProfileName(tt.giProfileID, tt.ciProfileID, tt.migMemoryMB, tt.deviceMemory)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMigProfileName_RejectsUnknownProfileIDs(t *testing.T) {
	t.Parallel()

	_, err := migProfileName(nvml.GPU_INSTANCE_PROFILE_COUNT, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, 4864, a100_40GiB)
	require.Error(t, err, "an out-of-range GPU instance profile has no slice count")

	_, err = migProfileName(nvml.GPU_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_PROFILE_COUNT, 4864, a100_40GiB)
	require.Error(t, err, "an out-of-range compute instance profile has no slice count")
}

// TestResolveMIGProfiles checks that a device is given the MIG tables of the
// architecture its YAML profile describes. Before MIG support every device was
// built from the dgxa100 base, so an H100 profile silently carried A100 tables.
func TestResolveMIGProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		deviceName    string
		memoryBytes   uint64
		wantSupported bool
		// wantOneSliceMemoryMB identifies which table was picked.
		wantOneSliceMemoryMB uint64
	}{
		{"a100 40gb", "NVIDIA A100-SXM4-40GB", a100_40GiB, true, 4864},
		{"a100 80gb", "NVIDIA A100-SXM4-80GB", 2 * a100_40GiB, true, 9856},
		{"a100 pcie 40gb", "NVIDIA A100-PCIE-40GB", a100_40GiB, true, 4864},
		{"a30", "NVIDIA A30", 25769803776, true, 5836},
		{"h100", "NVIDIA H100 80GB HBM3", h100_80GiB, true, 10240},
		{"h200", "NVIDIA H200 141GB HBM3e", 151397302272, true, 18432},
		{"b200", "NVIDIA B200 180GB HBM3e", 193273528320, true, 23552},
		{"gb200 uses blackwell tables", "NVIDIA GB200", 193273528320, true, 23552},
		{"t4 has no mig", "Tesla T4", 16106127360, false, 0},
		{"l40s has no mig", "NVIDIA L40S", 48305799168, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profiles, ok := resolveMIGProfiles(tt.deviceName, tt.memoryBytes)
			require.Equal(t, tt.wantSupported, ok)
			if !tt.wantSupported {
				return
			}
			gi, exists := profiles.GpuInstanceProfiles[nvml.GPU_INSTANCE_PROFILE_1_SLICE]
			require.True(t, exists, "a MIG-capable device must offer a 1-slice profile")
			require.Equal(t, tt.wantOneSliceMemoryMB, gi.MemorySizeMB)
		})
	}
}
