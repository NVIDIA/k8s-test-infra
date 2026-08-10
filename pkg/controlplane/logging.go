// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a JSON-formatted slog.Logger writing to stdout. An
// unrecognized level is a startup error rather than a silent fallback so a
// typo in a Helm value fails loudly instead of running at the wrong verbosity.
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
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(handler), nil
}
