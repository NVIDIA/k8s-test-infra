//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLineLimitWriterWritesOnlyFirstLines(t *testing.T) {
	input := []byte("one\ntwo\nthree\n")
	var out bytes.Buffer
	w := &lineLimitWriter{dst: &out, remaining: 2}

	n, err := w.Write(input)
	require.NoError(t, err, "lineLimitWriter write")
	require.Equal(t, len(input), n, "expected writer to accept %d bytes", len(input))
	require.Equal(t, "one\ntwo\n", out.String(), "expected truncated output")

	_, err = w.Write([]byte("four\n"))
	require.NoError(t, err, "lineLimitWriter second write")
	require.Equal(t, "one\ntwo\n", out.String(), "expected output to stay truncated")
}
