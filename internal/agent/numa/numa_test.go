// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func testHost(t *testing.T) *host.Host {
	t.Helper()
	h := host.New(t.TempDir())
	write := func(path, value string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(value), 0644))
	}
	write(h.SysPath("devices/system/cpu/online"), "0-7\n")
	write(h.HostPath("/var/lib/kubelet/config.yaml"), "apiVersion: kubelet.config.k8s.io/v1beta1\nkind: KubeletConfiguration\ncpuManagerPolicy: none\nmemoryManagerPolicy: None\n")
	write(h.ProcPath("meminfo"), "MemTotal: 16000 kB\nMemFree: 4000 kB\n")
	for cpu := range 8 {
		write(h.SysPath(fmt.Sprintf("devices/system/cpu/cpu%d/topology/core_id", cpu)), strconv.Itoa(cpu%4))
		write(h.SysPath(fmt.Sprintf("devices/system/cpu/cpu%d/topology/physical_package_id", cpu)), "0")
	}
	return h
}

func testState() *agent.State {
	return &agent.State{NodeShape: agent.NodeShape{Topology: agent.PCIeTopology{RootComplexes: []agent.RootComplex{
		{ID: "pci0000:00", NUMANode: 0, DeviceBDFs: []string{"0000:07:00.0"}},
		{ID: "pci0000:80", NUMANode: 1, DeviceBDFs: []string{"0000:87:00.0"}},
	}}}, ConfigRaw: []byte("version: '1.0'\npcie_topology:\n  root_complexes:\n    - id: pci0000:00\n      numa_node: 0\n    - id: pci0000:80\n      numa_node: 1\n")}
}

func TestResolveSharesTopologyAndKeepsThreadsTogether(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	state := testState()
	plan, err := Resolve(h, state)
	require.NoError(t, err)
	require.Len(t, plan.Zones, 2)
	require.Equal(t, []int{0, 1, 4, 5}, plan.Zones[0].CPUs)
	require.Equal(t, []int{2, 3, 6, 7}, plan.Zones[1].CPUs)
	require.Equal(t, "0-1,4-5", state.NodeShape.Topology.RootComplexes[0].CPUAffinity)
	require.Contains(t, string(state.ConfigRaw), "cpu_affinity: 0-1,4-5")
	require.Equal(t, uint64(8000), plan.Zones[0].MemoryKB)
}

func TestResolveRejectsInvalidCPUAssignments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, a, b string }{
		{"overlap", "0-3", "3-7"}, {"missing CPU", "0-3", "4-6"}, {"absent CPU", "0-3", "4-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := testHost(t)
			s := testState()
			s.NodeShape.Topology.RootComplexes[0].CPUAffinity = tc.a
			s.NodeShape.Topology.RootComplexes[1].CPUAffinity = tc.b
			_, err := Resolve(h, s)
			require.Error(t, err)
		})
	}
}

func TestRenderCreatesCPUReferencesAndMemoryWithoutTouchingPCI(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	s := testState()
	plan, err := Resolve(h, s)
	require.NoError(t, err)
	require.NoError(t, Render(h, plan))
	for _, tc := range []struct{ path, want string }{
		{"online", "0-1\n"}, {"node0/cpulist", "0-1,4-5\n"}, {"node1/cpumap", "000000cc\n"}, {"node1/distance", "21 10\n"},
	} {
		data, err := os.ReadFile(h.RootPath("numa/sysnode", tc.path))
		require.NoError(t, err)
		require.Equal(t, tc.want, string(data))
	}
	target, err := os.Readlink(h.RootPath("numa/sysnode/node1/cpu2"))
	require.NoError(t, err)
	require.Equal(t, "../../cpu/cpu2", target)
	data, err := os.ReadFile(h.RootPath("numa/sysnode/node1/meminfo"))
	require.NoError(t, err)
	require.Contains(t, string(data), "Node 1 MemTotal: 8000 kB")
	require.DirExists(t, h.RootPath("numa/sysnode/node1/hugepages"))
	require.NoDirExists(t, h.RootPath("sys/devices"))
}

