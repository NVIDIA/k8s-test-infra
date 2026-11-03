// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/kubelet"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
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
	state, err := NewPreparer(h).Prepare(t.Context(), state)
	require.NoError(t, err)
	plan := state.NodeShape.NUMA
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
			_, err := resolve(h, s)
			require.Error(t, err)
		})
	}
}

func TestRenderCreatesCPUReferencesAndMemoryWithoutTouchingPCI(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	s := testState()
	plan, err := resolve(h, s)
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
			_, err := resolve(testHost(t), s)
			require.Error(t, err)
		})
	}
}

func TestPersistentSetupIsNotRestartedOrRemovedOnPodTeardown(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	calls := 0
	runner := hostRunnerFunc(func(context.Context, HostAction, bool) error {
		calls++
		desired, err := os.ReadFile(h.RootPath("numa/desired"))
		require.NoError(t, err)
		return fsutil.Write(h.RunPath(appliedRel), desired, 0600)
	})
	sim, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true, Runner: runner})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.Equal(t, 2, calls)
	require.NoError(t, sim.Revoke(t.Context()))
	require.NoError(t, sim.Discard(t.Context()))
	require.FileExists(t, h.RunPath(appliedRel))
	changed, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true, Policy: "restricted"})
	require.NoError(t, err)
	require.ErrorContains(t, changed.Stage(t.Context(), testPreparedState(t, changed.host)), "disable NUMA simulation")
	restore := hostRunnerFunc(func(_ context.Context, action HostAction, _ bool) error {
		require.Equal(t, ActionRestore, action)
		return os.Remove(h.RunPath(appliedRel))
	})
	disabled, err := newTestSimulator(h, Options{ConfigureKubelet: true, Runner: restore})
	require.NoError(t, err)
	require.NoError(t, disabled.Stage(t.Context(), nil))
	require.NoError(t, disabled.Apply(t.Context(), nil))
	require.NoFileExists(t, h.RunPath(appliedRel))
}

func TestRenderAfterRestoreRemovesOldZones(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	plan, err := resolve(h, testState())
	require.NoError(t, err)
	require.NoError(t, Render(h, plan))
	require.DirExists(t, h.RootPath("numa/sysnode/node1"))
	plan.Zones = []agent.NUMAZone{{ID: 0, CPUs: []int{0, 1, 2, 3, 4, 5, 6, 7}, MemoryKB: 16000}}
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
			sim, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", ReservedCPUs: tc.reserved})
			require.NoError(t, err)
			require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
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
			sim, err := newTestSimulator(testHost(t), Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", ReservedCPUs: reserved})
			require.NoError(t, err)
			require.Error(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
		})
	}
	_, err := newTestSimulator(testHost(t), Options{CPUManagerPolicy: "invalid"})
	require.Error(t, err)
	_, err = newTestSimulator(testHost(t), Options{Enabled: true, CPUManagerPolicy: "static"})
	require.ErrorContains(t, err, "configureKubelet")
}

func TestStaticMemoryReservationsMatchNodeReservations(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.HostPath("/var/lib/kubelet/config.yaml"), []byte("kubeReserved:\n  memory: 1Mi\nevictionHard:\n  memory.available: 1Mi\n"), 0600))
	sim, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static", SystemReservedMemory: "2Mi"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	var config struct {
		MemoryManagerPolicy string
		ReservedMemory      []struct {
			NUMANode int               `json:"numaNode"`
			Limits   map[string]string `json:"limits"`
		} `json:"reservedMemory"`
	}
	require.NoError(t, yaml.Unmarshal(data, &config))
	require.Equal(t, "Static", config.MemoryManagerPolicy)
	require.Len(t, config.ReservedMemory, 2)
	for i, reservation := range config.ReservedMemory {
		require.Equal(t, i, reservation.NUMANode)
		amount := resource.MustParse(reservation.Limits["memory"])
		require.Equal(t, int64(2*1024*1024), amount.Value())
	}
}

func TestStaticMemoryRejectsImpossibleReservations(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"invalid", "0", "1Gi"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			sim, err := newTestSimulator(testHost(t), Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static", SystemReservedMemory: value})
			require.NoError(t, err)
			require.Error(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
		})
	}
	_, err := newTestSimulator(testHost(t), Options{Enabled: true, ConfigureKubelet: true, MemoryManagerPolicy: "static"})
	require.ErrorContains(t, err, "static CPU Manager")
}

