// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package identity is Mokka's scheme for simulated identifiers: a UUIDv5, in a
// fixed Mokka namespace, of a name built from where something sits. The same
// coordinates always give the same identifier, so identifiers are stable
// across runs and independent of everything else declared beside them.
package identity

import (
	"crypto/sha1" // UUIDv5 is defined in terms of SHA-1.
	"encoding/binary"
	"fmt"
	"slices"
)

// namespaceV1 is the fixed Mokka namespace for the first identity scheme. A
// new namespace preserves existing identities if the scheme evolves.
const namespaceV1 = "\xd9\x54\xd6\x17\xe8\x1d\x58\xd0\x8f\x22\xf4\x5a\xfd\xa6\x22\x60"

// Name is the name an identifier is derived from. Build one with New and the
// component methods; each returns a new Name, so a shared prefix can be
// extended in several directions.
type Name struct{ b []byte }

// New starts a name for one kind of identifier, such as "gpu".
func New(kind string) Name {
	return Name{b: []byte("mokka-identity-v1")}.String(kind)
}

// String appends a string component. Components are length-prefixed, so
// distinct coordinates never become equal through separator characters in
// names or UIDs.
func (n Name) String(s string) Name {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(s)))
	// Clip, so appending to a shared prefix never writes into another Name.
	return Name{b: append(append(slices.Clip(n.b), length[:]...), s...)}
}

// Int appends integer components, each as four big-endian bytes.
func (n Name) Int(values ...int32) Name {
	b := slices.Clip(n.b)
	for _, v := range values {
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], uint32(v))
		b = append(b, encoded[:]...)
	}
	return Name{b: b}
}

// Sum returns the name's UUIDv5 bytes.
func (n Name) Sum() [16]byte {
	hash := sha1.New()
	_, _ = hash.Write([]byte(namespaceV1))
	_, _ = hash.Write(n.b)

	var id [16]byte
	copy(id[:], hash.Sum(nil))
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

// UUID returns the name's UUIDv5 in its canonical text form.
func (n Name) UUID() string {
	id := n.Sum()
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(id[0:4]),
		binary.BigEndian.Uint16(id[4:6]),
		binary.BigEndian.Uint16(id[6:8]),
		binary.BigEndian.Uint16(id[8:10]),
		id[10:16],
	)
}
