// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import "testing"

func TestDiscoverPeerIPs_FiltersSelf(t *testing.T) {
	// Unit test uses empty host; integration uses real DNS in cluster.
	got := DiscoverPeerIPs("", "10.0.0.1")
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}
