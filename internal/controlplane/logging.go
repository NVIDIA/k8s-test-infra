// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a slog.Logger writing to stdout. Unrecognized level or
// format values are startup errors so a typo in a Helm value fails loudly
// instead of running at the wrong verbosity or format.
func NewLogger(cfg Config) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "", "info":
		level = slog.LevelInfo
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q; expected one of debug, info, warn, error", cfg.LogLevel)
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.LogFormat) {
	case "", "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "plain":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q; expected json or plain", cfg.LogFormat)
	}
	return slog.New(handler), nil
}
