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

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNvlinkStructVersionOK covers the struct-version rule without cgo, which
// test files in this package cannot use. A caller sets the version field on
// the way in; anything but an accepted tag — including an unset zero — must be
// rejected as ARGUMENT_VERSION_MISMATCH rather than silently served.
func TestNvlinkStructVersionOK(t *testing.T) {
	t.Parallel()

	// 12 is the nvmlNvlinkGetBwMode_v1_t size; the helper takes the size
	// because each struct in this family is a distinct C type.
	const v1Size = 12

	cases := map[string]struct {
		requested uint32
		want      bool
	}{
		"matching v1 tag": {FabricStructVersion(v1Size, 1), true},
		"unset zero":      {0, false},
		"bare size":       {v1Size, false},
		"wrong version":   {FabricStructVersion(v1Size, 2), false},
		"wrong size":      {FabricStructVersion(16, 1), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want,
				nvlinkStructVersionOK("testFunc", tc.requested, v1Size))
		})
	}
}

// TestNvlinkInfoStructVersionOK pins the one struct in this family that has
// more than one upstream layout. go-nvml v0.13.3-1 declares both
// nvmlNvLinkInfo_v1_t and _v2_t and aliases nvmlNvLinkInfo_t to v2, so a
// consumer built against NVML 13 — including the nvidia-smi the mock image
// bundles — sends the v2 tag. Rejecting it would answer
// ARGUMENT_VERSION_MISMATCH to `nvidia-smi nvlink --info`, so both are
// accepted. This is safe because v2 only appends firmwareInfo: version and
// isNvleEnabled sit at offsets 0 and 4 in both layouts, and those are the only
// fields the mock writes.
func TestNvlinkInfoStructVersionOK(t *testing.T) {
	t.Parallel()

	const (
		v1Size = 8    // version(4) + isNvleEnabled(4)
		v2Size = 1612 // v1 + firmwareInfo(1604); see nvlink_bw_layout_test.go
	)

	cases := map[string]struct {
		requested uint32
		want      bool
	}{
		"v1 tag":        {FabricStructVersion(v1Size, 1), true},
		"v2 tag":        {FabricStructVersion(v2Size, 2), true},
		"unset zero":    {0, false},
		"bare v1 size":  {v1Size, false},
		"v1 size as v2": {FabricStructVersion(v1Size, 2), false},
		"v2 size as v1": {FabricStructVersion(v2Size, 1), false},
		"future v3":     {FabricStructVersion(v2Size, 3), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want,
				nvlinkInfoStructVersionOK("testFunc", tc.requested, v1Size, v2Size))
		})
	}
}
