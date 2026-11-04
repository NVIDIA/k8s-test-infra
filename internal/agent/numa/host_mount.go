// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const kubeletConfig = "/var/lib/kubelet/config.yaml"

var topologyMounts = [...]struct{ source, target string }{
	{"numa/sysnode", "devices/system/node"},
	{"numa/sysbusnode", "bus/node/devices"},
}

func (s *hostSetup) mountTree(ctx context.Context) error {
	for _, mount := range topologyMounts {
		if err := s.mountReadOnly(ctx, s.host.RootPath(mount.source), s.host.SysPath(mount.target)); err != nil {
			return err
		}
	}
	return nil
}

func (s *hostSetup) unmountTree(ctx context.Context) error {
	for i := len(topologyMounts) - 1; i >= 0; i-- {
		mount := topologyMounts[i]
		if err := s.unmountOwned(ctx, s.host.RootPath(mount.source), s.host.SysPath(mount.target)); err != nil {
			return err
		}
	}
	return nil
}

func (s *hostSetup) mountConfig(ctx context.Context) error {
	return s.mountReadOnly(ctx, s.host.RootPath("numa/kubelet-config.yaml"), s.host.HostPath(kubeletConfig))
}

func (s *hostSetup) mountReadOnly(ctx context.Context, source, target string) error {
	ours, err := s.mountedFrom(source, target)
	if err != nil || ours {
		return err
	}
	mounted, err := s.mounted(target)
	if err != nil {
		return err
	}
	if mounted {
		return fmt.Errorf("refusing to replace unmanaged NUMA mount at %s", target)
	}
	return s.publishMount(ctx, source, target)
}

func (s *hostSetup) unmountOwned(ctx context.Context, source, target string) error {
	mounted, err := s.mounted(target)
	if err != nil || !mounted {
		return err
	}
	ours, err := s.mountedFrom(source, target)
	if err != nil {
		return err
	}
	if !ours {
		return fmt.Errorf("refusing to remove unmanaged NUMA mount at %s", target)
	}
	return s.run(ctx, "umount", target)
}

// Remount flags do not propagate. Prepare a read-only mount under a private
// parent, then attach it so the node receives those flags with the mount.
func (s *hostSetup) publishMount(ctx context.Context, source, target string) (result error) {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	parent, err := os.MkdirTemp("", "mokka-numa-mount-")
	if err != nil {
		return err
	}
	stage := filepath.Join(parent, "mount")
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		result = errors.Join(result, s.cleanupMountStage(cleanupCtx, stage, parent))
	}()
	if info.IsDir() {
		err = os.Mkdir(stage, 0700)
	} else {
		err = fsutil.Write(stage, nil, 0600)
	}
	if err != nil {
		return err
	}
	for _, args := range [][]string{
		{"--bind", parent, parent},
		{"--make-private", parent},
		{"--bind", source, stage},
		{"--make-private", stage},
		{"-o", "remount,bind,ro", stage},
		{"--move", stage, target},
	} {
		if err := s.run(ctx, "mount", args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *hostSetup) cleanupMountStage(ctx context.Context, stage, parent string) error {
	for _, path := range []string{stage, parent} {
		mounted, err := s.mounted(path)
		if err != nil {
			return err
		}
		if mounted {
			if err := s.run(ctx, "umount", path); err != nil {
				return err
			}
		}
	}
	// Never delete staging directories while they still contain a live mount.
	return os.RemoveAll(parent)
}
