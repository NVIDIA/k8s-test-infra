// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
)

// ControlPlaneSource will poll MEP-0001's Control Plane REST API for State
// updates. The REST contract is not yet defined; Watch blocks until ctx is
// cancelled. Replace the Watch body when MEP-0001 defines the endpoint.
type ControlPlaneSource struct{}

// NewControlPlaneSource returns a ControlPlaneSource skeleton.
func NewControlPlaneSource() *ControlPlaneSource { return &ControlPlaneSource{} }

// Watch returns a channel that blocks until ctx is cancelled.
func (c *ControlPlaneSource) Watch(ctx context.Context) <-chan agent.Update {
	// TODO(https://github.com/NVIDIA/k8s-test-infra/issues/614): gonna be implemented in followup tasks
	ch := make(chan agent.Update)
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch
}

// Close is a no-op; the real implementation will close the REST connection.
func (c *ControlPlaneSource) Close() error { return nil }
