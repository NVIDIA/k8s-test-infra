// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// serve publishes body at the path lock pins for amd64 and returns a lock
// pointing at it, a client trusting it and a count of the requests it got.
func serve(t *testing.T, pinned, body []byte) (Lock, *http.Client, *atomic.Int32) {
	t.Helper()

	lock := testLock(pinned)
	published := "/redist/" + lock.Artifacts["amd64"].Path
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != published {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	lock.BaseURL = srv.URL + "/redist"
	return lock, srv.Client(), &requests
}

func TestFetchArchiveDownloadsThePinnedArchive(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	lock, client, _ := serve(t, archive, archive)
	dir := t.TempDir()

	got, err := FetchArchive(t.Context(), client, lock, "amd64", dir)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "imex-test-archive.tar.xz"), got)
	requireFileContent(t, got, string(archive))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file is left behind")
}

// A populated directory, such as a CI cache, must never reach the network.
func TestFetchArchiveKeepsAVerifiedCopy(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	lock, client, requests := serve(t, archive, archive)
	dir := t.TempDir()
	cached := filepath.Join(dir, "imex-test-archive.tar.xz")
	require.NoError(t, os.WriteFile(cached, archive, 0o644))

	got, err := FetchArchive(t.Context(), client, lock, "amd64", dir)

	require.NoError(t, err)
	require.Equal(t, cached, got)
	require.Zero(t, requests.Load())
}

func TestFetchArchiveReplacesACopyThatDoesNotMatchThePin(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	lock, client, _ := serve(t, archive, archive)
	dir := t.TempDir()
	stale := filepath.Join(dir, "imex-test-archive.tar.xz")
	require.NoError(t, os.WriteFile(stale, []byte("truncated"), 0o644))

	_, err := FetchArchive(t.Context(), client, lock, "amd64", dir)

	require.NoError(t, err)
	requireFileContent(t, stale, string(archive))
}

func TestFetchArchiveRejectsADownloadThatDoesNotMatchThePin(t *testing.T) {
	t.Parallel()

	lock, client, _ := serve(t, testArchive(t, ""), testArchive(t, "LICENSE"))
	dir := t.TempDir()

	_, err := FetchArchive(t.Context(), client, lock, "amd64", dir)

	require.ErrorContains(t, err, "does not match the pinned")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "an unverified download never takes the pinned name")
}

// The lock allows only an HTTPS base URL, and a redirect must not undo that.
func TestFetchArchiveRefusesARedirectToCleartext(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(plain.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	lock := testLock(archive)
	lock.BaseURL = srv.URL + "/redist"

	_, err := FetchArchive(t.Context(), srv.Client(), lock, "amd64", t.TempDir())

	require.ErrorContains(t, err, "not HTTPS")
}

func TestFetchArchiveReportsAFailedRequest(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	lock, client, _ := serve(t, archive, archive)
	lock.Artifacts["amd64"] = Artifact{Path: "imex/linux-x86_64/missing.tar.xz", SHA256: lock.Artifacts["amd64"].SHA256}

	_, err := FetchArchive(t.Context(), client, lock, "amd64", t.TempDir())

	require.ErrorContains(t, err, "404 Not Found")
}
