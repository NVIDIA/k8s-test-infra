// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package registry

import "testing"

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.2", NodeName: "w1"})
	p, ok := r.Lookup("a088:c203:00ab:0001")
	if !ok || p.PodIP != "10.0.0.2" {
		t.Fatalf("lookup failed: %+v %v", p, ok)
	}
}

func TestRegistry_DuplicateGUIDPrefersLowerIP(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.9", NodeName: "node-a"})
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.2", NodeName: "node-b"})
	p, _ := r.Lookup("a088:c203:00ab:0001")
	if p.PodIP != "10.0.0.2" {
		t.Fatalf("want lower IP, got %s", p.PodIP)
	}
}

func TestRegistry_SameNodeRefreshesPodIP(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.244.0.5", NodeName: "node-a"})
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.244.0.6", NodeName: "node-a"})
	p, _ := r.Lookup("a088:c203:00ab:0001")
	if p.PodIP != "10.244.0.6" {
		t.Fatalf("want refreshed PodIP after restart, got %s", p.PodIP)
	}
}

func TestRegistry_NormalizesGUIDKey(t *testing.T) {
	r := New()
	r.Register("A088C20300AB0001", Peer{PodIP: "10.0.0.2"})
	p, ok := r.Lookup("a088:c203:00ab:0001")
	if !ok || p.PodIP != "10.0.0.2" {
		t.Fatalf("lookup with normalized key failed: %+v %v", p, ok)
	}
}

func TestRegistry_LookupByLID(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.2", LID: 0x0200})
	p, guid, ok := r.LookupByLID(0x0200)
	if !ok || p.PodIP != "10.0.0.2" || guid != "a088:c203:00ab:0001" {
		t.Fatalf("lookup by lid failed: %+v %q %v", p, guid, ok)
	}
}