func TestResolveRejectsSparseOrUnknownNUMAIDs(t *testing.T) {
	t.Parallel()
	for _, id := range []int{-1, 2} {
		t.Run(strconv.Itoa(id), func(t *testing.T) {
			t.Parallel()
			s := testState()
			s.NodeShape.Topology.RootComplexes[1].NUMANode = id
			_, err := Resolve(testHost(t), s)
			require.Error(t, err)
		})
	}
}

func TestPersistentSetupIsNotRestartedOrRemovedOnPodTeardown(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	sim, err := New(h, Options{Enabled: true, ConfigureKubelet: true})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testState()))
	calls := 0
	sim.runHost = func(context.Context, ...string) error {
		calls++
		return os.WriteFile(h.RootPath("numa/applied"), []byte(sim.desired+"\n"), 0600)
	}
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.Equal(t, 1, calls)
	require.NoError(t, sim.Revoke(t.Context()))
	require.NoError(t, sim.Discard(t.Context()))
	require.FileExists(t, h.RootPath("numa/applied"))
	changed, err := New(h, Options{Enabled: true, ConfigureKubelet: true, Policy: "restricted"})
	require.NoError(t, err)
	require.ErrorContains(t, changed.Stage(t.Context(), testState()), "disable NUMA simulation")
	disabled, err := New(h, Options{ConfigureKubelet: true})
	require.NoError(t, err)
	disabled.runHost = func(_ context.Context, args ...string) error {
		require.Contains(t, args, "restore")
		return os.Remove(h.RootPath("numa/applied"))
	}
	require.NoError(t, disabled.Stage(t.Context(), nil))
	require.NoError(t, disabled.Apply(t.Context(), nil))
	require.NoFileExists(t, h.RootPath("numa/applied"))
}

func TestRenderAfterRestoreRemovesOldZones(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	plan, err := Resolve(h, testState())
	require.NoError(t, err)
	require.NoError(t, Render(h, plan))
	require.DirExists(t, h.RootPath("numa/sysnode/node1"))
	plan.Zones = []Zone{{ID: 0, CPUs: []int{0, 1, 2, 3, 4, 5, 6, 7}, MemoryKB: 16000}}
	require.NoError(t, Render(h, plan))
	require.NoDirExists(t, h.RootPath("numa/sysnode/node1"))
	data, err := os.ReadFile(h.RootPath("numa/sysnode/online"))
	require.NoError(t, err)
	require.Equal(t, "0\n", string(data))
}

func TestStaticCPUManagerConfiguration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reserved, want string
	}{
		{"automatic reservation", "", "0"},
		{"explicit reservation", "1,5", "1,5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := testHost(t)
			sim, err := New(h, Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", ReservedCPUs: tc.reserved})
			require.NoError(t, err)
			require.NoError(t, sim.Stage(t.Context(), testState()))
			data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
			require.NoError(t, err)
			var config map[string]any
			require.NoError(t, yaml.Unmarshal(data, &config))
			require.Equal(t, "static", config["cpuManagerPolicy"])
			require.Equal(t, "None", config["memoryManagerPolicy"])
			require.Equal(t, tc.want, config["reservedSystemCPUs"])
		})
	}
}

func TestStaticCPUManagerRejectsUnusableReservations(t *testing.T) {
	t.Parallel()
	for _, reserved := range []string{"8", "0-7", "invalid"} {
		t.Run(reserved, func(t *testing.T) {
			t.Parallel()
			sim, err := New(testHost(t), Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", ReservedCPUs: reserved})
			require.NoError(t, err)
			require.Error(t, sim.Stage(t.Context(), testState()))
		})
	}
	_, err := New(testHost(t), Options{CPUManagerPolicy: "invalid"})
	require.Error(t, err)
	_, err = New(testHost(t), Options{Enabled: true, CPUManagerPolicy: "static"})
	require.ErrorContains(t, err, "configureKubelet")
}
