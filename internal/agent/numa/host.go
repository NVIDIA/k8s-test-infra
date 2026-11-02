// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cri"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"go.uber.org/zap"
)

var checkpointFiles = []string{"cpu_manager_state", "memory_manager_state"}

// RunHost performs NUMA setup in the node's own mount namespace. The node agent
// stages the statically linked numa-host binary and systemd starts it there.
func RunHost(ctx context.Context, action string, managed bool) error {
	setup := newHostSetup(host.New("/"))
	switch action {
	case "mount":
		return setup.mountTree(ctx)
	case "restore":
		return setup.restore(ctx, managed)
	case "apply":
		return setup.apply(ctx, managed)
	default:
		return fmt.Errorf("unknown NUMA host action %q", action)
	}
}

type hostSetup struct {
	host        *host.Host
	run         func(context.Context, string, ...string) error
	mounted     func(string) (bool, error)
	waitHealthy func(context.Context) error
}

func newHostSetup(h *host.Host) *hostSetup {
	s := &hostSetup{host: h, mounted: fsutil.IsMounted}
	s.run = func(ctx context.Context, name string, args ...string) error {
		chroot := cri.Chroot{Root: h.Prefix}
		if name == "systemctl" {
			return cri.Systemctl(ctx, chroot, args...)
		}
		_, err := chroot.Run(ctx, nil, name, args...)
		return err
	}

	s.waitHealthy = s.waitForKubelet
	return s
}

