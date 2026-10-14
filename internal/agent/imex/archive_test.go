// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"archive/tar"
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

type entry struct {
	name     string
	contents string
	typeflag byte
}

func tarXZ(t *testing.T, entries ...entry) []byte {
	t.Helper()

	var compressed bytes.Buffer
	xzw, err := xz.NewWriter(&compressed)
	require.NoError(t, err)
	tw := tar.NewWriter(xzw)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.contents)), Typeflag: typeflag}))
		_, err := tw.Write([]byte(e.contents))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, xzw.Close())
	return compressed.Bytes()
}

// Every member is checked, allowlisted or not: a name the archive controls
// must never be able to point outside it.
func TestExtractRejectsUnsafeMemberNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"../usr/bin/nvidia-imex",
		"..",
		"/imex-test-archive/usr/bin/nvidia-imex",
		"imex-test-archive/../../usr/bin/nvidia-imex",
		"imex-test-archive/usr/bin/../bin/nvidia-imex",
		"imex-test-archive/../outside-the-allowlist",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, cleanup, err := extractArchiveFrom(bytes.NewReader(tarXZ(t, entry{name: name, contents: "evil"})), maxDecompressedBytes)
			defer cleanup()
			require.ErrorContains(t, err, "unsafe IMEX archive member")
		})
	}
}

// O_EXCL: an archive cannot replace a member it already supplied.
func TestExtractRejectsADuplicateRequiredMember(t *testing.T) {
	t.Parallel()

	_, cleanup, err := extractArchiveFrom(bytes.NewReader(tarXZ(t,
		entry{name: "imex-test-archive/LICENSE", contents: "license"},
		entry{name: "imex-test-archive/LICENSE", contents: "another license"},
	)), maxDecompressedBytes)
	defer cleanup()

	require.ErrorContains(t, err, "create extracted LICENSE")
}

func TestExtractRejectsARequiredMemberThatIsNotARegularFile(t *testing.T) {
	t.Parallel()

	_, cleanup, err := extractArchiveFrom(bytes.NewReader(tarXZ(t,
		entry{name: "imex-test-archive/LICENSE", typeflag: tar.TypeSymlink},
	)), maxDecompressedBytes)
	defer cleanup()

	require.ErrorContains(t, err, "invalid IMEX archive member")
}

// The cap counts members the extraction skips too, since reading past them
// decompresses them all the same.
func TestExtractRejectsAnArchiveOverTheDecompressedCap(t *testing.T) {
	t.Parallel()

	archive := tarXZ(t, entry{name: "imex-test-archive/not-allowlisted", contents: strings.Repeat("x", 16<<10)})

	_, cleanup, err := extractArchiveFrom(bytes.NewReader(archive), 4<<10)
	defer cleanup()

	require.ErrorContains(t, err, "decompresses to more than 4096 bytes")
}

// The bytes extracted are the bytes hashed, so an archive swapped after the
// digest check is refused rather than staged.
func TestExtractRejectsAnArchiveThatChangedAfterVerification(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), "imex-test-archive.tar.xz")
	require.NoError(t, os.WriteFile(archive, testArchive(t, ""), 0o644))

	_, cleanup, err := extractArchive(archive, testLock(testArchive(t, "LICENSE")).Artifacts["amd64"].SHA256)
	defer cleanup()

	require.ErrorContains(t, err, "changed after it was verified")
}

// The provided directory comes from an image, so its archive may be anything:
// only a regular file is read, and a FIFO, which would block staging, or a
// symlink is refused.
func TestArchiveReadsRefuseAnythingButARegularFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := testArchive(t, "")
	archive := filepath.Join(dir, "imex-test-archive.tar.xz")
	require.NoError(t, os.WriteFile(archive, data, 0o644))
	link := filepath.Join(dir, "link.tar.xz")
	require.NoError(t, os.Symlink(archive, link))
	fifo := filepath.Join(dir, "fifo.tar.xz")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	digest := testLock(data).Artifacts["amd64"].SHA256

	for _, path := range []string{link, fifo} {
		_, err := fileHasDigest(path, digest)
		require.Error(t, err, path)
		_, cleanup, err := extractArchive(path, digest)
		cleanup()
		require.Error(t, err, path)
	}
	valid, err := fileHasDigest(archive, digest)
	require.NoError(t, err)
	require.True(t, valid)
}

// TestPinnedOfficialArchives checks the pins in imex.lock.json against the
// archives NVIDIA publishes: each downloads, matches its SHA-256 and holds
// every member the node agent stages. It needs the network, so it runs only
// through `make test-imex-archives`, which a change to the lock should pass.
func TestPinnedOfficialArchives(t *testing.T) {
	if os.Getenv("MOKKA_IMEX_DOWNLOAD_TEST") != "1" {
		t.Skip("set MOKKA_IMEX_DOWNLOAD_TEST=1 to verify the pinned NVIDIA archives")
	}

	lock := DefaultLock()
	client := &http.Client{Timeout: 2 * time.Minute}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			n := testNodeSoftware(t, lock)
			n.arch = arch
			_, err := FetchArchive(t.Context(), client, lock, arch, n.providedDir)
			require.NoError(t, err)

			n.stage()

			for _, rel := range stagedDestinations() {
				require.FileExists(t, filepath.Join(n.root, rel))
			}
		})
	}
}
