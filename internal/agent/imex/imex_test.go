// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

func testHost(t *testing.T) *host.Host {
	t.Helper()
	return host.New(t.TempDir())
}

func testState(enabled bool) *agent.State {
	state := &agent.State{}
	if enabled {
		state.IMEX = agent.IMEXState{
			Enabled:   true,
			IMEXMajor: 235,
			CapsMajor: 236,
		}
	}
	return state
}

func skipUnlessRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Skip("requires root on Linux (mknod)")
	}
}

// minimalProcDevices is a synthetic /proc/devices satisfying render.ProcDevices' parser.
const minimalProcDevices = "Character devices:\n  1 mem\n\nBlock devices:\n  7 loop\n"

// writeProcDevicesFixture writes minimalProcDevices to a temp file and returns its path.
func writeProcDevicesFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "proc-devices")
	require.NoError(t, os.WriteFile(p, []byte(minimalProcDevices), 0o644))
	return p
}

// ─── surface helpers ──────────────────────────────────────────────────────────

func TestStageProcDevices_WritesRenderedFile(t *testing.T) {
	h := testHost(t)

	require.NoError(t, stageProcDevices(h, minimalProcDevices, 235, 236))

	content, err := os.ReadFile(h.RootPath("imex/proc-devices"))
	require.NoError(t, err)
	require.Contains(t, string(content), "nvidia-caps-imex-channels")
	require.Contains(t, string(content), "nvidia-caps")
}

func TestStageProcDevices_Idempotent(t *testing.T) {
	h := testHost(t)

	require.NoError(t, stageProcDevices(h, minimalProcDevices, 235, 236))
	require.NoError(t, stageProcDevices(h, minimalProcDevices, 235, 236), "second call must not error")
}

func TestStageFabricImexMgmt_WritesFile(t *testing.T) {
	h := testHost(t)

	require.NoError(t, stageFabricImexMgmt(h))

	content, err := os.ReadFile(h.RootPath("driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt"))
	require.NoError(t, err)
	require.Contains(t, string(content), "DeviceFileMinor: 512")
	require.Contains(t, string(content), "DeviceFileMode: 438")
}

// A real node has channel0 at most, so that is all the agent stages, and a
// node staged by an earlier version loses the rest.
func TestStageChannelDevs_StagesOnlyChannel0(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	dir := h.RootPath("driver/dev/nvidia-caps-imex-channels")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel1"), nil, 0o644))

	require.NoError(t, stageChannelDevs(h, 235))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "channel0", entries[0].Name())
}

// ─── Simulator lifecycle ──────────────────────────────────────────────────────

func TestStage_NoopWhenDisabled(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	ctx := t.Context()

	require.NoError(t, sim.Stage(ctx, testState(false)))
	require.True(t, sim.Ready(), "disabled IMEX must still mark simulator ready")

	entries, err := os.ReadDir(h.RootPath())
	if !os.IsNotExist(err) {
		require.NoError(t, err)
		require.Empty(t, entries, "disabled IMEX must write nothing")
	}
}

func TestDiscard_SucceedsWithNothingStaged(t *testing.T) {
	sim := New(testHost(t))

	require.NoError(t, sim.Discard(t.Context()))
}

func TestStage_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := &Simulator{host: h, procDevicesPath: writeProcDevicesFixture(t)}
	state := testState(true)
	ctx := t.Context()

	require.NoError(t, sim.Stage(ctx, state))
	require.NoError(t, sim.Stage(ctx, state), "second Stage must not error")
}

func TestStage_WritesAndDiscardCleans(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := &Simulator{host: h, procDevicesPath: writeProcDevicesFixture(t)}
	state := testState(true)
	ctx := t.Context()

	require.NoError(t, sim.Stage(ctx, state))
	require.True(t, sim.Ready())

	require.NoError(t, sim.Discard(ctx))

	_, err := os.Stat(h.RootPath("driver/dev/nvidia-caps-imex-channels"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(h.RootPath("imex"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// imexSurface is what a successful Stage writes.
var imexSurface = []string{
	"driver/dev/nvidia-caps-imex-channels/channel0",
	"imex/proc-devices",
	"driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt",
}

// seedSurface writes what a successful Stage leaves on h. The channel node is
// a plain file: mknod needs root, and removal does not care.
func seedSurface(t *testing.T, h *host.Host) {
	t.Helper()
	for _, rel := range imexSurface {
		require.NoError(t, fsutil.Write(h.RootPath(rel), nil, 0o644))
	}
}

func requireNoSurface(t *testing.T, h *host.Host) {
	t.Helper()
	require.NoDirExists(t, h.RootPath("driver/dev/nvidia-caps-imex-channels"))
	require.NoDirExists(t, h.RootPath("imex"))
	require.NoFileExists(t, h.RootPath("driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt"))
}

// IMEX follows the profile: a node whose profile is no longer multi-node
// NVLink loses the surface at the next Stage, not only at shutdown.
func TestStage_DisabledRemovesTheSurface(t *testing.T) {
	h := testHost(t)
	seedSurface(t, h)
	sim := New(h)

	require.NoError(t, sim.Stage(t.Context(), testState(false)))

	require.True(t, sim.Ready())
	requireNoSurface(t, h)
}

// A node whose surface cannot be staged becomes a node without IMEX: Stage
// removes what an earlier pass staged and stays ready, so the node agent does
// too.
func TestStage_FailsOpenWhenTheSurfaceCannotBeStaged(t *testing.T) {
	h := testHost(t)
	seedSurface(t, h)
	sim := New(h)
	sim.procDevicesPath = filepath.Join(t.TempDir(), "missing")

	require.NoError(t, sim.Stage(t.Context(), testState(true)))

	require.True(t, sim.Ready())
	requireNoSurface(t, h)
}
