// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// overlayMount is the one mount every adjusted container gets, whatever the
// opt-ins say.
func overlayMount() Mount {
	return Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	}
}

func TestAdjustMountsTheOverlayForAPlainContainer(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), Container{Namespace: "default"})
	require.True(t, ok)
	require.Contains(t, adjustment.Mounts, overlayMount())
}

// An unannotated container gets the overlay and the environment but nothing
// else: both device paths are opt-in, so the default must stay empty.
func TestAdjustWithoutOptInsDeliversNoDevices(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), Container{Namespace: "default"})
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Empty(t, adjustment.CDIDevices)
	require.NotEmpty(t, adjustment.Env)
}

func TestAdjustSkipsOptOutExcludedNamespaceAndExistingMount(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = []string{"kube-system", "nvml-mock"}

	tests := map[string]Container{
		"opt out annotation": {
			Namespace: "default",
			PodAnnotations: map[string]string{
				"nvml-mock.nvidia.com/inject": "false",
			},
		},
		"excluded namespace": {
			Namespace: "kube-system",
		},
		// A container already carrying the overlay has been through here
		// before; injecting twice would stack duplicate LD_PRELOAD entries.
		"existing overlay mount": {
			Namespace: "default",
			Mounts:    []Mount{{Destination: "/opt/nvml-mock"}},
		},
	}

	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adjustment, ok := Adjust(cfg, container)
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}
}

// An empty exclusion list means "exclude nothing", so it must not fall back to
// the packaged kube-system default.
func TestEmptyExclusionListExcludesNothing(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = nil

	_, ok := Adjust(cfg, Container{Namespace: "kube-system"})
	require.True(t, ok)
}
