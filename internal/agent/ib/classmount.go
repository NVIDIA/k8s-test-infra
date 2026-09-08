// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"fmt"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/sysfs"
)

// classesToReproduce lists the node's own sysfs classes to carry into the
// served tree. Anything reading that tree gets its sys/class in place of the
// node's, so a class not carried across simply vanishes for it — sys/class/net
// above all, which an RDMA consumer resolves every HCA through.
//
// The renderer only creates the mountpoints. Attaching the node's classes to
// them is left to the NRI plugin, because the mount has to happen where the
// container's filesystem is assembled: a mount the agent made in its own
// namespace would need CAP_SYS_ADMIN and bidirectional propagation to be
// visible at all, and the runtime performing it needs neither.
func classesToReproduce(hostClassDir string) ([]string, error) {
	entries, err := os.ReadDir(hostClassDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", hostClassDir, err)
	}

	skip := make(map[string]struct{}, len(sysfs.MockOwnedClasses))
	for _, c := range sysfs.MockOwnedClasses {
		skip[c] = struct{}{}
	}

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		if _, ok := skip[e.Name()]; ok {
			continue
		}

		out = append(out, e.Name())
	}

	return out, nil
}
