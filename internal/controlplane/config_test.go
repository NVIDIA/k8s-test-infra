// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := controlplane.DefaultConfig()
	require.Equal(t, ":8080", cfg.ListenAddr)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "json", cfg.LogFormat)
	require.Equal(t, 5*time.Second, cfg.ShutdownTimeout)
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

func TestNewLoggerFormat(t *testing.T) {
	for _, tc := range []struct {
		format   string
		wantErr  bool
		wantJSON bool
	}{
		{"json", false, true},
		{"plain", false, false},
		{"JSON", false, true},   // Case-insensitive.
		{"Plain", false, false}, // Case-insensitive.
		{"", false, true},       // Empty falls back to json.
		{"text", true, false},   // Not an accepted alias.
		{"logfmt", true, false},
	} {
		t.Run(tc.format, func(t *testing.T) {
			cfg := controlplane.DefaultConfig()
			cfg.LogFormat = tc.format
			logger, err := controlplane.NewLogger(cfg)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, logger)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, logger)
			if tc.wantJSON {
				require.IsType(t, (*slog.JSONHandler)(nil), logger.Handler())
			} else {
				require.IsType(t, (*slog.TextHandler)(nil), logger.Handler())
			}
		})
	}
}
