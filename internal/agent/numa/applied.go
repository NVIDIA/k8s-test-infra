// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const (
	appliedRel       = "numa/.applied"
	legacyAppliedRel = "numa/applied"
)

// Migrate the previous marker before deciding whether a node is already
// configured. Losing this state could repeat setup or skip restoration.
func readApplied(h *host.Host) ([]byte, error) {
	data, err := os.ReadFile(h.RootPath(appliedRel))
	if !os.IsNotExist(err) {
		return data, err
	}
	data, err = os.ReadFile(h.RootPath(legacyAppliedRel))
	if err != nil {
		return nil, err
	}
	if err := fsutil.Write(h.RootPath(appliedRel), data, 0o600); err != nil {
		return nil, err
	}
	if err := fsutil.Remove(h.RootPath(legacyAppliedRel)); err != nil {
		return nil, err
	}
	return data, nil
}

func clearApplied(h *host.Host) error {
	for _, path := range []string{legacyAppliedRel, appliedRel} {
		if err := fsutil.Remove(h.RootPath(path)); err != nil {
			return err
		}
	}
	return nil
}
