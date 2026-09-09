// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package migcaps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/migcaps"
)

func testHost(t *testing.T) *host.Host {
	t.Helper()
	return host.New(t.TempDir())
}

// partitionedState is two GPUs each split into two GPU instances, the second of
// which is subdivided — enough shape to exercise every capability form.
func partitionedState() *agent.State {
	return &agent.State{MIG: agent.MIGState{
		CapsMajor: 236,
		GPUs: []agent.MIGGPU{
			{Minor: 0, GPUInstances: []agent.MIGGPUInstance{
				{ID: 0, ComputeInstances: computeInstances(0)},
				{ID: 1, ComputeInstances: computeInstances(0, 1)},
			}},
			{Minor: 1, GPUInstances: []agent.MIGGPUInstance{
				{ID: 0, ComputeInstances: computeInstances(0)},
			}},
		},
	}}
}

// computeInstances builds the compute instances of one GPU instance. The UUIDs
// are irrelevant to the capability surface — it is keyed by ID — so they are
// only distinct enough to keep the fixture honest.
func computeInstances(ids ...uint32) []agent.MIGComputeInstance {
	out := make([]agent.MIGComputeInstance, 0, len(ids))
	for _, id := range ids {
		out = append(out, agent.MIGComputeInstance{ID: id, UUID: fmt.Sprintf("MIG-ci%d", id)})
	}
	return out
}

func skipUnlessRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Skip("requires root on Linux (mknod)")
	}
}

func TestStageMinors_WritesTableAtTheConsumersPath(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	require.NoError(t, stageMinors(h, capsFor(partitionedState().MIG)))

	// nvidia-container-toolkit's NewMigCapsFromRoot joins the driver root with
	// /proc/driver/nvidia-caps/mig-minors, so this path is load-bearing.
	content, err := os.ReadFile(filepath.Join(h.Root, "driver/proc/driver/nvidia-caps/mig-minors"))
	require.NoError(t, err)

	require.Equal(t, `config 1
monitor 2
gpu0/gi0/access 3
gpu0/gi0/ci0/access 4
gpu0/gi1/access 5
gpu0/gi1/ci0/access 6
gpu0/gi1/ci1/access 7
gpu1/gi0/access 8
gpu1/gi0/ci0/access 9
`, string(content))
}

func TestStageCapabilityTree_MirrorsTheMinorsTable(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	caps := capsFor(partitionedState().MIG)
	require.NoError(t, stageCapabilityTree(h, caps))

	// Each capability file reports the same minor the table gives it;
	// a consumer may read either, so a disagreement is a silent mismatch.
	for _, c := range caps {
		path := filepath.Join(h.Root, capabilitiesDir, capabilityProcPath(c.Name))
		content, err := os.ReadFile(path) //nolint:gosec // test-controlled path
		require.NoError(t, err, "capability %q", c.Name)
		require.Contains(t, string(content), fmt.Sprintf("DeviceFileMinor: %d\n", c.Minor))
	}

	// Spot-check the two path shapes against MigCap.ProcPath.
	require.FileExists(t, filepath.Join(h.Root, capabilitiesDir, "mig/config"))
	require.FileExists(t, filepath.Join(h.Root, capabilitiesDir, "gpu0/mig/gi1/ci1/access"))
}

func TestCapabilityProcPath(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"config":              "mig/config",
		"monitor":             "mig/monitor",
		"gpu0/gi0/access":     "gpu0/mig/gi0/access",
		"gpu3/gi5/ci2/access": "gpu3/mig/gi5/ci2/access",
	} {
		require.Equal(t, want, capabilityProcPath(name), "cap %q", name)
	}
}

func TestStageCapDevs_RejectsUnusableMajor(t *testing.T) {
	t.Parallel()

	// A zero major reaching mknod would create nodes that open as the wrong
	// driver; failing here keeps the misconfiguration visible.
	err := stageCapDevs(testHost(t), 0, []migcaps.Cap{{Name: "config", Minor: 1}})
	require.ErrorContains(t, err, "not usable")
}

func TestStage_NoopWhenNothingPartitioned(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Stage(context.Background(), h, &agent.State{}))
	require.True(t, sim.Ready(), "a node without MIG must still mark the simulator ready")

	_, err := os.Stat(filepath.Join(h.Root, "driver/proc/driver/nvidia-caps/mig-minors"))
	require.ErrorIs(t, err, os.ErrNotExist,
		"the table's absence is how a consumer detects a non-MIG machine")
}

// TestStage_ClearsSurfaceWhenPartitionsGoAway covers a profile edit that turns
// MIG off: leaving the old table behind would keep advertising cap devices for
// partitions that no longer exist.
func TestStage_ClearsSurfaceWhenPartitionsGoAway(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	sim := New()
	ctx := context.Background()
	caps := capsFor(partitionedState().MIG)
	require.NoError(t, stageMinors(h, caps))
	require.NoError(t, stageCapabilityTree(h, caps))

	require.NoError(t, sim.Stage(ctx, h, &agent.State{}))

	_, err := os.Stat(filepath.Join(h.Root, "driver/proc/driver/nvidia-caps/mig-minors"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(h.Root, capabilitiesDir, "gpu0"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestRemoveSurface_KeepsIMEXCapability guards a directory these two simulators
// share: removing capabilities/ wholesale would silently break IMEX on a node
// running both.
func TestRemoveSurface_KeepsIMEXCapability(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	imexCap := filepath.Join(h.Root, capabilitiesDir, "fabric-imex-mgmt")
	require.NoError(t, os.MkdirAll(filepath.Dir(imexCap), 0o755))
	require.NoError(t, os.WriteFile(imexCap, []byte("DeviceFileMinor: 512\n"), 0o644))
	require.NoError(t, stageCapabilityTree(h, capsFor(partitionedState().MIG)))

	require.NoError(t, removeSurface(h))

	require.FileExists(t, imexCap, "the IMEX capability is not this simulator's to remove")
	require.NoDirExists(t, filepath.Join(h.Root, capabilitiesDir, "gpu0"))
	require.NoDirExists(t, filepath.Join(h.Root, capabilitiesDir, "mig"))
}

func TestDiscard_NopWhenNotReady(t *testing.T) {
	t.Parallel()

	require.NoError(t, New().Discard(context.Background(), testHost(t)))
}

func TestStage_CreatesCapDevices(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New()
	state := partitionedState()
	ctx := context.Background()

	require.NoError(t, sim.Stage(ctx, h, state))
	require.True(t, sim.Ready())

	for _, c := range capsFor(state.MIG) {
		path := filepath.Join(h.Root, capDevDir, fmt.Sprintf("nvidia-cap%d", c.Minor))
		require.FileExists(t, path, "cap device for %q", c.Name)
	}

	// gpudriver's Discard removes driver/dev wholesale, so this simulator has
	// to survive being restaged over its own output.
	require.NoError(t, sim.Stage(ctx, h, state), "second Stage must not error")

	require.NoError(t, sim.Discard(ctx, h))
	require.NoDirExists(t, filepath.Join(h.Root, capDevDir))
}
