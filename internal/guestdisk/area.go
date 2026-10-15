// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package guestdisk is the live area at the end of a KubeVirt guest bundle: the
// channel the host's runtime overrides reach a running guest through. The host
// rewrites the area in place while QEMU has the disk open, and the guest polls
// it, so every read may catch a write half-way; the header's hash is what tells
// a complete payload from a torn one.
package guestdisk

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

const (
	// AreaSize is the live area, always the last AreaSize bytes of the disk so
	// the guest finds it without being told an offset.
	AreaSize = 1 << 20
	// HeaderSize keeps the payload block-aligned for O_DIRECT readers.
	HeaderSize = 4096
	// MaxPayload is the largest payload an area holds.
	MaxPayload = AreaSize - HeaderSize
)

var (
	// ErrTooLarge is a payload that does not fit the area.
	ErrTooLarge = errors.New("payload exceeds the live area")
	// ErrInvalid is an area without a complete payload: blank, torn mid-write,
	// or not an area at all.
	ErrInvalid = errors.New("no valid live area")
)

var magic = [8]byte{'M', 'O', 'K', 'K', 'A', 'L', 'V', '1'}

const (
	lengthOffset = len(magic)
	sumOffset    = lengthOffset + 4
)

// Encode returns the AreaSize region carrying payload.
func Encode(payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, len(payload), MaxPayload)
	}
	area := make([]byte, AreaSize)
	copy(area, magic[:])
	binary.LittleEndian.PutUint32(area[lengthOffset:], uint32(len(payload)))
	sum := sha256.Sum256(payload)
	copy(area[sumOffset:], sum[:])
	copy(area[HeaderSize:], payload)

	return area, nil
}

// Decode returns the payload of an area and its SHA-256.
func Decode(area []byte) ([]byte, [32]byte, error) {
	var sum [32]byte
	if len(area) < HeaderSize || !bytes.Equal(area[:len(magic)], magic[:]) {
		return nil, sum, ErrInvalid
	}
	length := int(binary.LittleEndian.Uint32(area[lengthOffset:]))
	if length > MaxPayload || HeaderSize+length > len(area) {
		return nil, sum, fmt.Errorf("%w: payload length %d", ErrInvalid, length)
	}
	payload := area[HeaderSize : HeaderSize+length]
	sum = sha256.Sum256(payload)
	if !bytes.Equal(sum[:], area[sumOffset:sumOffset+len(sum)]) {
		return nil, sum, fmt.Errorf("%w: payload hash mismatch", ErrInvalid)
	}

	return bytes.Clone(payload), sum, nil
}

// Pad is the number of zero bytes that bring an n-byte archive to an area
// boundary, so the area that follows starts block-aligned.
func Pad(n int64) int64 {
	return (AreaSize - n%AreaSize) % AreaSize
}

// WriteAt replaces the last AreaSize bytes of f in place. f must already end
// in an area: QEMU holds the file open, so it can only be updated, never
// replaced or resized. A file whose tail is not an area, such as a bundle from
// before the area existed, is refused rather than overwritten.
func WriteAt(f *os.File, area []byte) error {
	if len(area) != AreaSize {
		return fmt.Errorf("%w: area is %d bytes, want %d", ErrInvalid, len(area), AreaSize)
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < AreaSize {
		return fmt.Errorf("%w: %s is %d bytes, too short to hold one", ErrInvalid, f.Name(), info.Size())
	}
	offset := info.Size() - AreaSize
	var current [len(magic)]byte
	if _, err := f.ReadAt(current[:], offset); err != nil {
		return fmt.Errorf("read live area of %s: %w", f.Name(), err)
	}
	if current != magic {
		return fmt.Errorf("%w: %s does not end in a live area", ErrInvalid, f.Name())
	}
	if _, err := f.WriteAt(area, offset); err != nil {
		return fmt.Errorf("write live area of %s: %w", f.Name(), err)
	}

	return nil
}
