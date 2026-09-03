// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
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
	sim := New(h)

	require.NoError(t, sim.Stage(context.Background(), stateWithTopology()))
	require.True(t, sim.Ready())

	// Renderer writes a /sys/bus/pci/devices/<bdf> symlink under h.Root.
	symlink := h.RootPath("sys/bus/pci/devices/0000:07:00.0")
	_, err := os.Lstat(symlink)
	require.NoError(t, err, "sysfs BDF symlink must exist")
}

func TestStage_NopWhenNoTopology(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	state := &agent.State{} // no topology, no devices

	require.NoError(t, sim.Stage(context.Background(), state))
	require.True(t, sim.Ready())

	sysDir := h.RootPath("sys")
	_, err := os.Stat(sysDir)
	require.True(t, os.IsNotExist(err), "sys/ must not be created when topology is empty")
}

func TestStage_Idempotent(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	state := stateWithTopology()

	require.NoError(t, sim.Stage(context.Background(), state))
	require.NoError(t, sim.Stage(context.Background(), state), "second Stage must not error")
}

// ─── Discard ─────────────────────────────────────────────────────────────────

func TestDiscard_NopWhenNotReady(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Discard(context.Background()))
}

func TestDiscard_RemovesSysTree(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(context.Background(), stateWithTopology()))
	require.NoError(t, sim.Discard(context.Background()))

	sysDir := h.RootPath("sys")
	_, err := os.Stat(sysDir)
	require.True(t, os.IsNotExist(err), "sys/ must be removed after Discard")
}

func TestDiscard_SysGoneIsNotError(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(context.Background(), stateWithTopology()))

	// Manually remove sys/ before calling Discard; RemoveAll on a missing path is
	// a no-op so Discard must still succeed.
	require.NoError(t, os.RemoveAll(h.RootPath("sys")))
	require.NoError(t, sim.Discard(context.Background()))
}

// ─── Apply / Revoke ──────────────────────────────────────────────────────────

func TestApply_WritesNFDFeatureFile(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Apply(context.Background(), nil))

	data, err := os.ReadFile(h.EtcPath(nfdFeatureFile))
	require.NoError(t, err)
	require.Equal(t, nfdContent, string(data))
}

func TestRevoke_RemovesNFDFile(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Apply(context.Background(), nil))
	require.NoError(t, sim.Revoke(context.Background()))

	_, err := os.Stat(h.EtcPath(nfdFeatureFile))
	require.True(t, os.IsNotExist(err), "NFD feature file must be removed")
}

func TestRevoke_IdempotentWhenFileAbsent(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Revoke(context.Background()), "Revoke on absent file must not error")
}

func TestApply_Idempotent(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Apply(context.Background(), nil))
	require.NoError(t, sim.Apply(context.Background(), nil), "second Apply must not error")
}

// ─── Ready ───────────────────────────────────────────────────────────────────

func TestReady_FalseBeforeStage(t *testing.T) {
	sim := New(testHost(t))
	require.False(t, sim.Ready())
}

func TestReady_TrueAfterStage(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(context.Background(), stateWithTopology()))
	require.True(t, sim.Ready())
}

func TestReady_SurvivesDiscard(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(context.Background(), stateWithTopology()))
	require.True(t, sim.Ready())

	require.NoError(t, sim.Discard(context.Background()))
	// Ready() records Stage success, not current sysfs presence — Discard reads
	// the flag as its own precondition, so teardown leaves it set.
	require.True(t, sim.Ready(), "Discard does not reset ready flag")
}
