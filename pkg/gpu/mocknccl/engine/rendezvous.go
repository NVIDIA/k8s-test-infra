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
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"time"
)

const (
	// dialRetryInterval is the pause between dial attempts while rank 0 is
	// not yet listening.
	dialRetryInterval = 50 * time.Millisecond
	// maxFrameSize bounds a single length-prefixed JSON frame.
	maxFrameSize = 1 << 20
)

// Peer is one participant's identity in the comm roster.
type Peer struct {
	Rank int    `json:"rank"`
	Addr string `json:"addr"`
}

// RendezvousResult is the agreed comm membership returned to every rank.
type RendezvousResult struct {
	Rank      int
	WorldSize int
	Peers     []Peer
	InterNode bool
}

// Listener wraps a TCP listener so tests can grab the bound port.
type Listener struct{ ln net.Listener }

// Listen binds addr ("host:port"; port 0 = ephemeral).
func Listen(addr string) (Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return Listener{}, err
	}
	return Listener{ln: l}, nil
}

// Port returns the bound TCP port.
func (l Listener) Port() int { return l.ln.Addr().(*net.TCPAddr).Port }

// Close closes the listener.
func (l Listener) Close() error { return l.ln.Close() }

// Accept waits for and returns the next connection to the listener.
func (l Listener) Accept() (net.Conn, error) { return l.ln.Accept() }

func computeInterNode(peers []Peer) bool {
	seen := map[string]struct{}{}
	for _, p := range peers {
		seen[p.Addr] = struct{}{}
	}
	return len(seen) > 1
}

// Rendezvous performs the barrier. Rank 0 binds rdzvAddr and serves; other
// ranks dial it. selfAddr is this rank's reported pod IP (drives inter-node
// detection). It blocks until worldSize ranks have registered.
func Rendezvous(ctx context.Context, rank, worldSize int, rdzvAddr, selfAddr string) (*RendezvousResult, error) {
	if worldSize <= 1 {
		return &RendezvousResult{Rank: rank, WorldSize: worldSize,
			Peers: []Peer{{Rank: rank, Addr: selfAddr}}, InterNode: false}, nil
	}
	if rank == 0 {
		ln, err := Listen(rdzvAddr)
		if err != nil {
			return nil, fmt.Errorf("rank0 listen %s: %w", rdzvAddr, err)
		}
		defer func() { _ = ln.Close() }()
		return rendezvousServe(ctx, ln, rank, worldSize, selfAddr)
	}
	return rendezvousDial(ctx, rdzvAddr, rank, worldSize, selfAddr)
}

func rendezvousServe(ctx context.Context, ln Listener, rank, worldSize int, selfAddr string) (*RendezvousResult, error) {
	type reg struct {
		peer Peer
		conn net.Conn
	}
	regs := []reg{{peer: Peer{Rank: rank, Addr: selfAddr}}}

	// Unblock a pending Accept when ctx is cancelled; tie the watcher's
	// lifetime to this function so it never parks forever on normal return.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = ln.Close()
		case <-done:
		}
	}()

	// Close any still-open accepted conns on every return path so an
	// early error (Accept/readJSON/writeJSON) doesn't leak fds.
	defer func() {
		for _, r := range regs {
			if r.conn != nil {
				_ = r.conn.Close()
			}
		}
	}()

	for len(regs) < worldSize {
		c, err := ln.Accept()
		if err != nil {
			return nil, fmt.Errorf("accept: %w", err)
		}
		var p Peer
		if err := readJSON(c, &p); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("read peer: %w", err)
		}
		regs = append(regs, reg{peer: p, conn: c})
	}

	peers := make([]Peer, 0, len(regs))
	for _, r := range regs {
		peers = append(peers, r.peer)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Rank < peers[j].Rank })
	res := &RendezvousResult{Rank: rank, WorldSize: worldSize, Peers: peers, InterNode: computeInterNode(peers)}

	for _, r := range regs {
		if r.conn == nil {
			continue
		}
		if err := writeJSON(r.conn, res); err != nil {
			return nil, fmt.Errorf("send roster: %w", err)
		}
	}
	return res, nil
}

func rendezvousDial(ctx context.Context, addr string, rank, worldSize int, selfAddr string) (*RendezvousResult, error) {
	var d net.Dialer
	var conn net.Conn
	var err error
	// Retry until ctx expires: rank 0 may not be listening yet.
	for {
		conn, err = d.DialContext(ctx, "tcp", addr)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("dial %s: %w", addr, ctx.Err())
		case <-time.After(dialRetryInterval):
		}
	}
	defer func() { _ = conn.Close() }()
	if err := writeJSON(conn, Peer{Rank: rank, Addr: selfAddr}); err != nil {
		return nil, fmt.Errorf("send peer: %w", err)
	}
	var res RendezvousResult
	if err := readJSON(conn, &res); err != nil {
		return nil, fmt.Errorf("read roster: %w", err)
	}
	res.Rank = rank
	// Deliberately recompute from the roster (the server also sets it) so the
	// client agrees on scope without trusting the wire value.
	res.InterNode = computeInterNode(res.Peers)
	return &res, nil
}

// length-prefixed JSON framing (mirrors mockib's wire format).
func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) == 0 || len(b) > maxFrameSize {
		return fmt.Errorf("bad frame length %d", len(b))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readJSON(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxFrameSize {
		return fmt.Errorf("bad frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}
