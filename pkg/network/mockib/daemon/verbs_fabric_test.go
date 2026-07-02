// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/stretchr/testify/require"
)

// newTestServer builds a Server with just the fields the verbs data path needs,
// avoiding the sysfs scan in NewServer.
func newTestServer(podIP string, reg *registry.Registry) *Server {
	return &Server{
		cfg:      Config{TCPPort: 18515},
		log:      log.New(io.Discard, "", 0),
		registry: reg,
		podIP:    podIP,
		verbs:    newVerbsRouter(),
	}
}

func TestResolveVerbsRoute_GIDFirst(t *testing.T) {
	reg := registry.New()
	reg.Register("a088:c203:00ab:0001", registry.Peer{PodIP: "10.0.0.5", LID: 0x0102})

	// GID whose lower 64 bits are the port GUID a088c20300ab0001.
	route, ok := resolveVerbsRoute(reg, protocol.VerbsQPConnectReq{
		LocalQPN: 0x100,
		DestQPN:  0x200,
		DGID:     "fe80000000000000a088c20300ab0001",
		DLID:     0x0102,
	})
	require.True(t, ok, "GID should resolve")
	require.Equal(t, "10.0.0.5", route.peerPodIP)
	require.Equal(t, uint32(0x200), route.dstQPN)
	require.Contains(t, route.key, "gid:")
}

func TestResolveVerbsRoute_LIDFallback(t *testing.T) {
	reg := registry.New()
	reg.Register("a088:c203:00ab:0001", registry.Peer{PodIP: "10.0.0.6", LID: 0x0103})

	route, ok := resolveVerbsRoute(reg, protocol.VerbsQPConnectReq{
		LocalQPN: 0x100,
		DestQPN:  0x201,
		DLID:     0x0103,
	})
	require.True(t, ok, "LID should resolve")
	require.Equal(t, "10.0.0.6", route.peerPodIP)
	require.Equal(t, uint32(0x201), route.dstQPN)
	require.Contains(t, route.key, "lid:")
}

func TestResolveVerbsRoute_NoMatch(t *testing.T) {
	reg := registry.New()
	reg.Register("a088:c203:00ab:0001", registry.Peer{PodIP: "10.0.0.6", LID: 0x0103})

	_, ok := resolveVerbsRoute(reg, protocol.VerbsQPConnectReq{
		LocalQPN: 0x100,
		DestQPN:  0x202,
		DLID:     0xbeef, // unregistered LID, no GID
	})
	require.False(t, ok, "unregistered destination must not resolve")
}

func TestValidateVerbsOp(t *testing.T) {
	require.NoError(t, validateVerbsOp(protocol.VerbsOp{Opcode: protocol.VerbsOpWrite, Length: 4, Data: []byte{1, 2, 3, 4}}))
	require.NoError(t, validateVerbsOp(protocol.VerbsOp{Opcode: protocol.VerbsOpReadReq, Length: 16}))

	require.Error(t, validateVerbsOp(protocol.VerbsOp{Opcode: "bogus", Length: 0}), "unknown opcode")
	require.Error(t, validateVerbsOp(protocol.VerbsOp{Opcode: protocol.VerbsOpWrite, Offset: 2, Length: 4, Data: []byte{1, 2, 3, 4}}), "offset+data over length")
	require.Error(t, validateVerbsOp(protocol.VerbsOp{Opcode: protocol.VerbsOpWrite, Length: uint32(protocol.VerbsSegMax + 1), Data: make([]byte, protocol.VerbsSegMax+1)}), "oversize chunk")
}

func TestVerbsRouter_DeliverInboundUnknownQPN(t *testing.T) {
	r := newVerbsRouter()
	err := r.deliverInbound(protocol.VerbsOp{DstQPN: 0x999, Opcode: protocol.VerbsOpWrite})
	require.Error(t, err, "delivery to an unattached QPN must error, not panic")
}

func TestVerbsRouter_AttachDeliverAndCleanup(t *testing.T) {
	r := newVerbsRouter()
	a := &verbsAttach{qpn: 0x111, inbound: make(chan protocol.VerbsOp, 4)}
	r.registerAttach(a)
	r.setRoute(0x111, verbsRoute{peerPodIP: "10.0.0.9", dstQPN: 0x222})

	op := protocol.VerbsOp{DstQPN: 0x111, Opcode: protocol.VerbsOpWrite, Length: 3, Data: []byte{9, 8, 7}}
	require.NoError(t, r.deliverInbound(op))
	select {
	case got := <-a.inbound:
		require.Equal(t, op, got)
	case <-time.After(time.Second):
		require.Fail(t, "inbound op not delivered")
	}

	// Disconnect cleanup: unregistering the attach purges BOTH the attach entry
	// and the QP route (stale route must not mis-deliver to a dead peer).
	r.unregisterAttach(a)
	_, ok := r.lookupRoute(0x111)
	require.False(t, ok, "route should be purged on attach disconnect")
	require.Error(t, r.deliverInbound(op), "delivery after detach must error")
}

func TestRouteVerbsEgress_LocalDelivery(t *testing.T) {
	s := newTestServer("10.0.0.1", registry.New())
	a := &verbsAttach{qpn: 0x222, inbound: make(chan protocol.VerbsOp, 4)}
	s.verbs.registerAttach(a)
	// Same-pod / loopback route: empty peer IP -> local delivery.
	s.verbs.setRoute(0x111, verbsRoute{peerPodIP: "", dstQPN: 0x222})

	require.NoError(t, s.routeVerbsEgress(protocol.VerbsOp{
		SrcQPN: 0x111, Opcode: protocol.VerbsOpWrite, Length: 2, Data: []byte{1, 2},
	}))
	select {
	case got := <-a.inbound:
		require.Equal(t, uint32(0x222), got.DstQPN, "egress must stamp the resolved DstQPN")
		require.Equal(t, []byte{1, 2}, got.Data)
	case <-time.After(time.Second):
		require.Fail(t, "egress op not delivered locally")
	}
}

func TestRouteVerbsEgress_NoRouteDropped(t *testing.T) {
	s := newTestServer("10.0.0.1", registry.New())
	// No route registered for SrcQPN: must drop (fire-and-forget), not error.
	require.NoError(t, s.routeVerbsEgress(protocol.VerbsOp{
		SrcQPN: 0xabc, Opcode: protocol.VerbsOpWrite, Length: 1, Data: []byte{1},
	}))
}

func TestHandleVerbsQPConnect_StoresRoute(t *testing.T) {
	reg := registry.New()
	reg.Register("a088:c203:00ab:0001", registry.Peer{PodIP: "10.0.0.7", LID: 0x0104})
	s := newTestServer("10.0.0.1", reg)

	require.NoError(t, s.handleVerbsQPConnect(protocol.VerbsQPConnectReq{
		LocalQPN: 0x300, DestQPN: 0x400, DLID: 0x0104,
	}))
	route, ok := s.verbs.lookupRoute(0x300)
	require.True(t, ok)
	require.Equal(t, "10.0.0.7", route.peerPodIP)
	require.Equal(t, uint32(0x400), route.dstQPN)

	// Destroy purges the route.
	require.NoError(t, s.handleVerbsQPDestroy(protocol.VerbsQPDestroyReq{QPN: 0x300}))
	_, ok = s.verbs.lookupRoute(0x300)
	require.False(t, ok, "route should be gone after qp_destroy")
}
