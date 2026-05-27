// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"sync"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

// Loopback matches outbound ping MADs to local port GUIDs under ib-root and synthesizes RECV payloads.
type Loopback struct {
	mu     sync.RWMutex
	byGUID map[string]protocol.PortAdvert
	byGID  map[string]protocol.PortAdvert
	byLID  map[uint16]protocol.PortAdvert
}

// NewLoopback indexes localPorts by port GUID, default GID, and LID.
func NewLoopback(localPorts []protocol.PortAdvert) *Loopback {
	byGUID := make(map[string]protocol.PortAdvert, len(localPorts))
	byGID := make(map[string]protocol.PortAdvert, len(localPorts))
	byLID := make(map[uint16]protocol.PortAdvert, len(localPorts))
	for _, p := range localPorts {
		byGUID[registry.NormalizePortGUID(p.PortGUID)] = p
		if p.DefaultGID != "" {
			byGID[gid.Normalize(p.DefaultGID)] = p
		}
		byLID[p.LID] = p
	}
	return &Loopback{
		byGUID: byGUID,
		byGID:  byGID,
		byLID:  byLID,
	}
}

// ShouldQueueRecv reports whether sendMad should produce a synthetic RECV for loopback.
func (l *Loopback) ShouldQueueRecv(sendMad []byte) bool {
	if len(sendMad) < umadMADOffset+ibMADMethodOff+1 {
		return false
	}
	return l.matchesLocal(sendMad)
}

// SynthesizeRecv builds the next RECV buffer for a prior SEND.
func (l *Loopback) SynthesizeRecv(sendMad []byte) []byte {
	out := make([]byte, len(sendMad))
	copy(out, sendMad)
	if len(out) >= 8 {
		binary.LittleEndian.PutUint32(out[4:8], 0) // umad.status must be zero
	}
	if len(out) >= umadMADOffset+ibMADMethodOff+1 {
		mad := out[umadMADOffset:]
		mad[ibMADMethodOff] |= 0x80
		// Do not clear mad[ibMADStatusOff]: on subnet/vendor MADs byte 4 is part of TRID;
		// libibmad _do_madrpc loops until recv TRID matches the request.
	}
	return out
}

func (l *Loopback) matchesLocal(umad []byte) bool {
	if g, ok := destPortGUID(umad); ok {
		l.mu.RLock()
		_, ok := l.byGUID[g]
		l.mu.RUnlock()
		if ok {
			return true
		}
	}
	if g, ok := destGID(umad); ok {
		l.mu.RLock()
		_, ok := l.byGID[gid.Normalize(g)]
		l.mu.RUnlock()
		if ok {
			return true
		}
	}
	if lid, ok := destLID(umad); ok {
		l.mu.RLock()
		_, ok := l.byLID[lid]
		l.mu.RUnlock()
		if ok {
			return true
		}
	}
	return false
}

// lidForGID returns the LID for a 16-byte destination GID when it matches a local port.
func (l *Loopback) lidForGID(gidBytes []byte) (uint16, bool) {
	if len(gidBytes) != 16 {
		return 0, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if p, ok := l.byGID[gid.Normalize(gid.Format(gidBytes))]; ok {
		return p.LID, true
	}
	if g := gid.PortGUIDFromBytes(gidBytes); g != "" {
		if p, ok := l.byGUID[g]; ok {
			return p.LID, true
		}
	}
	return 0, false
}
