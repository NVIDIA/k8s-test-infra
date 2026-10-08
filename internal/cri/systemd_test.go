// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cri

import (
	"io"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemctlActsOnTheHost(t *testing.T) {
	root := t.TempDir()
	executable(t, root, "usr/bin/systemctl")

	var ran *exec.Cmd
	c := Chroot{Root: root, run: func(cmd *exec.Cmd) error {
		ran = cmd

		return nil
	}}

	require.NoError(t, Systemctl(t.Context(), c, "restart", "--no-block", "containerd"))

	require.Equal(t, []string{"/usr/bin/systemctl", "restart", "--no-block", "containerd"}, ran.Args)
	require.Subset(t, ran.Env, []string{"SYSTEMD_IN_CHROOT=0", "SYSTEMD_IGNORE_CHROOT=1"},
		"a systemctl that believes it runs in a chroot ignores the request")
}

func TestSystemctlFailure(t *testing.T) {
	root := t.TempDir()
	executable(t, root, "usr/bin/systemctl")

	c := Chroot{Root: root, run: func(cmd *exec.Cmd) error {
		_, _ = io.WriteString(cmd.Stderr, "Job for containerd.service failed\n")

		return &exec.ExitError{}
	}}

	require.ErrorContains(t, Systemctl(t.Context(), c, "restart", "containerd"), "Job for containerd.service failed")
}
