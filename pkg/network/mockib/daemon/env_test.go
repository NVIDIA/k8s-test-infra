// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"testing"
)

func TestEnvOr(t *testing.T) {
	const key = "MOCK_IB_TEST_ENV_OR"
	t.Setenv(key, "")
	if got := EnvOr(key, "def"); got != "def" {
		t.Fatalf("unset: got %q want def", got)
	}
	t.Setenv(key, "val")
	if got := EnvOr(key, "def"); got != "val" {
		t.Fatalf("set: got %q want val", got)
	}
}

func TestEnvIntOr(t *testing.T) {
	const key = "MOCK_IB_TEST_ENV_INT"
	t.Setenv(key, "")
	if got := EnvIntOr(key, 42); got != 42 {
		t.Fatalf("unset: got %d want 42", got)
	}
	t.Setenv(key, "99")
	if got := EnvIntOr(key, 42); got != 99 {
		t.Fatalf("set: got %d want 99", got)
	}
	t.Setenv(key, "nope")
	if got := EnvIntOr(key, 42); got != 42 {
		t.Fatalf("invalid: got %d want 42", got)
	}
}

func TestEnvBoolOr(t *testing.T) {
	const key = "MOCK_IB_TEST_ENV_BOOL"
	t.Setenv(key, "")
	if got := EnvBoolOr(key, true); !got {
		t.Fatal("unset default true: want true")
	}
	if got := EnvBoolOr(key, false); got {
		t.Fatal("unset default false: want false")
	}
	for _, v := range []string{"1", "true", "TRUE"} {
		t.Setenv(key, v)
		if !EnvBoolOr(key, false) {
			t.Fatalf("%q: want true", v)
		}
	}
	t.Setenv(key, "0")
	if EnvBoolOr(key, true) {
		t.Fatal("0: want false")
	}
}

func TestParsePeerList(t *testing.T) {
	if got := ParsePeerList(""); got != nil {
		t.Fatalf("empty: got %v want nil", got)
	}
	got := ParsePeerList(" 10.0.0.1 , , 10.0.0.2 ")
	want := []string{"10.0.0.1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestEnvOr_readsProcessEnv(t *testing.T) {
	// Guard against tests that forget t.Setenv cleanup.
	if os.Getenv("PATH") == "" {
		t.Skip("PATH unset in test environment")
	}
	if EnvOr("PATH", "") == "" {
		t.Fatal("expected non-empty PATH")
	}
}
