// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cri

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cri"
)

// stubRuntime is a Runtime whose Installed, Setup and Cleanup return what the
// test says, counting the calls to the last two.
type stubRuntime struct {
	missing              error
	setupErr, cleanupErr error
	setups, cleanups     int
}

func (s *stubRuntime) Installed() error { return s.missing }

func (s *stubRuntime) Setup(context.Context) error {
	s.setups++

	return s.setupErr
}

func (s *stubRuntime) Cleanup(context.Context) error {
	s.cleanups++

	return s.cleanupErr
}

// setupRig is a node with the toolkit binaries in the image and a cri
// simulator set up to install them. It captures the global logger, which
// rules out t.Parallel.
type setupRig struct {
	t    *testing.T
	h    *host.Host
	rt   *stubRuntime
	sim  *Simulator
	logs *observer.ObservedLogs
}

// toolkitBinaries are the binaries the image ships.
var toolkitBinaries = []string{"nvidia-container-runtime", "nvidia-ctk", "nvidia-cdi-hook"}

func newSetupRig(t *testing.T) *setupRig {
	t.Helper()

	source := t.TempDir()
	for _, b := range toolkitBinaries {
		require.NoError(t, os.WriteFile(filepath.Join(source, b), []byte("#!/bin/sh\n# "+b+"\n"), 0o755))
	}

	core, logs := observer.New(zapcore.DebugLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))

	r := &setupRig{t: t, h: host.New(t.TempDir()), rt: &stubRuntime{}, logs: logs}
	r.sim = New(r.h, Options{Runtime: r.rt, ToolkitSource: source})

	return r
}

func (r *setupRig) apply() error {
	r.t.Helper()

	require.NoError(r.t, r.sim.Stage(r.t.Context(), testState()))

	return r.sim.Apply(r.t.Context(), testState())
}

// logged reports whether a line at level mentions s, in its message or a
// field.
func (r *setupRig) logged(level zapcore.Level, s string) bool {
	for _, e := range r.logs.FilterLevelExact(level).All() {
		if strings.Contains(e.Message, s) {
			return true
		}

		for _, v := range e.ContextMap() {
			if strings.Contains(fmt.Sprint(v), s) {
				return true
			}
		}
	}

	return false
}

func (r *setupRig) exists(path string) bool {
	_, err := os.Lstat(r.h.HostPath(path))

	return err == nil
}

func (r *setupRig) link(path string) string {
	r.t.Helper()

	target, err := os.Readlink(r.h.HostPath(path))
	require.NoError(r.t, err)

	return target
}

func TestApplySetsUpTheRuntime(t *testing.T) {
	r := newSetupRig(t)

	require.NoError(t, r.apply())

	require.True(t, r.exists("/usr/bin/nvidia-cdi-hook"), "the hook the specs call is installed first")
	require.FileExists(t, r.h.RunPath(nvidiaSpecFile))
	require.Equal(t, 1, r.rt.setups)
	require.True(t, r.sim.Ready())
	require.Empty(t, r.logs.Filter(func(e observer.LoggedEntry) bool { return e.Level >= zapcore.WarnLevel }).All(),
		"a setup that works has nothing to warn about")
}

func TestApplyLeavesAForeignToolkitAlone(t *testing.T) {
	r := newSetupRig(t)
	require.NoError(t, os.MkdirAll(r.h.HostPath("/usr/bin"), 0o755))
	require.NoError(t, os.WriteFile(r.h.HostPath("/usr/bin/nvidia-container-runtime"), []byte("apt"), 0o755))

	require.NoError(t, r.apply())

	require.False(t, r.exists("/usr/bin/nvidia-cdi-hook"), "nothing is installed")
	require.Zero(t, r.rt.setups, "containerd is left alone")
	require.FileExists(t, r.h.RunPath(nvidiaSpecFile), "the specs are still published")
	require.True(t, r.sim.Ready())
	require.True(t, r.logged(zapcore.WarnLevel, "/usr/bin/nvidia-container-runtime"), "the log names what another installer owns")
}

