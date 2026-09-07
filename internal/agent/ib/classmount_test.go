// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Serving the tree's sys/class over the node's shadows every class the node
// really has, so each one has to be carried across — except those the renderer
// writes itself.
func TestClassesToReproduce_ExcludesMockOwned(t *testing.T) {
	t.Parallel()

	hostClass := filepath.Join(t.TempDir(), "class")
	for _, c := range []string{"net", "block", "infiniband", "infiniband_verbs", "power_supply"} {
		require.NoError(t, os.MkdirAll(filepath.Join(hostClass, c), 0o755))
	}

	got, err := classesToReproduce(hostClass)
	require.NoError(t, err)

	// On a CPU-only node the host's infiniband class is empty or absent, so
	// letting it shadow the rendered one would retract every simulated HCA.
	// net is carried across rather than rendered: the agent's links are real
	// kernel devices, so the host's class already lists them next to the
	// node's own interfaces.
	require.ElementsMatch(t, []string{"net", "block", "power_supply"}, got)
}

// A node whose /sys is not mounted must surface that rather than silently
// serving a tree with none of its classes.
func TestClassesToReproduce_ReportsAMissingClassDir(t *testing.T) {
	t.Parallel()

	_, err := classesToReproduce(filepath.Join(t.TempDir(), "absent"))
	require.Error(t, err)
}
