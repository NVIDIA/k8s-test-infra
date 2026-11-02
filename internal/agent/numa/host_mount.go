// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"bytes"
	"context"
	"errors"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const (
	hostHelper     = hostDir + "/numa-host"
	filesystemUnit = "mokka-numa-filesystem.service"
	kubeletDropin  = "systemd/system/kubelet.service.d/30-mokka-numa.conf"
)

const filesystemService = `[Unit]
Description=Mokka NUMA filesystem simulation
After=local-fs.target
Before=kubelet.service
[Service]
Type=oneshot
ExecStart=` + hostHelper + ` mount
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
`

const kubeletDependency = `[Unit]
Requires=mokka-numa-filesystem.service
After=mokka-numa-filesystem.service
`

func (s *hostSetup) ownMount() (bool, error) {
	data, err := os.ReadFile(s.host.SysPath("devices/system/node/.mokka-numa"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(bytes.TrimSpace(data)) == "mokka-numa-v1", nil
}

func (s *hostSetup) mountTree(ctx context.Context) error {
	target := s.host.SysPath("devices/system/node")
	mounted, err := s.mounted(target)
	if err != nil {
		return err
	}
	if mounted {
		ours, err := s.ownMount()
		if err != nil {
			return err
		}
		if !ours {
			return errors.New("refusing to replace an unmanaged NUMA mount")
		}
		return nil
	}
	// Kind nodes share the VM's sysfs. New mounts must stay inside this node.
	for _, args := range [][]string{
		{"--make-rprivate", s.host.Sys},
		{"--bind", s.host.RootPath("numa/sysnode"), target},
		{"-o", "remount,bind,ro", target},
	} {
		if err := s.run(ctx, "mount", args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *hostSetup) removeMount(ctx context.Context) error {
	if err := fsutil.Remove(s.host.EtcPath(kubeletDropin)); err != nil {
		return err
	}
	unit := s.host.EtcPath("systemd/system/" + filesystemUnit)
	exists, err := fileExists(unit)
	if err != nil {
		return err
	}
	if exists {
		if err := s.systemctl(ctx, "disable", "--now", filesystemUnit); err != nil {
			return err
		}
	}
	ours, err := s.ownMount()
	if err != nil {
		return err
	}
	if ours {
		if err := s.run(ctx, "umount", s.host.SysPath("devices/system/node")); err != nil {
			return err
		}
	}
	if err := fsutil.Remove(unit); err != nil {
		return err
	}
	return s.systemctl(ctx, "daemon-reload")
}