func TestApplyLeavesANodeWithoutTheRuntimeAlone(t *testing.T) {
	r := newSetupRig(t)
	r.rt.missing = errors.New("containerd not found on the host's PATH")

	require.NoError(t, r.apply())

	require.False(t, r.exists("/usr/bin/nvidia-cdi-hook"), "nothing is installed")
	require.Zero(t, r.rt.setups)
	require.FileExists(t, r.h.RunPath(nvidiaSpecFile), "the specs are still published")
	require.True(t, r.sim.Ready(), "a CRI-O or k3s node is not a failure")
	require.True(t, r.logged(zapcore.WarnLevel, "containerd not found"), "the warning says what is missing")

	require.NoError(t, r.sim.Revoke(t.Context()))
	require.Zero(t, r.rt.cleanups, "nothing to revert")
}

func TestApplyReportsTheRuntimeSetup(t *testing.T) {
	for name, c := range map[string]struct {
		err   error
		ready bool
		// level and log are the line that says why; none when set up.
		level zapcore.Level
		log   string
	}{
		"set up":          {ready: true},
		"restart pending": {err: cri.ErrRestartPending, ready: true, level: zapcore.WarnLevel, log: "when the runtime restarts"},
		"failed":          {err: errors.New("containerd does not come back"), ready: false, level: zapcore.ErrorLevel, log: "containerd does not come back"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newSetupRig(t)
			r.rt.setupErr = c.err

			require.NoError(t, r.apply(), "a failed setup does not fail the Apply wave")

			require.Equal(t, c.ready, r.sim.Ready())
			require.FileExists(t, r.h.RunPath(nvidiaSpecFile))

			if c.log != "" {
				require.True(t, r.logged(c.level, c.log), "the log says why")
			}
		})
	}
}

func TestApplyWithoutTheBinaries(t *testing.T) {
	r := newSetupRig(t)
	require.NoError(t, os.Remove(filepath.Join(r.sim.opts.ToolkitSource, "nvidia-ctk")))

	require.NoError(t, r.apply())

	require.Zero(t, r.rt.setups, "a handler whose runtime is missing is not registered")
	require.FileExists(t, r.h.RunPath(nvidiaSpecFile))
	require.False(t, r.sim.Ready())
	require.True(t, r.logged(zapcore.ErrorLevel, "nvidia-ctk"), "the log names the missing binary")
}

func TestRevokeRevertsTheRuntime(t *testing.T) {
	r := newSetupRig(t)
	require.NoError(t, r.apply())

	require.NoError(t, r.sim.Revoke(t.Context()))

	require.Equal(t, 1, r.rt.cleanups)
	_, err := os.Stat(r.h.RunPath(nvidiaSpecFile))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.FileExists(t, r.h.HostPath(r.link("/usr/bin/nvidia-container-runtime")),
		"containers created through the handler keep calling the runtime")
}

func TestRevokeReportsACleanupFailure(t *testing.T) {
	r := newSetupRig(t)
	require.NoError(t, r.apply())
	r.rt.cleanupErr = errors.New("containerd does not come back")

	err := r.sim.Revoke(t.Context())

	require.ErrorContains(t, err, "containerd does not come back")
	_, statErr := os.Stat(r.h.RunPath(nvidiaSpecFile))
	require.ErrorIs(t, statErr, os.ErrNotExist, "the specs are withdrawn regardless")
}

func TestRevokeLeavesAForeignToolkitAlone(t *testing.T) {
	r := newSetupRig(t)
	require.NoError(t, os.MkdirAll(r.h.HostPath("/usr/bin"), 0o755))
	require.NoError(t, os.WriteFile(r.h.HostPath("/usr/bin/nvidia-ctk"), []byte("apt"), 0o755))
	require.NoError(t, r.apply())

	require.NoError(t, r.sim.Revoke(t.Context()))

	require.Zero(t, r.rt.cleanups)
}
