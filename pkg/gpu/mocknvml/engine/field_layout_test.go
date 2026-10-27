// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalFieldID_OlderDriversKeepTheBundledLayout(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"", "550.163.01", "580.65.06", "580.94.99"} {
		for _, id := range []uint32{74, 251, 253, 256, 259, 269, 271, 273} {
			got, ok := CanonicalFieldID(id, version)
			require.True(t, ok, "field %d on %q", id, version)
			require.Equal(t, id, got, "field %d on %q", id, version)
		}
	}
}

func TestCanonicalFieldID_NewerLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   uint32
		want uint32
		ok   bool
	}{
		{"outside the renumbered block", 250, 250, true},
		{"power smoothing enabled", 251, fiPwrSmoothingEnabled, true},
		{"applied TMP ceiling", 254, fiPwrSmoothingAppliedTMPCeil, true},
		{"last power smoothing id", 268, fiPwrSmoothingAdminOverrideRampDownHyst, true},
		{"SW thermal slowdown", 269, fiClocksEventSwThermSlowdown, true},
		{"HW thermal slowdown", 270, fiClocksEventHwThermSlowdown, true},
		{"HW power brake", 271, fiClocksEventHwPowerBrake, true},
		{"power sync balancing is not simulated", 272, 0, false},
		{"power sync balancing AF is not simulated", 273, 0, false},
		{"beyond the block", 274, 274, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := CanonicalFieldID(tt.id, newLayoutMinDriver)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
