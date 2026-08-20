//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import "strings"

// splitImage splits "repo:tag" into ("repo", "tag"), defaulting tag to latest.
func splitImage(ref string) (repo, tag string) {
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}
