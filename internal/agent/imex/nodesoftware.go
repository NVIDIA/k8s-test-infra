// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Pacing for the background download. The cap keeps a node that cannot reach
// NVIDIA from polling it constantly, yet converges within minutes once the
// cause clears.
const (
	defaultRetryInitial = 5 * time.Second
	defaultRetryMax     = 5 * time.Minute
)

// nodeSoftware tracks the published IMEX node software and the background
// fetch that fills a cache miss. Stage and Discard never run concurrently, so
// cancel and done need no lock.
type nodeSoftware struct {
	published    atomic.Bool
	cancel       context.CancelFunc
	done         chan struct{}
	retryInitial time.Duration
	retryMax     time.Duration
}

// reconcileNodeSoftware publishes the IMEX node software from the verified
// node cache and never waits on the network. A cache miss starts a background
// fetch that retries until it succeeds; meanwhile only this simulator reports
// not ready, so GPU, CDI and PCI staging carry on. Only removing the software
// once disabled can fail Stage.
func (s *Simulator) reconcileNodeSoftware(ctx context.Context, enabled bool) error {
	s.stopFetch()
	if !enabled {
		s.nodeSoftware.published.Store(true)
		if err := s.installer.discard(); err != nil {
			return fmt.Errorf("remove disabled imex node software: %w", err)
		}
		return nil
	}

	published, err := s.installer.publishCached()
	if err != nil {
		zap.L().Error("cannot publish cached IMEX node software; fetching it again", zap.Error(err))
	}
	s.nodeSoftware.published.Store(published)
	if !published {
		// Published files always match the lock: drop any left by an earlier
		// version before fetching this one.
		if err := s.installer.discard(); err != nil {
			zap.L().Error("cannot remove stale IMEX node software", zap.Error(err))
		}
		s.startFetch(ctx)
	}
	return nil
}

func (s *Simulator) startFetch(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.nodeSoftware.cancel, s.nodeSoftware.done = cancel, done
	go func() {
		defer close(done)
		s.fetchUntilPublished(ctx)
	}()
}

// stopFetch cancels a background fetch and waits for it, so nothing writes the
// driver tree after Stage or Discard moves on.
func (s *Simulator) stopFetch() {
	if s.nodeSoftware.cancel == nil {
		return
	}
	s.nodeSoftware.cancel()
	<-s.nodeSoftware.done
	s.nodeSoftware.cancel, s.nodeSoftware.done = nil, nil
}

func (s *Simulator) fetchUntilPublished(ctx context.Context) {
	delay := s.nodeSoftware.retryInitial
	for attempt := 1; ; attempt++ {
		err := s.installer.download(ctx)
		if err == nil {
			var published bool
			published, err = s.installer.publishCached()
			if published {
				s.nodeSoftware.published.Store(true)
				zap.L().Info("IMEX node software published", zap.Int("attempt", attempt))
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		zap.L().Warn("IMEX node software not published; retrying",
			zap.Int("attempt", attempt), zap.Duration("retryIn", delay), zap.Error(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, s.nodeSoftware.retryMax)
	}
}
