// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/imex"
)

// computeDomainConfig points the overlay at a temporary tree with the node's
// topology staged, and the IMEX node software as asked.
func computeDomainConfig(t *testing.T, imex bool) Config {
	t.Helper()

	root := t.TempDir()
	stage := func(rel string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(rel), 0o644))
	}
	if imex {
		stage(realIMEXRelPath)
	}
	stage(topologyRelPath)
	cfg := DefaultConfig()
	cfg.HostOverlayPath = root
	cfg.NodeName = "worker-0"
	return withDefaults(cfg)
}

func computeDomainDaemonContainer() Container {
	return Container{
		Name:      computeDomainContainerName,
		Namespace: "nvidia-dra-driver-gpu",
		PodLabels: map[string]string{computeDomainLabel: "6b4f0c0e-0000-4000-8000-000000000001"},
		Env:       []string{"NODE_NAME=worker-0"},
	}
}

// The daemon takes its clique from the DRA driver, not from NVML, so it gets
// the real IMEX daemon and not the node's topology.
func TestAdjustGivesTheComputeDomainDaemonOnlyRealIMEX(t *testing.T) {
	t.Parallel()

	cfg := computeDomainConfig(t, true)

	adjustment, ok := Adjust(cfg, computeDomainDaemonContainer())

	require.True(t, ok)
	require.Equal(t, []Mount{
		readOnlyFileMount(filepath.Join(cfg.HostOverlayPath, realIMEXRelPath), realIMEXContainerPath),
	}, adjustment.Mounts)
	require.Empty(t, adjustment.Env)
	require.Empty(t, adjustment.Devices, "the DRA driver's CDI edits deliver the daemon's devices")
	require.Empty(t, adjustment.CDIDevices)
}

// The DRA driver's CDI edits are the daemon's driver footprint; a second one
// from the overlay would put two mock trees in reach.
func TestAdjustDoesNotApplyTheOverlayToTheComputeDomainDaemon(t *testing.T) {
	t.Parallel()

	cfg := computeDomainConfig(t, true)

	adjustment, _ := Adjust(cfg, computeDomainDaemonContainer())

	for _, mount := range adjustment.Mounts {
		require.NotEqual(t, cfg.ContainerOverlayPath, mount.Destination)
	}
	require.NotContains(t, adjustment.Env, "LD_PRELOAD")
}

// Fail open: without IMEX the daemon runs as on a node without the IMEX
// package and fails there, rather than being refused here.
func TestAdjustLeavesTheComputeDomainDaemonUnmodifiedWithNothingStaged(t *testing.T) {
	t.Parallel()

	_, ok := Adjust(computeDomainConfig(t, false), computeDomainDaemonContainer())

	require.False(t, ok)
}

// A withdrawn install keeps the shim, which DRA claims bind, but not
// nvidia-imex.real, the presence signal; the daemon then gets nothing.
func TestAdjustLeavesTheDaemonUnmodifiedWhenIMEXWasWithdrawn(t *testing.T) {
	t.Parallel()

	cfg := computeDomainConfig(t, false)
	shim := filepath.Join(cfg.HostOverlayPath, imex.BinDir, imex.DaemonBinary)
	require.NoError(t, os.MkdirAll(filepath.Dir(shim), 0o755))
	require.NoError(t, os.WriteFile(shim, nil, 0o755))

	_, ok := Adjust(cfg, computeDomainDaemonContainer())

	require.False(t, ok)
}

// Only the DRA driver's daemon container in a ComputeDomain pod is routed; a
// container sharing just its name or just its pod label gets the generic
// treatment, which without an opt-in leaves it alone.
func TestAdjustRoutesOnlyTheComputeDomainDaemon(t *testing.T) {
	t.Parallel()

	cfg := computeDomainConfig(t, true)
	renamed := computeDomainDaemonContainer()
	renamed.Name = "init"
	unlabeled := computeDomainDaemonContainer()
	unlabeled.PodLabels = nil

	for name, container := range map[string]Container{"other container": renamed, "unlabeled pod": unlabeled} {
		_, ok := Adjust(cfg, container)
		require.False(t, ok, name)
	}
}

func TestAdjustHonorsExclusionsForTheComputeDomainDaemon(t *testing.T) {
	t.Parallel()

	cfg := computeDomainConfig(t, true)
	cfg.ExcludedNamespaces = []string{"nvidia-dra-driver-gpu"}

	_, ok := Adjust(cfg, computeDomainDaemonContainer())

	require.False(t, ok)
}

// The daemon checks the shim itself, so it does not wait for the staging gate;
// every other container does.
func TestOnlyTheComputeDomainDaemonSkipsTheStagingGate(t *testing.T) {
	t.Parallel()

	renamed := computeDomainDaemonContainer()
	renamed.Name = "init"

	require.False(t, Decide(DefaultConfig(), computeDomainDaemonContainer()).Gated())
	require.True(t, Decide(DefaultConfig(), renamed).Gated())
	require.True(t, Decide(DefaultConfig(), Container{Name: "main", Namespace: "default"}).Gated())
}
