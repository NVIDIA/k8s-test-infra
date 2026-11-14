// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/numatopology"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func reconfigurationFixture(t *testing.T, configure func(*hostOperations)) (*Simulator, *agent.State, *[]string) {
	t.Helper()
	h := testHost(t)
	require.NoError(t, fsutil.Write(h.HostPath(kubeletConfig), []byte("cpuManagerPolicy: none\nmemoryManagerPolicy: None\nevictionHard:\n  memory.available: 1Mi\n"), 0600))
	ops, calls := fakeHostOperations()
	if configure != nil {
		configure(&ops)
	}
	sim, err := newTestSimulator(h, Options{Enabled: true, CPUManagerPolicy: "static", ReservedCPUs: "0", MemoryManagerPolicy: "static", SystemReservedMemory: "2Mi"}, ops)
	require.NoError(t, err)
	state := testEnrichedState(t, h)
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	*calls = nil
	return sim, state, calls
}

func replacementState(t *testing.T, h *host.Host) *agent.State {
	t.Helper()
	state := testState()
	state.Generation = 2
	state.NodeShape.Topology.RootComplexes = append(state.NodeShape.Topology.RootComplexes, agent.RootComplex{ID: "pci0000:c0", NUMANode: 2})
	require.NoError(t, numatopology.NewEnricher(h).Enrich(t.Context(), state))
	return state
}

func TestLayoutChangeRestoresBeforeRenderingAndReapplies(t *testing.T) {
	t.Parallel()
	sim, _, calls := reconfigurationFixture(t, nil)
	h := sim.host
	oldKey := sim.desired
	state := replacementState(t, h)
	for _, checkpoint := range checkpointFiles {
		require.NoError(t, fsutil.Write(h.HostPath("/var/lib/kubelet/"+checkpoint), []byte("allocation A"), 0600))
	}
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.False(t, sim.Ready())
	require.NoFileExists(t, h.RunPath(appliedRel))
	require.Contains(t, *calls, "umount "+h.HostPath(kubeletConfig))
	require.Contains(t, *calls, "umount "+h.SysPath("devices/system/node"))
	requireFileContent(t, h.RootPath("numa/sysnode/online"), "0-1\n")
	require.NoError(t, sim.Stage(t.Context(), state))
	requireFileContent(t, h.RootPath("numa/sysnode/online"), "0-2\n")
	require.NoError(t, sim.Apply(t.Context(), state))
	require.True(t, sim.Ready())
	require.NotEqual(t, oldKey, sim.desired)
	requireFileContent(t, h.RunPath(appliedRel), sim.desired+"\n")
	for _, checkpoint := range checkpointFiles {
		require.NoFileExists(t, h.HostPath("/var/lib/kubelet/"+checkpoint))
		require.NoError(t, fsutil.Write(h.HostPath("/var/lib/kubelet/"+checkpoint), []byte("allocation B"), 0600))
	}
	config, err := os.ReadFile(h.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(config), "numaNode: 2")
	*calls = nil
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	require.Empty(t, *calls, "the unchanged layout must retain mounts and checkpoints")
	for _, checkpoint := range checkpointFiles {
		requireFileContent(t, h.HostPath("/var/lib/kubelet/"+checkpoint), "allocation B")
	}
}

func TestFailedLayoutResetPreservesRecoveryAndCanRetry(t *testing.T) {
	t.Parallel()
	fail := false
	failure := errors.New("unmount failed")
	sim, _, calls := reconfigurationFixture(t, func(ops *hostOperations) {
		run := ops.run
		ops.run = func(ctx context.Context, name string, args ...string) error {
			if fail && name == "umount" {
				return failure
			}
			return run(ctx, name, args...)
		}
	})
	state := replacementState(t, sim.host)
	oldKey := sim.desired
	sim.retryFirst = time.Millisecond
	fail = true
	require.ErrorIs(t, sim.PrepareStage(t.Context(), state), failure)
	select {
	case <-sim.Requeue():
	case <-time.After(time.Second):
		t.Fatal("failed reset did not schedule a retry")
	}
	require.False(t, sim.Ready())
	requireFileContent(t, sim.host.RootPath("numa/sysnode/online"), "0-1\n")
	requireFileContent(t, sim.host.RunPath(appliedRel), oldKey+"\n")
	require.FileExists(t, sim.host.RunPath("mokka/numa/original/kubelet-config.yaml"))
	require.Contains(t, strings.Join(*calls, "\n"), "systemctl stop kubelet")
	fail = false
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	require.True(t, sim.Ready())
	requireFileContent(t, sim.host.RootPath("numa/sysnode/online"), "0-2\n")
}

func TestInvalidReplacementLeavesActiveLayoutUntouched(t *testing.T) {
	t.Parallel()
	sim, _, calls := reconfigurationFixture(t, nil)
	state := replacementState(t, sim.host)
	state.NodeShape.NUMA.Zones[0].CPUs = []int{4, 5}
	oldKey := sim.desired
	require.Error(t, sim.PrepareStage(t.Context(), state), "reserved CPU 0 is absent")
	require.Empty(t, *calls)
	requireFileContent(t, sim.host.RunPath(appliedRel), oldKey+"\n")
	requireFileContent(t, sim.host.RootPath("numa/sysnode/online"), "0-1\n")
}

func TestFailedReplacementApplyRestoresOriginalAndCanRetry(t *testing.T) {
	t.Parallel()
	startsUntilFailure := 0
	failure := errors.New("new kubelet setup failed")
	sim, _, _ := reconfigurationFixture(t, func(ops *hostOperations) {
		run := ops.run
		ops.run = func(ctx context.Context, name string, args ...string) error {
			if name == "systemctl" && args[0] == "start" && startsUntilFailure > 0 {
				startsUntilFailure--
				if startsUntilFailure == 0 {
					return failure
				}
			}
			return run(ctx, name, args...)
		}
	})
	state := replacementState(t, sim.host)
	startsUntilFailure = 2 // Original kubelet starts on restore; the new setup fails.
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.ErrorIs(t, sim.Apply(t.Context(), state), failure)
	require.False(t, sim.Ready())
	require.NoFileExists(t, sim.host.RunPath(appliedRel))
	config, err := os.ReadFile(sim.host.HostPath(kubeletConfig))
	require.NoError(t, err)
	require.Contains(t, string(config), "memoryManagerPolicy: None")
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	require.True(t, sim.Ready())
}

func TestPolicyChangeRestoresBeforeStaging(t *testing.T) {
	t.Parallel()
	sim, state, calls := reconfigurationFixture(t, nil)
	oldKey := sim.desired
	sim.opts.Policy = "restricted"
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.Contains(t, *calls, "systemctl stop kubelet")
	require.NoFileExists(t, sim.host.RunPath(appliedRel))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	require.NotEqual(t, oldKey, sim.desired)
	config, err := os.ReadFile(sim.host.RootPath("numa/kubelet-config.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(config), "topologyManagerPolicy: restricted")
}

func TestPendingLayoutChangeRestoresBeforeStaging(t *testing.T) {
	t.Parallel()
	sim, _, calls := reconfigurationFixture(t, nil)
	require.NoError(t, os.Rename(sim.host.RunPath(appliedRel), sim.host.RunPath(pendingRel)))
	state := replacementState(t, sim.host)
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.Contains(t, *calls, "systemctl stop kubelet")
	require.NoFileExists(t, sim.host.RunPath(pendingRel))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	requireFileContent(t, sim.host.RootPath("numa/sysnode/online"), "0-2\n")
}
