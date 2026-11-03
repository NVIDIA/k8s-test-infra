// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
)

func (a *Agent) prepare(ctx context.Context, state *State) (*State, error) {
	// Preparation and staging share the closed gate and exclusive staging lock.
	// A failed plan must not leave NRI accepting changes from a previous wave.
	a.staged.Store(false)
	if a.preparer == nil {
		return state, nil
	}
	prepared, err := a.preparer.Prepare(ctx, state)
	if err == nil && prepared == nil {
		err = errors.New("preparer returned no state")
	}
	if err != nil {
		a.setLive(health.Unhealthy("state preparation failed: %v", err))
		return nil, fmt.Errorf("prepare state: %w", err)
	}
	return prepared, nil
}
