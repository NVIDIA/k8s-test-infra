// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// downloading gives n a downloader that fetches from a test server answering
// each request with respond, and returns the number of requests it got.
func downloading(t *testing.T, n *nodeSoftware, respond func(attempt int32, w http.ResponseWriter)) *atomic.Int32 {
	t.Helper()

	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(requests.Add(1), w)
	}))
	t.Cleanup(srv.Close)

	n.lock.BaseURL = srv.URL + "/redist"
	n.download = &downloader{client: srv.Client(), backoff: time.Millisecond, window: 10 * time.Second, requeue: make(chan struct{}, 1)}
	return &requests
}

// offline gives n a downloader whose client fails the test if it is used.
func offline(t *testing.T, n *nodeSoftware) {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected download of %s", r.URL)
		return nil, errors.New("offline")
	})}
	n.download = &downloader{client: client, requeue: make(chan struct{}, 1)}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func awaitRequeue(t *testing.T, n *nodeSoftware) {
	t.Helper()
	select {
	case <-n.download.requeue:
	case <-time.After(10 * time.Second):
		t.Fatal("the download never asked for a requeue")
	}
}

func cacheArchive(n *nodeSoftware) string {
	return filepath.Join(cachedArchiveDir(n.root, n.lock, n.arch), "imex-test-archive.tar.xz")
}

func TestNodeSoftwareUsesAProvidedArchiveWithoutDownloading(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	offline(t, n)
	provide(t, n, archive)

	n.stage(t.Context())

	requireFileContent(t, filepath.Join(n.root, shimDestination), "mokka-shim")
}

func TestNodeSoftwareUsesTheNodeCacheWithoutDownloading(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	offline(t, n)
	require.NoError(t, os.MkdirAll(filepath.Dir(cacheArchive(n)), 0o755))
	require.NoError(t, os.WriteFile(cacheArchive(n), archive, 0o644))

	n.stage(t.Context())

	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex.real"), "real-imex")
}

// A stale archive in the provided directory must not hide a good one further
// down the chain.
func TestNodeSoftwareSkipsAMismatchedProvidedArchiveForTheNodeCache(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	offline(t, n)
	provide(t, n, testArchive(t, "LICENSE"))
	require.NoError(t, os.MkdirAll(filepath.Dir(cacheArchive(n)), 0o755))
	require.NoError(t, os.WriteFile(cacheArchive(n), archive, 0o644))

	n.stage(t.Context())

	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex.real"), "real-imex")
}

// Stage runs under NRI's staging lock, so it only starts the download; the
// requeue that follows stages what it fetched.
func TestNodeSoftwareDownloadsIntoTheNodeCacheInTheBackground(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	requests := downloading(t, n, func(_ int32, w http.ResponseWriter) { _, _ = w.Write(archive) })
	stale := filepath.Join(n.root, cacheDir, "older-version")
	require.NoError(t, os.MkdirAll(stale, 0o755))

	n.stage(t.Context())
	awaitRequeue(t, n)
	n.stage(t.Context())

	require.Equal(t, int32(1), requests.Load())
	requireFileContent(t, cacheArchive(n), string(archive))
	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex.real"), "real-imex")
	require.NoDirExists(t, stale, "downloads of other IMEX versions are pruned")
}

func TestNodeSoftwareRetriesAFailedDownload(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	requests := downloading(t, n, func(attempt int32, w http.ResponseWriter) {
		if attempt == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(archive)
	})

	n.stage(t.Context())
	awaitRequeue(t, n)
	n.stage(t.Context())

	require.Equal(t, int32(2), requests.Load())
	requireFileContent(t, filepath.Join(n.root, shimDestination), "mokka-shim")
}

// A download that fails retries within its window, so a short outage at boot
// still gets the node IMEX.
func TestNodeSoftwareRetriesAFailedDownloadWithinItsWindow(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	requests := downloading(t, n, func(attempt int32, w http.ResponseWriter) {
		if attempt <= 5 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(archive)
	})

	n.stage(t.Context())
	requireNothingStaged(t, n.root)
	awaitRequeue(t, n)
	n.stage(t.Context())

	require.Equal(t, int32(6), requests.Load())
	requireFileContent(t, filepath.Join(n.root, shimDestination), "mokka-shim")
	retries := logs.FilterMessage("IMEX download failed; retrying").All()
	require.Len(t, retries, 5)
	require.Equal(t, zapcore.WarnLevel, retries[0].Level)
	require.Contains(t, retries[0].ContextMap()["error"], "503 Service Unavailable")
}

