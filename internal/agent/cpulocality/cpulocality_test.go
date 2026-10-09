// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cpulocality

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// The profile's set follows the engine's rule: an explicit cpu_affinity, else
// the root's NUMA node times cores_per_numa, else nothing.
func TestLocal_ProfileSet(t *testing.T) {
	t.Parallel()

	require.Equal(t, []int{0, 1, 2, 8}, Local(agent.RootComplex{NUMANode: 0, CPUAffinity: "8,0-2,1"}, 36, nil))
	require.Equal(t, seq(36, 71), Local(agent.RootComplex{NUMANode: 1}, 36, nil))
	require.Equal(t, seq(64, 127), Local(agent.RootComplex{NUMANode: 1}, 0, nil),
		"an unset cores_per_numa falls back to the engine default")
	require.Nil(t, Local(agent.RootComplex{NUMANode: -1}, 36, seq(0, 13)), "no NUMA node, no locality")
	require.Nil(t, Local(agent.RootComplex{CPUAffinity: "x"}, 36, nil), "an unparseable cpu_affinity publishes nothing")
}

// A profile models the CPUs of the machine it describes, not of the host the
// mock runs on. A kernel never lists a CPU the machine lacks, and Slurm will
// not allocate a GPU whose local CPUs are all absent, so the set keeps only
// the host's CPUs, and falls back to all of them when none of its own exist.
func TestLocal_KeepsOnlyTheHostsCPUs(t *testing.T) {
	t.Parallel()

	numa0, numa1 := agent.RootComplex{NUMANode: 0}, agent.RootComplex{NUMANode: 1}

	require.Equal(t, seq(0, 13), Local(numa0, 0, seq(0, 13)))
	require.Equal(t, seq(0, 13), Local(numa1, 0, seq(0, 13)), "no NUMA 1 CPU exists, so every CPU is local")
	require.Equal(t, seq(0, 63), Local(numa0, 0, seq(0, 99)))
	require.Equal(t, seq(64, 99), Local(numa1, 0, seq(0, 99)))
}

func TestOnline(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	require.Nil(t, Online(h), "no sysfs, no limit")

	path := h.SysPath("devices/system/cpu/online")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("8-13,0-7\n"), 0o644))
	require.Equal(t, seq(0, 13), Online(h))

	require.NoError(t, os.WriteFile(path, []byte("garbage\n"), 0o644))
	require.Nil(t, Online(h), "an unreadable list imposes no limit")
}

func seq(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for c := lo; c <= hi; c++ {
		out = append(out, c)
	}
	return out
}
