// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
)

// TestMain silences the steps' fail-open warnings so a passing run is quiet.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// warning is one captured record, flattened to what the assertions need.
type warning struct {
	Message string
	Attrs   map[string]string
}

// captureWarnings routes the default logger into a recorder for one test.
// It does not combine with t.Parallel: slog.SetDefault is process-wide.
func captureWarnings(t *testing.T) *recorder {
	t.Helper()
	original := slog.Default()
	rec := &recorder{}
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(original) })
	return rec
}

type recorder struct {
	mu       sync.Mutex
	warnings []warning
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, record slog.Record) error {
	captured := warning{Message: record.Message, Attrs: make(map[string]string)}
	record.Attrs(func(attr slog.Attr) bool {
		captured.Attrs[attr.Key] = attr.Value.String()
		return true
	})

	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, captured)
	return nil
}

func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler      { return r }

func (r *recorder) captured() []warning {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]warning(nil), r.warnings...)
}
