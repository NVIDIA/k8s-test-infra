// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

func testHost(t *testing.T) *host.Host {
	t.Helper()
	return host.New(t.TempDir())
}

// stateWithTopology returns a State carrying one root complex and one device,
// with the h100 profile's PCI identity words so rendered attribute files are
// checkable against real values.
func stateWithTopology() *agent.State {
	return &agent.State{
		NodeShape: agent.NodeShape{
			Topology: agent.PCIeTopology{
				RootComplexes: []agent.RootComplex{
					{ID: "pci0000:00", NUMANode: 0, DeviceBDFs: []string{"0000:07:00.0"}},
				},
			},
		},
		Devices: []agent.DeviceSpec{
			{
				Index:          0,
				PCIBusID:       "0000:07:00.0",
				PCIDeviceID:    0x233010DE,
				PCISubsystemID: 0x165810DE,
			},
		},
	}
}

// ─── Stage ───────────────────────────────────────────────────────────────────

func TestStage_RendersTopology(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Stage(context.Background(), h, stateWithTopology()))
	require.True(t, sim.Ready())

	// Renderer writes a /sys/bus/pci/devices/<bdf> symlink under h.Root.
	symlink := filepath.Join(h.Root, "sys/bus/pci/devices/0000:07:00.0")
	_, err := os.Lstat(symlink)
	require.NoError(t, err, "sysfs BDF symlink must exist")
}

func TestStage_NopWhenNoTopology(t *testing.T) {
	h := testHost(t)
	sim := New()
	state := &agent.State{} // no topology, no devices

	require.NoError(t, sim.Stage(context.Background(), h, state))
	require.True(t, sim.Ready())

	sysDir := filepath.Join(h.Root, "sys")
	_, err := os.Stat(sysDir)
	require.True(t, os.IsNotExist(err), "sys/ must not be created when topology is empty")
}

func TestStage_Idempotent(t *testing.T) {
	h := testHost(t)
	sim := New()
	state := stateWithTopology()

	require.NoError(t, sim.Stage(context.Background(), h, state))
	require.NoError(t, sim.Stage(context.Background(), h, state), "second Stage must not error")
}

// ─── Discard ─────────────────────────────────────────────────────────────────

func TestDiscard_NopWhenNotReady(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Discard(context.Background(), h))
}

func TestDiscard_EmptiesSysTree(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Stage(context.Background(), h, stateWithTopology()))
	require.NoError(t, sim.Discard(context.Background(), h))

	for _, rel := range []string{pcisysfs.SysDevicesRelPath, pcisysfs.PCIDevicesRelPath} {
		entries, err := os.ReadDir(filepath.Join(h.Root, rel))
		require.NoError(t, err)
		require.Empty(t, entries, "%s still carries a discarded profile", rel)
	}
}

// The CDI spec names the two served directories as mount sources, so a consumer
// container that outlives an agent restart reads whatever those inodes hold. If
// Discard replaces them the container is left on the removed ones, reading an
// empty tree no restage can reach.
func TestDiscard_KeepsTheDirectoriesTheCDISpecMounts(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Stage(context.Background(), h, stateWithTopology()))

	for _, rel := range []string{pcisysfs.SysDevicesRelPath, pcisysfs.PCIDevicesRelPath} {
		path := filepath.Join(h.Root, rel)
		before, err := os.Stat(path)
		require.NoError(t, err)

		require.NoError(t, sim.Discard(context.Background(), h))

		after, err := os.Stat(path)
		require.NoError(t, err, "%s must outlive the teardown", rel)
		require.True(t, os.SameFile(before, after), "%s was replaced rather than emptied", rel)
	}
}

func TestDiscard_SysGoneIsNotError(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Stage(context.Background(), h, stateWithTopology()))

	// Removing sys/ before Discard: a teardown with nothing left to tear down
	// must still succeed.
	require.NoError(t, os.RemoveAll(filepath.Join(h.Root, "sys")))
	require.NoError(t, sim.Discard(context.Background(), h))
}

// ─── Apply / Revoke ──────────────────────────────────────────────────────────

func TestApply_WritesNFDFeatureFile(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Apply(context.Background(), h, nil))

	data, err := os.ReadFile(filepath.Join(h.Etc, nfdFeatureFile))
	require.NoError(t, err)
	require.Equal(t, nfdContent, string(data))
}

func TestRevoke_RemovesNFDFile(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Apply(context.Background(), h, nil))
	require.NoError(t, sim.Revoke(context.Background(), h))

	_, err := os.Stat(filepath.Join(h.Etc, nfdFeatureFile))
	require.True(t, os.IsNotExist(err), "NFD feature file must be removed")
}

func TestRevoke_IdempotentWhenFileAbsent(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Revoke(context.Background(), h), "Revoke on absent file must not error")
}

func TestApply_Idempotent(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Apply(context.Background(), h, nil))
	require.NoError(t, sim.Apply(context.Background(), h, nil), "second Apply must not error")
}

// ─── Ready ───────────────────────────────────────────────────────────────────

func TestReady_FalseBeforeStage(t *testing.T) {
	sim := New()
	require.False(t, sim.Ready())
}

func TestReady_TrueAfterStage(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Stage(context.Background(), h, stateWithTopology()))
	require.True(t, sim.Ready())
}

func TestReady_SurvivesDiscard(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Stage(context.Background(), h, stateWithTopology()))
	require.True(t, sim.Ready())

	require.NoError(t, sim.Discard(context.Background(), h))
	// Ready() records Stage success, not current sysfs presence — Discard reads
	// the flag as its own precondition, so teardown leaves it set.
	require.True(t, sim.Ready(), "Discard does not reset ready flag")
}
