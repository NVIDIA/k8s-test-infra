// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// warnUnlessHCAsStaged reports an InfiniBand opt-in on a node that simulates no
// fabric. It fails open like the GPU and IMEX paths: a profile without
// InfiniBand gets an empty IB tree from the node agent, and the pod should
// still start, with IB tools that find no HCA.
func warnUnlessHCAsStaged(cfg Config) {
	dir := filepath.Join(cfg.HostOverlayPath, ibRelPath, "sys/class/infiniband")
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return
	}
	zap.L().Warn("infiniband injection requested but no HCA is staged; "+
		"the container's IB tools will find none (does this node's profile enable infiniband?)",
		zap.String("path", dir), zap.Error(err))
}
