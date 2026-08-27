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
