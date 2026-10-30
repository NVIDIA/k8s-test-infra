// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysoverlay

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
)

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Applier   = (*Simulator)(nil)
)

// StageFunc writes an overlay's entry into the directory it is given, from the
// state. Once served that directory is what the node shows, so a restage must
// leave it exactly as the new state describes.
type StageFunc func(entry string, state *agent.State) error

// Simulator serves one Overlay on a node whose kernel provides no entry, and
// does nothing on a node whose kernel does.
//
// Apply's mount reaches the node only through a Bidirectional mount of Dir,
// and only pods started afterwards see it: a hostPath mount of /sys is a
// snapshot taken when its container starts.
type Simulator struct {
	name    string
	overlay Overlay
	stage   StageFunc
	ready   atomic.Bool
}

// NewSimulator returns a Simulator serving overlay, its entry written by stage.
func NewSimulator(name string, overlay Overlay, stage StageFunc) *Simulator {
	return &Simulator{name: name, overlay: overlay, stage: stage}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return s.name }

// Ready reports whether the node shows the entry, the kernel's or ours.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage writes the entry, unless the kernel provides one.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", s.name))

	v, err := s.overlay.Look()
	if err != nil {
		return err
	}
	if v == Kernel {
		zap.L().Info("node kernel provides the entry, nothing to simulate",
			zap.String("simulator", s.name), zap.String("dir", s.overlay.Dir), zap.String("entry", s.overlay.Entry))
		return nil
	}

	if err := os.MkdirAll(s.overlay.StagedEntry(), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", s.overlay.StagedEntry(), err)
	}
	if err := s.stage(s.overlay.StagedEntry(), state); err != nil {
		return fmt.Errorf("stage %s: %w", s.overlay.Entry, err)
	}

	zap.L().Info("simulator staged", zap.String("simulator", s.name))
	return nil
}

// Apply serves the staged entry on a node whose kernel provides none.
func (s *Simulator) Apply(_ context.Context, _ *agent.State) error {
	zap.L().Info("applying simulator", zap.String("simulator", s.name))
	s.ready.Store(false)

	v, err := s.overlay.Look()
	if err != nil {
		return err
	}
	if v == Absent {
		if err := s.overlay.Serve(); err != nil {
			return err
		}
		zap.L().Info("serving simulated sysfs entry",
			zap.String("simulator", s.name), zap.String("dir", s.overlay.Dir), zap.String("entry", s.overlay.Entry))
	}

	s.ready.Store(true)
	return nil
}

// Revoke withdraws the overmount, returning the node to its own view.
func (s *Simulator) Revoke(_ context.Context) error {
	zap.L().Info("revoking simulator", zap.String("simulator", s.name))
	s.ready.Store(false)

	return s.overlay.Withdraw()
}

// Discard removes the staged entry. It refuses while the entry is still
// served: removing it would leave the node an empty entry instead of none.
//
// The rest of the staged tree stays. A container started while the entry was
// served holds its own copy of the overmount, with the directory's own entries
// bound onto the tree's placeholders; removing a placeholder detaches that
// entry from the container. Kept, the tree also lets such a container see the
// entry the next agent stages, which lands in the same directory.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", s.name))

	v, err := s.overlay.Look()
	if err != nil {
		return err
	}
	if v == Served {
		return fmt.Errorf("%s still served at %s, keeping %s", s.overlay.Entry, s.overlay.Dir, s.overlay.StagedEntry())
	}

	if err := os.RemoveAll(s.overlay.StagedEntry()); err != nil {
		return fmt.Errorf("remove %s: %w", s.overlay.StagedEntry(), err)
	}
	return nil
}
