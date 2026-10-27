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
