// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenderSysfsFromConfig_disabled(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("infiniband:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "ib")
	if err := RenderSysfsFromConfig(RenderSysfsOptions{
		ConfigPath: cfg,
		OutputDir:  out,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "sys/class/infiniband")); err == nil {
		t.Fatal("expected no sysfs tree when infiniband.enabled=false")
	}
}

func TestRenderSysfsFromConfig_enabled(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(`
infiniband:
  enabled: true
  hca_count: 1
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "ib")
	if err := RenderSysfsFromConfig(RenderSysfsOptions{
		ConfigPath: cfg,
		GPUCount:   2,
		NodeName:   "node-a",
		OutputDir:  out,
	}); err != nil {
		t.Fatal(err)
	}
	lidPath := filepath.Join(out, "sys/class/infiniband/mlx5_0/ports/1/lid")
	if _, err := os.Stat(lidPath); err != nil {
		t.Fatalf("missing rendered lid: %v", err)
	}
}
