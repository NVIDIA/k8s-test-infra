// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const chartProfilesDir = "../../../../../deployments/nvml-mock/helm/nvml-mock/profiles"

// TestSupportedClocksProblems_AcceptsEveryHardwareCapture runs the check the
// e2e spec runs, against what real boards print, with each profile's table as
// the expectation. It holds both ends at once: the schema still finds the
// section in a real document, and the profile tables are the captured ones.
func TestSupportedClocksProblems_AcceptsEveryHardwareCapture(t *testing.T) {
	t.Parallel()
	for _, name := range profile.KnownProfiles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join("testdata", "hardware", "qx-"+name+".xml"))
			require.NoError(t, err)
			p, err := profile.Load(chartProfilesDir, name)
			require.NoError(t, err)
			require.NotEmpty(t, p.SupportedClocks())

			require.Empty(t, SupportedClocksProblems(string(raw), p.SupportedClocks()))
		})
	}
}

func TestSupportedClocksProblems_ReportsNA(t *testing.T) {
	t.Parallel()
	want := []profile.SupportedMemoryClock{{MemoryMHz: 2619, GraphicsMHz: []int{1980}}}

	problems := SupportedClocksProblems(loadFixture(t, "qx-h100-mig-enabled.xml"), want)
	require.NotEmpty(t, problems)
	require.Contains(t, problems[0], "N/A")
	require.Contains(t, problems[0], "nvmlDeviceGetSupportedMemoryClocks")
}

func TestSupportedClocksProblems_NamesTheFirstDifference(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "hardware", "qx-h100.xml"))
	require.NoError(t, err)
	p, err := profile.Load(chartProfilesDir, "h100")
	require.NoError(t, err)

	cases := map[string]struct {
		mutate func([]profile.SupportedMemoryClock) []profile.SupportedMemoryClock
		want   string
	}{
		"missing memory clock": {
			func(c []profile.SupportedMemoryClock) []profile.SupportedMemoryClock { return c[:1] },
			"lists 2 memory clocks [2619 1593], want 1 [2619]",
		},
		"wrong memory clock": {
			func(c []profile.SupportedMemoryClock) []profile.SupportedMemoryClock { c[1].MemoryMHz = 1600; return c },
			"supported_mem_clock[1] = 1593 MHz, want 1600 MHz",
		},
		"graphics clock out of order": {
			func(c []profile.SupportedMemoryClock) []profile.SupportedMemoryClock {
				c[0].GraphicsMHz[0], c[0].GraphicsMHz[1] = c[0].GraphicsMHz[1], c[0].GraphicsMHz[0]
				return c
			},
			"memory clock 2619 MHz graphics clock [0] = 1980 MHz, want 1965 MHz",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			problems := SupportedClocksProblems(string(raw), tc.mutate(p.SupportedClocks()))
			require.NotEmpty(t, problems)
			for _, pr := range problems {
				require.True(t, strings.HasSuffix(pr, tc.want), "%q", pr)
			}
		})
	}
}

func TestApplicationsClocks_DecodeFromCapture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "hardware", "qx-h100.xml"))
	require.NoError(t, err)
	snap, err := ParseSnapshot(string(raw))
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	mem, gfx, ok := gpu.ApplicationsClocksMHz()
	require.True(t, ok)
	require.Equal(t, [2]int{2619, 1980}, [2]int{mem, gfx})
	mem, gfx, ok = gpu.DefaultApplicationsClocksMHz()
	require.True(t, ok)
	require.Equal(t, [2]int{2619, 1980}, [2]int{mem, gfx})
}
