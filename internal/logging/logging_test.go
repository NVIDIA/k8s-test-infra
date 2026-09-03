// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package logging_test

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
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

func TestNewLoggerOutputFormat(t *testing.T) {
	for _, tc := range []struct {
		format   logging.Format
		wantJSON bool
	}{
		{logging.FormatJSON, true},
		{logging.FormatPlain, false},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			r, w, err := os.Pipe()
			require.NoError(t, err)

			origStdout := os.Stdout
			os.Stdout = w
			logger := logging.NewLogger(logging.Config{Level: logging.LevelInfo, Format: tc.format})
			logger.Info("hello")
			os.Stdout = origStdout
			require.NoError(t, w.Close())

			out, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Equal(t, tc.wantJSON, json.Valid(out))
		})
	}
}

func TestNewLoggerLevel(t *testing.T) {
	logger := logging.NewLogger(logging.Config{Level: logging.LevelWarn, Format: logging.FormatJSON})
	require.False(t, logger.Core().Enabled(zapcore.InfoLevel))
	require.True(t, logger.Core().Enabled(zapcore.WarnLevel))
	require.True(t, logger.Core().Enabled(zapcore.ErrorLevel))
}