func (s *hostSetup) systemctl(ctx context.Context, args ...string) error {
	return s.run(ctx, "systemctl", args...)
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func copyIfPresent(src, dst string) (bool, error) {
	exists, err := fileExists(src)
	if err != nil || !exists {
		return false, err
	}
	return true, fsutil.Copy(src, dst, 0o600)
}

func (s *hostSetup) restore(ctx context.Context, managed bool) error {
	zap.L().Info("restoring original node NUMA setup")
	backup := s.host.RootPath("numa/original")
	restored, err := s.restoreKubelet(ctx, backup)
	if err != nil {
		return err
	}
	managed = managed || restored
	if err := s.removeMount(ctx); err != nil {
		return err
	}
	if managed {
		if err := s.systemctl(ctx, "restart", "kubelet"); err != nil {
			return err
		}
		if err := s.waitHealthy(ctx); err != nil {
			return err
		}
	}
	// Keep the only recovery copies and the applied marker until all recovery
	// steps, including a managed kubelet's health check, have succeeded.
	if err := fsutil.Remove(s.host.EtcPath("systemd/system/mokka-numa-setup.service")); err != nil {
		return err
	}
	if err := fsutil.Remove(s.host.RootPath("numa/applied")); err != nil {
		return err
	}
	return os.RemoveAll(backup)
}

func (s *hostSetup) restoreKubelet(ctx context.Context, backup string) (bool, error) {
	exists, err := fileExists(filepath.Join(backup, "kubelet-config.yaml"))
	if err != nil || !exists {
		return false, err
	}
	if err := s.systemctl(ctx, "stop", "kubelet"); err != nil {
		return false, err
	}
	if err := fsutil.Copy(filepath.Join(backup, "kubelet-config.yaml"), s.host.HostPath("/var/lib/kubelet/config.yaml"), 0o600); err != nil {
		return false, err
	}
	for _, state := range checkpointFiles {
		if err := s.restoreCheckpoint(backup, state); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *hostSetup) restoreCheckpoint(backup, state string) error {
	target := s.host.HostPath("/var/lib/kubelet/" + state)
	copied, err := copyIfPresent(filepath.Join(backup, state), target)
	if err != nil || copied {
		return err
	}
	absent, err := fileExists(filepath.Join(backup, state+".absent"))
	if err != nil || !absent {
		return err
	}
	return fsutil.Remove(target)
}

func (s *hostSetup) backupKubelet() error {
	backup := s.host.RootPath("numa/original")
	if err := s.backupFile(s.host.HostPath("/var/lib/kubelet/config.yaml"), filepath.Join(backup, "kubelet-config.yaml")); err != nil {
		return err
	}
	for _, state := range checkpointFiles {
		target := filepath.Join(backup, state)
		saved, err := fileExists(target)
		if err != nil {
			return err
		}
		absent, err := fileExists(target + ".absent")
		if err != nil {
			return err
		}
		if saved || absent {
			continue
		}
		copied, err := copyIfPresent(s.host.HostPath("/var/lib/kubelet/"+state), target)
		if err != nil {
			return err
		}
		if !copied {
			if err := fsutil.Write(target+".absent", nil, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *hostSetup) backupFile(src, dst string) error {
	exists, err := fileExists(dst)
	if err != nil || exists {
		return err
	}
	return fsutil.Copy(src, dst, 0o600)
}

func (s *hostSetup) installUnits(ctx context.Context) error {
	if err := fsutil.Write(s.host.EtcPath("systemd/system/"+filesystemUnit), []byte(filesystemService), 0o644); err != nil {
		return err
	}
	if err := fsutil.Write(s.host.EtcPath(kubeletDropin), []byte(kubeletDependency), 0o644); err != nil {
		return err
	}
	return s.systemctl(ctx, "daemon-reload")
}

func (s *hostSetup) apply(ctx context.Context, managed bool) error {
	desired, applied, err := s.prepareApply(managed)
	if err != nil {
		return err
	}
	if applied {
		// Upgrade persistent services from the old Bash helper without changing an
		// active layout, overwriting backups, or restarting kubelet.
		return s.installUnits(ctx)
	}
	zap.L().Info("applying node NUMA setup", zap.Bool("configure_kubelet", managed))
	applyErr := s.applySetup(ctx, managed)
	if applyErr == nil {
		applyErr = fsutil.Write(s.host.RootPath("numa/applied"), desired, 0o600)
	}
	if applyErr != nil {
		zap.L().Warn("NUMA setup failed; restoring original node setup", zap.Error(applyErr))
		// The failed setup may have consumed its deadline. Give rollback its own
		// bounded window so cancellation does not prevent recovery.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		restoreErr := s.restore(rollbackCtx, managed)
		if restoreErr != nil {
			return errors.Join(applyErr, fmt.Errorf("NUMA rollback failed; inspect %s: %w", s.host.RootPath("numa/original"), restoreErr))
		}
		return applyErr
	}
	return nil
}

func (s *hostSetup) prepareApply(managed bool) ([]byte, bool, error) {
	desired, err := os.ReadFile(s.host.RootPath("numa/desired"))
	if err != nil {
		return nil, false, err
	}
	if _, err := os.Stat(s.host.RootPath("numa/sysnode/online")); err != nil {
		return nil, false, err
	}
	applied, err := os.ReadFile(s.host.RootPath("numa/applied"))
	if err != nil && !os.IsNotExist(err) {
		return nil, false, err
	}
	if bytes.Equal(applied, desired) {
		return desired, true, nil
	}
	if managed {
		if _, err := os.Stat(s.host.RootPath("numa/kubelet-config.yaml")); err != nil {
			return nil, false, err
		}
		if err := s.backupKubelet(); err != nil {
			return nil, false, err
		}
	}
	return desired, false, nil
}

func (s *hostSetup) applySetup(ctx context.Context, managed bool) error {
	if err := s.installUnits(ctx); err != nil {
		return err
	}
	if err := s.systemctl(ctx, "enable", "--now", filesystemUnit); err != nil {
		return err
	}
	if err := s.mountTree(ctx); err != nil {
		return err
	}
	if !managed {
		return nil
	}
	if err := s.systemctl(ctx, "stop", "kubelet"); err != nil {
		return err
	}
	if err := fsutil.Copy(s.host.RootPath("numa/kubelet-config.yaml"), s.host.HostPath("/var/lib/kubelet/config.yaml"), 0o600); err != nil {
		return err
	}
	for _, state := range checkpointFiles {
		if err := fsutil.Remove(s.host.HostPath("/var/lib/kubelet/" + state)); err != nil {
			return err
		}
	}
	if err := s.systemctl(ctx, "start", "kubelet"); err != nil {
		return err
	}
	return s.waitHealthy(ctx)
}
