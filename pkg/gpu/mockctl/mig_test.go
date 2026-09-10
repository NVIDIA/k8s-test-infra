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

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// a100Base is a MIG-capable A100 as a profile ships it: partitionable, MIG off,
// with a declared layout waiting to be turned on.
func a100Base() *engine.DeviceConfig {
	return &engine.DeviceConfig{
		Name:   "NVIDIA A100-SXM4-40GB",
		Memory: &engine.MemoryConfig{TotalBytes: 42949672960, FreeBytes: 42949672960},
		MIG: &engine.MIGConfig{
			ModeCurrent:     "disabled",
			ModePending:     "disabled",
			MaxGPUInstances: 7,
			GPUInstances:    []engine.MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
		},
	}
}

func TestMIGEnablePatch_WithLayout(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("1g.5gb", 7)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"mode_current": "enabled",
		"mode_pending": "enabled",
		"gpu_instances": []any{map[string]any{
			"profile": "1g.5gb",
			"count":   7,
		}},
	}, patch)
}

// TestMIGEnablePatch_BareNamesNoLayout: enabling without a profile must write
// only the mode fields, so the profile's own declared layout survives the merge.
func TestMIGEnablePatch_BareNamesNoLayout(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("", 0)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"mode_current": "enabled", "mode_pending": "enabled"}, patch)
}

// TestMIGEnablePatch_CountNeedsProfile: a count means nothing without the
// profile being counted, and defaulting the profile would pick a partition size
// the caller never named.
func TestMIGEnablePatch_CountNeedsProfile(t *testing.T) {
	t.Parallel()

	_, err := MIGEnablePatch("", 4)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--profile")
}

func TestMIGEnablePatch_CountDefaultsToOne(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("3g.20gb", 0)
	require.NoError(t, err)
	require.Equal(t, []any{map[string]any{"profile": "3g.20gb", "count": 1}}, patch["gpu_instances"])
}

func TestMIGDisablePatch(t *testing.T) {
	t.Parallel()

	require.Equal(t, map[string]any{"mode_current": "disabled", "mode_pending": "disabled"},
		MIGDisablePatch())
}

// TestSetMIG_ReplacesTheWholeBlock is the reason MIG is assigned rather than
// merged: a deep merge would leave the previous gpu_instances list behind, so a
// board asked for 3g.20gb x2 would keep reporting 1g.5gb x7.
func TestSetMIG_ReplacesTheWholeBlock(t *testing.T) {
	t.Parallel()

	doc := &Doc{}
	target := Target{Index: 0}

	first, err := MIGEnablePatch("1g.5gb", 7)
	require.NoError(t, err)
	doc.SetMIG(target, first)

	second, err := MIGEnablePatch("3g.20gb", 2)
	require.NoError(t, err)
	doc.SetMIG(target, second)

	mig := doc.Devices["0"]["mig"].(map[string]any)
	require.Equal(t, []any{map[string]any{"profile": "3g.20gb", "count": 2}}, mig["gpu_instances"])
}

// TestSetMIG_TargetsTheAllBucket covers the target `--gpu all` resolves to,
// whose bucket is a separate branch from the per-device one.
func TestSetMIG_TargetsTheAllBucket(t *testing.T) {
	t.Parallel()

	doc := &Doc{}
	patch, err := MIGEnablePatch("1g.5gb", 7)
	require.NoError(t, err)

	doc.SetMIG(Target{All: true}, patch)

	require.Equal(t, patch, doc.All["mig"])
	require.Empty(t, doc.Devices, "an 'all' target must not write a per-device bucket")
}

// TestSetMIG_LeavesOtherOverridesAlone: assigning the mig block wholesale must
// not disturb the device's other overrides.
func TestSetMIG_LeavesOtherOverridesAlone(t *testing.T) {
	t.Parallel()

	doc := &Doc{}
	target := Target{Index: 0}
	doc.SetFields(target, map[string]any{"temperature_gpu_c": 85})

	doc.SetMIG(target, MIGDisablePatch())

	require.Equal(t, 85, doc.Devices["0"]["temperature_gpu_c"])
	require.Contains(t, doc.Devices["0"], "mig")
}

