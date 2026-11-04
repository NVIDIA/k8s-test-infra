// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func numaSource(t *testing.T) (*FileSource, *host.Host) {
	t.Helper()
	f, _ := sourceWith(t, "")
	h := host.New(t.TempDir())
	for path, data := range map[string]string{
		h.SysPath("devices/system/cpu/online"): "0-7\n",
		h.ProcPath("meminfo"):                  "MemTotal: 16000 kB\n",
	} {
		require.NoError(t, fsutil.Write(path, []byte(data), 0600))
	}
	for cpu := range 8 {
		base := h.SysPath(fmt.Sprintf("devices/system/cpu/cpu%d/topology", cpu))
		require.NoError(t, fsutil.Write(filepath.Join(base, "core_id"), []byte(strconv.Itoa(cpu%4)), 0600))
		require.NoError(t, fsutil.Write(filepath.Join(base, "physical_package_id"), []byte("0"), 0600))
	}
	return f.WithNUMA(h, true), h
}

func TestFileSourceEmitsResolvedNUMAState(t *testing.T) {
	t.Parallel()
	f, _ := numaSource(t)
	original, err := os.ReadFile(f.configPath)
	require.NoError(t, err)
	var hash [32]byte
	update := reloadOnce(t, f, &hash)
	require.NotNil(t, update)
	require.NoError(t, update.Err)
	state := update.State
	require.Len(t, state.NodeShape.NUMA.Zones, 2)
	require.Equal(t, []int{0, 1, 4, 5}, state.NodeShape.NUMA.Zones[0].CPUs)
	require.Equal(t, uint64(8000), state.NodeShape.NUMA.Zones[0].MemoryKB)
	require.Equal(t, "0-1,4-5", state.NodeShape.Topology.RootComplexes[0].CPUAffinity)
	require.Contains(t, string(state.ConfigRaw), "cpu_affinity: 0-1,4-5")
	unchanged, err := os.ReadFile(f.configPath)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	require.Nil(t, reloadOnce(t, f, &hash), "unchanged complete state must not be emitted again")
}

func TestFileSourceRetriesNUMAFailureWithoutProfileEdit(t *testing.T) {
	t.Parallel()
	f, h := numaSource(t)
	require.NoError(t, os.Remove(h.ProcPath("meminfo")))
	var hash [32]byte
	failed := reloadOnce(t, f, &hash)
	require.NotNil(t, failed)
	require.Error(t, failed.Err)
	require.Nil(t, failed.State)
	require.Equal(t, [32]byte{}, hash, "failed builds must not consume the source update")
	require.NoError(t, fsutil.Write(h.ProcPath("meminfo"), []byte("MemTotal: 16000 kB\n"), 0600))
	recovered := reloadOnce(t, f, &hash)
	require.NotNil(t, recovered)
	require.NoError(t, recovered.Err)
	require.NotNil(t, recovered.State.NodeShape.NUMA)
}

func TestFileSourceDetectsHostNUMAInputChanges(t *testing.T) {
	t.Parallel()
	f, h := numaSource(t)
	var hash [32]byte
	first := reloadOnce(t, f, &hash)
	require.NoError(t, first.Err)
	require.NoError(t, fsutil.Write(h.ProcPath("meminfo"), []byte("MemTotal: 32000 kB\n"), 0600))
	second := reloadOnce(t, f, &hash)
	require.NotNil(t, second)
	require.NoError(t, second.Err)
	require.Equal(t, uint64(16000), second.State.NodeShape.NUMA.Zones[0].MemoryKB)
	require.Equal(t, uint64(8000), first.State.NodeShape.NUMA.Zones[0].MemoryKB, "previous state must remain unchanged")
	require.NoError(t, fsutil.Write(h.SysPath("devices/system/cpu/cpu1/topology/core_id"), []byte("0"), 0600))
	third := reloadOnce(t, f, &hash)
	require.NotNil(t, third)
	require.NoError(t, third.Err)
	require.NotEqual(t, second.State.NodeShape.NUMA.Zones, third.State.NodeShape.NUMA.Zones)
}

func TestFileSourceNUMADisabledNeedsNoHostTopology(t *testing.T) {
	t.Parallel()
	f, _ := sourceWith(t, "")
	f.WithNUMA(host.New(t.TempDir()), false)
	var hash [32]byte
	update := reloadOnce(t, f, &hash)
	require.NoError(t, update.Err)
	require.Nil(t, update.State.NodeShape.NUMA)
	require.Nil(t, reloadOnce(t, f, &hash))
}
