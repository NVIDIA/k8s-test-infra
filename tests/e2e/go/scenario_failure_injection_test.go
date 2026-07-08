//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import "testing"

func TestMaxIntegerLine(t *testing.T) {
	got := maxIntegerLine("0\n12\nnot-a-number\n7\n")
	if got != 12 {
		t.Fatalf("expected max integer 12, got %d", got)
	}
}

func TestHasFailureMarker(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{name: "na marker", out: "[N/A]", want: true},
		{name: "unknown error", out: "[Unknown Error]", want: true},
		{name: "gpu lost", out: "GPU is lost", want: true},
		{name: "err text", out: "ERR!", want: true},
		{name: "healthy value", out: "42", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasFailureMarker(c.out); got != c.want {
				t.Fatalf("expected hasFailureMarker(%q)=%v, got %v", c.out, c.want, got)
			}
		})
	}
}
