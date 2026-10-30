// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

type resolvedSource struct {
	agent.StateSource
	host *host.Host
}

// WrapSource resolves CPU affinities before the agent stages simulators in parallel.
func WrapSource(source agent.StateSource, h *host.Host) agent.StateSource {
	return &resolvedSource{StateSource: source, host: h}
}

func (s *resolvedSource) Watch(ctx context.Context) <-chan agent.Update {
	out := make(chan agent.Update)
	go func() {
		defer close(out)
		for update := range s.StateSource.Watch(ctx) {
			if update.Err == nil && update.State != nil {
				if _, err := Resolve(s.host, update.State); err != nil {
					update.State = nil
					update.Err = err
				}
			}
			select {
			case out <- update:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
