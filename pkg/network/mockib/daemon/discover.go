// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"net"
)

// lookupHost is a seam over the default resolver: hosts-file fast paths
// complete without honoring ctx, so ctx-cancellation behavior can only be
// tested against a substituted resolver.
var lookupHost = net.DefaultResolver.LookupHost

// DiscoverPeerIPs resolves a headless Kubernetes service DNS name to pod IPs,
// excluding selfIP. Used when MOCK_IB_PEERS is unset and MOCK_IB_PING_SERVICE_HOST
// points at the chart's -ibping Service (clusterIP: None). Resolution honors
// ctx so daemon shutdown is not held up by a slow or unresponsive resolver.
func DiscoverPeerIPs(ctx context.Context, serviceHost, selfIP string) []string {
	if serviceHost == "" {
		return nil
	}
	addrs, err := lookupHost(ctx, serviceHost)
	if err != nil {
		return nil
	}
	var out []string
	for _, addr := range addrs {
		if addr == selfIP {
			continue
		}
		if net.ParseIP(addr) == nil {
			continue
		}
		out = append(out, addr)
	}
	return out
}
