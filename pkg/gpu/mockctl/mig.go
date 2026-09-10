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
	"errors"
	"fmt"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// MIGEnablePatch builds the mig block that turns MIG on. An empty profile
// writes only the mode fields: gpu_instances is a list, and the config merge
// replaces lists wholesale, so omitting it is how the profile's own declared
// layout survives.
func MIGEnablePatch(profile string, count int) (map[string]any, error) {
	mig := map[string]any{"mode_current": "enabled", "mode_pending": "enabled"}
	if profile == "" {
		if count != 0 {
			return nil, errors.New(
				"--count needs --profile: a partition count means nothing without the profile being counted")
		}
		return mig, nil
	}
	if count < 0 {
		return nil, fmt.Errorf("--count %d is negative", count)
	}
	mig["gpu_instances"] = []any{map[string]any{
		"profile": profile,
		"count":   max(count, 1),
	}}
	return mig, nil
}

// MIGDisablePatch is the mig block that turns MIG off, which on hardware also
// destroys every instance.
func MIGDisablePatch() map[string]any {
	return map[string]any{"mode_current": "disabled", "mode_pending": "disabled"}
}

// SetMIG assigns the target's mig block wholesale, the way Doc.Fail assigns
// failure. A deep merge would keep the gpu_instances of a previous invocation,
// so a board asked to switch layouts would end up with both.
func (d *Doc) SetMIG(t Target, mig map[string]any) {
	d.bucket(t)["mig"] = mig
}

// ValidateMIGLayout requires a requested layout to materialize completely on
// the board the profile describes. Partitioning is best-effort inside the
// engine — an unplaceable instance is logged and skipped — so without this the
// caller's only signal that a profile name was wrong or a count too large is
// that the node comes up with fewer partitions than asked for.
func ValidateMIGLayout(base *engine.DeviceConfig, mig map[string]any) error {
	if base == nil {
		// A nil base is the same "profile unavailable" case as an unnamed one
		// and is handled as such below, but engine.MergeDeviceConfig marshals
		// nil to a JSON null it then cannot merge into, so the schema check
		// needs an empty document to merge over.
		base = &engine.DeviceConfig{}
	}
	merged, err := engine.MergeDeviceConfig(base, map[string]any{"mig": mig})
	if err != nil {
		// The merge failure names a field but not the block it came from, and
		// the operator only ever asked about MIG.
		return fmt.Errorf("invalid mig override: %w", err)
	}
	// The effective config does not turn partitioning on, so there is no
	// layout to place: a disable destroys instances rather than creating them.
	if merged.MIG == nil || merged.MIG.ModeCurrent != "enabled" {
		return nil
	}
	// An unnamed base means the profile could not be loaded, leaving no board
	// to check the request against. Accepting it keeps the CLI usable in that
	// state, the same way the --gpu index bounds check degrades to best-effort
	// rather than refusing every command; the engine still logs whatever it
	// cannot place when it reloads the override.
	if base.Name == "" {
		return nil
	}

	want := 0
	for _, gi := range merged.MIG.GPUInstances {
		want += max(gi.Count, 1)
	}
	if want == 0 {
		// MIG enabled with no instances declared anywhere is a real state — a
		// partitioned board with nothing carved out of it yet — not an
		// incomplete request, so there is nothing to place and nothing to fail.
		return nil
	}

	layouts := engine.DeclaredMIGLayout(&engine.Config{
		NumDevices: 1,
		YAMLConfig: &engine.YAMLConfig{DeviceDefaults: *merged},
	})
	got := 0
	if len(layouts) > 0 {
		got = len(layouts[0].GPUInstances)
	}
	if got == want {
		return nil
	}
	return fmt.Errorf(
		"%s cannot hold the requested layout %s: asked for %d GPU instances, %d can be placed"+
			" (check the profile name and count against the board)",
		base.Name, migLayoutSummary(merged.MIG.GPUInstances), want, got)
}

// migLayoutSummary spells a requested layout back the way it was asked for, so
// a refusal names the profile the operator typed rather than only a count.
func migLayoutSummary(instances []engine.MIGGPUInstanceConfig) string {
	parts := make([]string, 0, len(instances))
	for _, gi := range instances {
		name := gi.Profile
		if name == "" && gi.ProfileID != nil {
			name = fmt.Sprintf("profile_id=%d", *gi.ProfileID)
		}
		if name == "" {
			// An instance that names neither a profile nor an id can only come
			// from a malformed profile, and a refusal that spells it as an
			// empty string names nothing at all.
			name = "<unnamed profile>"
		}
		parts = append(parts, fmt.Sprintf("%s x%d", name, max(gi.Count, 1)))
	}
	return strings.Join(parts, ", ")
}
