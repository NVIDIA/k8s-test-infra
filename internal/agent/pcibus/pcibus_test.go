// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"os"
	"path/filepath"
	"strings"
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
	sim := New(h)

	require.NoError(t, sim.Stage(t.Context(), stateWithTopology()))
	require.False(t, sim.Ready(), "Stage does not publish the NFD feature file")

	// Renderer writes a /sys/bus/pci/devices/<bdf> symlink under h.Root.
	symlink := h.RootPath("sys/bus/pci/devices/0000:07:00.0")
	_, err := os.Lstat(symlink)
	require.NoError(t, err, "sysfs BDF symlink must exist")
}

func TestStage_NopWhenNoTopology(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	state := &agent.State{} // no topology, no devices

	require.NoError(t, sim.Stage(t.Context(), state))
	require.False(t, sim.Ready(), "Stage does not publish the NFD feature file")

	sysDir := h.RootPath("sys")
	_, err := os.Stat(sysDir)
	require.True(t, os.IsNotExist(err), "sys/ must not be created when topology is empty")
}

func TestStage_Idempotent(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	state := stateWithTopology()

	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state), "second Stage must not error")
}

// ─── Discard ─────────────────────────────────────────────────────────────────

func TestDiscard_NopWhenNotReady(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Discard(t.Context()))
}

func TestDiscard_EmptiesSysTree(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(t.Context(), stateWithTopology()))
	require.NoError(t, sim.Discard(t.Context()))

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
	sim := New(h)

	require.NoError(t, sim.Stage(t.Context(), stateWithTopology()))

	for _, rel := range []string{pcisysfs.SysDevicesRelPath, pcisysfs.PCIDevicesRelPath} {
		path := filepath.Join(h.Root, rel)
		before, err := os.Stat(path)
		require.NoError(t, err)

		require.NoError(t, sim.Discard(t.Context()))

		after, err := os.Stat(path)
		require.NoError(t, err, "%s must outlive the teardown", rel)
		require.True(t, os.SameFile(before, after), "%s was replaced rather than emptied", rel)
	}
}

func TestDiscard_SysGoneIsNotError(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(t.Context(), stateWithTopology()))

	// Removing sys/ before Discard: a teardown with nothing left to tear down
	// must still succeed.
	require.NoError(t, os.RemoveAll(h.RootPath("sys")))
	require.NoError(t, sim.Discard(t.Context()))
}

// ─── Apply / Revoke ──────────────────────────────────────────────────────────

func TestApply_WritesNFDFeatureFile(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Apply(t.Context(), nil))
	require.True(t, sim.Ready())

	data, err := os.ReadFile(h.EtcPath(nfdFeatureFile))
	require.NoError(t, err)
	// Literal, not nfdContent: comparing the file back to the constant that
	// wrote it holds for any value of that constant, including an empty one.
	// This pins the exact bytes NFD's local source parses, trailing newline
	// included; the semantic facts are asserted separately below.
	require.Equal(t, "pci-10de.present=true\nnvml-mock.present=true\n", string(data))
}

func TestRevoke_RemovesNFDFile(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Apply(t.Context(), nil))
	require.NoError(t, sim.Revoke(t.Context()))

	_, err := os.Stat(h.EtcPath(nfdFeatureFile))
	require.True(t, os.IsNotExist(err), "NFD feature file must be removed")
}

func TestRevoke_IdempotentWhenFileAbsent(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Revoke(t.Context()), "Revoke on absent file must not error")
}

func TestApply_Idempotent(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Apply(t.Context(), nil))
	require.NoError(t, sim.Apply(t.Context(), nil), "second Apply must not error")
}

// ─── Ready ───────────────────────────────────────────────────────────────────

func TestReady_FalseBeforeStage(t *testing.T) {
	sim := New(testHost(t))
	require.False(t, sim.Ready())
}

func TestReady_TrueAfterApply(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(t.Context(), stateWithTopology()))
	require.False(t, sim.Ready())
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.True(t, sim.Ready())
}

func TestReady_SurvivesDiscard(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Stage(t.Context(), stateWithTopology()))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.True(t, sim.Ready())

	require.NoError(t, sim.Discard(t.Context()))
	// Discard removes staged artifacts but does not withdraw published ones;
	// Revoke runs first during teardown and clears readiness.
	require.True(t, sim.Ready(), "Discard does not reset ready flag")
}

// A Mokka node is otherwise byte-identical to real hardware at the label
// level: NFD derives feature.node.kubernetes.io/pci-10de.present=true from
// this file, and a real NVIDIA node carries the same key. Nothing downstream
// could tell the GPUs were simulated, so snapshots and conformance evidence
// captured against a mocked cluster read as real silicon. The agent therefore
// states the fact rather than leaving consumers to infer it.
//
// Asserted against literals, not against nfdContent: comparing the file back
// to the constant that wrote it passes for any value of that constant.
func TestApply_FeatureFileDeclaresTheGPUsAreSimulated(t *testing.T) {
	h := testHost(t)
	sim := New(h)

	require.NoError(t, sim.Apply(t.Context(), nil))

	data, err := os.ReadFile(h.EtcPath(nfdFeatureFile))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Contains(t, lines, "pci-10de.present=true",
		"NFD must still derive the PCI vendor label")
	require.Contains(t, lines, "nvml-mock.present=true",
		"a simulated node must be distinguishable from real hardware")
}
