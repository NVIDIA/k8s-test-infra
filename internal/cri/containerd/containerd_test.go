// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package containerd

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

	"github.com/NVIDIA/k8s-test-infra/internal/cri"
)

const testMokkaConfig = "/etc/containerd/conf.d/50-mokka.toml"

// node is a fake node: the host's paths under a temp root, an nvidia-ctk that
// writes the file it is given as --drop-in-config, and a systemctl that
// records its calls.
type node struct {
	t       *testing.T
	root    string
	rt      *Runtime
	content string // what nvidia-ctk writes
	ctkArgs [][]string
	ctkErr  error
	calls   [][]string
	sysErr  error
}

func newNode(t *testing.T, opts ...func(*cri.RuntimeOptions)) *node {
	t.Helper()

	n := &node{t: t, root: t.TempDir(), content: "version = 2\n"}

	o := cri.RuntimeOptions{NvidiaCTK: "/usr/local/libexec/nvml-mock/container-toolkit/nvidia-ctk", RestartMode: cri.RestartSystemd}
	for _, opt := range opts {
		opt(&o)
	}

	rt, err := New(o)
	require.NoError(t, err)

	rt.root = n.root
	rt.configure = n.configure
	rt.systemctl = n.systemctl
	n.rt = rt

	return n
}

func (n *node) configure(_ context.Context, args ...string) error {
	n.ctkArgs = append(n.ctkArgs, args)
	if n.ctkErr != nil {
		return n.ctkErr
	}

	for _, arg := range args {
		if path, ok := strings.CutPrefix(arg, "--drop-in-config="); ok {
			n.write(path, n.content)
		}
	}

	return nil
}

func (n *node) systemctl(_ context.Context, args ...string) error {
	n.calls = append(n.calls, args)

	return n.sysErr
}

func (n *node) write(path, data string) {
	full := filepath.Join(n.root, path)
	require.NoError(n.t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(n.t, os.WriteFile(full, []byte(data), 0o644))
}

func (n *node) exists(path string) bool {
	_, err := os.Stat(filepath.Join(n.root, path))

	return err == nil
}

func TestSetupRegistersTheHandlerThroughNvidiaCTK(t *testing.T) {
	n := newNode(t)

	require.NoError(t, n.rt.Setup(t.Context()))

	require.Equal(t, [][]string{{
		"runtime", "configure",
		"--runtime=containerd",
		"--config-source=file",
		"--config=/etc/containerd/config.toml",
		"--drop-in-config=" + testMokkaConfig,
		"--nvidia-runtime-name=nvidia",
		"--nvidia-runtime-path=/usr/bin/nvidia-container-runtime",
		"--nvidia-set-as-default",
		"--cdi.enabled",
	}}, n.ctkArgs, "CDI mode, and the default handler")
	require.Equal(t, [][]string{{"restart", "containerd"}}, n.calls)
}

func TestSetupFollowsTheConfiguredPaths(t *testing.T) {
	n := newNode(t, func(o *cri.RuntimeOptions) {
		o.ConfigPath = "/etc/containerd/custom.toml"
		o.ConfigDir = "/etc/containerd/config.d"
		o.Unit = "containerd.service"
	})

	require.NoError(t, n.rt.Setup(t.Context()))

	require.Contains(t, n.ctkArgs[0], "--config=/etc/containerd/custom.toml")
	require.Contains(t, n.ctkArgs[0], "--drop-in-config=/etc/containerd/config.d/50-mokka.toml")
	require.Equal(t, [][]string{{"restart", "containerd.service"}}, n.calls)
}

func TestSetupRestartsOnlyWhenTheFileChanged(t *testing.T) {
	n := newNode(t)
	require.NoError(t, n.rt.Setup(t.Context()))

	require.NoError(t, n.rt.Setup(t.Context()))
	require.Len(t, n.calls, 1, "an unchanged file needs no restart")

	n.content = "version = 3\n"
	require.NoError(t, n.rt.Setup(t.Context()))
	require.Len(t, n.calls, 2)
}

func TestRestartsAreLogged(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))

	n := newNode(t)
	n.write(testMokkaConfig, n.content)
	require.NoError(t, n.rt.Setup(t.Context()))
	require.NoError(t, n.rt.Setup(t.Context()))
	n.content = "version = 3\n"
	require.NoError(t, n.rt.Setup(t.Context()))
	require.NoError(t, n.rt.Cleanup(t.Context()))

	restarts := logs.FilterLevelExact(zapcore.InfoLevel).FilterMessage("restarting containerd").All()
	reasons := make([]string, 0, len(restarts))

	for _, e := range restarts {
		reasons = append(reasons, fmt.Sprint(e.ContextMap()["reason"]))
	}

	require.Equal(t, []string{"node pod started", "Mokka's config file changed", "Mokka's config file removed"}, reasons,
		"one line per restart, and none for the pass that needed no restart")
}

