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

func TestDeepMergeMaps_NestedOverrideAndPreserve(t *testing.T) {
	dst := map[string]any{"ecc": map[string]any{"mode_current": "enabled", "default_mode": "enabled"}}
	src := map[string]any{"ecc": map[string]any{"mode_current": "disabled"}}
	deepMergeMaps(dst, src)
	ecc := dst["ecc"].(map[string]any)
	require.Equal(t, "disabled", ecc["mode_current"], "mode_current not overridden")
	require.Equal(t, "enabled", ecc["default_mode"], "default_mode should be preserved")
}

func TestDeviceConfigOverride_AllThenPerIndexPrecedence(t *testing.T) {
	o := &ConfigOverrideDoc{
		All:     map[string]any{"failure": map[string]any{"mode": "lost"}},
		Devices: map[string]map[string]any{"0": {"failure": map[string]any{"mode": "ecc_uncorrectable"}}},
	}
	require.Equal(t, "lost",
		o.DeviceConfigOverride(1)["failure"].(map[string]any)["mode"],
		"device 1 should inherit All")
	require.Equal(t, "ecc_uncorrectable",
		o.DeviceConfigOverride(0)["failure"].(map[string]any)["mode"],
		"device 0 per-index should win")
}

func TestMergeDeviceConfig_AppliesFailureMode(t *testing.T) {
	base := &DeviceConfig{}
	patch := map[string]any{"failure": map[string]any{"mode": "ecc_uncorrectable", "after_calls": 1}}
	merged, err := MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.NotNil(t, merged.Failure, "failure mode not applied")
	require.Equal(t, "ecc_uncorrectable", merged.Failure.Mode, "failure mode not applied")
	require.Nil(t, base.Failure, "base must not be mutated")
}

func TestMergeDeviceConfig_RejectsUnknownField(t *testing.T) {
	_, err := MergeDeviceConfig(&DeviceConfig{}, map[string]any{"not_a_field": 1})
	require.Error(t, err, "expected error for unknown field")
}

func TestParseConfigOverride_Empty(t *testing.T) {
	o, err := ParseConfigOverride(nil)
	require.NoError(t, err, "empty config override should be (nil,nil)")
	require.Nil(t, o, "empty config override should be (nil,nil)")
}
