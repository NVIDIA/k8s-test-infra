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
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/numatopology"
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

func TestResolveKeepsThreadsTogether(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	state := testEnrichedState(t, h)
	plan := state.NodeShape.NUMA
	require.Len(t, plan.Zones, 2)
	require.Equal(t, []int{0, 1, 4, 5}, plan.Zones[0].CPUs)
	require.Equal(t, []int{2, 3, 6, 7}, plan.Zones[1].CPUs)
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
			_, err := numatopology.Resolve(h, s)
			require.Error(t, err)
		})
	}
}

func TestRenderCreatesCPUReferencesAndMemoryWithoutTouchingPCI(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	s := testState()
	plan, err := numatopology.Resolve(h, s)
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
	for _, name := range []string{"node0", "node1"} {
		target, err := os.Readlink(h.RootPath("numa/sysbusnode", name))
		require.NoError(t, err)
		require.Equal(t, "../../../devices/system/node/"+name, target)
	}
	require.NoDirExists(t, h.RootPath("sys/devices"))
}

func TestResolveRejectsSparseOrUnknownNUMAIDs(t *testing.T) {
	t.Parallel()
	for _, id := range []int{-1, 2} {
		t.Run(strconv.Itoa(id), func(t *testing.T) {
			t.Parallel()
			s := testState()
			s.NodeShape.Topology.RootComplexes[1].NUMANode = id
			_, err := numatopology.Resolve(testHost(t), s)
			require.Error(t, err)
		})
	}
}

func TestPersistentSetupIsNotRestartedOrRemovedOnPodTeardown(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	ops, calls := fakeHostOperations()
	sim, err := newTestSimulator(h, Options{Enabled: true}, ops)
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.Equal(t, 1, strings.Count(strings.Join(*calls, "\n"), "systemctl stop kubelet"))
	require.NoError(t, sim.Revoke(t.Context()))
	require.NoError(t, sim.Discard(t.Context()))
	require.FileExists(t, h.RunPath(appliedRel))
	changed, err := newTestSimulator(h, Options{Enabled: true, Policy: "restricted"})
	require.NoError(t, err)
	require.ErrorContains(t, changed.Stage(t.Context(), testEnrichedState(t, changed.host)), "disable NUMA simulation")
	disabled, err := newTestSimulator(h, Options{}, ops)
	require.NoError(t, err)
	require.NoError(t, disabled.Stage(t.Context(), nil))
	require.NoError(t, disabled.Apply(t.Context(), nil))
	require.NoFileExists(t, h.RunPath(appliedRel))
}

func TestRenderAfterRestoreRemovesOldZones(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	plan, err := numatopology.Resolve(h, testState())
	require.NoError(t, err)
	require.NoError(t, Render(h, plan))
	require.DirExists(t, h.RootPath("numa/sysnode/node1"))
	plan.Zones = []agent.NUMAZone{{ID: 0, CPUs: []int{0, 1, 2, 3, 4, 5, 6, 7}, MemoryKB: 16000}}
	require.NoError(t, Render(h, plan))
	require.NoDirExists(t, h.RootPath("numa/sysnode/node1"))
	_, err = os.Lstat(h.RootPath("numa/sysbusnode/node1"))
	require.True(t, os.IsNotExist(err), "restored layouts must not retain old bus links")
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
			sim, err := newTestSimulator(h, Options{Enabled: true, CPUManagerPolicy: "static", ReservedCPUs: tc.reserved})
			require.NoError(t, err)
			require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
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
			sim, err := newTestSimulator(testHost(t), Options{Enabled: true, CPUManagerPolicy: "static", ReservedCPUs: reserved})
			require.NoError(t, err)
			require.Error(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
		})
	}
	_, err := newTestSimulator(testHost(t), Options{CPUManagerPolicy: "invalid"})
	require.Error(t, err)
}

