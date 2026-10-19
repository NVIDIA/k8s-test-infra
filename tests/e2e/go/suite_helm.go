//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strings"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
)

// withSuiteFeatureGates adds the feature gates the job pins to the values of an
// nvml-mock install that does not reuse earlier values, so a reinstall cannot
// turn back on what the job turned off. Installs that reuse values keep the
// gates the job's own install set.
func withSuiteFeatureGates(set map[string]string) map[string]string {
	if !config.IMEXDownload() {
		set["featureGates.IMEXDownload"] = "false"
	}
	return set
}

// splitImage splits "repo:tag" into ("repo", "tag"), defaulting tag to latest.
func splitImage(ref string) (repo, tag string) {
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}