func TestFractionalSystemCPUReservation(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	sim, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", SystemReservedCPU: "100m"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	var config map[string]any
	require.NoError(t, yaml.Unmarshal(data, &config))
	require.NotContains(t, config, "reservedSystemCPUs")
	require.Equal(t, "100m", config["systemReserved"].(map[string]any)["cpu"])
	_, err = newTestSimulator(h, Options{SystemReservedCPU: "100m", ReservedCPUs: "0"})
	require.ErrorContains(t, err, "mutually exclusive")
}

func newTestSimulator(h *host.Host, opts Options) (*Simulator, error) {
	sim, err := New(h, opts)
	if err != nil {
		return nil, err
	}
	sim.helperSource = h.RootPath("test-helper-source")
	// New does not stage files; this fixture supplies the image's helper binary.
	if err := os.MkdirAll(h.Root, 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(sim.helperSource, []byte("test Go executable"), 0700); err != nil {
		return nil, err
	}

	return sim, nil
}

func TestStageRefreshesHostHelper(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	sim, err := newTestSimulator(h, Options{Enabled: true})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	requireFileContent(t, h.RootPath("kubelet/mokka-kubelet-helper"), "test Go executable")
	require.NoError(t, os.WriteFile(sim.helperSource, []byte("updated executable"), 0700))
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	requireFileContent(t, h.RootPath("kubelet/mokka-kubelet-helper"), "updated executable")
}

func TestLegacySetupMustBeRestoredWithPreviousVersion(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			t.Parallel()
			h := testHost(t)
			require.NoError(t, os.MkdirAll(h.RootPath("numa"), 0755))
			require.NoError(t, os.WriteFile(h.RootPath("numa/.applied"), []byte("legacy"), 0600))
			sim, err := newTestSimulator(h, Options{Enabled: enabled})
			require.NoError(t, err)
			require.ErrorContains(t, sim.Apply(t.Context(), nil), "previous Mokka version")
			if enabled {
				require.ErrorContains(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)), "previous Mokka version")
			}
			requireFileContent(t, h.RootPath("numa/.applied"), "legacy")
		})
	}
}

func TestLowercaseMemoryPolicyPreservesAppliedConfiguration(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.HostPath("/var/lib/kubelet/config.yaml"), []byte("evictionHard:\n  memory.available: 1Mi\n"), 0600))
	runner := hostRunnerFunc(func(context.Context, HostAction, bool) error { return nil })
	sim, err := newTestSimulator(h, Options{Runner: runner, Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static", SystemReservedMemory: "2Mi"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	// This fingerprint was produced by the previous capitalized setting.
	const previous = "339badff2f87bb98162abc14e5e739ff2a7a0fb192e5987be55339009087961f:true:single-numa-node:static::Static:2Mi"
	requireFileContent(t, h.RootPath("numa/desired"), previous+"\n")
	require.NoError(t, fsutil.Write(h.RunPath(appliedRel), []byte(previous+"\n"), 0600))
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	require.NoError(t, sim.Apply(t.Context(), nil))
}

func TestKubeletConfigPreservesUnmanagedFields(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	const original = `apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
featureGates:
  FutureFeature: true
podPidsLimit: 2048
systemReserved:
  cpu: 300m
  memory: 1Mi
  ephemeral-storage: 1Gi
kubeReserved:
  memory: 1Mi
  ephemeral-storage: 2Gi
evictionHard:
  memory.available: 1Mi
  nodefs.available: 10%
reservedSystemCPUs: "7"
reservedMemory:
  - numaNode: 0
    limits:
      memory: 1Mi
    futureField: keep
`
	require.NoError(t, os.WriteFile(h.HostPath("/var/lib/kubelet/config.yaml"), []byte(original), 0600))
	sim, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", SystemReservedCPU: "100m"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	var before, after map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(original), &before))
	require.NoError(t, yaml.Unmarshal(data, &after))
	for _, key := range []string{"apiVersion", "kind", "featureGates", "podPidsLimit", "kubeReserved", "evictionHard", "reservedMemory"} {
		require.Equal(t, before[key], after[key], key)
	}
	require.Equal(t, map[string]any{"cpu": "100m", "memory": "1Mi", "ephemeral-storage": "1Gi"}, after["systemReserved"])
	require.NotContains(t, after, "reservedSystemCPUs")
}

func TestKubeletConfigRejectsInvalidManagedFields(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.HostPath("/var/lib/kubelet/config.yaml"), []byte("systemReserved: invalid\n"), 0600))
	sim, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true})
	require.NoError(t, err)
	require.ErrorContains(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)), "decode kubelet config")
}

