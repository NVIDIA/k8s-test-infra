// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// Many kernels already give the configured majors to other drivers (236 is
// often hidraw), so the agent stages free ones, the same in channel0 and in
// the /proc/devices substitute.
func TestStage_StagesFreeMajorsWhenTheConfiguredOnesAreTaken(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	procDevices := filepath.Join(t.TempDir(), "proc-devices")
	require.NoError(t, os.WriteFile(procDevices,
		[]byte("Character devices:\n235 foo\n236 hidraw\n\nBlock devices:\n  7 loop\n"), 0o644))
	sim := New(h)
	sim.procDevicesPath = procDevices

	require.NoError(t, sim.Stage(t.Context(), testState(true)))

	require.EqualValues(t, 384, channel0Major(t, h))
	content, err := os.ReadFile(h.RootPath("imex/proc-devices"))
	require.NoError(t, err)
	require.Contains(t, string(content), "384 nvidia-caps-imex-channels\n")
	require.Contains(t, string(content), "385 nvidia-caps\n")
}

// A host driver can take channel0's major after it was staged. The next Stage
// picks another major and replaces channel0, which mknod alone would keep.
func TestStage_ReplacesChannel0WhenItsMajorChanges(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	sim.procDevicesPath = writeProcDevicesFixture(t)
	require.NoError(t, sim.Stage(t.Context(), testState(true)))
	require.EqualValues(t, 235, channel0Major(t, h))

	taken := filepath.Join(t.TempDir(), "proc-devices")
	require.NoError(t, os.WriteFile(taken, []byte("Character devices:\n235 foo\n\nBlock devices:\n  7 loop\n"), 0o644))
	sim.procDevicesPath = taken
	require.NoError(t, sim.Stage(t.Context(), testState(true)))

	require.EqualValues(t, 384, channel0Major(t, h))
	content, err := os.ReadFile(h.RootPath("imex/proc-devices"))
	require.NoError(t, err)
	require.Contains(t, string(content), "384 nvidia-caps-imex-channels\n")
}

func channel0Major(t *testing.T, h *host.Host) uint32 {
	t.Helper()
	var st unix.Stat_t
	require.NoError(t, unix.Stat(h.RootPath("driver/dev/nvidia-caps-imex-channels/channel0"), &st))
	return unix.Major(st.Rdev)
}
