// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"context"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// Download pacing: retry with exponential backoff and jitter for about a
// minute, then not again until the node agent restarts. A node that cannot
// reach NVIDIA does not poll it forever: it stays ready without IMEX, and
// ComputeDomain daemons fail there as on a real node without the IMEX package.
const (
	downloadBackoff     = 5 * time.Second
	downloadRetryWindow = time.Minute
)

// DefaultDownloadTimeout bounds one download attempt. The archive is a few
// megabytes, so a healthy attempt takes seconds.
const DefaultDownloadTimeout = time.Minute

// cacheDir holds downloaded archives under the host root, one directory per
// IMEX version and architecture.
const cacheDir = "cache/imex"

// downloader fills the node cache from NVIDIA in the background. Stage runs
// under NRI's staging lock, so it never waits on the network: it starts a
// download, and stages the cached archive once requeue brings it back.
type downloader struct {
	client  *http.Client
	backoff time.Duration
	// window bounds when a retry may start, counted from the first attempt.
	window  time.Duration
	requeue chan struct{}

	mu      sync.Mutex
	running bool
	// failed ends the last download once its retry window ran out; no
	// download starts again until the node agent restarts.
	failed error
	done   sync.WaitGroup
}

func newDownloader(client *http.Client) *downloader {
	return &downloader{
		client:  client,
		backoff: downloadBackoff,
		window:  downloadRetryWindow,
		requeue: make(chan struct{}, 1),
	}
}

func cachedArchiveDir(root string, lock Lock, arch string) string {
	return filepath.Join(root, cacheDir, lock.Version, arch)
}

// start downloads the archive lock pins for arch into the node cache under
// root, unless a download is already running, and reports whether it started
// one. It returns the error that ended the last download once its retry window
// ran out. ctx bounds the download; one it cancels starts again at the next
// start.
func (d *downloader) start(ctx context.Context, root string, lock Lock, arch string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed != nil {
		return false, d.failed
	}
	if d.running {
		return false, nil
	}
	d.running = true
	d.done.Go(func() { d.run(ctx, root, lock, arch) })
	return true, nil
}

// wait returns once no download is running, or when ctx is done.
func (d *downloader) wait(ctx context.Context) {
	finished := make(chan struct{})
	go func() {
		d.done.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
	}
}

func (d *downloader) run(ctx context.Context, root string, lock Lock, arch string) {
	err := d.fetch(ctx, root, lock, arch)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = false
	if ctx.Err() != nil {
		return // cancelled: the node agent is stopping
	}
	d.failed = err
	select {
	case d.requeue <- struct{}{}:
	default: // a requeue is already pending
	}
}

func (d *downloader) fetch(ctx context.Context, root string, lock Lock, arch string) error {
	// Archives of other IMEX versions are never read again.
	cache := filepath.Join(root, cacheDir)
	if err := fsutil.PruneDir(cache, func(version string) bool { return version == lock.Version }); err != nil {
		zap.L().Warn("cannot prune IMEX downloads of other versions", zap.String("simulator", name), zap.Error(err))
	}

	deadline := time.Now().Add(d.window)
	delay := d.backoff
	for attempt := 1; ; attempt++ {
		_, err := FetchArchive(ctx, d.client, lock, arch, cachedArchiveDir(root, lock, arch))
		if err == nil || ctx.Err() != nil {
			return err
		}
		wait := retryDelay(delay)
		if time.Now().Add(wait).After(deadline) {
			return err
		}
		zap.L().Warn("IMEX download failed; retrying", zap.String("simulator", name),
			zap.Int("attempt", attempt), zap.Duration("retryIn", wait), zap.Error(err))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
	}
}

// retryDelay takes up to half of delay off at random, so nodes that failed
// together do not retry together.
func retryDelay(delay time.Duration) time.Duration {
	return delay - rand.N(delay/2+1)
}
