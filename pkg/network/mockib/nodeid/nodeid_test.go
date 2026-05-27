// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nodeid

import "testing"

func TestNodeID_DeterministicAndDistinct(t *testing.T) {
	if NodeID("") != 0 {
		t.Fatalf("empty name must yield 0")
	}
	a1 := NodeID("worker-a")
	a2 := NodeID("worker-a")
	b := NodeID("worker-b")
	if a1 != a2 {
		t.Fatalf("not deterministic: %v vs %v", a1, a2)
	}
	if a1 == b {
		t.Fatalf("collision on distinct inputs: %v", a1)
	}
}
