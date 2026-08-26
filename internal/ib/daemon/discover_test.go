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
// the lookup instead of blocking on the resolver. The resolver is faked via
// the lookupHost seam because hosts-file fast paths resolve names without
// ever reaching a cancellation point (resolving "localhost" here passed on
// darwin and failed on linux runners), while real in-cluster resolution goes
// through DNS where ctx is the only bound on a hang.
func TestDiscoverPeerIPs_CancelledCtxAborts(t *testing.T) {
	orig := lookupHost
	t.Cleanup(func() { lookupHost = orig })
	lookupHost = func(ctx context.Context, _ string) ([]string, error) {
		<-ctx.Done() // a hung resolver: only ctx cancellation releases it
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := DiscoverPeerIPs(ctx, "mock-ib-ibping.default.svc", "")
	require.Empty(t, got, "canceled ctx must abort DNS resolution")
}
