// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnits_ReadsHICBlock(t *testing.T) {
	t.Parallel()
	cards, err := ParseUnits(loadFixture(t, "qux-gb200-hic.xml"))
	require.NoError(t, err)
	require.Equal(t, []HIC{
		{ID: "0", Firmware: "1.2.3.4"},
		{ID: "1", Firmware: "5.6.7.8"},
	}, cards)
}

// TestParseUnits_EmptyHICBlock is the shape every modern node produces:
// nvidia-smi still prints <hic_info>, just with no cards inside it.
func TestParseUnits_EmptyHICBlock(t *testing.T) {
	t.Parallel()
	cards, err := ParseUnits(loadFixture(t, "qux-gb200-no-hic.xml"))
	require.NoError(t, err)
	require.Empty(t, cards)
}

func TestParseUnits_RejectsTheDeviceDocument(t *testing.T) {
	t.Parallel()
	// The device query shares the nvidia_smi_log root, so it decodes — but it
	// carries no HIC block, which is the signal a spec would act on.
	cards, err := ParseUnits(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	assert.Empty(t, cards, "the device query has no hic_info block to report")
}

func TestParseUnits_RejectsGarbage(t *testing.T) {
	t.Parallel()
	_, err := ParseUnits("Unable to determine number of available units: N/A")
	require.Error(t, err)
}

func TestHICProblems(t *testing.T) {
	t.Parallel()
	configured := loadFixture(t, "qux-gb200-hic.xml")
	none := loadFixture(t, "qux-gb200-no-hic.xml")

	tests := []struct {
		name    string
		out     string
		want    []HIC
		problem string
	}{
		{
			name: "configured cards match",
			out:  configured,
			want: []HIC{{ID: "0", Firmware: "1.2.3.4"}, {ID: "1", Firmware: "5.6.7.8"}},
		},
		{
			name: "no cards expected and none reported",
			out:  none,
			want: nil,
		},
		{
			name:    "a profile's cards never reached nvidia-smi",
			out:     none,
			want:    []HIC{{ID: "0", Firmware: "1.2.3.4"}},
			problem: "reports 0 HIC(s)",
		},
		{
			name:    "an unexpected card appeared",
			out:     configured,
			want:    nil,
			problem: "reports 2 HIC(s)",
		},
		{
			name:    "firmware string was mangled",
			out:     configured,
			want:    []HIC{{ID: "0", Firmware: "1.2.3.4"}, {ID: "1", Firmware: "9.9.9.9"}},
			problem: `hic "1" firmware = "5.6.7.8", want "9.9.9.9"`,
		},
		{
			name:    "ids are out of order",
			out:     configured,
			want:    []HIC{{ID: "1", Firmware: "5.6.7.8"}, {ID: "0", Firmware: "1.2.3.4"}},
			problem: `hic[0] id = "0", want "1"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problems := HICProblems(tc.out, tc.want)
			if tc.problem == "" {
				assert.Empty(t, problems)
				return
			}
			require.NotEmpty(t, problems)
			assert.Contains(t, problems[0], tc.problem)
		})
	}
}

// TestUnitQueryProblems_CatchesTheStubbedUnitCount is the regression this
// guards: nvidia-smi prints that message to stderr and still exits 0, so a
// spec that only checks the exit status would pass against a driver whose
// nvmlUnitGetCount is a stub and never print a HIC block at all.
func TestUnitQueryProblems_CatchesTheStubbedUnitCount(t *testing.T) {
	t.Parallel()
	problems := UnitQueryProblems("Unable to determine number of available units: N/A\n")
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "nvmlUnitGetCount failed")
}

func TestUnitQueryProblems_AcceptsAnAnsweredQuery(t *testing.T) {
	t.Parallel()
	assert.Empty(t, UnitQueryProblems(loadFixture(t, "qux-gb200-hic.xml")))
	assert.Empty(t, UnitQueryProblems(loadFixture(t, "qux-gb200-no-hic.xml")))
}

// TestParseUnits_SkipsAStderrPreamble covers the combined output the exec
// helper passes in: specs keep stderr so the unit-count failure is visible,
// and any warning printed there must not defeat the decode.
func TestParseUnits_SkipsAStderrPreamble(t *testing.T) {
	t.Parallel()
	out := "WARNING: infoROM is corrupted\n" + loadFixture(t, "qux-gb200-hic.xml")
	cards, err := ParseUnits(out)
	require.NoError(t, err)
	assert.Len(t, cards, 2)
}
