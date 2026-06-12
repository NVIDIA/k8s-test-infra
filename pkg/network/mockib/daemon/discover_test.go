// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoverPeerIPs_FiltersSelf(t *testing.T) {
	// Unit test uses empty host; integration uses real DNS in cluster.
	got := DiscoverPeerIPs(context.Background(), "", "10.0.0.1")
	require.Empty(t, got)
}

// TestDiscoverPeerIPs_CancelledCtxAborts pins that DNS resolution honors the
// caller's ctx: a canceled ctx (daemon shutdown mid-register-loop) must abort
// the lookup instead of blocking on the resolver. "localhost" resolves from
// the hosts file everywhere, so a non-ctx-aware lookup would return addresses
// here and fail the test.
func TestDiscoverPeerIPs_CancelledCtxAborts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := DiscoverPeerIPs(ctx, "localhost", "")
	require.Empty(t, got, "canceled ctx must abort DNS resolution")
}
