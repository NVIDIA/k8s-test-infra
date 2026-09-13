// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package logging provides a shared logger constructor for all Mokka binaries.
package logging

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

// NewLogger returns a zap.Logger writing to stdout and installs it as the
// process-wide global logger (zap.L() / zap.S()).
func NewLogger(cfg Config) *zap.Logger {
	var lvl zapcore.Level

	switch cfg.Level {
	case LevelDebug:
		lvl = zapcore.DebugLevel
	case LevelWarn:
		lvl = zapcore.WarnLevel
	case LevelError:
		lvl = zapcore.ErrorLevel
	default:
		lvl = zapcore.InfoLevel
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "time"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var enc zapcore.Encoder
	if cfg.Format == FormatPlain {
		enc = zapcore.NewConsoleEncoder(encCfg)
	} else {
		enc = zapcore.NewJSONEncoder(encCfg)
	}

	core := zapcore.NewCore(enc, zapcore.Lock(os.Stdout), lvl)
	l := zap.New(core)

	zap.ReplaceGlobals(l)

	return l
}
