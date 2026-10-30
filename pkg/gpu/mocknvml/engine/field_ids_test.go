// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCanonicalFieldID pins the field-id renumbering the NVML 13.1 header (the
// 590 driver branch) made: power smoothing moved down by five, from 256-273 to
// 251-268, and the three clocks-event slowdown counters and the two power
// sync balancing fields moved up by eighteen, to 269-273. A consumer built
// against the newer header asks for the newer ids, so a profile on 590 or later
// must read them as the fields they name there.
func TestCanonicalFieldID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		driver  string
		fieldID uint32
		want    uint32
	}{
		{"580 slowdown counter is unchanged", "580.65.06", 251, fiClocksEventSwThermSlowdown},
		{"580 power smoothing is unchanged", "580.65.06", 256, fiPwrSmoothingEnabled},
		{"580 has no field at 269 but smoothing's active preset", "580.65.06", 269, fiPwrSmoothingActivePresetProfile},
		{"590 smoothing enabled", "590.0", 251, fiPwrSmoothingEnabled},
		{"615 smoothing admin override ramp down hyst", "615.71.10", 268, fiPwrSmoothingAdminOverrideRampDownHyst},
		{"615 sw thermal slowdown", "615.71.10", 269, fiClocksEventSwThermSlowdown},
		{"615 hw power brake", "615.71.10", 271, fiClocksEventHwPowerBrake},
		{"615 power sync balancing freq", "615.71.10", 272, 254},
		{"615 power sync balancing af", "615.71.10", 273, 255},
		{"615 field below the moved range", "615.71.10", 250, 250},
		{"615 field above the moved range", "615.71.10", 274, 274},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, CanonicalFieldID(tt.fieldID, tt.driver))
		})
	}
}