func TestStaticMemoryReservationsMatchNodeReservations(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.HostPath("/var/lib/kubelet/config.yaml"), []byte("kubeReserved:\n  memory: 1Mi\nevictionHard:\n  memory.available: 1Mi\n"), 0600))
	sim, err := newTestSimulator(h, Options{Enabled: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static", SystemReservedMemory: "2Mi"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
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

// The expected values come from kubelet's eviction formula, which keeps
// float32 precision; reservedMemory must match it exactly or kubelet refuses
// to start with the static Memory Manager.
func TestMemoryEvictionPercentMatchesKubeletRounding(t *testing.T) {
	t.Parallel()
	const capacity = 16 << 30
	for _, tc := range []struct {
		threshold string
		want      int64
	}{
		{threshold: "1%", want: 171798688},
		{threshold: "5%", want: 858993472},
		{threshold: "7.5%", want: 1288490240},
		{threshold: "10%", want: 1717986944},
	} {
		t.Run(tc.threshold, func(t *testing.T) {
			t.Parallel()
			got, err := memoryEviction(&kubelet.Settings{EvictionHard: map[string]string{"memory.available": tc.threshold}}, capacity)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestStaticMemoryRejectsImpossibleReservations(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"invalid", "0", "1Gi"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			sim, err := newTestSimulator(testHost(t), Options{Enabled: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static", SystemReservedMemory: value})
			require.NoError(t, err)
			require.Error(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
		})
	}
	_, err := newTestSimulator(testHost(t), Options{Enabled: true, MemoryManagerPolicy: "static"})
	require.ErrorContains(t, err, "static CPU Manager")
}

func TestFractionalSystemCPUReservation(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	sim, err := newTestSimulator(h, Options{Enabled: true, CPUManagerPolicy: "static", SystemReservedCPU: "100m"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
	data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	var config map[string]any
	require.NoError(t, yaml.Unmarshal(data, &config))
	require.NotContains(t, config, "reservedSystemCPUs")
	require.Equal(t, "100m", config["systemReserved"].(map[string]any)["cpu"])
	_, err = newTestSimulator(h, Options{SystemReservedCPU: "100m", ReservedCPUs: "0"})
	require.ErrorContains(t, err, "mutually exclusive")
}

func newTestSimulator(h *host.Host, opts Options, operations ...hostOperations) (*Simulator, error) {
	ops, _ := fakeHostOperations()
	if len(operations) > 0 {
		ops = operations[0]
	}
	sim, err := newSimulator(h, opts, ops)
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
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
	requireFileContent(t, h.RootPath("kubelet/mokka-kubelet-helper"), "test Go executable")
	require.NoError(t, os.WriteFile(sim.helperSource, []byte("updated executable"), 0700))
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
	requireFileContent(t, h.RootPath("kubelet/mokka-kubelet-helper"), "updated executable")
}

func TestLowercaseMemoryPolicyUsesKubeletSpellingInAppliedConfiguration(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.HostPath("/var/lib/kubelet/config.yaml"), []byte("evictionHard:\n  memory.available: 1Mi\n"), 0600))
	sim, err := newTestSimulator(h, Options{Enabled: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static", SystemReservedMemory: "2Mi"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
	// User settings are lowercase; the stored policy uses kubelet spelling.
	const previous = "339badff2f87bb98162abc14e5e739ff2a7a0fb192e5987be55339009087961f:true:single-numa-node:static::Static:2Mi"
	requireFileContent(t, h.RootPath("numa/desired"), previous+"\n")
	require.NoError(t, fsutil.Write(h.RunPath(appliedRel), []byte(previous+"\n"), 0600))
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
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
	sim, err := newTestSimulator(h, Options{Enabled: true, CPUManagerPolicy: "static", SystemReservedCPU: "100m"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
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
	sim, err := newTestSimulator(h, Options{Enabled: true})
	require.NoError(t, err)
	require.ErrorContains(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)), "decode kubelet config")
}

func TestDefaultSystemMemoryReservation(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.ProcPath("meminfo"), []byte("MemTotal: 1048576 kB\n"), 0644))
	sim, err := newTestSimulator(h, Options{Enabled: true, CPUManagerPolicy: "static", MemoryManagerPolicy: "static"})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, sim.host)))
	data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	var config kubelet.Settings
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
			ops, calls := fakeHostOperations()
			sim, err := newTestSimulator(h, Options{}, ops)
			require.NoError(t, err)
			require.NoError(t, sim.Apply(t.Context(), nil))
			require.Contains(t, *calls, "systemctl start kubelet")
			require.NoFileExists(t, h.RunPath(pendingRel))
			require.NoFileExists(t, h.RootPath("kubelet/boot-reset.json"))
		})
	}
}

