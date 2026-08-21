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
	var slogLevel slog.Level
	switch cfg.Level {
	case LevelDebug:
		slogLevel = slog.LevelDebug
	case LevelWarn:
		slogLevel = slog.LevelWarn
	case LevelError:
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: slogLevel}
	var handler slog.Handler
	if cfg.Format == FormatPlain {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
