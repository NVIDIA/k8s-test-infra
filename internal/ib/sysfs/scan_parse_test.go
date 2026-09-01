// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLID(t *testing.T) {
	tests := []struct {
		in      string
		want    uint16
		wantErr bool
	}{
		{"768", 768, false},
		{"0x0300", 0x0300, false},
		{"", 0, true},
		{"not-a-lid", 0, true},
	}
	for _, tc := range tests {
		got, err := parseLID(tc.in)
		if tc.wantErr {
			require.Error(t, err, "parseLID(%q): want error", tc.in)
			continue
		}
		require.NoError(t, err, "parseLID(%q)", tc.in)
		require.Equal(t, tc.want, got, "parseLID(%q)", tc.in)
	}
}
