// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the CDI and NRI config fallbacks at paths that cannot exist.
// Without it, every test expecting the built-in defaults loads the profile of
// any machine that serves one at /etc/nvml-mock/config.yaml or
// /opt/nvml-mock/driver/config/config.yaml, such as a mokka node, a container
// given the nvidia.com/gpu CDI spec or one the NRI plugin injects. Tests that
// exercise a fallback set their own path with withCDIConfigPath or
// withNRIConfigPath, which restore these.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mocknvml-engine-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir for the config fallbacks: %v\n", err)
		os.Exit(1)
	}
	cdiConfigPath = filepath.Join(dir, "absent", "config.yaml")
	nriConfigPath = filepath.Join(dir, "absent", "nri", "config.yaml")

	code := m.Run()

	// A leftover empty directory under the OS temp dir is harmless, so a
	// failed cleanup is reported but does not change the test result.
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "remove %s: %v\n", dir, err)
	}
	os.Exit(code)
}
