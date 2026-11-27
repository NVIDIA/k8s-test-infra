// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package identity

import (
	"crypto/sha1"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComponentsAreUnambiguous(t *testing.T) {
	t.Parallel()
	require.NotEqual(t, New("x").String("ab").String("c").Sum(), New("x").String("a").String("bc").Sum())
	require.NotEqual(t, New("x").String("a").Sum(), New("y").String("a").Sum())
	require.NotEqual(t, New("x").Int(1, 2).Sum(), New("x").Int(2, 1).Sum())
}

// TestSharedPrefix checks that extending one prefix in two directions leaves
// each result intact: the encoded names against their bytes written out, and
// the UUIDs against SHA-1 computed here.
func TestSharedPrefix(t *testing.T) {
	t.Parallel()
	base := New("x").String("node")
	a := base.String("a")
	b := base.String("b")
	const prefix = "mokka-identity-v1" + "\x00\x00\x00\x01x" + "\x00\x00\x00\x04node"
	require.Equal(t, prefix+"\x00\x00\x00\x01a", string(a.b))
	require.Equal(t, prefix+"\x00\x00\x00\x01b", string(b.b))
	require.Equal(t, uuidV5(prefix+"\x00\x00\x00\x01a"), a.Sum())
	require.Equal(t, uuidV5(prefix+"\x00\x00\x00\x01b"), b.Sum())
}

// uuidV5 is RFC 9562's name-based UUID of name in Mokka's namespace, spelled
// out here rather than taken from Sum.
func uuidV5(name string) [16]byte {
	const namespace = "\xd9\x54\xd6\x17\xe8\x1d\x58\xd0\x8f\x22\xf4\x5a\xfd\xa6\x22\x60"
	h := sha1.Sum([]byte(namespace + name)) //nolint:gosec // UUIDv5 is defined in terms of SHA-1.
	var id [16]byte
	copy(id[:], h[:16])
	id[6] = id[6]&0x0f | 0x50
	id[8] = id[8]&0x3f | 0x80
	return id
}

func TestUUIDIsVersion5(t *testing.T) {
	t.Parallel()
	u := New("x").UUID()
	require.Len(t, u, 36)
	require.Equal(t, byte('5'), u[14])
}
