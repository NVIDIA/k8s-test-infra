// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package logging provides a shared logger constructor for all Mokka binaries.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Level is the log verbosity.
type Level string

// Supported log levels.
const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// ParseLevel maps a CLI/env string to a Level, defaulting to LevelInfo on empty input.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(s) {
	case "", "info":
		return LevelInfo, nil
	case "debug":
		return LevelDebug, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return "", fmt.Errorf("unknown log level %q; expected one of debug, info, warn, error", s)
	}
}

// Format is the log output encoding.
type Format string

// Supported log formats.
const (
	FormatJSON  Format = "json"
	FormatPlain Format = "plain"
)

// ParseFormat maps a CLI/env string to a Format, defaulting to FormatJSON on empty input.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "", "json":
		return FormatJSON, nil
	case "plain":
		return FormatPlain, nil
	default:
		return "", fmt.Errorf("unknown log format %q; expected json or plain", s)
	}
}

// Config carries the two knobs that shape log output.
type Config struct {
	Level  Level
	Format Format
}

// NewLogger returns a slog.Logger writing to stdout.
func NewLogger(cfg Config) *slog.Logger {
	var lvl slog.Level

	switch cfg.Level {
	case LevelDebug:
		lvl = slog.LevelDebug
	case LevelWarn:
		lvl = slog.LevelWarn
	case LevelError:
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler

	if cfg.Format == FormatPlain {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	l := slog.New(h)

	slog.SetDefault(l)

	return l
}
