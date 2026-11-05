// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const (
	appliedRel = "mokka/numa/.applied"
	pendingRel = "mokka/numa/.pending"
)

// Runtime markers survive pod restarts but disappear on node reboot. A pending
// marker keeps a partially applied setup recoverable if rollback also fails.
func readApplied(h *host.Host) ([]byte, error) {
	data, err := os.ReadFile(h.RunPath(appliedRel))
	if !os.IsNotExist(err) {
		return data, err
	}
	return os.ReadFile(h.RunPath(pendingRel))
}

func clearApplied(h *host.Host) error {
	for _, path := range []string{appliedRel, pendingRel} {
		if err := fsutil.Remove(h.RunPath(path)); err != nil {
			return err
		}
	}
	return nil
}
