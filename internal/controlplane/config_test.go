// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane_test

import (
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := controlplane.DefaultConfig()
	require.Equal(t, ":8080", cfg.ListenAddr)
	require.Equal(t, 5*time.Second, cfg.ShutdownTimeout)
}
