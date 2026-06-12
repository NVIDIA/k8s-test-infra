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

// TestRegistry_RegisterReportsChange pins Register's changed return value:
// true only when the stored registration actually differs (new GUID, LID,
// PodIP, node, ...). The daemon uses it to log REGISTER outcomes on change
// only instead of per-port every 2s re-register.
func TestRegistry_RegisterReportsChange(t *testing.T) {
	const guid = "a088:c203:00ab:0001"
	const guid2 = "a088:c203:00ab:0002"
	base := Peer{PodIP: "10.0.0.2", NodeName: "node-a", CAName: "mlx5_0", Port: 1, LID: 0x0200}
	relid := Peer{PodIP: "10.0.0.2", NodeName: "node-a", CAName: "mlx5_0", Port: 1, LID: 0x0300}
	reip := Peer{PodIP: "10.0.0.9", NodeName: "node-a", CAName: "mlx5_0", Port: 1, LID: 0x0200}
	otherHi := Peer{PodIP: "10.0.0.9", NodeName: "node-b", CAName: "mlx5_0", Port: 1, LID: 0x0400}
	otherLo := Peer{PodIP: "10.0.0.1", NodeName: "node-b", CAName: "mlx5_0", Port: 1, LID: 0x0400}
	secondCA := Peer{PodIP: "10.0.0.2", NodeName: "node-a", CAName: "mlx5_1", Port: 1, LID: 0x0201}
	anon := Peer{PodIP: "10.0.0.2", LID: 0x0200}

	type reg struct {
		guid string
		peer Peer
	}
	tests := []struct {
		name       string
		pre        []reg
		reg        reg
		want       bool
		wantStored Peer // expected Lookup(reg.guid) after the final Register
	}{
		{"first registration", nil, reg{guid, base}, true, base},
		{"identical re-register", []reg{{guid, base}}, reg{guid, base}, false, base},
		{"same node LID change", []reg{{guid, base}}, reg{guid, relid}, true, relid},
		{"same node PodIP change (pod restart)", []reg{{guid, base}}, reg{guid, reip}, true, reip},
		{"duplicate GUID from other node, higher IP ignored", []reg{{guid, base}}, reg{guid, otherHi}, false, base},
		{"duplicate GUID from other node, lower IP wins", []reg{{guid, base}}, reg{guid, otherLo}, true, otherLo},
		{"new port GUID", []reg{{guid, base}}, reg{guid2, secondCA}, true, secondCA},
		{"identical re-register with empty node name", []reg{{guid, anon}}, reg{guid, anon}, false, anon},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			for _, p := range tc.pre {
				r.Register(p.guid, p.peer)
			}
			require.Equal(t, tc.want, r.Register(tc.reg.guid, tc.reg.peer))
			stored, ok := r.Lookup(tc.reg.guid)
			require.True(t, ok)
			require.Equal(t, tc.wantStored, stored)
		})
	}
}

func TestRegistry_LookupByLID(t *testing.T) {
	r := New()
	r.Register("a088:c203:00ab:0001", Peer{PodIP: "10.0.0.2", LID: 0x0200})
	p, guid, ok := r.LookupByLID(0x0200)
	require.True(t, ok, "lookup by lid failed: %+v %q %v", p, guid, ok)
	require.Equal(t, "10.0.0.2", p.PodIP, "lookup by lid failed: %+v %q %v", p, guid, ok)
	require.Equal(t, "a088:c203:00ab:0001", guid, "lookup by lid failed: %+v %q %v", p, guid, ok)
}
