// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvOr(t *testing.T) {
	const key = "MOCK_IB_TEST_ENV_OR"
	t.Setenv(key, "")
	require.Equal(t, "def", EnvOr(key, "def"), "unset")
	t.Setenv(key, "val")
	require.Equal(t, "val", EnvOr(key, "def"), "set")
}

func TestEnvIntOr(t *testing.T) {
	const key = "MOCK_IB_TEST_ENV_INT"
	t.Setenv(key, "")
	require.Equal(t, 42, EnvIntOr(key, 42), "unset")
	t.Setenv(key, "99")
	require.Equal(t, 99, EnvIntOr(key, 42), "set")
	t.Setenv(key, "nope")
	require.Equal(t, 42, EnvIntOr(key, 42), "invalid")
}

func TestEnvBoolOr(t *testing.T) {
	const key = "MOCK_IB_TEST_ENV_BOOL"
	t.Setenv(key, "")
	require.True(t, EnvBoolOr(key, true), "unset default true: want true")
	require.False(t, EnvBoolOr(key, false), "unset default false: want false")
	for _, v := range []string{"1", "true", "TRUE"} {
		t.Setenv(key, v)
		require.True(t, EnvBoolOr(key, false), "%q: want true", v)
	}
	t.Setenv(key, "0")
	require.False(t, EnvBoolOr(key, true), "0: want false")
}

func TestParsePeerList(t *testing.T) {
	require.Nil(t, ParsePeerList(""), "empty")
	got := ParsePeerList(" 10.0.0.1 , , 10.0.0.2 ")
	want := []string{"10.0.0.1", "10.0.0.2"}
	require.Equal(t, want, got)
}

func TestEnvOr_readsProcessEnv(t *testing.T) {
	// Guard against tests that forget t.Setenv cleanup.
	if os.Getenv("PATH") == "" {
		t.Skip("PATH unset in test environment")
	}
	require.NotEmpty(t, EnvOr("PATH", ""), "expected non-empty PATH")
}
