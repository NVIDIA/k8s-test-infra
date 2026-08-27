// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDeviceInjectionMode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  DeviceInjectionMode
	}{
		"raw":                {input: "raw", want: DeviceInjectionModeRaw},
		"cdi":                {input: "cdi", want: DeviceInjectionModeCDI},
		"empty defaults raw": {input: "", want: DeviceInjectionModeRaw},
		"case insensitive":   {input: "CDI", want: DeviceInjectionModeCDI},
		"surrounding space":  {input: " cdi ", want: DeviceInjectionModeCDI},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mode, err := ParseDeviceInjectionMode(test.input)
			require.NoError(t, err)
			require.Equal(t, test.want, mode)
		})
	}
}

// A typo must not coerce to raw: that looks exactly like a working CDI
// deployment, and the difference is only visible in the OCI spec of an
// already-running pod.
func TestParseDeviceInjectionModeRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"cdo", "none", "true"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			_, err := ParseDeviceInjectionMode(input)
			require.ErrorContains(t, err, "invalid device injection mode")
		})
	}
}

// The comma-separated CLI flags reach Config unnormalized, so the padding and
// empty elements a hand-written --flag= leaves behind are dropped here.
func TestWithDefaultsCompactsSliceFlags(t *testing.T) {
	t.Parallel()

	cfg := withDefaults(Config{
		ExcludedNamespaces: []string{" mokka ", "", "kube-system"},
		Shims:              []string{"driver/lib/a.so", "  ", "driver/lib/b.so"},
	})

	require.Equal(t, []string{"mokka", "kube-system"}, cfg.ExcludedNamespaces)
	require.Equal(t, []string{"driver/lib/a.so", "driver/lib/b.so"}, cfg.Shims)
}

// Shims restore their defaults when empty because a container with no shim
// preloaded sees the real libraries; an empty namespace list is a valid choice
// and must not be overwritten.
func TestWithDefaultsRestoresShimsButNotExclusions(t *testing.T) {
	t.Parallel()

	cfg := withDefaults(Config{})

	require.Equal(t, defaultShims, cfg.Shims)
	require.Empty(t, cfg.ExcludedNamespaces)
}

// The derived paths resolve against whatever overlay roots were configured, not
// against the packaged defaults.
func TestWithDefaultsDerivesPathsFromTheConfiguredOverlay(t *testing.T) {
	t.Parallel()

	cfg := withDefaults(Config{
		HostOverlayPath:      "/custom/host",
		ContainerOverlayPath: "/custom/container",
	})

	require.Equal(t, "/custom/host/driver/dev/nvidia-caps-imex-channels", cfg.ImexChannelHostPath)
	require.Equal(t, "/custom/host/topology/topology.yaml", cfg.TopologyHostPath)
	require.Equal(t, "/custom/container/topology/topology.yaml", cfg.TopologyContainerPath)
}
