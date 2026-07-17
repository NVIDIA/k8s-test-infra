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

package mockctl

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func TestParseSet_TypesAndNesting(t *testing.T) {
	m, err := ParseSet([]string{"ecc.mode_current=disabled", "failure.after_calls=1", "failure.mode=lost"})
	if err != nil {
		t.Fatal(err)
	}
	ecc := m["ecc"].(map[string]any)
	if ecc["mode_current"] != "disabled" {
		t.Fatalf("bad ecc: %v", ecc)
	}
	fail := m["failure"].(map[string]any)
	if fail["after_calls"] != float64(1) && fail["after_calls"] != 1 {
		t.Fatalf("after_calls should parse numeric: %#v", fail["after_calls"])
	}
}

func TestDocFail_SetsFailureForIndex(t *testing.T) {
	d := &Doc{}
	if err := d.Fail(Target{Index: 2}, "ecc_uncorrectable", 1, 79); err != nil {
		t.Fatal(err)
	}
	f := d.Devices["2"]["failure"].(map[string]any)
	if f["mode"] != "ecc_uncorrectable" {
		t.Fatalf("mode not set: %v", f)
	}
}

func TestDocFail_RejectsBadMode(t *testing.T) {
	if err := (&Doc{}).Fail(Target{All: true}, "banana", 0, 0); err == nil {
		t.Fatal("expected invalid mode error")
	}
}

func TestReset_All(t *testing.T) {
	d := &Doc{All: map[string]any{"x": 1}, Devices: map[string]map[string]any{"0": {"y": 2}}}
	d.Reset(Target{All: true})
	if d.All != nil || len(d.Devices) != 0 {
		t.Fatalf("reset all should clear everything: %+v", d)
	}
}

func TestValidate_RejectsUnknownField(t *testing.T) {
	if err := Validate(&engine.DeviceConfig{}, map[string]any{"nope": 1}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestResolveTarget_UUID(t *testing.T) {
	cfg := &engine.Config{YAMLConfig: &engine.YAMLConfig{
		Devices: []engine.DeviceOverride{{Index: 3, UUID: "GPU-abc"}},
	}}
	tg, err := ResolveTarget("GPU-abc", cfg)
	if err != nil || tg.Index != 3 {
		t.Fatalf("uuid resolve failed: %+v %v", tg, err)
	}
}
