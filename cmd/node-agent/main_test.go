// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/cri"
	"github.com/NVIDIA/k8s-test-infra/internal/cri/containerd"
)

// parseStart parses args through the real start command and returns the
// runtime options they produce, without starting the agent.
func parseStart(t *testing.T, args ...string) (string, cri.RuntimeOptions, error) {
	t.Helper()

	var (
		name string
		opts cri.RuntimeOptions
	)

	command := newCLI()
	command.Commands[0].Action = func(_ context.Context, cmd *cli.Command) error {
		var err error
		name, opts, err = runtimeOptions(cmd)

		return err
	}

	err := command.Run(t.Context(), append([]string{"node-agent", "start"}, args...))

	return name, opts, err
}

func TestContainerRuntimeIsLeftAloneByDefault(t *testing.T) {
	name, _, err := parseStart(t)

	require.NoError(t, err)
	require.Empty(t, name, "the chart turns the setup on; the binary alone leaves the runtime alone")
}

func TestContainerRuntimeFlags(t *testing.T) {
	name, opts, err := parseStart(t,
		"--container-runtime=containerd",
		"--container-runtime-restart=none",
		"--container-runtime-config=/etc/containerd/config.toml",
		"--container-runtime-config-dir=/etc/containerd/config.d",
		"--container-runtime-systemd-unit=containerd.service",
		"--host-fs=/hostfs",
	)

	require.NoError(t, err)
	require.Equal(t, "containerd", name)
	require.Equal(t, cri.RestartNone, opts.RestartMode)
	require.Equal(t, "/etc/containerd/config.toml", opts.ConfigPath)
	require.Equal(t, "/etc/containerd/config.d", opts.ConfigDir)
	require.Equal(t, "containerd.service", opts.Unit)
	require.Equal(t, "/hostfs", opts.Chroot.Root)
	require.Equal(t, filepath.Join(cri.DefaultToolkitSource, "nvidia-ctk"), opts.NvidiaCTK, "the image's own nvidia-ctk")
}

func TestContainerRuntimeEnvironment(t *testing.T) {
	t.Setenv("MOKKA_AGENT_CONTAINER_RUNTIME", "containerd")
	t.Setenv("MOKKA_AGENT_CONTAINER_RUNTIME_CONFIG", "/etc/containerd/custom.toml")

	name, opts, err := parseStart(t)

	require.NoError(t, err)
	require.Equal(t, "containerd", name)
	require.Equal(t, "/etc/containerd/custom.toml", opts.ConfigPath)
	require.Equal(t, cri.RestartSystemd, opts.RestartMode)
}

func TestContainerRuntimeRejects(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown restart mode": {"--container-runtime=containerd", "--container-runtime-restart=kill"},
		"relative path":        {"--container-runtime=containerd", "--container-runtime-config=etc/containerd/config.toml"},
		"relative host root":   {"--container-runtime=containerd", "--host-fs=hostfs"},
		"no host root":         {"--container-runtime=containerd", "--host-fs="},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseStart(t, args...)

			require.Error(t, err)
		})
	}
}

func TestNewRuntime(t *testing.T) {
	rt, err := newRuntime("containerd", cri.RuntimeOptions{})
	require.NoError(t, err)
	require.IsType(t, &containerd.Runtime{}, rt)

	_, err = newRuntime("cri-o", cri.RuntimeOptions{})
	require.ErrorContains(t, err, "containerd", "the error lists what is supported")
}