// Fail open: once the retry window runs out the node goes without IMEX until
// the node agent restarts, instead of polling NVIDIA forever.
func TestNodeSoftwareGivesUpAfterTheRetryWindow(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	requests := downloading(t, n, func(_ int32, w http.ResponseWriter) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	n.download.backoff = 5 * time.Millisecond
	n.download.window = 50 * time.Millisecond

	n.stage(t.Context())
	awaitRequeue(t, n)
	attempts := requests.Load()
	n.stage(t.Context())
	n.stage(t.Context())

	require.Greater(t, attempts, int32(1), "the download retried within its window")
	require.Equal(t, attempts, requests.Load(), "no download starts once the window ran out")
	requireNothingStaged(t, n.root)
	gaveUp := logs.FilterMessageSnippet("cannot download IMEX node software").All()
	require.Len(t, gaveUp, 2, "each pass after the window says why")
	require.Equal(t, zapcore.WarnLevel, gaveUp[0].Level)
	require.Contains(t, gaveUp[0].ContextMap()["error"], "503 Service Unavailable")
}

// Discard waits for a cancelled download to stop, and the download can start
// again. The transport holds each request past its cancellation until the
// test releases it, so a Discard that did not wait would return early.
func TestDiscardWaitsForACancelledDownload(t *testing.T) {
	t.Parallel()

	n := testNodeSoftware(t, testLock(testArchive(t, "")))
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-r.Context().Done()
		<-release
		return nil, r.Context().Err()
	})}
	n.download = &downloader{client: client, backoff: time.Millisecond, window: 10 * time.Second, requeue: make(chan struct{}, 1)}
	sim := &Simulator{host: host.New(n.root), nodeSoftware: *n}
	ctx, cancel := context.WithCancel(t.Context())

	started, err := n.download.start(ctx, n.root, n.lock, n.arch)
	require.NoError(t, err)
	require.True(t, started)
	<-entered
	cancel()
	discarded := make(chan error, 1)
	go func() { discarded <- sim.Discard(t.Context()) }()
	select {
	case <-discarded:
		t.Fatal("Discard returned while the download was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-discarded)

	n.download.mu.Lock()
	running := n.download.running
	n.download.mu.Unlock()
	require.False(t, running, "a cancelled download no longer counts as running")
	started, err = n.download.start(t.Context(), n.root, n.lock, n.arch)
	require.NoError(t, err, "a cancelled download did not fail")
	require.True(t, started, "a cancelled download can start again")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the download did not start again")
	}
}

func TestRetryDelayTakesOffAtMostHalf(t *testing.T) {
	t.Parallel()

	for range 1000 {
		got := retryDelay(time.Minute)
		require.GreaterOrEqual(t, got, 30*time.Second)
		require.LessOrEqual(t, got, time.Minute)
	}
}

// A pinned archive that cannot be staged, say because the tree is read-only,
// ends the search: downloading the same bytes would fail the same way, and
// every requeue would download again.
func TestNodeSoftwareDoesNotDownloadWhenThePinnedArchiveCannotBeStaged(t *testing.T) {
	t.Parallel()

	for _, location := range []string{"provided", "node cache"} {
		t.Run(location, func(t *testing.T) {
			t.Parallel()

			archive := testArchive(t, "")
			n := testNodeSoftware(t, testLock(archive))
			offline(t, n)
			n.shimPath = filepath.Join(t.TempDir(), "missing-shim")
			if location == "provided" {
				provide(t, n, archive)
			} else {
				require.NoError(t, os.MkdirAll(filepath.Dir(cacheArchive(n)), 0o755))
				require.NoError(t, os.WriteFile(cacheArchive(n), archive, 0o644))
			}

			n.stage(t.Context())

			requireNothingStaged(t, n.root)
			select {
			case <-n.download.requeue:
				t.Fatal("no download was started, so nothing may ask for a requeue")
			default:
			}
		})
	}
}

// The warning about a skipped archive says which version and digest were
// expected, so an operator can tell a stale binaries image from a corrupt one.
func TestNodeSoftwareWarnsWithThePinWhenAnArchiveDoesNotMatch(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))

	archive := testArchive(t, "")
	lock := testLock(archive)
	n := testNodeSoftware(t, lock)
	file := provide(t, n, testArchive(t, "LICENSE"))

	n.stage(t.Context())

	mismatch := logs.FilterMessageSnippet("does not match the pinned SHA-256").All()
	require.Len(t, mismatch, 1)
	fields := mismatch[0].ContextMap()
	require.Equal(t, "test-version", fields["version"])
	require.Equal(t, lock.Artifacts["amd64"].SHA256, fields["sha256"])
	require.Equal(t, file, fields["archive"])
	require.Equal(t, "provided", fields["location"])
}

func TestDownloadTimeoutDefaultsToAMinute(t *testing.T) {
	t.Parallel()

	byDefault := New(host.New(t.TempDir()), Options{Download: true})
	configured := New(host.New(t.TempDir()), Options{Download: true, DownloadTimeout: 30 * time.Second})

	require.Equal(t, time.Minute, byDefault.nodeSoftware.download.client.Timeout)
	require.Equal(t, 30*time.Second, configured.nodeSoftware.download.client.Timeout)
}
