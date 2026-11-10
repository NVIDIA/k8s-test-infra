//go:build linux && integration

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"os"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
)

// Run only in a disposable, privileged hostPID pod with the node mounted at
// /host with bidirectional /host/sys and /host/var/lib/kubelet volumes, plus
// writable /host/run/mokka and /host/etc/systemd/system volumes.
// The explicit action prevents the integration suite changing a node
// unless its caller has prepared the dedicated lifecycle test environment.
func TestPrivilegedPodLifecycle(t *testing.T) {
	action := os.Getenv("MOKKA_NUMA_TEST_ACTION")
	if action == "" {
		t.Skip("requires a disposable node and MOKKA_NUMA_TEST_ACTION")
	}
	h := host.New("/host")
	baseline := h.RootPath("numa-test/original-config")
	switch action {
	case "apply":
		original, err := os.ReadFile(h.HostPath(kubeletConfig))
		require.NoError(t, err)
		if _, err := os.Stat(baseline); os.IsNotExist(err) {
			require.NoError(t, fsutil.Write(baseline, original, 0600))
		}
		sim, err := New(h, Options{Enabled: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static", SystemReservedCPU: "100m"})
		require.NoError(t, err)
		sim.helperSource = "/tmp/mokka-kubelet-helper"
		require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, h)))
		require.NoError(t, sim.Apply(t.Context(), nil))
		require.Equal(t, "0-1\n", string(readNodeFile(t, h, "/sys/devices/system/node/online")))
		config := readNodeFile(t, h, kubeletConfig)
		require.Contains(t, string(config), "memoryManagerPolicy: Static")
		require.Contains(t, string(config), "cpuManagerPolicy: static")
		// Reconciliation must leave live allocation checkpoints unchanged.
		checkpoint, err := os.ReadFile(h.HostPath("/var/lib/kubelet/memory_manager_state"))
		require.NoError(t, err)
		require.NoError(t, sim.Apply(t.Context(), nil))
		requireFileContent(t, h.HostPath("/var/lib/kubelet/memory_manager_state"), string(checkpoint))
	case "restore":
		sim, err := New(h, Options{})
		require.NoError(t, err)
		require.NoError(t, sim.Apply(t.Context(), nil))
		original, err := os.ReadFile(baseline)
		require.NoError(t, err)
		require.Equal(t, string(original), string(readNodeFile(t, h, kubeletConfig)))
		require.NoFileExists(t, h.RootPath("kubelet/boot-reset.json"))
		require.NoFileExists(t, h.RunPath(appliedRel))
	case "boot":
		original, err := os.ReadFile(baseline)
		require.NoError(t, err)
		require.Equal(t, string(original), string(readNodeFile(t, h, kubeletConfig)))
		require.NoFileExists(t, h.RunPath(appliedRel))
		require.NoDirExists(t, h.RunPath("mokka/numa/original"))
		require.FileExists(t, h.RunPath("mokka/kubelet-reset.done"))
	default:
		t.Fatalf("unknown lifecycle action %q", action)
	}
}

func readNodeFile(t *testing.T, h *host.Host, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(h.HostPath(path))
	require.NoError(t, err)
	return data
}