type hostRunnerFunc func(context.Context, HostAction, bool) error

func (f hostRunnerFunc) Run(ctx context.Context, action HostAction, managed bool) error {
	return f(ctx, action, managed)
}

func TestConstructorHostRunnerReceivesActionsAndErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                   string
		enabled, managed, fail bool
		action                 HostAction
	}{
		{name: "mount only", enabled: true, action: ActionApply},
		{name: "managed apply", enabled: true, managed: true, action: ActionApply},
		{name: "restore", action: ActionRestore},
		{name: "runner failure", enabled: true, fail: true, action: ActionApply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := testHost(t)
			require.NoError(t, os.MkdirAll(h.RootPath("numa"), 0755))
			if !tc.enabled {
				require.NoError(t, fsutil.Write(h.RunPath(appliedRel), []byte("active\n"), 0600))
			}
			calls := 0
			failure := errors.New("test host runner failed")
			runner := hostRunnerFunc(func(ctx context.Context, action HostAction, managed bool) error {
				require.Equal(t, t.Context(), ctx)
				require.Equal(t, tc.action, action)
				require.Equal(t, tc.managed, managed)
				calls++
				if tc.fail {
					return failure
				}
				return nil
			})
			sim, err := newTestSimulator(h, Options{Enabled: tc.enabled, ConfigureKubelet: tc.managed, Runner: runner})
			require.NoError(t, err)
			require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
			err = sim.Apply(t.Context(), nil)
			if tc.fail {
				require.ErrorIs(t, err, failure)
				require.False(t, sim.Ready())
			} else {
				require.NoError(t, err)
				require.True(t, sim.Ready())
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestDefaultSystemMemoryReservation(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.ProcPath("meminfo"), []byte("MemTotal: 1048576 kB\n"), 0644))
	sim, err := newTestSimulator(h, Options{Enabled: true, ConfigureKubelet: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testPreparedState(t, sim.host)))
	data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	var config kubeletSettings
	require.NoError(t, yaml.Unmarshal(data, &config))
	require.Equal(t, "256Mi", config.SystemReserved.Memory)
	require.Len(t, config.ReservedMemory, 2)
	// Each zone gets half of the 256Mi system and 100Mi eviction reservations.
	for _, reservation := range config.ReservedMemory {
		require.Equal(t, "178Mi", reservation.Limits["memory"])
	}
}

func TestDisableRecoversPendingSetupAndBootRegistration(t *testing.T) {
	t.Parallel()
	for _, pending := range []bool{true, false} {
		t.Run(strconv.FormatBool(pending), func(t *testing.T) {
			t.Parallel()
			h := testHost(t)
			if pending {
				require.NoError(t, fsutil.Write(h.RunPath(pendingRel), []byte("partial"), 0600))
			} else {
				require.NoError(t, kubelet.ArmBootReset(h, kubelet.BootResetPlan{NUMA: true}))
				require.NoError(t, os.RemoveAll(h.Run))
			}
			called := false
			runner := hostRunnerFunc(func(_ context.Context, action HostAction, managed bool) error {
				require.Equal(t, ActionRestore, action)
				require.False(t, managed)
				called = true
				return nil
			})
			sim, err := newTestSimulator(h, Options{Runner: runner})
			require.NoError(t, err)
			require.NoError(t, sim.Apply(t.Context(), nil))
			require.True(t, called)
		})
	}
}

func testPreparedState(t *testing.T, h *host.Host) *agent.State {
	t.Helper()
	state, err := NewPreparer(h).Prepare(t.Context(), testState())
	require.NoError(t, err)
	return state
}
