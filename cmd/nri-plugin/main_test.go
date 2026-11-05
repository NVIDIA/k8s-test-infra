// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/nri"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// runCLI parses args through the real command and returns the config they
// produce, without starting the plugin.
func runCLI(args ...string) (nri.Config, error) {
	var got nri.Config

	command := newCLI()
	command.Action = func(_ context.Context, cmd *cli.Command) error {
		var err error
		got, err = configFrom(cmd)
		return err
	}

	err := command.Run(context.Background(), append([]string{"nri-plugin"}, args...))
	return got, err
}

// The chart renders one comma-joined argument, so a slice flag that treated the
// whole value as a single namespace would silently inject into namespaces an
// operator asked it to skip.
func TestExcludedNamespacesSplitOnCommas(t *testing.T) {
	t.Parallel()

	cfg, err := runCLI("--excluded-namespaces=mokka,kube-system")
	require.NoError(t, err)
	require.Equal(t, []string{"mokka", "kube-system"}, cfg.Inject.ExcludedNamespaces)
}

// Providing a slice flag must replace its default rather than append to it,
// which is the behaviour urfave/cli v2 had and v3 changed.
func TestSliceFlagsReplaceTheirDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := runCLI("--excluded-namespaces=mokka", "--ld-preload-shims=/opt/only.so")
	require.NoError(t, err)
	require.Equal(t, []string{"mokka"}, cfg.Inject.ExcludedNamespaces)
	require.Equal(t, []string{"/opt/only.so"}, cfg.Inject.Shims)
}

func TestUnsetFlagsFallBackToPackageDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := runCLI()
	require.NoError(t, err)

	defaults := nri.DefaultConfig()
	require.Equal(t, defaults.SocketPath, cfg.SocketPath)
	require.Equal(t, defaults.PluginName, cfg.PluginName)
	require.Equal(t, defaults.PluginIndex, cfg.PluginIndex)
	require.Equal(t, defaults.Inject.HostOverlayPath, cfg.Inject.HostOverlayPath)
	require.Equal(t, defaults.Inject.ContainerOverlayPath, cfg.Inject.ContainerOverlayPath)
	require.Equal(t, defaults.Inject.Shims, cfg.Inject.Shims)
	require.Equal(t, defaults.Inject.InfiniBandAnnotation, cfg.Inject.InfiniBandAnnotation)
	require.Equal(t, inject.DeviceInjectionModeRaw, cfg.Inject.DeviceInjectionMode)
}

func TestInvalidDeviceInjectionModeIsRejected(t *testing.T) {
	t.Parallel()

	_, err := runCLI("--device-injection-mode=cdo")
	require.ErrorContains(t, err, "invalid device injection mode")
}

func TestAgentGateFlagsMustBePaired(t *testing.T) {
	t.Parallel()

	_, err := runCLI("--agent-staged-url=http://127.0.0.1:9091/stagedz")
	require.ErrorContains(t, err, "must be set together")
	_, err = runCLI("--staging-lock-path=/run/nvml-mock/nri-staging.lock")
	require.ErrorContains(t, err, "must be set together")

	cfg, err := runCLI(
		"--agent-staged-url=http://127.0.0.1:9091/stagedz",
		"--staging-lock-path=/run/nvml-mock/nri-staging.lock",
	)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:9091/stagedz", cfg.AgentStagedURL)
	require.Equal(t, "/run/nvml-mock/nri-staging.lock", cfg.StagingLockPath)
}
