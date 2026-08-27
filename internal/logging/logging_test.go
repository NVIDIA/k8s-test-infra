// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package logging_test

import (
	"log/slog"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	"github.com/stretchr/testify/require"
)

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    logging.Level
		wantErr bool
	}{
		{"debug", logging.LevelDebug, false},
		{"info", logging.LevelInfo, false},
		{"warn", logging.LevelWarn, false},
		{"warning", logging.LevelWarn, false},
		{"error", logging.LevelError, false},
		{"Info", logging.LevelInfo, false}, // case-insensitive
		{"", logging.LevelInfo, false},     // empty defaults to info
		{"trace", "", true},
		{"noisy", "", true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := logging.ParseLevel(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseFormat(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    logging.Format
		wantErr bool
	}{
		{"json", logging.FormatJSON, false},
		{"plain", logging.FormatPlain, false},
		{"JSON", logging.FormatJSON, false},   // case-insensitive
		{"Plain", logging.FormatPlain, false}, // case-insensitive
		{"", logging.FormatJSON, false},       // empty defaults to json
		{"text", "", true},
		{"logfmt", "", true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := logging.ParseFormat(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNewLoggerHandlerType(t *testing.T) {
	for _, tc := range []struct {
		format   logging.Format
		wantJSON bool
	}{
		{logging.FormatJSON, true},
		{logging.FormatPlain, false},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			logger := logging.NewLogger(logging.Config{Level: logging.LevelInfo, Format: tc.format})
			require.NotNil(t, logger)
			if tc.wantJSON {
				require.IsType(t, (*slog.JSONHandler)(nil), logger.Handler())
			} else {
				require.IsType(t, (*slog.TextHandler)(nil), logger.Handler())
			}
		})
	}
}
