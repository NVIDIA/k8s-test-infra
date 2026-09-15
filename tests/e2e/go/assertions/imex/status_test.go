//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseStatus(t *testing.T) {
	t.Parallel()

	status, err := ParseStatus(`{
		"status": "UP",
		"nodes": {
			"10.0.0.1": {"status": "READY", "version": "NO_GPU"},
			"10.0.0.2": {"status": "READY", "version": "NO_GPU"}
		}
	}`)

	require.NoError(t, err)
	require.Equal(t, "UP", status.State)
	require.Equal(t, 2, status.ReadyNodes())
	require.Equal(t, 2, status.NoGPUNodes())
}

func TestParseStatusRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := ParseStatus(`not json`)

	require.ErrorContains(t, err, "decode IMEX status")
}

func TestParseStatusRejectsMissingDomainState(t *testing.T) {
	t.Parallel()

	_, err := ParseStatus(`{"nodes": {}}`)

	require.ErrorContains(t, err, "missing status")
}
