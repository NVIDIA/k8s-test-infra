// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

func (s *Server) startFabric(ctx context.Context) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", s.cfg.TCPPort))
	if err != nil {
		return nil, fmt.Errorf("listen tcp :%d: %w", s.cfg.TCPPort, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go s.registerWithPeersLoop(ctx)
	go s.acceptFabric(ctx, ln)
	return ln, nil
}

// registerWithPeersLoop retries REGISTER until ctx is canceled (MOCK_IB_PEERS).
func (s *Server) registerWithPeersLoop(ctx context.Context) {
	// Peers finish setup.sh at different times; avoid noisy refused logs at t=0.
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	for {
		s.registerWithPeers(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Server) acceptFabric(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Printf("mock-ib: fabric accept: %v", err)
			return
		}
		go s.serveFabricConn(ctx, conn)
	}
}

func (s *Server) serveFabricConn(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		if ctx.Err() != nil {
			return
		}
		var env protocol.Envelope
		if err := protocol.ReadEnvelope(c, &env); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return
			}
			s.log.Printf("mock-ib: fabric read: %v", err)
			return
		}
		if err := s.dispatchFabric(c, env); err != nil {
			s.log.Printf("mock-ib: fabric %s: %v", env.Type, err)
			return
		}
	}
}

func (s *Server) dispatchFabric(c net.Conn, env protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeRegister:
		var body protocol.RegisterBody
		if err := protocol.DecodeBody(env, &body); err != nil {
			return err
		}
		s.applyRegister(body)
		return nil
	case protocol.TypePing:
		var ping protocol.PingBody
		if err := protocol.DecodeBody(env, &ping); err != nil {
			return err
		}
		return s.handleFabricPing(c, ping)
	default:
		return fmt.Errorf("unknown fabric message type %q", env.Type)
	}
}

func (s *Server) applyRegister(body protocol.RegisterBody) {
	for _, port := range body.Ports {
		s.registry.Register(port.PortGUID, registry.Peer{
			PodIP:    body.PodIP,
			NodeName: body.NodeName,
			CAName:   port.CAName,
			Port:     port.Port,
			LID:      port.LID,
		})
	}
	s.rebuildGraph()
	// Unconditional log so cross-pod REGISTER outcomes are visible in
	// kubectl logs without enabling per-feature debug flags. Cross-release
	// ibping (ibping-multinode CI job) depends on this REGISTER reaching every
	// peer; absence of this line on a pod immediately tells us the one-shot
	// `mock-ib -register-peers` did not arrive (TCP firewall, wrong port, ...).
	for _, port := range body.Ports {
		s.log.Printf("mock-ib: register from podIP=%s node=%q ca=%s port=%d lid=0x%04x port_guid=%s",
			body.PodIP, body.NodeName, port.CAName, port.Port, port.LID, port.PortGUID)
	}
}

func (s *Server) handleFabricPing(c net.Conn, ping protocol.PingBody) error {
	if !s.pingTargetsLocalPort(ping) {
		// The peer's tryFabricSend resolved this MAD to *our* PodIP, so the
		// dst either has the wrong LID or the wrong port_guid for our locals.
		// Logging this here is the only way to spot a one-shot REGISTER that
		// shipped stale/wrong port advertisements without re-running the
		// validate-ibping.sh harness by hand.
		s.log.Printf("mock-ib: fabric ping mismatch: dst_lid=0x%04x dst_guid=%s does not match any local port",
			ping.DstLID, ping.DstPortGUID)
		return nil
	}
	return protocol.WriteMessage(c, protocol.TypePong, protocol.PongBody{
		Seq:      ping.Seq,
		ServerTS: time.Now().UnixNano(),
	})
}

func (s *Server) pingTargetsLocalPort(ping protocol.PingBody) bool {
	if ping.DstPortGUID != "" && s.hasLocalPortGUID(ping.DstPortGUID) {
		return true
	}
	if ping.DstLID != 0 {
		for _, p := range s.localPorts {
			if p.LID == ping.DstLID {
				return true
			}
		}
	}
	return false
}

func (s *Server) registerWithPeers(ctx context.Context) {
	peers := ParsePeerList(EnvOr(EnvMockIBPeers, ""))
	if len(peers) == 0 {
		peers = DiscoverPeerIPs(EnvOr(EnvMockIBPingServiceHost, ""), s.podIP)
	}
	if len(peers) == 0 {
		return
	}
	body := protocol.RegisterBody{
		NodeName: s.nodeName,
		PodIP:    s.podIP,
		Ports:    s.localPorts,
	}
	wantPeers := 0
	var ok int
	for _, peerIP := range peers {
		if peerIP == s.podIP {
			continue
		}
		wantPeers++
		if err := s.sendRegister(peerIP, body); err != nil {
			if ctx.Err() == nil {
				s.logRegisterError(peerIP, err)
			}
			continue
		}
		s.clearRegisterWarn(peerIP)
		ok++
	}
	if ctx.Err() != nil || wantPeers == 0 {
		return
	}
	if ok > s.lastPeerRegisterOK {
		switch {
		case ok >= wantPeers:
			s.log.Printf("mock-ib: fabric ready — registered %d port(s) with all %d peer(s)", len(body.Ports), wantPeers)
		default:
			s.log.Printf("mock-ib: registered %d port(s) with %d/%d peer(s) (waiting for remaining pods)", len(body.Ports), ok, wantPeers)
		}
		s.lastPeerRegisterOK = ok
	}
}

