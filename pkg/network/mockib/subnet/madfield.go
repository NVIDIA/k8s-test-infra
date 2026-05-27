// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import "encoding/binary"

// Bitsoffs converts IB spec bit offsets to libibmad wire offsets (fields.c BITSOFFS).
func Bitsoffs(o, w int) int {
	return (o &^ 31) | (32 - (o & 31) - w)
}

// SetFieldSpec sets a field using the IB spec bit offset from fields.c (SMI/GSI payload).
func SetFieldSpec(buf []byte, specOff, width int, val uint32) {
	SetField(buf, Bitsoffs(specOff, width), width, val)
}

// GetFieldSpec reads a field using the IB spec bit offset from fields.c.
func GetFieldSpec(buf []byte, specOff, width int) uint32 {
	return GetField(buf, Bitsoffs(specOff, width), width)
}

// SetField writes a libibmad-style bit field (fields.c _set_field / 3^idx).
func SetField(buf []byte, bitOff, bitLen int, val uint32) {
	if len(buf) == 0 {
		return
	}
	pre := (8 - (bitOff & 7)) & 7
	post := (bitOff + bitLen) & 7
	byteLen := bitLen / 8
	idx := bitOff / 8

	if byteLen == 0 && (bitOff&7)+bitLen < 8 {
		i := 3 ^ idx
		if i >= len(buf) {
			return
		}
		mask := uint32((1 << bitLen) - 1)
		buf[i] &^= byte(mask << (bitOff & 7))
		buf[i] |= byte((val & mask) << (bitOff & 7))
		return
	}

	if pre > 0 {
		i := 3 ^ idx
		if i < len(buf) {
			buf[i] &= byte((1 << (8 - pre)) - 1)
			buf[i] |= byte((val & ((1 << pre) - 1)) << (8 - pre))
		}
		val >>= pre
		idx++
	}

	for n := 0; n < byteLen; n++ {
		i := 3 ^ (idx + n)
		if i < len(buf) {
			buf[i] = byte(val & 0xff)
		}
		val >>= 8
	}

	if post > 0 {
		i := 3 ^ (bitOff+bitLen)/8
		if i < len(buf) {
			buf[i] &^= byte((1 << post) - 1)
			buf[i] |= byte(val)
		}
	}
}

// GetField reads a libibmad-style bit field (fields.c _get_field).
func GetField(buf []byte, bitOff, bitLen int) uint32 {
	if len(buf) == 0 {
		return 0
	}
	pre := (8 - (bitOff & 7)) & 7
	post := (bitOff + bitLen) & 7
	byteLen := bitLen / 8
	idx := bitOff / 8

	if byteLen == 0 && (bitOff&7)+bitLen < 8 {
		i := 3 ^ idx
		if i >= len(buf) {
			return 0
		}
		return uint32(buf[i]>>(bitOff&7)) & uint32((1<<bitLen)-1)
	}

	var v, val uint32
	if pre > 0 {
		i := 3 ^ idx
		if i < len(buf) {
			v = uint32(buf[i] >> (8 - pre))
		}
		idx++
	}
	if post > 0 {
		i := 3 ^ ((bitOff + bitLen) / 8)
		if i < len(buf) {
			val = uint32(buf[i]) & uint32((1<<post)-1)
		}
	}
	idx2 := idx + byteLen - 1
	for n := byteLen; n > 0; n-- {
		i := 3 ^ idx2
		if i < len(buf) {
			val = (val << 8) | uint32(buf[i])
		}
		idx2--
	}
	return (val << pre) | v
}

// SetField64 copies a 64-bit big-endian value at a byte-aligned bit offset.
func SetField64(buf []byte, bitOff int, val uint64) {
	off := bitOff / 8
	if off+8 > len(buf) {
		return
	}
	binary.BigEndian.PutUint64(buf[off:], val)
}