func TestSetupRestartsOnItsFirstPass(t *testing.T) {
	n := newNode(t)
	n.write(testMokkaConfig, n.content)

	require.NoError(t, n.rt.Setup(t.Context()))

	require.Len(t, n.calls, 1, "a pod killed before its restart left a file containerd never loaded")
}

func TestSetupWithoutRestarts(t *testing.T) {
	n := newNode(t, func(o *cri.RuntimeOptions) { o.RestartMode = cri.RestartNone })

	require.ErrorIs(t, n.rt.Setup(t.Context()), cri.ErrRestartPending)

	require.True(t, n.exists(testMokkaConfig))
	require.Empty(t, n.calls)
}

func TestSetupFailures(t *testing.T) {
	t.Run("nvidia-ctk", func(t *testing.T) {
		n := newNode(t)
		n.ctkErr = errors.New("unable to load config")

		require.ErrorContains(t, n.rt.Setup(t.Context()), "unable to load config")
		require.Empty(t, n.calls, "nothing to restart for")
	})

	t.Run("restart", func(t *testing.T) {
		n := newNode(t)
		n.sysErr = errors.New("Job for containerd.service failed")

		require.ErrorContains(t, n.rt.Setup(t.Context()), "Job for containerd.service failed")

		n.sysErr = nil
		require.NoError(t, n.rt.Setup(t.Context()))
		require.Len(t, n.calls, 2, "a failed restart is tried again on the next pass")
	})
}

func TestCleanupRemovesMokkasConfig(t *testing.T) {
	n := newNode(t)
	require.NoError(t, n.rt.Setup(t.Context()))

	require.NoError(t, n.rt.Cleanup(t.Context()))

	require.False(t, n.exists(testMokkaConfig))
	require.Equal(t, []string{"restart", "--no-block", "containerd"}, n.calls[1],
		"systemd finishes the restart after the pod is gone")
}

func TestCleanupWithNothingToRemove(t *testing.T) {
	n := newNode(t)

	require.NoError(t, n.rt.Cleanup(t.Context()))

	require.Empty(t, n.calls)
}

func TestCleanupWithoutRestarts(t *testing.T) {
	n := newNode(t, func(o *cri.RuntimeOptions) { o.RestartMode = cri.RestartNone })
	n.write(testMokkaConfig, n.content)

	require.NoError(t, n.rt.Cleanup(t.Context()))

	require.False(t, n.exists(testMokkaConfig))
	require.Empty(t, n.calls)
}

func TestInstalled(t *testing.T) {
	root := t.TempDir()
	rt, err := New(cri.RuntimeOptions{Chroot: cri.Chroot{Root: root}})
	require.NoError(t, err)

	require.ErrorContains(t, rt.Installed(), "containerd",
		"a node that runs CRI-O, or the containerd k3s embeds, has none on the host's PATH")

	binary := filepath.Join(root, "usr/local/bin/containerd")
	require.NoError(t, os.MkdirAll(filepath.Dir(binary), 0o755))
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, rt.Installed())
}

func TestNewRefusesAConfigDirOutsideConfigTomlsDirectory(t *testing.T) {
	_, err := New(cri.RuntimeOptions{ConfigDir: "/opt/conf.d"})

	require.ErrorContains(t, err, "must be under /etc/containerd")
}
