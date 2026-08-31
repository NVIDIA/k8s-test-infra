// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package registry maps InfiniBand port GUIDs to peers for mock-ib fabric routing.
package registry

import (
	"sync"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/gid"
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

// Register records peer for portGUID and reports whether anything changed, so
// callers can log arrivals without logging every 2s re-register.
//
// A repeat from the same NodeName refreshes the entry, which is how a restarted
// pod's new IP takes effect. A GUID claimed by two different nodes keeps the
// lower PodIP — an arbitrary but stable tie-break, so the two do not alternate.
func (r *Registry) Register(portGUID string, peer Peer) bool {
	key := gid.NormalizePortGUID(portGUID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.m[key]; ok {
		if cur.NodeName != "" && cur.NodeName == peer.NodeName {
			r.m[key] = peer
			return cur != peer
		}
		if peer.PodIP >= cur.PodIP {
			return false
		}
	}
	r.m[key] = peer
	return true
}

// Lookup returns the peer for portGUID and whether it was found.
func (r *Registry) Lookup(portGUID string) (Peer, bool) {
	key := gid.NormalizePortGUID(portGUID)

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.m[key]

	return p, ok
}

// Size returns the number of peer entries currently registered. Useful for
// diagnostic logging (e.g. distinguishing "no REGISTER arrived" from
// "REGISTER arrived but key didn't match the requested LID/GUID").
func (r *Registry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.m)
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

// LookupByLID returns the peer and port GUID for lid when registered. If two
// peers ever share a LID (shouldn't happen in a healthy fabric), the one with
// the lexicographically lower PodIP wins, matching Register's tie-break so the
// result is deterministic instead of depending on map iteration order.
func (r *Registry) LookupByLID(lid uint16) (Peer, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var (
		best     Peer
		bestGUID string
		found    bool
	)

	for guid, p := range r.m {
		if p.LID != lid {
			continue
		}
		if !found || p.PodIP < best.PodIP {
			best, bestGUID, found = p, guid, true
		}
	}

	return best, bestGUID, found
}
