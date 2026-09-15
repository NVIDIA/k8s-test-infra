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

	"github.com/stretchr/testify/require"
)

// bwFabric builds a single-device fabric from an nvlink config block, the
// same shape c2c_test.go uses.
func bwFabric(t *testing.T, nvlink *NVLinkConfig) *NodeFabric {
	t.Helper()
	return BuildNodeFabric(&Config{
		NumDevices: 1,
		YAMLConfig: &YAMLConfig{NVLink: nvlink},
	})
}

func TestNodeFabric_NvlinkBwModeConfig(t *testing.T) {
	t.Parallel()

	mode := uint8(3)
	f := bwFabric(t, &NVLinkConfig{
		NvleEnabled: true,
		BwMode: &NVLinkBwModeConfig{
			Supported: []uint8{0, 3},
			Mode:      &mode,
		},
	})

	require.Equal(t, []uint8{0, 3}, f.NvlinkSupportedBwModes(), "supported modes")
	got, ok := f.NvlinkConfiguredBwMode()
	require.True(t, ok, "mode was configured")
	require.Equal(t, uint8(3), got, "configured mode")
	require.True(t, f.NvleEnabled(), "nvle_enabled")
}

// TestNodeFabric_NvlinkBwModeUnset pins that an absent block reports "not
// configured" rather than a zero value, so the device layer can tell the
// difference between an explicit mode 0 (FULL) and no config at all.
func TestNodeFabric_NvlinkBwModeUnset(t *testing.T) {
	t.Parallel()

	cases := map[string]*NodeFabric{
		"no bw_mode block": bwFabric(t, &NVLinkConfig{}),
		"no nvlink block":  bwFabric(t, nil),
		"nil fabric":       (*NodeFabric)(nil),
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Nil(t, f.NvlinkSupportedBwModes(), "supported modes")
			_, ok := f.NvlinkConfiguredBwMode()
			require.False(t, ok, "mode configured")
			require.False(t, f.NvleEnabled(), "nvle_enabled")
		})
	}
}