func (s *Server) logRegisterError(peerIP string, err error) {
	if isPeerNotReady(err) {
		s.registerWarnedMu.Lock()
		_, seen := s.registerWarned[peerIP]
		if !seen {
			s.registerWarned[peerIP] = struct{}{}
			s.log.Printf("mock-ib: peer %s not listening yet (setup in progress; will retry)", peerIP)
		}
		s.registerWarnedMu.Unlock()
		return
	}
	s.log.Printf("mock-ib: register to %s: %v", peerIP, err)
}

func (s *Server) clearRegisterWarn(peerIP string) {
	s.registerWarnedMu.Lock()
	delete(s.registerWarned, peerIP)
	s.registerWarnedMu.Unlock()
}

func isPeerNotReady(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return true
		}
	}
	return strings.Contains(err.Error(), "connection refused")
}

// RegisterWithPeers sends REGISTER to every address in MOCK_IB_PEERS. It does
// not start listeners; use from a one-shot CLI while the main daemon is running.
func (s *Server) RegisterWithPeers() {
	s.registerWithPeers(context.Background())
}

func (s *Server) sendRegister(peerIP string, body protocol.RegisterBody) error {
	addr := net.JoinHostPort(peerIP, strconv.Itoa(s.cfg.TCPPort))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return protocol.WriteMessage(conn, protocol.TypeRegister, body)
}

func (s *Server) pingPeer(peerIP, portGUID string, dstLID uint16) error {
	addr := net.JoinHostPort(peerIP, strconv.Itoa(s.cfg.TCPPort))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}

	seq := atomic.AddUint32(&s.nextSeq, 1)
	ping := protocol.PingBody{
		DstPortGUID: portGUID,
		DstLID:      dstLID,
		Seq:         seq,
		ClientTS:    time.Now().UnixNano(),
	}
	if err := protocol.WriteMessage(conn, protocol.TypePing, ping); err != nil {
		return err
	}
	var env protocol.Envelope
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		return err
	}
	if env.Type != protocol.TypePong {
		return fmt.Errorf("unexpected fabric response %q", env.Type)
	}
	var pong protocol.PongBody
	if err := protocol.DecodeBody(env, &pong); err != nil {
		return err
	}
	if pong.Seq != seq {
		return fmt.Errorf("pong seq mismatch: got %d want %d", pong.Seq, seq)
	}
	return nil
}

func (s *Server) hasLocalPortGUID(guid string) bool {
	key := registry.NormalizePortGUID(guid)
	for _, p := range s.localPorts {
		if registry.NormalizePortGUID(p.PortGUID) == key {
			return true
		}
	}
	return false
}

func (s *Server) tryFabricSend(h *portHandle, sendMad []byte) bool {
	if s.loopback.matchesLocal(sendMad) {
		return false
	}
	var (
		peer  registry.Peer
		guid  string
		found bool
	)
	if g, ok := destPortGUID(sendMad); ok {
		peer, found = s.registry.Lookup(g)
		guid = g
	}
	if !found {
		if lid, ok := destLID(sendMad); ok {
			peer, guid, found = s.registry.LookupByLID(lid)
		}
	}
	if !found || peer.PodIP == "" || peer.PodIP == s.podIP {
		// Useful diagnostic for cross-pod ibping failures: print the
		// destination we could not route AND the registry size so it is
		// immediately obvious whether REGISTER never arrived (size=0) or
		// arrived with the wrong key (size>0 + miss).
		if dstLID, ok := destLID(sendMad); ok && dstLID != 0 {
			s.log.Printf("mock-ib: no fabric route for lid=0x%04x (registry size=%d, self=%s, peer.PodIP=%q)",
				dstLID, s.registry.Size(), s.podIP, peer.PodIP)
		}
		return false
	}
	var dstLID uint16
	if lid, ok := destLID(sendMad); ok {
		dstLID = lid
	} else {
		dstLID = peer.LID
	}
	if err := s.pingPeer(peer.PodIP, guid, dstLID); err != nil {
		s.log.Printf("mock-ib: fabric ping %s@%s: %v", guid, peer.PodIP, err)
		return false
	}
	resp := s.loopback.SynthesizeRecv(sendMad)
	h.mu.Lock()
	h.recvQ = append(h.recvQ, resp)
	h.mu.Unlock()
	return true
}

func localPodIP() string {
	for _, key := range []string{"POD_IP", "MOCK_IB_POD_IP"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return "127.0.0.1"
}

func localNodeName() string {
	if v := os.Getenv("NODE_NAME"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return ""
}
