// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"fmt"
	"os"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestMain silences the steps' fail-open warnings so a passing run is quiet.
func TestMain(m *testing.M) {
	zap.ReplaceGlobals(zap.NewNop())
	os.Exit(m.Run())
}

// warning is one captured record, flattened to what the assertions need.
type warning struct {
	Message string
	Attrs   map[string]string
}

// recorder observes everything the global logger emits for one test.
type recorder struct {
	logs *observer.ObservedLogs
}

// captureWarnings routes the global logger into a recorder for one test.
// It does not combine with t.Parallel: zap.ReplaceGlobals is process-wide.
func captureWarnings(t *testing.T) *recorder {
	t.Helper()
	core, logs := observer.New(zapcore.WarnLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))
	return &recorder{logs: logs}
}

func (r *recorder) captured() []warning {
	entries := r.logs.All()
	out := make([]warning, len(entries))
	for i, e := range entries {
		attrs := make(map[string]string, len(e.Context))
		for k, v := range e.ContextMap() {
			attrs[k] = fmt.Sprint(v)
		}
		out[i] = warning{Message: e.Message, Attrs: attrs}
	}
	return out
}
