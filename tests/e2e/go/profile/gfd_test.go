// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The product and memory values are the ones GPU Feature Discovery actually
// published on a live gb200 Mokka cluster on 2026-07-25:
//
//	nvidia.com/gpu.product=NVIDIA-GB200
//	nvidia.com/gpu.memory=196608
//
// Deriving them from the profile and comparing against those observed literals
// is what makes the e2e GFD assertion discriminating. Two literals have moved
// since that capture, both because the profile changed under it rather than
// because GFD did: the node declared eight GPUs then and now models the four
// an NVL72 compute tray reports, and the board declared 192 GiB then and now
// declares the 186 GiB its published MIG profiles are eighths of.
func TestGB200ProfileDerivesObservedGFDLabelValues(t *testing.T) {
	t.Parallel()

	p, err := Load(profilesDir, "gb200")
	require.NoError(t, err)

	assert.Equal(t, "NVIDIA-GB200", p.GFDProductName())
	assert.Equal(t, 190464, p.MemoryMiB())
	assert.Equal(t, 4, p.ExpectedGPUs())
}

func TestGFDProductNameReplacesSpacesWithDashes(t *testing.T) {
	t.Parallel()

	p, err := Load(profilesDir, "a100")
	require.NoError(t, err)

	// device_defaults.name is "NVIDIA A100-SXM4-40GB"; GFD publishes it dashed.
	assert.Equal(t, "NVIDIA-A100-SXM4-40GB", p.GFDProductName())
	assert.NotContains(t, p.GFDProductName(), " ")
}

// Memory must come from the profile rather than a constant, or the assertion
// would not notice a profile whose device memory changed.
func TestMemoryMiBDiffersAcrossProfiles(t *testing.T) {
	t.Parallel()

	gb200, err := Load(profilesDir, "gb200")
	require.NoError(t, err)
	a100, err := Load(profilesDir, "a100")
	require.NoError(t, err)

	assert.Positive(t, gb200.MemoryMiB())
	assert.Positive(t, a100.MemoryMiB())
	assert.Greater(t, gb200.MemoryMiB(), a100.MemoryMiB(),
		"GB200 carries more device memory than A100; if these match, memory is not being read from the profile")
}

// Every shipped profile must yield usable GFD expectations, otherwise the
// gpu-operator scenario would silently assert against a zero value.
func TestEveryKnownProfileYieldsNonZeroGFDExpectations(t *testing.T) {
	t.Parallel()

	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "load profile %s", name)

		assert.NotEmpty(t, p.GFDProductName(), "profile %s has no product name", name)
		assert.Positive(t, p.MemoryMiB(), "profile %s reports zero device memory", name)
		assert.Positive(t, p.ExpectedGPUs(), "profile %s reports zero GPUs", name)
	}
}
