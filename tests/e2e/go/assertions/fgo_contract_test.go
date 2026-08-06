// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFGOProfileConfigMapNameUsesLoaderPrefix(t *testing.T) {
	t.Parallel()

	// The literal FGO's loader builds: CmNamePrefix + profileName.
	assert.Equal(t, "gpu-profile-a100", FGOProfileConfigMapName("a100"))
	assert.Equal(t, "gpu-profile-gb200", FGOProfileConfigMapName("gb200"))
}

// A conformant ConfigMap must produce no problems, or every negative case
// below would pass for the wrong reason.
func TestDiffFGOProfileConfigMapAcceptsAConformantConfigMap(t *testing.T) {
	t.Parallel()

	problems := DiffFGOProfileConfigMap(
		"a100",
		"gpu-profile-a100",
		map[string]string{FGODiscoveryLabel: "true", "run.ai/gpu-profile": "true"},
		map[string]string{FGOProfileDataKey: "version: \"1.0\"\n"},
	)

	assert.Empty(t, problems)
}

// Each of the three contract fields must be caught on its own. A single
// combined check would let two of them drift undetected.
func TestDiffFGOProfileConfigMapCatchesEachContractFieldIndependently(t *testing.T) {
	t.Parallel()

	conformantLabels := map[string]string{FGODiscoveryLabel: "true"}
	conformantData := map[string]string{FGOProfileDataKey: "version: \"1.0\"\n"}

	for _, tc := range []struct {
		name    string
		cmName  string
		labels  map[string]string
		data    map[string]string
		wantSub string
	}{
		{
			name:    "name carries the pre-retarget prefix",
			cmName:  "nvml-mock-profile-a100",
			labels:  conformantLabels,
			data:    conformantData,
			wantSub: `name is "nvml-mock-profile-a100", want "gpu-profile-a100"`,
		},
		{
			name:    "discovery label absent",
			cmName:  "gpu-profile-a100",
			labels:  map[string]string{"run.ai/gpu-profile": "true"},
			data:    conformantData,
			wantSub: `label fake-gpu-operator/gpu-profile missing (want "true")`,
		},
		{
			name:    "discovery label present with the wrong value",
			cmName:  "gpu-profile-a100",
			labels:  map[string]string{FGODiscoveryLabel: "false"},
			data:    conformantData,
			wantSub: `label fake-gpu-operator/gpu-profile = "false", want "true"`,
		},
		{
			name:    "data keyed under the pre-retarget config.yaml",
			cmName:  "gpu-profile-a100",
			labels:  conformantLabels,
			data:    map[string]string{"config.yaml": "version: \"1.0\"\n"},
			wantSub: `data key "profile.yaml" missing`,
		},
		{
			name:    "data key present but empty",
			cmName:  "gpu-profile-a100",
			labels:  conformantLabels,
			data:    map[string]string{FGOProfileDataKey: ""},
			wantSub: `data key "profile.yaml" is empty`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			problems := DiffFGOProfileConfigMap("a100", tc.cmName, tc.labels, tc.data)

			require.Len(t, problems, 1, "expected exactly one problem, got %#v", problems)
			assert.Equal(t, tc.wantSub, problems[0])
		})
	}
}

// Reporting only the first problem would hide the rest behind a fix-one-rerun
// loop; a wholly wrong ConfigMap should show all three at once.
func TestDiffFGOProfileConfigMapReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	problems := DiffFGOProfileConfigMap(
		"t4",
		"nvml-mock-profile-t4",
		map[string]string{"run.ai/gpu-profile": "true"},
		map[string]string{"config.yaml": "version: \"1.0\"\n"},
	)

	assert.Len(t, problems, 3)
}
