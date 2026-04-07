// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzLoadYAMLConfig(f *testing.F) {
	// Seed corpus with known-good configs
	f.Add([]byte(`
gpu_name: "Tesla T4"
architecture: "Turing"
num_devices: 2
total_memory_mib: 16384
`))
	f.Add([]byte(`
gpu_name: "A100-SXM4-40GB"
architecture: "Ampere"
num_devices: 8
total_memory_mib: 40960
`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Write fuzzed data to a temp file
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		// LoadYAMLConfig should never panic on any input
		_, _ = LoadYAMLConfig(path)
	})
}
