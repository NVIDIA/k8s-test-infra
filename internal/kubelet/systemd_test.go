// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kubelet

import (
	"context"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func TestRemoveBootResetReloadsBeforeStoppingDependency(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	require.NoError(t, fsutil.Write(h.HostPath(HelperPath), []byte("helper"), 0700))
	require.NoError(t, InstallBootReset(t.Context(), h, BootResetPlan{NUMA: true}, func(context.Context, ...string) error { return nil }))
	var calls [][]string
	require.NoError(t, RemoveBootReset(t.Context(), h, func(_ context.Context, args ...string) error {
		require.NoFileExists(t, h.EtcPath(dropinPath))
		calls = append(calls, args)
		return nil
	}))
	require.Equal(t, [][]string{{"daemon-reload"}, {"stop", serviceName}, {"daemon-reload"}}, calls)
	require.NoFileExists(t, h.RootPath(planPath))
}
