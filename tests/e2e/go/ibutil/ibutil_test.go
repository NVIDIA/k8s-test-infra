// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ibutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeLID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"16", "16", false},
		{"0x0001", "1", false},
		{"0x000a", "10", false},
		{"0xFFFF", "65535", false},
		{"  0x10  ", "16", false},
		{"", "", true},
		{"0xZZ", "", true},
	}
	// Each case is a named subtest so all rows still run when one fails, while
	// require.* stays compatible with testifylint's require-error check.
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := NormalizeLID(c.in)
			if c.wantErr {
				require.Error(t, err, "NormalizeLID(%q) should fail", c.in)
				return
			}
			require.NoError(t, err, "NormalizeLID(%q)", c.in)
			require.Equal(t, c.want, got, "NormalizeLID(%q)", c.in)
		})
	}
}

func TestNormalizeGUID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"a288:c203:00ab:1234", "0xa288c20300ab1234", false},
		{"0xa288c20300ab1234", "0xa288c20300ab1234", false},
		{"A288:C203:00AB:1234", "0xa288c20300ab1234", false},
		{"  a288c2:0300:ab00  ", "0xa288c20300ab00", false},
		{":::", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := NormalizeGUID(c.in)
			if c.wantErr {
				require.Error(t, err, "NormalizeGUID(%q) should fail", c.in)
				return
			}
			require.NoError(t, err, "NormalizeGUID(%q)", c.in)
			require.Equal(t, c.want, got, "NormalizeGUID(%q)", c.in)
		})
	}
}
