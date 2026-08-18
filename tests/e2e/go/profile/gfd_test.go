// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The expected values are the ones GPU Feature Discovery actually published on a
// live gb200 Mokka cluster on 2026-07-25:
//
//	nvidia.com/gpu.product=NVIDIA-GB200
//	nvidia.com/gpu.memory=196608
//	nvidia.com/gpu.count=8
//
// Deriving them from the profile and comparing against those observed literals
// is what makes the e2e GFD assertion discriminating.
func TestGB200ProfileDerivesObservedGFDLabelValues(t *testing.T) {
	t.Parallel()

	p, err := Load(profilesDir, "gb200")
	require.NoError(t, err)

	assert.Equal(t, "NVIDIA-GB200", p.GFDProductName())
	assert.Equal(t, 196608, p.MemoryMiB())
	assert.Equal(t, 8, p.ExpectedGPUs())
}

// gpu.machine comes from the profile's dmi: block, dashed the same way as
// gpu.product. Profiles that ship no block (commodity-server platforms) must
// resolve to the literal "unknown" GFD publishes when it cannot read DMI, so
// the e2e expectation stays an assertion rather than a skip.
func TestGFDMachineTypeComesFromTheDMIBlock(t *testing.T) {
	t.Parallel()

	gb200, err := Load(profilesDir, "gb200")
	require.NoError(t, err)
	a100, err := Load(profilesDir, "a100")
	require.NoError(t, err)
	t4, err := Load(profilesDir, "t4")
	require.NoError(t, err)

	assert.Equal(t, "NVIDIA-GB200-NVL72", gb200.GFDMachineType())
	assert.Equal(t, "DGXA100", a100.GFDMachineType())
	assert.Equal(t, GFDMachineTypeUnknown, t4.GFDMachineType())
	assert.NotContains(t, gb200.GFDMachineType(), " ")
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
