// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/stretchr/testify/require"
)

func TestServer_sendRegister(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 1, NodeName: "node-a", Output: dir,
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	tcpPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	srv, err := NewServer(Config{IBRoot: dir, TCPPort: tcpPort, Fabric: true}, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.acceptFabric(ctx, ln)

	body := protocol.RegisterBody{
		PodIP:    "10.0.0.2",
		NodeName: "node-b",
		Ports: []protocol.PortAdvert{
			{PortGUID: "a088:c203:00ab:2001", LID: 0x0300, CAName: "mlx5_0", Port: 1},
		},
	}
	require.NoError(t, srv.sendRegister("127.0.0.1", body))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if peer, ok := srv.registry.Lookup("a088:c203:00ab:2001"); ok && peer.PodIP == "10.0.0.2" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Fail(t, "peer not registered")
}
