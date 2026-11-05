// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package staginggate coordinates node-agent staging with NRI adjustments.
package staginggate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// FileName is the lock file inside the gate directory. The directory must not
// be part of the overlay NRI injects: any process that can open the file can
// hold a shared lock and stall staging.
const FileName = "nri-staging.lock"

// retryInterval paces Exclusive's attempts. Shared holders are single NRI
// adjustment decisions, which release within milliseconds.
const retryInterval = 20 * time.Millisecond

// Lock serializes node-agent staging against NRI adjustment decisions. It
// covers the decision only: the runtime applies an adjustment's mounts after
// NRI releases its shared lock, so a teardown starting in that gap is still
// possible. The gate narrows the window to that gap rather than closing it.
type Lock struct{ file *os.File }

// Exclusive waits for in-flight adjustment decisions before staging or
// teardown. It gives up when ctx ends so a lock holder cannot hang the agent.
func Exclusive(ctx context.Context, path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open staging lock %s: %w", path, err)
	}

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &Lock{file: file}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = file.Close()
			return nil, fmt.Errorf("lock staging tree %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("lock staging tree %s: %w", path, ctx.Err())
		case <-ticker.C:
		}
	}
}

// Shared returns acquired=false while staging is in progress. NRI must then
// fail open rather than block container creation behind a long staging wave.
func Shared(path string) (lock *Lock, acquired bool, err error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, false, fmt.Errorf("open staging lock %s: %w", path, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock staged tree %s: %w", path, err)
	}
	return &Lock{file: file}, true, nil
}

// Close releases the lock and its file descriptor.
func (l *Lock) Close() error {
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close staging lock: %w", err)
	}
	return nil
}
