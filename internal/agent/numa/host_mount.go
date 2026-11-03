// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"fmt"
)

const kubeletConfig = "/var/lib/kubelet/config.yaml"

func (s *hostSetup) mountTree(ctx context.Context) error {
	mounted, err := s.mounted(s.host.SysPath("devices/system/node"))
	if err != nil {
		return err
	}
	if !mounted {
		if err := s.run(ctx, "mount", "--make-rprivate", s.host.Sys); err != nil {
			return err
		}
	}
	return s.mountReadOnly(ctx, s.host.RootPath("numa/sysnode"), s.host.SysPath("devices/system/node"))
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
	if err := s.run(ctx, "mount", "--bind", source, target); err != nil {
		return err
	}
	return s.run(ctx, "mount", "-o", "remount,bind,ro", target)
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
