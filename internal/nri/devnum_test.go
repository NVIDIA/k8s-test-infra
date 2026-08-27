// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Device numbers are Linux dev_t values harvested from a node's /dev/nvidia*
// nodes. glibc encodes them as MMMM Mmmm mmmM MMmm: the major occupies bits
// 8-19 and 44-63, the minor bits 0-7 and 20-43. Cases below are derived from
// that encoding, not from the implementation.
//
// The two "wide" cases are the ones that matter. NVIDIA's real majors (195,
// 510, 511) and minors are all small, so they sit entirely in the legacy low
// bits and decode correctly even under a truncating formula. A test built only
// from real device numbers passes on broken code and proves nothing.
func TestMajorMinorDecodeLinuxDeviceNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dev       uint64
		wantMajor uint64
		wantMinor uint64
	}{
		{
			name: "zero",
			dev:  0x0, wantMajor: 0, wantMinor: 0,
		},
		{
			// /dev/null. Legacy 16-bit MMmm layout, entirely in the low bits.
			name: "legacy low bits only",
			dev:  0x103, wantMajor: 1, wantMinor: 3,
		},
		{
			// A real nvidia device number: major 511, minor 5. Included as an
			// anchor, and as the reason this bug survived review -- it decodes
			// correctly even when the high bits are dropped.
			name: "nvidia caps device",
			dev:  0x1ff05, wantMajor: 511, wantMinor: 5,
		},
		{
			// Major bit 44, the lowest bit of the extended major field. A
			// formula masking the major to 12 bits returns 0 here.
			name: "major above the legacy 12 bits",
			dev:  0x100000000000, wantMajor: 4096, wantMinor: 0,
		},
		{
			// Major bit 63, the highest bit of the extended major field.
			name: "major at the top of the extended field",
			dev:  0x8000000000000000, wantMajor: 2147483648, wantMinor: 0,
		},
		{
			// Minor bit 32. The extended minor field is bits 20-43; a formula
			// masking it to bits 20-31 returns 0 here.
			name: "minor above the legacy 20 bits",
			dev:  0x100000000, wantMajor: 0, wantMinor: 1048576,
		},
		{
			// Minor bit 43, the highest bit of the extended minor field.
			name: "minor at the top of the extended field",
			dev:  0x80000000000, wantMajor: 0, wantMinor: 2147483648,
		},
		{
			// Every field populated at once, so a swapped or overlapping mask
			// cannot pass by coincidence. Encodes major 0x12345678 and minor
			// 0x9abcdef0 per the glibc layout.
			name: "all fields populated",
			dev:  0x123459abcde678f0, wantMajor: 0x12345678, wantMinor: 0x9abcdef0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.wantMajor, major(tt.dev), "major(%#x)", tt.dev)
			require.Equal(t, tt.wantMinor, minor(tt.dev), "minor(%#x)", tt.dev)
		})
	}
}
