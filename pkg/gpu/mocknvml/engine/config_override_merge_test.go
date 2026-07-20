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

import "testing"

func TestDeepMergeMaps_NestedOverrideAndPreserve(t *testing.T) {
	dst := map[string]any{"ecc": map[string]any{"mode_current": "enabled", "default_mode": "enabled"}}
	src := map[string]any{"ecc": map[string]any{"mode_current": "disabled"}}
	deepMergeMaps(dst, src)
	ecc := dst["ecc"].(map[string]any)
	if ecc["mode_current"] != "disabled" {
		t.Fatalf("mode_current not overridden: %v", ecc["mode_current"])
	}
	if ecc["default_mode"] != "enabled" {
		t.Fatalf("default_mode should be preserved: %v", ecc["default_mode"])
	}
}

func TestDeviceConfigOverride_AllThenPerIndexPrecedence(t *testing.T) {
	o := &ConfigOverrideDoc{
		All:     map[string]any{"failure": map[string]any{"mode": "lost"}},
		Devices: map[string]map[string]any{"0": {"failure": map[string]any{"mode": "ecc_uncorrectable"}}},
	}
	if got := o.DeviceConfigOverride(1)["failure"].(map[string]any)["mode"]; got != "lost" {
		t.Fatalf("device 1 should inherit All: %v", got)
	}
	if got := o.DeviceConfigOverride(0)["failure"].(map[string]any)["mode"]; got != "ecc_uncorrectable" {
		t.Fatalf("device 0 per-index should win: %v", got)
	}
}

func TestMergeDeviceConfig_AppliesFailureMode(t *testing.T) {
	base := &DeviceConfig{}
	patch := map[string]any{"failure": map[string]any{"mode": "ecc_uncorrectable", "after_calls": 1}}
	merged, err := MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Failure == nil || merged.Failure.Mode != "ecc_uncorrectable" {
		t.Fatalf("failure mode not applied: %+v", merged.Failure)
	}
	if base.Failure != nil {
		t.Fatal("base must not be mutated")
	}
}

func TestMergeDeviceConfig_RejectsUnknownField(t *testing.T) {
	if _, err := MergeDeviceConfig(&DeviceConfig{}, map[string]any{"not_a_field": 1}); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestParseConfigOverride_Empty(t *testing.T) {
	o, err := ParseConfigOverride(nil)
	if err != nil || o != nil {
		t.Fatalf("empty config override should be (nil,nil): %v %v", o, err)
	}
}
