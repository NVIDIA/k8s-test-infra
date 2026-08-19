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
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, 5*time.Second, cfg.ShutdownTimeout)
	require.Equal(t, "mokka-control-plane.mokka.nvidia.com", cfg.LeaderElectionName)
	require.Equal(t, 2, cfg.Workers)
	require.Equal(t, 100*time.Millisecond, cfg.StatusDebounce)
	require.Equal(t, time.Second, cfg.StatusProgressInterval)
	require.Equal(t, 2*time.Second, cfg.LiveNodeGetTimeout)
}

func TestNewLogger(t *testing.T) {
	for _, tc := range []struct {
		level   string
		wantErr bool
	}{
		{"debug", false},
		{"info", false},
		{"warn", false},
		{"warning", false},
		{"error", false},
		{"Info", false}, // Case-insensitive.
		{"", false},     // Empty falls back to info.
		{"trace", true}, // Not a slog level.
		{"noisy", true},
	} {
		t.Run(tc.level, func(t *testing.T) {
			cfg := controlplane.DefaultConfig()
			cfg.LogLevel = tc.level
			logger, err := controlplane.NewLogger(cfg)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, logger)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, logger)
		})
	}
}
