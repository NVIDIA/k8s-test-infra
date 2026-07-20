// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// renderNICs is the extracted helper the CLI uses; keeping it testable avoids
// shelling out to a built binary.
func TestRenderNICs_WritesMellanoxEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := []byte(`
devices:
  - index: 0
    pci:
      bus_id: "0000:07:00.0"
infiniband:
  enabled: true
  hcas_per_gpu: 1
`)
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfg, 0o644))

	out := filepath.Join(dir, "out")
	require.NoError(t, renderFromConfig(cfgPath, out, 1, false, false), "renderFromConfig")

	vendor, err := os.ReadFile(filepath.Join(out, "sys/devices/pci0000:e0/0000:e0:00.0/vendor"))
	require.NoError(t, err, "read nic vendor")
	require.Equal(t, "0x15b3\n", string(vendor))
}
