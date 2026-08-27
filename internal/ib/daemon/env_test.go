// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvOr(t *testing.T) {
	const key = "MOCK_IB_TEST_ENV_OR"
	t.Setenv(key, "")
	require.Equal(t, "def", envOr(key, "def"), "unset")
	t.Setenv(key, "val")
	require.Equal(t, "val", envOr(key, "def"), "set")
}

func TestParsePeerList(t *testing.T) {
	require.Nil(t, parsePeerList(""), "empty")
	got := parsePeerList(" 10.0.0.1 , , 10.0.0.2 ")
	want := []string{"10.0.0.1", "10.0.0.2"}
	require.Equal(t, want, got)
}
