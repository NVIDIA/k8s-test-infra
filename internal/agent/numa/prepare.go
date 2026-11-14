// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"go.uber.org/zap"
)

// PrepareStage restores a changed layout before any simulator replaces files.
// This is disruptive: existing workloads can fail and need to be recreated.
func (s *Simulator) PrepareStage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)
	if !s.opts.Enabled {
		return nil
	}
	plan := state.NodeShape.NUMA
	if plan == nil {
		return errors.New("NUMA layout is not resolved")
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	// Validate kubelet reservations before removing a working layout. The plan
	// itself was resolved and validated by the read-only topology enricher.
	if _, err := s.renderKubeletConfig(plan); err != nil {
		return err
	}
	previous, err := readApplied(s.host)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(previous) == 0 || strings.TrimSpace(string(previous)) == s.configurationKey(data) {
		return nil
	}
	zap.L().Warn("NUMA layout or policy changed; restoring the node before reapplying simulation; existing workloads may fail")
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	err = s.setup.restore(ctx)
	s.scheduleRetry(err)
	if err != nil {
		return fmt.Errorf("restore previous NUMA layout: %w", err)
	}
	return nil
}
