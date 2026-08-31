// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane_test

import (
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "test-namespace")

	cfg := controlplane.DefaultConfig()
	require.Equal(t, controlplane.ServerConfig{
		ListenAddr:      ":8080",
		ShutdownTimeout: 5 * time.Second,
	}, cfg.Server)
	require.Equal(t, controlplane.KubernetesConfig{QPS: 50, Burst: 100}, cfg.Kubernetes)
	require.Equal(t, controlplane.LeaderElectionConfig{
		Namespace:     "test-namespace",
		Name:          "mokka-control-plane.mokka.nvidia.com",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	}, cfg.LeaderElection)
	require.Equal(t, mokkacontroller.DefaultOptions(), cfg.Controller)
}
