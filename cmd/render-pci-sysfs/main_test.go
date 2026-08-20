// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
)

const profileWithDevices = `
devices:
  - index: 0
    pci:
      bus_id: "0000:07:00.0"
`

// A profile whose devices declare no bus_id renders no topology. It is
// reachable through gpu.customConfig, and it used to return before Render.
const profileWithoutBusIDs = `
devices:
  - index: 0
`

// TestRun_ClearsTreeWhenProfileDeclaresNoDevices covers re-profiling a node
// onto a profile with nothing to render. The tree and the completion marker
// left by the previous profile would otherwise stay on disk, and both serving
// channels would keep mounting devices this profile does not declare —
// setup.sh gates on the marker, which said "rendered" about the old tree.
func TestRun_ClearsTreeWhenProfileDeclaresNoDevices(t *testing.T) {
	out := t.TempDir()
	require.NoError(t, run(options{
		configPath: writeProfile(t, profileWithDevices),
		outputDir:  out,
	}), "render a profile with devices")
	require.FileExists(t, filepath.Join(out, render.MarkerRelPath), "marker after the first render")

	require.NoError(t, run(options{
		configPath: writeProfile(t, profileWithoutBusIDs),
		outputDir:  out,
	}), "render a profile without devices")

	entries, err := os.ReadDir(filepath.Join(out, render.PCIDevicesRelPath))
	require.NoError(t, err, "read devices dir")
	require.Empty(t, entries, "the previous profile's devices are still served")
	require.NoFileExists(t, filepath.Join(out, render.MarkerRelPath),
		"the marker still claims a rendered tree")
}

// TestRun_DryRunWritesNothing pins that --dry-run stays a validation pass on
// both paths, including the one that now prunes.
func TestRun_DryRunWritesNothing(t *testing.T) {
	for name, profile := range map[string]string{
		"with devices":    profileWithDevices,
		"without devices": profileWithoutBusIDs,
	} {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			require.NoError(t, run(options{
				configPath: writeProfile(t, profile),
				outputDir:  out,
				dryRun:     true,
			}), "dry run")
			entries, err := os.ReadDir(out)
			require.NoError(t, err, "read output dir")
			require.Empty(t, entries, "--dry-run wrote to the output directory")
		})
	}
}

func writeProfile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644), "write profile")
	return path
}
