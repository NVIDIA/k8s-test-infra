// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/containerd/nri/pkg/api"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/NVIDIA/k8s-test-infra/internal/staginggate"
)

// TestClosedGateWarnsForContainersItLeavesUnmocked pins the signal that keeps
// failing open from going unnoticed. A container the gate leaves unmodified is
// logged at Warn with its identity; a container NRI skips anyway is not.
// Not parallel: it swaps the global logger.
func TestClosedGateWarnsForContainersItLeavesUnmocked(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	lockPath := filepath.Join(t.TempDir(), staginggate.FileName)
	lock, err := staginggate.Exclusive(t.Context(), lockPath)
	require.NoError(t, err)
	require.NoError(t, lock.Close())

	cfg := DefaultConfig()
	cfg.AgentStagedURL = server.URL + "/stagedz"
	cfg.StagingLockPath = lockPath
	plugin := NewPlugin(cfg)

	excluded := &api.PodSandbox{Name: "system", Namespace: "kube-system"}
	adjustment, _, err := plugin.CreateContainer(t.Context(), excluded, &api.Container{Name: "sidecar"})
	require.NoError(t, err)
	require.Nil(t, adjustment)
	require.Zero(t, logs.Len(), "a container NRI would skip anyway is not a gate skip")

	plain := &api.PodSandbox{Name: "plain", Namespace: "default"}
	adjustment, _, err = plugin.CreateContainer(t.Context(), plain, &api.Container{Name: "main"})
	require.NoError(t, err)
	require.Nil(t, adjustment)
	require.Zero(t, logs.Len(), "a container that asked for no GPU is not a gate skip")

	workload := &api.PodSandbox{
		Name:        "workload",
		Namespace:   "default",
		Annotations: map[string]string{cfg.Inject.DeviceAnnotation: "true"},
	}
	adjustment, _, err = plugin.CreateContainer(t.Context(), workload, &api.Container{Name: "main"})
	require.NoError(t, err)
	require.Nil(t, adjustment, "a closed gate fails open")
	require.Equal(t, 1, logs.Len())
	fields := logs.All()[0].ContextMap()
	require.Equal(t, "default", fields["namespace"])
	require.Equal(t, "workload", fields["pod"])
	require.Equal(t, "main", fields["container"])
	require.Equal(t, "node agent has not staged the driver tree", fields["reason"])

	plugin.health.setRegistered(true)
	require.Equal(t, "not injecting new containers: node agent has not staged the driver tree",
		plugin.Readiness().Reason)
}

func TestAgentRestartSuspendsAdjustments(t *testing.T) {
	t.Parallel()

	var agentReady atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !agentReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(server.Close)

	lockPath := filepath.Join(t.TempDir(), staginggate.FileName)
	lock, err := staginggate.Exclusive(t.Context(), lockPath)
	require.NoError(t, err)
	require.NoError(t, lock.Close())

	cfg := DefaultConfig()
	cfg.AgentStagedURL = server.URL + "/stagedz"
	cfg.StagingLockPath = lockPath
	plugin := NewPlugin(cfg)
	plugin.health.setRegistered(true)
	pod := &api.PodSandbox{
		Name:        "test",
		Namespace:   "default",
		Annotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
	}
	container := &api.Container{Name: "test"}
	adjust := func() *api.ContainerAdjustment {
		adjustment, _, err := plugin.CreateContainer(t.Context(), pod, container)
		require.NoError(t, err)
		return adjustment
	}

	require.False(t, plugin.Readiness().OK)
	require.Nil(t, adjust(), "an unstaged agent must not produce stale mounts")
	agentReady.Store(true)
	require.True(t, plugin.Readiness().OK)
	require.NotNil(t, adjust())

	lock, err = staginggate.Exclusive(t.Context(), lockPath)
	require.NoError(t, err)
	require.False(t, plugin.Readiness().OK)
	require.Nil(t, adjust(), "an exclusive staging wave must suspend adjustments")
	require.NoError(t, lock.Close())

	agentReady.Store(false)
	require.False(t, plugin.Readiness().OK)
	require.Nil(t, adjust(), "a dead or restarting agent must leave new containers unmodified")
	agentReady.Store(true)
	require.NotNil(t, adjust(), "adjustments resume only after restaging")
}

// TestClosedGateHoldsBackComputeDomainDaemonWhileStagingIsExpected pins the one
// exception to failing open. The DRA ComputeDomain daemon cannot run without
// the IMEX node software, so while staging is expected a closed gate fails its
// creation and kubelet retries. Without staging it fails open like any other
// container.
func TestClosedGateHoldsBackComputeDomainDaemonWhileStagingIsExpected(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	lockPath := filepath.Join(t.TempDir(), staginggate.FileName)
	lock, err := staginggate.Exclusive(context.Background(), lockPath)
	require.NoError(t, err)
	require.NoError(t, lock.Close())

	daemonPod := &api.PodSandbox{
		Name:      "computedomain-daemon",
		Namespace: "nvidia",
		Labels:    map[string]string{"resource.nvidia.com/computeDomain": "domain-uid"},
	}
	daemon := &api.Container{Name: "compute-domain-daemon"}

	for _, staging := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.AgentStagedURL = server.URL + "/stagedz"
		cfg.StagingLockPath = lockPath
		cfg.Inject.ComputeDomainStaging = staging

		adjustment, _, err := NewPlugin(cfg).CreateContainer(context.Background(), daemonPod, daemon)
		require.Nil(t, adjustment)
		if staging {
			require.ErrorContains(t, err, "node agent has not staged the driver tree")
		} else {
			require.NoError(t, err, "without expected staging the daemon fails open")
		}
	}
}