// A failed Apply requeues with a doubling delay until it succeeds, because the
// agent does not retry it on its own.
func TestFailedApplyRequeuesUntilItSucceeds(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	fail := true
	ops, _ := fakeHostOperations()
	run := ops.run
	failure := errors.New("test host command failed")
	ops.run = func(ctx context.Context, name string, args ...string) error {
		if fail {
			return failure
		}
		return run(ctx, name, args...)
	}
	sim, err := newTestSimulator(h, Options{Enabled: true}, ops)
	require.NoError(t, err)
	sim.retryFirst = time.Millisecond
	state := testEnrichedState(t, h)
	require.NoError(t, sim.Stage(t.Context(), state))
	requeued := func() bool {
		select {
		case <-sim.Requeue():
			return true
		case <-time.After(time.Second):
			return false
		}
	}

	require.ErrorIs(t, sim.Apply(t.Context(), state), failure)
	require.False(t, sim.Ready())
	require.True(t, requeued())
	require.ErrorIs(t, sim.Apply(t.Context(), state), failure)
	require.False(t, sim.Ready())
	require.Equal(t, 4*time.Millisecond, sim.retryDelay, "the delay doubles")
	require.True(t, requeued())

	fail = false
	require.NoError(t, sim.Apply(t.Context(), state))
	require.True(t, sim.Ready())
	require.Zero(t, sim.retryDelay, "a success resets the delay")
	select {
	case <-sim.Requeue():
		t.Fatal("a successful Apply must not requeue")
	case <-time.After(20 * time.Millisecond):
	}
}

func testEnrichedState(t *testing.T, h *host.Host) *agent.State {
	t.Helper()
	state := testState()
	require.NoError(t, numatopology.NewEnricher(h).Enrich(t.Context(), state))
	return state
}

func TestNUMAEnabledAlwaysStagesKubeletConfiguration(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	sim, err := newTestSimulator(h, Options{Enabled: true})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, h)))
	data, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "topologyManagerPolicy: single-numa-node")
	require.Contains(t, string(data), "topologyManagerScope: container")
}

func TestSimulatorUsesHostOperationsWithDeadline(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	ops, calls := fakeHostOperations()
	run := ops.run
	ops.run = func(ctx context.Context, name string, args ...string) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.Positive(t, time.Until(deadline))
		require.LessOrEqual(t, time.Until(deadline), 120*time.Second)
		return run(ctx, name, args...)
	}
	sim, err := newTestSimulator(h, Options{Enabled: true}, ops)
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), testEnrichedState(t, h)))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.True(t, sim.Ready())
	require.Contains(t, *calls, "systemctl start kubelet")
	require.Contains(t, *calls, "healthcheck")
	disabled, err := newTestSimulator(h, Options{}, ops)
	require.NoError(t, err)
	require.NoError(t, disabled.Apply(t.Context(), nil))
	require.True(t, disabled.Ready())
	require.NoFileExists(t, h.RunPath(appliedRel))
	require.Contains(t, *calls, "umount "+h.HostPath(kubeletConfig))
}

func TestDisabledSimulatorLeavesUnmanagedNodeUntouched(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	ops, calls := fakeHostOperations()
	sim, err := newTestSimulator(h, Options{}, ops)
	require.NoError(t, err)
	require.Empty(t, *calls, "construction must not change the node")
	require.NoError(t, sim.Stage(t.Context(), nil))
	require.NoError(t, sim.Apply(t.Context(), nil))
	require.True(t, sim.Ready())
	require.Empty(t, *calls, "disabled simulation must not change an unmanaged node")
}

func TestSimulatorRestoreFailureKeepsRecoveryState(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, fsutil.Write(h.RunPath(pendingRel), []byte("partial"), 0600))
	ops, _ := fakeHostOperations()
	failure := errors.New("cannot stop kubelet")
	ops.run = func(context.Context, string, ...string) error { return failure }
	sim, err := newTestSimulator(h, Options{}, ops)
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), nil))
	require.ErrorIs(t, sim.Apply(t.Context(), nil), failure)
	t.Cleanup(func() { sim.retryTimer.Stop() })
	require.False(t, sim.Ready())
	requireFileContent(t, h.RunPath(pendingRel), "partial")
}
