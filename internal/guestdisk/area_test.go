// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package guestdisk

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string][]byte{
		"overrides": []byte("version: 1\nall:\n  temperature: 90\n"),
		"empty":     {},
		"largest":   bytes.Repeat([]byte("x"), MaxPayload),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			area, err := Encode(payload)
			require.NoError(t, err)
			require.Len(t, area, AreaSize)

			got, sum, err := Decode(area)
			require.NoError(t, err)
			require.Equal(t, payload, got)
			require.Equal(t, sha256.Sum256(payload), sum)
		})
	}
}

func TestEncodeRefusesAPayloadLargerThanTheArea(t *testing.T) {
	t.Parallel()

	_, err := Encode(make([]byte, MaxPayload+1))
	require.ErrorIs(t, err, ErrTooLarge)
}

// A reader can catch the host mid-write; the hash is what tells it so.
func TestDecodeRejectsATornOrBlankArea(t *testing.T) {
	t.Parallel()

	area, err := Encode([]byte("version: 1\n"))
	require.NoError(t, err)
	torn := bytes.Clone(area)
	torn[HeaderSize] ^= 0xff
	tooLong := bytes.Clone(area)
	tooLong[8], tooLong[9], tooLong[10], tooLong[11] = 0xff, 0xff, 0xff, 0xff

	for name, bad := range map[string][]byte{
		"torn payload":   torn,
		"blank area":     make([]byte, AreaSize),
		"length too big": tooLong,
		"short buffer":   area[:HeaderSize-1],
	} {
		_, _, err := Decode(bad)
		require.ErrorIs(t, err, ErrInvalid, name)
	}
}

func TestPadReachesTheNextAreaBoundary(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(0), Pad(0))
	require.Equal(t, int64(AreaSize-10), Pad(10))
	require.Equal(t, int64(0), Pad(3*AreaSize))
}

// QEMU holds the disk open, so an update must land in the file it has, not in
// a replacement.
func TestWriteAtUpdatesTheLastAreaInPlace(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bundle.tar")
	archive := bytes.Repeat([]byte("a"), 3000)
	first, err := Encode([]byte("first"))
	require.NoError(t, err)
	initial := append(append(bytes.Clone(archive), make([]byte, Pad(int64(len(archive))))...), first...)
	require.NoError(t, os.WriteFile(path, initial, 0o644))
	before := inode(t, path)

	second, err := Encode([]byte("second"))
	require.NoError(t, err)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, WriteAt(f, second))
	require.NoError(t, f.Close())

	require.Equal(t, before, inode(t, path))
	area, err := Read(path)
	require.NoError(t, err)
	payload, _, err := Decode(area)
	require.NoError(t, err)
	require.Equal(t, []byte("second"), payload)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, archive, data[:len(archive)], "the archive must survive an update")
}

func TestWriteAtRefusesAFileWithoutAnArea(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bundle.tar")
	require.NoError(t, os.WriteFile(path, []byte("short"), 0o644))
	area, err := Encode(nil)
	require.NoError(t, err)

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })
	require.ErrorIs(t, WriteAt(f, area), ErrInvalid)
}

// A bundle written before the live area existed ends in archive data; writing
// over it would corrupt what the guest extracts on its next boot.
func TestWriteAtRefusesAFileThatDoesNotEndInAnArea(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bundle.tar")
	old := bytes.Repeat([]byte("t"), 2*AreaSize)
	require.NoError(t, os.WriteFile(path, old, 0o644))
	area, err := Encode([]byte("new"))
	require.NoError(t, err)

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	require.ErrorIs(t, WriteAt(f, area), ErrInvalid)
	require.NoError(t, f.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, old, data)
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)

	return stat.Ino
}
