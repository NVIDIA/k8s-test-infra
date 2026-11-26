// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kubelet

import (
	"os"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func TestBootResetOnlyRemovesRegisteredNUMAStateOncePerBoot(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	cpu := h.HostPath("/var/lib/kubelet/cpu_manager_state")
	memory := h.HostPath("/var/lib/kubelet/memory_manager_state")
	config := h.HostPath("/var/lib/kubelet/config.yaml")
	require.NoError(t, fsutil.Write(config, []byte("original"), 0600))
	require.NoError(t, fsutil.Write(cpu, []byte("original cpu"), 0600))
	require.NoError(t, ResetForBoot(h)) // No ownership: leave kubelet alone.
	require.FileExists(t, cpu)
	require.NoError(t, ArmBootReset(h))
	require.NoError(t, fsutil.Write(cpu, []byte("simulated cpu"), 0600))
	require.NoError(t, fsutil.Write(memory, []byte("simulated memory"), 0600))
	require.NoError(t, ResetForBoot(h)) // Ordinary kubelet restart in the same boot.
	require.FileExists(t, cpu)
	require.FileExists(t, memory)
	require.NoError(t, os.RemoveAll(h.Run)) // A node reboot removes /run.
	require.NoError(t, ResetForBoot(h))
	require.NoFileExists(t, cpu)
	require.NoFileExists(t, memory)
	require.FileExists(t, h.RunPath("mokka/kubelet-reset.done"))
	data, err := os.ReadFile(config)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	require.NoError(t, fsutil.Write(cpu, []byte("new allocation"), 0600))
	require.NoError(t, ResetForBoot(h))
	require.FileExists(t, cpu) // Restarting kubelet again must preserve new state.
}

func TestFailedBootResetRetriesAndKeepsOwnership(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	require.NoError(t, ArmBootReset(h))
	require.NoError(t, os.RemoveAll(h.Run))
	cpu := h.HostPath("/var/lib/kubelet/cpu_manager_state")
	require.NoError(t, os.MkdirAll(cpu, 0755))
	require.NoError(t, fsutil.Write(cpu+"/block", []byte("failure"), 0600))
	require.Error(t, ResetForBoot(h))
	require.NoFileExists(t, h.RunPath(donePath))
	require.FileExists(t, h.RootPath(enabledPath))
	require.NoError(t, os.RemoveAll(cpu))
	require.NoError(t, ResetForBoot(h))
	require.FileExists(t, h.RunPath(donePath))
}

func TestDisarmDoesNotResetLaterBoots(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	require.NoError(t, ArmBootReset(h))
	require.NoError(t, DisarmBootReset(h))
	require.NoError(t, os.RemoveAll(h.Run))
	cpu := h.HostPath("/var/lib/kubelet/cpu_manager_state")
	require.NoError(t, fsutil.Write(cpu, []byte("unrelated state"), 0600))
	require.NoError(t, ResetForBoot(h))
	require.FileExists(t, cpu)
}

func TestBootResetRejectsInvalidMarkerWithoutRemovingState(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	require.NoError(t, os.MkdirAll(h.RootPath("kubelet/boot-reset.enabled"), 0700))
	cpu := h.HostPath("/var/lib/kubelet/cpu_manager_state")
	require.NoError(t, fsutil.Write(cpu, []byte("allocation"), 0600))
	require.ErrorContains(t, ResetForBoot(h), "boot reset marker is not a regular file")
	data, err := os.ReadFile(cpu)
	require.NoError(t, err)
	require.Equal(t, "allocation", string(data))
	require.NoFileExists(t, h.RunPath("mokka/kubelet-reset.done"))
}

func TestBootResetLeavesUnregisteredState(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	cpu := h.HostPath("/var/lib/kubelet/cpu_manager_state")
	require.NoError(t, fsutil.Write(cpu, []byte("allocation"), 0600))
	require.NoError(t, ResetForBoot(h))
	data, err := os.ReadFile(cpu)
	require.NoError(t, err)
	require.Equal(t, "allocation", string(data))
	require.NoFileExists(t, h.RunPath("mokka/kubelet-reset.done"))
}

func TestBootResetRegistrationIsAnEmptyMarker(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	require.NoError(t, ArmBootReset(h))
	data, err := os.ReadFile(h.RootPath("kubelet/boot-reset.enabled"))
	require.NoError(t, err)
	require.Empty(t, data)
	registered, err := BootResetRegistered(h)
	require.NoError(t, err)
	require.True(t, registered)
	require.NoError(t, DisarmBootReset(h))
	registered, err = BootResetRegistered(h)
	require.NoError(t, err)
	require.False(t, registered)
	require.NoFileExists(t, h.RootPath("kubelet/boot-reset.enabled"))
}
