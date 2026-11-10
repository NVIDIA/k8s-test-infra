// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"fmt"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// PodRunner applies node changes directly from the privileged agent pod.
type PodRunner struct{ host *host.Host }

// NewPodRunner constructs a runner without changing the node.
func NewPodRunner(h *host.Host) *PodRunner { return &PodRunner{host: h} }

// Run publishes mounts through the pod's bidirectional host volumes. The node
// service is only a boot reset; it never performs simulation setup or restore.
func (r *PodRunner) Run(ctx context.Context, action HostAction) error {
	if action != ActionApply && action != ActionRestore {
		return fmt.Errorf("unknown NUMA host action %q", action)
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	setup := newHostSetup(r.host)
	switch action {
	case ActionApply:
		return setup.apply(ctx)
	case ActionRestore:
		return setup.restore(ctx)
	default:
		return fmt.Errorf("unknown NUMA host action %q", action)
	}
}

var _ HostRunner = (*PodRunner)(nil)
