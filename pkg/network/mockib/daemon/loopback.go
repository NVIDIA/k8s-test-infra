// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

const (
	umadMADOffset  = 64
	umadLIDOffset  = 28
	umadGRHPresent = 32
	umadGIDOffset  = 36
	ibMADClassOff  = 1
	ibMADMethodOff = 3
	ibMADStatusOff = 4
	vendorClass0x81 = 0x81
)

// Loopback matches outbound ping MADs to local port GUIDs under ib-root and synthesizes RECV payloads.
type Loopback struct {
	mu sync.RWMutex
	// serverGUIDs holds port GUIDs registered by ibping -S -G (server mode).
	serverGUIDs map[string]struct{}
	byGUID      map[string]protocol.PortAdvert
	byGID       map[string]protocol.PortAdvert
	byLID       map[uint16]protocol.PortAdvert
}

// NewLoopback indexes localPorts by port GUID, default GID, and LID.
func NewLoopback(localPorts []protocol.PortAdvert) *Loopback {
	byGUID := make(map[string]protocol.PortAdvert, len(localPorts))
	byGID := make(map[string]protocol.PortAdvert, len(localPorts))
	byLID := make(map[uint16]protocol.PortAdvert, len(localPorts))
	for _, p := range localPorts {
		byGUID[registry.NormalizePortGUID(p.PortGUID)] = p
		if p.DefaultGID != "" {
			byGID[normalizeGID(p.DefaultGID)] = p
		}
		byLID[p.LID] = p
	}
	return &Loopback{
		serverGUIDs: make(map[string]struct{}),
		byGUID:      byGUID,
		byGID:       byGID,
		byLID:       byLID,
	}
}

// RegisterServerGUID marks guid as listening (ibping -S -G).
func (l *Loopback) RegisterServerGUID(guid string) {
	key := registry.NormalizePortGUID(guid)
	l.mu.Lock()
	l.serverGUIDs[key] = struct{}{}
	l.mu.Unlock()
}

// ShouldQueueRecv reports whether sendMad should produce a synthetic RECV for loopback.
func (l *Loopback) ShouldQueueRecv(sendMad []byte) bool {
	if len(sendMad) < umadMADOffset+ibMADMethodOff+1 {
		return false
	}
	if l.matchesLocal(sendMad) {
		return true
	}
	if guid, ok := destPortGUID(sendMad); ok {
		l.mu.RLock()
		_, server := l.serverGUIDs[registry.NormalizePortGUID(guid)]
		l.mu.RUnlock()
		if server && isPingLikeMAD(sendMad) {
			return true
		}
	}
	return false
}

// SynthesizeRecv builds the next RECV buffer for a prior SEND.
func (l *Loopback) SynthesizeRecv(sendMad []byte) []byte {
	out := make([]byte, len(sendMad))
	copy(out, sendMad)
	if len(out) >= 8 {
		binary.LittleEndian.PutUint32(out[4:8], 0)
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
	if guid, ok := destPortGUID(umad); ok {
		l.mu.RLock()
		_, ok := l.byGUID[registry.NormalizePortGUID(guid)]
		l.mu.RUnlock()
		if ok {
			return true
		}
	}
	if gid, ok := destGID(umad); ok {
		l.mu.RLock()
		_, ok := l.byGID[normalizeGID(gid)]
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

func destLID(umad []byte) (uint16, bool) {
	if len(umad) < umadLIDOffset+2 {
		return 0, false
	}
	// ib_user_mad.addr.lid (uint16) at byte offset 28; libibumad uses network byte order.
	return binary.BigEndian.Uint16(umad[umadLIDOffset:]), true
}

func destGID(umad []byte) (string, bool) {
	if len(umad) < umadGIDOffset+16 {
		return "", false
	}
	if binary.LittleEndian.Uint32(umad[umadGRHPresent:]) == 0 {
		return "", false
	}
	return formatGID(umad[umadGIDOffset : umadGIDOffset+16]), true
}

func formatGID(g []byte) string {
	if len(g) != 16 {
		return ""
	}
	return fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x",
		g[0], g[1], g[2], g[3], g[4], g[5], g[6], g[7],
		g[8], g[9], g[10], g[11], g[12], g[13], g[14], g[15])
}

func destPortGUID(umad []byte) (string, bool) {
	if len(umad) < umadGIDOffset+16 {
		return "", false
	}
	if binary.LittleEndian.Uint32(umad[umadGRHPresent:]) == 0 {
		return "", false
	}
	gid := umad[umadGIDOffset : umadGIDOffset+16]
	b := gid[8:16]
	return registry.NormalizePortGUID(fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7])), true
}

func normalizeGID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func isPingLikeMAD(umad []byte) bool {
	if len(umad) < umadMADOffset+4 {
		return false
	}
	mad := umad[umadMADOffset:]
	if mad[ibMADClassOff] == vendorClass0x81 {
		return true
	}
	return len(umad) >= 72 && mad[ibMADMethodOff]&0x7f != 0
}

func isSolicitedMAD(umad []byte) bool {
	if len(umad) < umadMADOffset+ibMADMethodOff+1 {
		return false
	}
	return umad[umadMADOffset+ibMADClassOff] == 3
}
