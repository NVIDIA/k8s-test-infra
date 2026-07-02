// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

// RDMA verbs data-path routing (issue #374).
//
// The in-process libibmockrdma.so shim relays each RDMA operation to its local
// mock-ib daemon as a verbs_op frame. This file is the daemon side:
//
//   - attach table: dst QPN -> the long-lived shim connection that owns it, so
//     a peer daemon's inbound op is delivered to the right process.
//   - route table: local src QPN -> the resolved peer (pod IP + dst QPN),
//     learned from verbs_qp_connect at modify_qp(->RTR).
//   - egress: a verbs_op from a local shim is routed by its SrcQPN to the peer
//     daemon over the existing TCP fabric (or delivered locally for same-pod /
//     loopback), reusing the dial discipline of pingPeer.
//   - ingress: a verbs_op arriving over the fabric is delivered to the owning
//     local shim by DstQPN.
//
// Egress is fire-and-forget: the shim never blocks on a per-WR reply (the send
// completion is generated locally once the op is handed to the daemon). Inbound
// delivery uses a bounded per-QP channel so a slow shim applies natural
// backpressure instead of letting the daemon grow without limit.
//
// NOTE: the reported bandwidth is a functional artifact of this JSON/TCP relay,
// not an InfiniBand measurement.

const (
	// verbsInboundDepth bounds the per-QP inbound queue. A full queue blocks the
	// fabric reader (backpressure) rather than buffering without limit.
	verbsInboundDepth = 64
	// verbsPeerDialTimeout matches pingPeer's dial timeout.
	verbsPeerDialTimeout = 5 * time.Second
	// verbsPeerWriteTimeout caps a single egress/inbound write so a stalled peer
	// or shim cannot pin a goroutine for the pod's life.
	verbsPeerWriteTimeout = 30 * time.Second
)

// verbsRoute is the resolved remote endpoint for a local QP.
type verbsRoute struct {
	peerPodIP string
	dstQPN    uint32
	key       string // "gid:..." or "lid:0x...." for diagnostics
}

// verbsAttach is one shim's inbound stream for a single QPN.
type verbsAttach struct {
	qpn     uint32
	inbound chan protocol.VerbsOp
}

// verbsRouter holds the data-path attach/route tables and a small egress TCP
// connection cache. All maps are guarded by mu; the peer-conn cache has its own
// lock so a slow dial does not block attach/route lookups.
type verbsRouter struct {
	mu     sync.Mutex
	attach map[uint32]*verbsAttach
	routes map[uint32]verbsRoute

	peerMu sync.Mutex
	peers  map[string]net.Conn
}

func newVerbsRouter() *verbsRouter {
	return &verbsRouter{
		attach: make(map[uint32]*verbsAttach),
		routes: make(map[uint32]verbsRoute),
		peers:  make(map[string]net.Conn),
	}
}

func (r *verbsRouter) registerAttach(a *verbsAttach) {
	r.mu.Lock()
	r.attach[a.qpn] = a
	r.mu.Unlock()
}

// unregisterAttach drops a's attach entry (only if it is still the current
// owner) and purges the QP's route. Tying route cleanup to the attach
// connection means a shim/process that dies without verbs_qp_destroy (e.g. a
// killed kubectl exec) cannot leave a stale route mis-delivering to a dead
// peer — mirroring the bounded-map discipline in verbs.go.
func (r *verbsRouter) unregisterAttach(a *verbsAttach) {
	r.mu.Lock()
	if cur, ok := r.attach[a.qpn]; ok && cur == a {
		delete(r.attach, a.qpn)
		delete(r.routes, a.qpn)
	}
	r.mu.Unlock()
}

func (r *verbsRouter) setRoute(localQPN uint32, route verbsRoute) {
	r.mu.Lock()
	r.routes[localQPN] = route
	r.mu.Unlock()
}

func (r *verbsRouter) lookupRoute(localQPN uint32) (verbsRoute, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.routes[localQPN]
	return rt, ok
}

func (r *verbsRouter) destroyQP(qpn uint32) {
	r.mu.Lock()
	delete(r.routes, qpn)
	delete(r.attach, qpn)
	r.mu.Unlock()
}

// deliverInbound hands op to the shim that owns op.DstQPN. It blocks (up to
// verbsPeerWriteTimeout) when the per-QP queue is full so a slow applier exerts
// backpressure. An unknown DstQPN is an error (the caller logs / returns a
// rem_inv_req_err op), never a panic.
func (r *verbsRouter) deliverInbound(op protocol.VerbsOp) error {
	r.mu.Lock()
	a, ok := r.attach[op.DstQPN]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("no attach for dst qpn 0x%x", op.DstQPN)
	}
	timer := time.NewTimer(verbsPeerWriteTimeout)
	defer timer.Stop()
	select {
	case a.inbound <- op:
		return nil
	case <-timer.C:
		return fmt.Errorf("inbound queue full for qpn 0x%x", op.DstQPN)
	}
}

