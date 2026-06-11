// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSysfsFromConfig_disabled(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("infiniband:\n  enabled: false\n"), 0o644))
	out := filepath.Join(dir, "ib")
	require.NoError(t, RenderSysfsFromConfig(RenderSysfsOptions{
		ConfigPath: cfg,
		OutputDir:  out,
	}))
	_, err := os.Stat(filepath.Join(out, "sys/class/infiniband"))
	require.Error(t, err, "expected no sysfs tree when infiniband.enabled=false")
}

func TestRenderSysfsFromConfig_enabled(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte(`
infiniband:
  enabled: true
  hca_count: 1
`), 0o644))
	out := filepath.Join(dir, "ib")
	require.NoError(t, RenderSysfsFromConfig(RenderSysfsOptions{
		ConfigPath: cfg,
		GPUCount:   2,
		NodeName:   "node-a",
		OutputDir:  out,
	}))
	lidPath := filepath.Join(out, "sys/class/infiniband/mlx5_0/ports/1/lid")
	_, err := os.Stat(lidPath)
	require.NoError(t, err, "missing rendered lid")
}
