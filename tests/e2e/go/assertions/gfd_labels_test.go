// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cover the pure derivation and comparison used by WaitGFDLabels, so they
// run without a cluster. The cluster-facing poll is exercised by the
// gpu-operator e2e scenario.

func TestExpectedGFDLabelsDerivesCountAndProductFromProfile(t *testing.T) {
	t.Parallel()

	want := ExpectedGFDLabels("NVIDIA-GB200", 196608, 8)

	assert.Equal(t, "NVIDIA-GB200", want[GFDLabelProduct])
	assert.Equal(t, "196608", want[GFDLabelMemory])
	assert.Equal(t, "8", want[GFDLabelCount])
}

// The count label is the one that actually discriminates: if GFD were removed or
// stopped reading the mock NVML, the count would be absent or wrong. Deriving it
// from the profile rather than reading it back off the node is what makes the
// assertion real.
func TestExpectedGFDLabelsCountTracksProfileNotAConstant(t *testing.T) {
	t.Parallel()

	four := ExpectedGFDLabels("NVIDIA-A100-SXM4-40GB", 40960, 4)
	eight := ExpectedGFDLabels("NVIDIA-GB200", 196608, 8)

	assert.Equal(t, "4", four[GFDLabelCount])
	assert.Equal(t, "8", eight[GFDLabelCount])
	assert.NotEqual(t, four[GFDLabelCount], eight[GFDLabelCount])
	assert.NotEqual(t, four[GFDLabelProduct], eight[GFDLabelProduct])
}

func TestDiffGFDLabelsReportsMissingLabel(t *testing.T) {
	t.Parallel()

	want := ExpectedGFDLabels("NVIDIA-GB200", 196608, 8)
	got := map[string]string{
		GFDLabelProduct: "NVIDIA-GB200",
		GFDLabelMemory:  "196608",
		// gpu.count absent: this is what a removed or broken GFD looks like.
	}

	problems := DiffGFDLabels(want, got)

	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], GFDLabelCount)
	assert.Contains(t, problems[0], "missing")
}

func TestDiffGFDLabelsReportsWrongValue(t *testing.T) {
	t.Parallel()

	want := ExpectedGFDLabels("NVIDIA-GB200", 196608, 8)
	got := map[string]string{
		GFDLabelProduct: "NVIDIA-GB200",
		GFDLabelMemory:  "196608",
		GFDLabelCount:   "1", // GFD saw a different device count than the profile declares
	}

	problems := DiffGFDLabels(want, got)

	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], GFDLabelCount)
	assert.Contains(t, problems[0], `"1"`)
	assert.Contains(t, problems[0], `"8"`)
}

func TestDiffGFDLabelsReturnsNothingWhenLabelsMatch(t *testing.T) {
	t.Parallel()

	want := ExpectedGFDLabels("NVIDIA-GB200", 196608, 8)
	got := map[string]string{
		GFDLabelProduct: "NVIDIA-GB200",
		GFDLabelMemory:  "196608",
		GFDLabelCount:   "8",
	}

	assert.Empty(t, DiffGFDLabels(want, got))
}

func TestDiffGFDLabelsReportsEveryProblemNotJustTheFirst(t *testing.T) {
	t.Parallel()

	want := ExpectedGFDLabels("NVIDIA-GB200", 196608, 8)

	problems := DiffGFDLabels(want, map[string]string{})

	assert.Len(t, problems, 3, "an empty label set must report all three labels, not stop at the first")
}

// A label that exists but carries no value must count as missing rather than
// matching against an empty expectation.
func TestDiffGFDLabelsTreatsEmptyValueAsMissing(t *testing.T) {
	t.Parallel()

	want := ExpectedGFDLabels("NVIDIA-GB200", 196608, 8)
	got := map[string]string{
		GFDLabelProduct: "",
		GFDLabelMemory:  "196608",
		GFDLabelCount:   "8",
	}

	problems := DiffGFDLabels(want, got)

	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "missing")
}
