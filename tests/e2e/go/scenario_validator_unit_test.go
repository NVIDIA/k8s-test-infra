//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import "testing"

func TestValidatorGFDRequiredLabels(t *testing.T) {
	want := []string{
		"nvidia.com/gpu.product",
		"nvidia.com/gpu.memory",
		"nvidia.com/gpu.compute.major",
	}

	got := validatorGFDRequiredLabels()
	if len(got) != len(want) {
		t.Fatalf("expected GFD labels %#v, got %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected GFD labels %#v, got %#v", want, got)
		}
	}
}
