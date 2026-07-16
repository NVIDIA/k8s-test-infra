//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"bytes"
	"testing"
)

func TestLineLimitWriterWritesOnlyFirstLines(t *testing.T) {
	input := []byte("one\ntwo\nthree\n")
	var out bytes.Buffer
	w := &lineLimitWriter{dst: &out, remaining: 2}

	n, err := w.Write(input)

	if err != nil {
		t.Fatalf("lineLimitWriter write: %v", err)
	}
	if n != len(input) {
		t.Fatalf("expected writer to accept %d bytes, got %d", len(input), n)
	}
	if got, want := out.String(), "one\ntwo\n"; got != want {
		t.Fatalf("expected truncated output %q, got %q", want, got)
	}

	if _, err := w.Write([]byte("four\n")); err != nil {
		t.Fatalf("lineLimitWriter second write: %v", err)
	}
	if got, want := out.String(), "one\ntwo\n"; got != want {
		t.Fatalf("expected output to stay truncated at %q, got %q", want, got)
	}
}
