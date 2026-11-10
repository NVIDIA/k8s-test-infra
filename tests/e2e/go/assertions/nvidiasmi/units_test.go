// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// unitQueryEmpty is real `nvidia-smi -q -u` output: nvidia-smi 550.163.01
// against the mock with the unit enumeration wired. The HIC row above the count
// is kept because it is the neighbouring N/A the count must not be confused
// with.
const unitQueryEmpty = `
==============NVSMI LOG==============

Timestamp                                 : Wed Sep 16 13:19:17 2026
Driver Version                            : 550.163.01
CUDA Version                              : 12.4

HIC Info                                  : N/A
Attached Units                            : 0

`

// unitQueryUnsupported is the same command against the generated stub the
// enumeration replaced: nvmlUnitGetCount answered NOT_SUPPORTED, so nvidia-smi
// printed nothing else and exited 3.
const unitQueryUnsupported = "Unable to determine number of available units: N/A\n"

func TestUnitInventoryProblems(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		out          string
		exit         int
		wantProblems bool
	}{
		{"node with no S-class chassis", unitQueryEmpty, 0, false},
		{"the stub this replaced", unitQueryUnsupported, 3, true},
		{
			"a count nvidia-smi could not read",
			"HIC Info                                  : N/A\nAttached Units                            : N/A\n",
			0, true,
		},
		{
			"a unit no enumeration can produce",
			"Attached Units                            : 1\n",
			0, true,
		},
		{"succeeded but said nothing about units", "==============NVSMI LOG==============\n", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problems := UnitInventoryProblems(tc.out, tc.exit)
			if tc.wantProblems {
				require.NotEmpty(t, problems)
				return
			}
			require.Empty(t, problems)
		})
	}
}