// sendToPeer forwards op to peerIP's fabric listener, caching one TCP
// connection per peer. On a write error it drops the cached conn and retries
// once with a fresh dial (peers restart; a half-open conn must not wedge the
// data path), matching the resilience of the ping fabric.
func (r *verbsRouter) sendToPeer(peerIP string, port int, op protocol.VerbsOp) error {
	r.peerMu.Lock()
	defer r.peerMu.Unlock()
	c := r.peers[peerIP]
	if c == nil {
		nc, err := net.DialTimeout("tcp", net.JoinHostPort(peerIP, strconv.Itoa(port)), verbsPeerDialTimeout)
		if err != nil {
			return err
		}
		c = nc
		r.peers[peerIP] = c
	}
	_ = c.SetWriteDeadline(time.Now().Add(verbsPeerWriteTimeout))
	if err := protocol.WriteMessage(c, protocol.TypeVerbsOp, op); err == nil {
		return nil
	}
	_ = c.Close()
	delete(r.peers, peerIP)
	nc, err := net.DialTimeout("tcp", net.JoinHostPort(peerIP, strconv.Itoa(port)), verbsPeerDialTimeout)
	if err != nil {
		return err
	}
	r.peers[peerIP] = nc
	_ = nc.SetWriteDeadline(time.Now().Add(verbsPeerWriteTimeout))
	return protocol.WriteMessage(nc, protocol.TypeVerbsOp, op)
}

func (r *verbsRouter) closePeers() {
	r.peerMu.Lock()
	for ip, c := range r.peers {
		_ = c.Close()
		delete(r.peers, ip)
	}
	r.peerMu.Unlock()
}

// resolveVerbsRoute maps a verbs_qp_connect to a peer, GID/GUID first then LID.
// GIDs carry the port GUID in their lower 64 bits, which is the registry's
// higher-fidelity key; LIDs are only 16-bit and node-derived, so they can
// collide across releases and are a best-effort fallback (mirrors ibping).
func resolveVerbsRoute(reg *registry.Registry, req protocol.VerbsQPConnectReq) (verbsRoute, bool) {
	if req.DGID != "" {
		var gidBytes [16]byte
		gid.ParseInto(gidBytes[:], req.DGID)
		if guid := gid.PortGUIDFromBytes(gidBytes[:]); guid != "" {
			if peer, ok := reg.Lookup(guid); ok && peer.PodIP != "" {
				return verbsRoute{peerPodIP: peer.PodIP, dstQPN: req.DestQPN, key: "gid:" + req.DGID}, true
			}
		}
	}
	if req.DLID != 0 {
		if peer, _, ok := reg.LookupByLID(req.DLID); ok && peer.PodIP != "" {
			return verbsRoute{peerPodIP: peer.PodIP, dstQPN: req.DestQPN, key: fmt.Sprintf("lid:0x%04x", req.DLID)}, true
		}
	}
	return verbsRoute{}, false
}

// validateVerbsOp rejects structurally malformed ops before any routing so a
// hostile/garbled frame can never index out of bounds. MR-level bounds
// enforcement (rkey + remote_addr against the registered buffer) happens in the
// responder shim, which is the only party that holds the registered memory; the
// daemon only guarantees self-consistency of the chunk envelope here.
func validateVerbsOp(op protocol.VerbsOp) error {
	switch op.Opcode {
	case protocol.VerbsOpWrite, protocol.VerbsOpReadReq, protocol.VerbsOpReadResp, protocol.VerbsOpSend:
	default:
		return fmt.Errorf("unknown verbs opcode %q", op.Opcode)
	}
	if uint64(op.Offset)+uint64(len(op.Data)) > uint64(op.Length) {
		return fmt.Errorf("chunk out of bounds: offset=%d data=%d length=%d", op.Offset, len(op.Data), op.Length)
	}
	if len(op.Data) > protocol.VerbsSegMax {
		return fmt.Errorf("chunk too large: %d bytes (max %d)", len(op.Data), protocol.VerbsSegMax)
	}
	return nil
}

// ---- Server wiring -------------------------------------------------------

// handleVerbsQPCreate records (logs) a software QP announcement. The QPN is
// assigned by the shim; the daemon allocates nothing here. Fire-and-forget.
func (s *Server) handleVerbsQPCreate(req protocol.VerbsQPCreateReq) error {
	if s.verbsDebug {
		s.log.Printf("mock-ib: verbs qp_create qpn=0x%x ca=%s port=%d", req.QPN, req.CAName, req.Port)
	}
	return nil
}

