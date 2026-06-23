// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRendezvousBarrierTwoRanks(t *testing.T) {
	// Rank 0 binds an ephemeral port; rank 1 dials it. Distinct selfAddrs
	// make this an inter-node roster.
	ln, addr := listenEphemeral(t)
	results := make([]*RendezvousResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := rendezvousServe(ctx, ln, 0, 2, "10.0.0.1")
		if err != nil {
			t.Errorf("rank0: %v", err)
			return
		}
		results[0] = r
	}()
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := rendezvousDial(ctx, addr, 1, 2, "10.0.0.2")
		if err != nil {
			t.Errorf("rank1: %v", err)
			return
		}
		results[1] = r
	}()
	wg.Wait()

	for i, r := range results {
		require.NotNilf(t, r, "rank %d nil result", i)
		require.Equalf(t, 2, r.WorldSize, "rank %d worldsize", i)
		require.Lenf(t, r.Peers, 2, "rank %d peers", i)
		require.Truef(t, r.InterNode, "rank %d expected inter-node (distinct addrs)", i)
	}
}

func TestInterNodeSingleAddr(t *testing.T) {
	peers := []Peer{{Rank: 0, Addr: "10.0.0.1"}, {Rank: 1, Addr: "10.0.0.1"}}
	require.False(t, computeInterNode(peers), "same addr should be intra-node")
	peers[1].Addr = "10.0.0.2"
	require.True(t, computeInterNode(peers), "distinct addrs should be inter-node")
}

func TestRendezvousDialRetryBeforeBind(t *testing.T) {
	// Discover a free port by binding then immediately releasing it, so the
	// dialer can target a known address before rank 0 actually serves.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := probe.Addr().String()
	require.NoError(t, probe.Close())

	results := make([]*RendezvousResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	// Rank 1 starts dialing before rank 0 binds; it must retry until serve.
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := rendezvousDial(ctx, addr, 1, 2, "10.0.0.2")
		if err != nil {
			t.Errorf("rank1: %v", err)
			return
		}
		results[1] = r
	}()

	go func() {
		defer wg.Done()
		time.Sleep(150 * time.Millisecond)
		ln, err := Listen(addr)
		if err != nil {
			t.Errorf("rank0 listen: %v", err)
			return
		}
		defer func() { _ = ln.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := rendezvousServe(ctx, ln, 0, 2, "10.0.0.1")
		if err != nil {
			t.Errorf("rank0: %v", err)
			return
		}
		results[0] = r
	}()
	wg.Wait()

	for i, r := range results {
		require.NotNilf(t, r, "rank %d nil result", i)
		require.Equalf(t, 2, r.WorldSize, "rank %d worldsize", i)
		require.Lenf(t, r.Peers, 2, "rank %d peers", i)
	}
}

func TestRendezvousRank0BindsLocalPort(t *testing.T) {
	// Exercises the top-level Rendezvous: rank 0 must bind the local wildcard
	// for rdzvAddr's port (rdzvAddr is a dial-only Service name in k8s), while
	// rank 1 dials the loopback host:port. Regression test for rank 0 trying
	// to bind() the remote rendezvous address.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := probe.Addr().String()
	require.NoError(t, probe.Close())

	results := make([]*RendezvousResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := Rendezvous(ctx, 0, 2, addr, "10.0.0.1")
		if err != nil {
			t.Errorf("rank0: %v", err)
			return
		}
		results[0] = r
	}()
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := Rendezvous(ctx, 1, 2, addr, "10.0.0.2")
		if err != nil {
			t.Errorf("rank1: %v", err)
			return
		}
		results[1] = r
	}()
	wg.Wait()

	for i, r := range results {
		require.NotNilf(t, r, "rank %d nil result", i)
		require.Equalf(t, 2, r.WorldSize, "rank %d worldsize", i)
		require.Lenf(t, r.Peers, 2, "rank %d peers", i)
		require.Truef(t, r.InterNode, "rank %d expected inter-node (distinct addrs)", i)
	}
}

func TestReadJSONFramingBounds(t *testing.T) {
	frame := func(n uint32) []byte {
		var buf bytes.Buffer
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], n)
		buf.Write(hdr[:])
		return buf.Bytes()
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "zero length", data: frame(0)},
		{name: "oversized", data: frame(maxFrameSize + 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var p Peer
			err := readJSON(bytes.NewReader(tc.data), &p)
			require.Error(t, err)
			require.Contains(t, err.Error(), "bad frame length")
		})
	}
}

// helper using the package's own listener constructor (defined in rendezvous.go)
func listenEphemeral(t *testing.T) (Listener, string) {
	t.Helper()
	ln, err := Listen("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	return ln, fmt.Sprintf("127.0.0.1:%d", ln.Port())
}
