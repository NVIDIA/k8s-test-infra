// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"testing"

	"github.com/containerd/nri/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestMemoryMappingPreservesUsableNodes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, allowed, requested, want string
		changed                        bool
	}{
		{"disabled", "", "1", "", false},
		{"unallocated", "0", "", "", false},
		{"existing node", "0-1", "1", "1", false},
		{"virtual node", "0", "1", "0", true},
		{"mixed group", "0", "0-1", "0", true},
		{"nonzero host", "2-3", "0-1", "2-3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPlugin(Config{MemoryNodes: tc.allowed})
			mapped, changed, err := p.memoryNodes(&api.LinuxResources{Cpu: &api.LinuxCPU{Mems: tc.requested}})
			require.NoError(t, err)
			require.Equal(t, tc.want, mapped)
			require.Equal(t, tc.changed, changed)
		})
	}
}

func TestMemoryOnlyContainerIsMappedWithoutGPUInjection(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.MemoryNodes = "0"
	p := NewPlugin(cfg)
	pod := &api.PodSandbox{Namespace: "cpu-only", Name: "memory-test"}
	container := &api.Container{Id: "memory-only", Linux: &api.LinuxContainer{Resources: &api.LinuxResources{Cpu: &api.LinuxCPU{Mems: "1"}}}}
	adjustment, _, err := p.CreateContainer(t.Context(), pod, container)
	require.NoError(t, err)
	require.NotNil(t, adjustment)
	require.Equal(t, "0", adjustment.GetLinux().GetResources().GetCpu().GetMems())
	require.Empty(t, adjustment.Mounts)
	require.Equal(t, "1", container.Linux.Resources.Cpu.Mems)
}

func TestMemoryReconciliationPreservesOtherResourceUpdates(t *testing.T) {
	t.Parallel()
	p := NewPlugin(Config{MemoryNodes: "0"})
	resources := &api.LinuxResources{Cpu: &api.LinuxCPU{Mems: "1", Cpus: "4-5", Quota: api.Int64(200000)}, Memory: &api.LinuxMemory{Limit: api.Int64(512 * 1024 * 1024)}}
	updates, err := p.UpdateContainer(t.Context(), nil, &api.Container{Id: "container"}, resources)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.Equal(t, "container", updates[0].ContainerId)
	mapped := updates[0].Linux.Resources
	require.Equal(t, "0", mapped.Cpu.Mems)
	require.Equal(t, "4-5", mapped.Cpu.Cpus)
	require.Equal(t, int64(200000), mapped.Cpu.Quota.Value)
	require.Equal(t, int64(512*1024*1024), mapped.Memory.Limit.Value)
	require.Equal(t, "1", resources.Cpu.Mems)
}
