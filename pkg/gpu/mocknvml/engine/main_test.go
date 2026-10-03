// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the CDI config fallback at a path that cannot exist. Without
// it, every test expecting the built-in defaults loads the profile of any
// machine that serves one at /etc/nvml-mock/config.yaml, such as a mokka node
// or a container given the nvidia.com/gpu CDI spec. Tests that exercise the
// fallback set their own path with withCDIConfigPath, which restores this one.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mocknvml-engine-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir for the CDI config fallback: %v\n", err)
		os.Exit(1)
	}
	cdiConfigPath = filepath.Join(dir, "absent", "config.yaml")

	code := m.Run()

	// A leftover empty directory under the OS temp dir is harmless, so a
	// failed cleanup is reported but does not change the test result.
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "remove %s: %v\n", dir, err)
	}
	os.Exit(code)
}
