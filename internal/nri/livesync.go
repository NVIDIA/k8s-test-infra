// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/guestdisk"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// liveSyncInterval matches the engine's override TTL: a guest is never more
// than one host tick, one guest tick and one TTL behind the node.
const liveSyncInterval = time.Second

// liveSync keeps the live area of every guest bundle equal to the host's
// runtime overrides. It is the only writer of bundles, so a bundle created
// while the host file changes can never end up with the older payload.
type liveSync struct {
	src       bundleSource
	guestRoot string

	mu     sync.Mutex
	last   [32]byte
	synced bool
}

func newLiveSync(src bundleSource, guestRoot string) *liveSync {
	return &liveSync{src: src, guestRoot: guestRoot}
}

// writeBundle writes a new bundle carrying the host's current overrides.
func (s *liveSync) writeBundle(b inject.GuestBundle, owner bundleOwner) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	overrides, err := s.hostOverrides()
	if err != nil {
		return err
	}

	return writeBundle(s.src, b, owner, overrides)
}

// tick rewrites every bundle's live area when the host's overrides changed
// since the last tick. Failures are reported once per change rather than
// retried every second: a bundle that cannot take the area, such as one from
// before it existed, would otherwise log forever.
func (s *liveSync) tick() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	overrides, err := s.hostOverrides()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(overrides)
	if s.synced && sum == s.last {
		return nil
	}
	s.last, s.synced = sum, true

	area, err := guestdisk.Encode(overrides)
	if err != nil {
		return fmt.Errorf("host overrides %s: %w", hostOverridesPath(s.src.OverlayRoot), err)
	}
	bundles, err := s.bundles()
	if err != nil {
		return err
	}
	var errs []error
	for _, bundle := range bundles {
		if err := updateArea(bundle, area); err != nil {
			errs = append(errs, err)
		}
	}
	zap.L().Debug("synced host overrides into guest bundles",
		zap.String("sha256", hex.EncodeToString(sum[:])), zap.Int("bundles", len(bundles)-len(errs)))

	return errors.Join(errs...)
}

// run ticks until ctx is cancelled.
func (s *liveSync) run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if err := s.tick(); err != nil {
			zap.L().Warn("could not sync host overrides into KubeVirt guest bundles", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// hostOverrides reads the host's override file; no file is no overrides.
func (s *liveSync) hostOverrides() ([]byte, error) {
	data, err := os.ReadFile(hostOverridesPath(s.src.OverlayRoot))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read host overrides: %w", err)
	}

	return data, nil
}

// bundles lists the bundle files under the guest root, by the same rule the
// sweep uses, so nothing the plugin did not create is ever written.
func (s *liveSync) bundles() ([]string, error) {
	entries, err := os.ReadDir(s.guestRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.guestRoot, err)
	}
	var bundles []string
	for _, entry := range entries {
		if !entry.IsDir() || !isBundleDirName(entry.Name()) {
			continue
		}
		path := filepath.Join(s.guestRoot, entry.Name(), BundleFile)
		if _, err := os.Stat(path); err == nil {
			bundles = append(bundles, path)
		}
	}

	return bundles, nil
}

func updateArea(bundle string, area []byte) error {
	f, err := os.OpenFile(bundle, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := guestdisk.WriteAt(f, area); err != nil {
		_ = f.Close() // the write error is the one to report
		return err
	}

	return f.Close()
}
