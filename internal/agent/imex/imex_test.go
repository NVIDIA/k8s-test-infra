// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

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
)

func testHost(t *testing.T) *host.Host {
	t.Helper()
	return host.New(t.TempDir())
}

func testState(enabled bool) *agent.State {
	state := &agent.State{}
	if enabled {
		state.IMEX = agent.IMEXState{
			Enabled:      true,
			IMEXMajor:    235,
			CapsMajor:    236,
			ChannelCount: 3, // small count to keep tests fast
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
	state := testState(true)

	require.NoError(t, stageProcDevices(h, state, writeProcDevicesFixture(t)))

	content, err := os.ReadFile(filepath.Join(h.Root, "imex/proc-devices"))
	require.NoError(t, err)
	require.Contains(t, string(content), "nvidia-caps-imex-channels")
	require.Contains(t, string(content), "nvidia-caps")
}

func TestStageProcDevices_Idempotent(t *testing.T) {
	h := testHost(t)
	state := testState(true)
	src := writeProcDevicesFixture(t)

	require.NoError(t, stageProcDevices(h, state, src))
	require.NoError(t, stageProcDevices(h, state, src), "second call must not error")
}

func TestStageFabricImexMgmt_WritesFile(t *testing.T) {
	h := testHost(t)

	require.NoError(t, stageFabricImexMgmt(h))

	content, err := os.ReadFile(filepath.Join(h.Root, "driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt"))
	require.NoError(t, err)
	require.Contains(t, string(content), "DeviceFileMinor: 512")
	require.Contains(t, string(content), "DeviceFileMode: 438")
}

func TestStageChannelDevs_CreatesNodes(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	state := testState(true)

	require.NoError(t, stageChannelDevs(h, state))

	dir := filepath.Join(h.Root, "driver/dev/nvidia-caps-imex-channels")
	for i := range state.IMEX.ChannelCount {
		_, err := os.Stat(filepath.Join(dir, fmt.Sprintf("channel%d", i)))
		require.NoError(t, err, "channel%d must exist", i)
	}
}

// ─── Simulator lifecycle ──────────────────────────────────────────────────────

func TestStage_NoopWhenDisabled(t *testing.T) {
	h := testHost(t)
	sim := New()
	ctx := context.Background()

	require.NoError(t, sim.Stage(ctx, h, testState(false)))
	require.True(t, sim.Ready(), "disabled IMEX must still mark simulator ready")

	entries, err := os.ReadDir(h.Root)
	if !os.IsNotExist(err) {
		require.NoError(t, err)
		require.Empty(t, entries, "disabled IMEX must write nothing")
	}
}

func TestDiscard_NopWhenNotReady(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Discard(context.Background(), h))
}

func TestStage_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := &Simulator{procDevicesPath: writeProcDevicesFixture(t)}
	state := testState(true)
	ctx := context.Background()

	require.NoError(t, sim.Stage(ctx, h, state))
	require.NoError(t, sim.Stage(ctx, h, state), "second Stage must not error")
}

func TestStage_WritesAndDiscardCleans(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := &Simulator{procDevicesPath: writeProcDevicesFixture(t)}
	state := testState(true)
	ctx := context.Background()

	require.NoError(t, sim.Stage(ctx, h, state))
	require.True(t, sim.Ready())

	require.NoError(t, sim.Discard(ctx, h))

	_, err := os.Stat(filepath.Join(h.Root, "driver/dev/nvidia-caps-imex-channels"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(h.Root, "imex"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