func TestValidateMIGLayout_AcceptsAProfileTheBoardOffers(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("1g.5gb", 7)
	require.NoError(t, err)
	require.NoError(t, ValidateMIGLayout(a100Base(), patch))
}

// TestValidateMIGLayout_RejectsUnknownProfile: without this check a typo
// produces a board with fewer partitions than asked for, silently.
func TestValidateMIGLayout_RejectsUnknownProfile(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("1g.6gb", 7)
	require.NoError(t, err)

	err = ValidateMIGLayout(a100Base(), patch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "1g.6gb")
	require.Contains(t, err.Error(), "asked for 7 GPU instances, 0 can be placed")
}

// TestValidateMIGLayout_NamesAnUnnamedInstance: a profile declaring an instance
// with neither a profile name nor an id still has to produce a refusal that
// names something.
func TestValidateMIGLayout_NamesAnUnnamedInstance(t *testing.T) {
	t.Parallel()

	err := ValidateMIGLayout(a100Base(), map[string]any{
		"mode_current":  "enabled",
		"mode_pending":  "enabled",
		"gpu_instances": []any{map[string]any{"count": 2}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "<unnamed profile> x2")
}

// TestValidateMIGLayout_RejectsUnplaceableCount: an A100 fits three 3g.20gb
// instances' worth of slices but only two can be placed.
func TestValidateMIGLayout_RejectsUnplaceableCount(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("3g.20gb", 3)
	require.NoError(t, err)

	err = ValidateMIGLayout(a100Base(), patch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "3g.20gb x3")
}

func TestValidateMIGLayout_DisableNeedsNoPlacement(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateMIGLayout(a100Base(), MIGDisablePatch()))
}

// TestValidateMIGLayout_BareEnableAcceptsTheProfilesLayout: nothing was asked
// for, so there is nothing to check beyond the schema.
func TestValidateMIGLayout_BareEnableAcceptsTheProfilesLayout(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("", 0)
	require.NoError(t, err)
	require.NoError(t, ValidateMIGLayout(a100Base(), patch))
}

// TestValidateMIGLayout_DegradesWithoutABase mirrors the CLI's existing policy:
// when the profile cannot be loaded, checks that need it degrade to
// best-effort rather than refusing every command.
func TestValidateMIGLayout_DegradesWithoutABase(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("1g.5gb", 7)
	require.NoError(t, err)
	require.NoError(t, ValidateMIGLayout(&engine.DeviceConfig{}, patch))
	require.NoError(t, ValidateMIGLayout(nil, patch))
}

// TestValidateMIGLayout_RejectsMIGOnANonMIGBoard: a T4 has no MIG tables, so a
// request to partition it can never be satisfied. The refusal must come from
// the placement check finding nothing placeable, not from the schema merge
// rejecting the block — the mig block itself is perfectly valid YAML for any
// board, which is exactly why the placement check has to exist.
func TestValidateMIGLayout_RejectsMIGOnANonMIGBoard(t *testing.T) {
	t.Parallel()

	patch, err := MIGEnablePatch("1g.5gb", 7)
	require.NoError(t, err)

	err = ValidateMIGLayout(&engine.DeviceConfig{Name: "Tesla T4"}, patch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Tesla T4")
	require.Contains(t, err.Error(), "asked for 7 GPU instances, 0 can be placed")
}

// TestMIGEnablePatch_RejectsNegativeCount: --count comes straight from the
// command line, where a negative is a typo rather than a request.
func TestMIGEnablePatch_RejectsNegativeCount(t *testing.T) {
	t.Parallel()

	_, err := MIGEnablePatch("1g.5gb", -1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--count")
}
