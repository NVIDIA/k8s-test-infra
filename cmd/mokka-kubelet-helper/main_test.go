// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/kubelet"
	"github.com/stretchr/testify/require"
)

func TestResetCommandUsesRegisteredNodeRoot(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	require.NoError(t, kubelet.ArmBootReset(h, kubelet.BootResetPlan{NUMA: true}))
	require.NoError(t, os.RemoveAll(h.Run))
	cpu := h.HostPath("/var/lib/kubelet/cpu_manager_state")
	require.NoError(t, fsutil.Write(cpu, []byte("simulated"), 0600))
	require.NoError(t, newCLI().Run(t.Context(), []string{"mokka-kubelet-helper", "--host-root", h.Prefix, "reset-for-boot"}))
	require.NoFileExists(t, cpu)
}

func TestRejectInvalidArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"unknown"}, {"reset-for-boot", "extra"}} {
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()
			require.Error(t, newCLI().Run(t.Context(), append([]string{"mokka-kubelet-helper"}, args...)))
		})
	}
}