// handleVerbsQPConnect resolves and stores the peer route for a local QP.
// Fire-and-forget: an unresolved route is logged (the egress writes will drop)
// but never blocks the shim, which gates readiness on perftest's own OOB
// handshake.
func (s *Server) handleVerbsQPConnect(req protocol.VerbsQPConnectReq) error {
	route, ok := resolveVerbsRoute(s.registry, req)
	if !ok {
		s.log.Printf("mock-ib: verbs qp_connect local=0x%x dest=0x%x: no route (dlid=0x%04x dgid=%q registry size=%d)",
			req.LocalQPN, req.DestQPN, req.DLID, req.DGID, s.registry.Size())
		return nil
	}
	s.verbs.setRoute(req.LocalQPN, route)
	s.log.Printf("mock-ib: verbs qp_connect local=0x%x -> peer=%s dst=0x%x via %s",
		req.LocalQPN, route.peerPodIP, route.dstQPN, route.key)
	return nil
}

func (s *Server) handleVerbsQPDestroy(req protocol.VerbsQPDestroyReq) error {
	s.verbs.destroyQP(req.QPN)
	if s.verbsDebug {
		s.log.Printf("mock-ib: verbs qp_destroy qpn=0x%x", req.QPN)
	}
	return nil
}

// handleVerbsAttach takes over the connection for the lifetime of one QP's
// inbound stream: it registers the QPN, then pushes each inbound verbs_op back
// on this connection until the shim disconnects or the daemon shuts down. A
// tiny reader goroutine detects the shim closing the socket so a QP with no
// inbound traffic is still reaped promptly (and its route purged).
func (s *Server) handleVerbsAttach(ctx context.Context, c net.Conn, req protocol.VerbsAttachReq) error {
	a := &verbsAttach{qpn: req.QPN, inbound: make(chan protocol.VerbsOp, verbsInboundDepth)}
	s.verbs.registerAttach(a)
	defer s.verbs.unregisterAttach(a)
	s.log.Printf("mock-ib: verbs attach qpn=0x%x", req.QPN)

	// Clear the serveConn read deadline: an attach conn is long-lived and idle
	// (the shim only reads), so the generic unixConnIdleTimeout must not reap an
	// active QP. Liveness is observed via the EOF reader below instead.
	_ = c.SetReadDeadline(time.Time{})

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		buf := make([]byte, 64)
		for {
			// No further frames are expected from the shim on the attach conn;
			// this read exists only to observe EOF when the process exits.
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-closed:
			return nil
		case op := <-a.inbound:
			_ = c.SetWriteDeadline(time.Now().Add(verbsPeerWriteTimeout))
			if err := protocol.WriteMessage(c, protocol.TypeVerbsOp, op); err != nil {
				return err
			}
		}
	}
}

// routeVerbsEgress routes one verbs_op from a local shim by its SrcQPN to the
// resolved peer (or delivers locally for same-pod / loopback). Fire-and-forget.
func (s *Server) routeVerbsEgress(op protocol.VerbsOp) error {
	if err := validateVerbsOp(op); err != nil {
		s.log.Printf("mock-ib: verbs egress drop (src=0x%x op=%s): %v", op.SrcQPN, op.Opcode, err)
		return nil
	}
	route, ok := s.verbs.lookupRoute(op.SrcQPN)
	if !ok {
		s.log.Printf("mock-ib: verbs egress drop: no route for src qpn 0x%x (op=%s)", op.SrcQPN, op.Opcode)
		return nil
	}
	op.DstQPN = route.dstQPN
	if route.peerPodIP == "" || route.peerPodIP == s.podIP {
		if err := s.verbs.deliverInbound(op); err != nil {
			s.log.Printf("mock-ib: verbs local deliver (dst=0x%x op=%s): %v", op.DstQPN, op.Opcode, err)
		}
		return nil
	}
	if err := s.verbs.sendToPeer(route.peerPodIP, s.cfg.TCPPort, op); err != nil {
		s.log.Printf("mock-ib: verbs egress to %s (dst=0x%x op=%s): %v", route.peerPodIP, op.DstQPN, op.Opcode, err)
	}
	return nil
}

// handleFabricVerbsOp delivers an inbound verbs_op (arrived over the TCP
// fabric) to the local shim that owns op.DstQPN.
func (s *Server) handleFabricVerbsOp(op protocol.VerbsOp) error {
	if err := validateVerbsOp(op); err != nil {
		s.log.Printf("mock-ib: verbs fabric drop (dst=0x%x op=%s): %v", op.DstQPN, op.Opcode, err)
		return nil
	}
	if err := s.verbs.deliverInbound(op); err != nil {
		// Unknown/closed QPN or full queue: log and drop rather than panic the
		// fabric goroutine (it serves all cross-pod traffic for this peer).
		s.log.Printf("mock-ib: verbs fabric deliver (dst=0x%x op=%s): %v", op.DstQPN, op.Opcode, err)
	}
	return nil
}
