// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cri

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// executable creates an executable file at path under root.
func executable(t *testing.T, root, path string) {
	t.Helper()

	full := filepath.Join(root, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("#!/bin/sh\n"), 0o755))
}

func TestChrootLookPathFollowsTheHostPATHOrder(t *testing.T) {
	root := t.TempDir()
	executable(t, root, "usr/bin/systemctl")
	executable(t, root, "usr/local/bin/systemctl")

	got, err := Chroot{Root: root}.LookPath("systemctl")

	require.NoError(t, err)
	require.Equal(t, "/usr/local/bin/systemctl", got, "/usr/local/bin comes ahead of /usr/bin, as on the host")
}

func TestChrootLookPathSkipsWhatIsNotExecutable(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "usr/local/bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "usr/local/bin/systemctl"), []byte("x"), 0o644))
	executable(t, root, "usr/bin/systemctl")

	got, err := Chroot{Root: root}.LookPath("systemctl")

	require.NoError(t, err)
	require.Equal(t, "/usr/bin/systemctl", got)
}

func TestChrootLookPathMissing(t *testing.T) {
	_, err := Chroot{Root: t.TempDir()}.LookPath("systemctl")

	require.ErrorContains(t, err, "systemctl")
}

func TestChrootLookPathFollowsAbsoluteSymlinksInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	executable(t, root, "opt/bin/systemctl")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "usr/local"), 0o755))
	// An absolute link on the host; followed naively, it would point into this
	// container instead.
	require.NoError(t, os.Symlink("/opt/bin", filepath.Join(root, "usr/local/bin")))

	got, err := Chroot{Root: root}.LookPath("systemctl")

	require.NoError(t, err)
	require.Equal(t, "/usr/local/bin/systemctl", got)
}

func TestChrootRunShapesTheCommand(t *testing.T) {
	root := t.TempDir()
	executable(t, root, "usr/bin/systemctl")
	t.Setenv("LD_PRELOAD", "/usr/lib/libmock.so")

	var ran *exec.Cmd
	c := Chroot{Root: root, run: func(cmd *exec.Cmd) error {
		ran = cmd
		_, err := io.WriteString(cmd.Stdout, "42\n")

		return err
	}}

	out, err := c.Run(t.Context(), []string{"SYSTEMD_IN_CHROOT=0"}, "systemctl", "show", "--value")

	require.NoError(t, err)
	require.Equal(t, "42\n", string(out))
	require.Equal(t, "/usr/bin/systemctl", ran.Path, "the path is resolved inside the chroot")
	require.Equal(t, []string{"/usr/bin/systemctl", "show", "--value"}, ran.Args)
	require.Equal(t, "/", ran.Dir)
	require.Equal(t, root, ran.SysProcAttr.Chroot)
	require.Equal(t, []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SYSTEMD_IN_CHROOT=0",
	}, ran.Env, "nothing from the agent's environment, LD_PRELOAD above all, reaches the host command")
}

func TestChrootRunReportsStderr(t *testing.T) {
	root := t.TempDir()
	executable(t, root, "usr/bin/systemctl")

	c := Chroot{Root: root, run: func(cmd *exec.Cmd) error {
		_, _ = io.WriteString(cmd.Stdout, "partial")
		_, _ = io.WriteString(cmd.Stderr, "Job for containerd.service failed\n")

		return &exec.ExitError{}
	}}

	out, err := c.Run(t.Context(), nil, "systemctl", "restart", "containerd")

	require.ErrorContains(t, err, "Job for containerd.service failed")
	require.Equal(t, "partial", string(out), "stdout comes back even on failure")
}
