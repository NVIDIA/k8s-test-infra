// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package registry maps InfiniBand port GUIDs to peers for mock-ib fabric routing.
package registry

import (
	"strings"
	"sync"
)

// Peer identifies a mock-ib instance advertising a port GUID.
type Peer struct {
	PodIP    string
	NodeName string
	CAName   string
	Port     int
	LID      uint16
}

// Registry is an in-memory GUID → peer table updated by REGISTER messages.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Peer // key: normalized port_guid
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{m: make(map[string]Peer)}
}

// Register records peer for portGUID. When the same NodeName re-registers (pod
// restart with a new PodIP), the entry is refreshed. Otherwise duplicate GUIDs
// keep the peer with the lexicographically lower PodIP to avoid flip-flopping.
func (r *Registry) Register(portGUID string, peer Peer) {
	key := NormalizePortGUID(portGUID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.m[key]; ok {
		if cur.NodeName != "" && cur.NodeName == peer.NodeName {
			r.m[key] = peer
			return
		}
		if peer.PodIP >= cur.PodIP {
			return
		}
	}
	r.m[key] = peer
}

// Lookup returns the peer for portGUID and whether it was found.
func (r *Registry) Lookup(portGUID string) (Peer, bool) {
	key := NormalizePortGUID(portGUID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.m[key]
	return p, ok
}

// Snapshot returns a copy of the GUID → peer table (for fabric graph rebuild).
func (r *Registry) Snapshot() map[string]Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Peer, len(r.m))
	for k, v := range r.m {
		out[k] = v
	}
	return out
}

// LookupByLID returns the peer and port GUID for lid when registered.
func (r *Registry) LookupByLID(lid uint16) (Peer, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for guid, p := range r.m {
		if p.LID == lid {
			return p, guid, true
		}
	}
	return Peer{}, "", false
}

// NormalizePortGUID lowercases a port GUID and formats it with colon separators
// (a088:c203:00ab:0001). Non-hex characters are stripped; short inputs are
// left-padded to 16 hex digits.
func NormalizePortGUID(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			b.WriteByte(byte(c))
		}
	}
	hex := b.String()
	if len(hex) < 16 {
		hex = strings.Repeat("0", 16-len(hex)) + hex
	} else if len(hex) > 16 {
		hex = hex[len(hex)-16:]
	}
	return hex[0:4] + ":" + hex[4:8] + ":" + hex[8:12] + ":" + hex[12:16]
}
