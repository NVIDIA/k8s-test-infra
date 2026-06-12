// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.2", NodeName: "w1"})
	p, ok := r.Lookup("a088:c203:00ab:0001")
	require.True(t, ok, "lookup failed: %+v %v", p, ok)
	require.Equal(t, "10.0.0.2", p.PodIP, "lookup failed: %+v %v", p, ok)
}

func TestRegistry_DuplicateGUIDPrefersLowerIP(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.9", NodeName: "node-a"})
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.2", NodeName: "node-b"})
	p, _ := r.Lookup("a088:c203:00ab:0001")
	require.Equal(t, "10.0.0.2", p.PodIP, "want lower IP")
}

func TestRegistry_SameNodeRefreshesPodIP(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.244.0.5", NodeName: "node-a"})
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.244.0.6", NodeName: "node-a"})
	p, _ := r.Lookup("a088:c203:00ab:0001")
	require.Equal(t, "10.244.0.6", p.PodIP, "want refreshed PodIP after restart")
}

func TestRegistry_NormalizesGUIDKey(t *testing.T) {
	r := New()
	r.Register("A088C20300AB0001", Peer{PodIP: "10.0.0.2"})
	p, ok := r.Lookup("a088:c203:00ab:0001")
	require.True(t, ok, "lookup with normalized key failed: %+v %v", p, ok)
	require.Equal(t, "10.0.0.2", p.PodIP, "lookup with normalized key failed: %+v %v", p, ok)
}

func TestRegistry_LookupByLID(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.2", LID: 0x0200})
	p, guid, ok := r.LookupByLID(0x0200)
	require.True(t, ok, "lookup by lid failed: %+v %q %v", p, guid, ok)
	require.Equal(t, "10.0.0.2", p.PodIP, "lookup by lid failed: %+v %q %v", p, guid, ok)
	require.Equal(t, "a088:c203:00ab:0001", guid, "lookup by lid failed: %+v %q %v", p, guid, ok)
}
