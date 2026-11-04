// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cri"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/kubelet"
	"go.uber.org/zap"
)

var checkpointFiles = []string{"cpu_manager_state", "memory_manager_state"}

type hostSetup struct {
	host        *host.Host
	run         func(context.Context, string, ...string) error
	mounted     func(string) (bool, error)
	mountedFrom func(string, string) (bool, error)
	waitHealthy func(context.Context) error
}

func newHostSetup(h *host.Host) *hostSetup {
	s := &hostSetup{host: h, mounted: fsutil.IsMounted, mountedFrom: fsutil.IsBindMounted}
	s.run = func(ctx context.Context, name string, args ...string) error {
		chroot := cri.Chroot{Root: h.Prefix}
		if name == "systemctl" {
			return cri.Systemctl(ctx, chroot, args...)
		}
		cmd := exec.CommandContext(ctx, name, args...)
		// Mount paths are in the pod view; only systemctl needs the host root.
		cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %v: %w: %s", name, args, err, output)
		}
		return nil
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
	backup := s.host.RunPath("mokka/numa/original")
	managed, err := s.restoreKubeletState(ctx, backup, managed)
	if err != nil {
		return err
	}
	if err := s.unmountTree(ctx); err != nil {
		return err
	}
	if err := s.finishRestore(ctx, managed); err != nil {
		return err
	}
	// Keep recovery copies until restoration and the health check succeeded.
	if err := clearApplied(s.host); err != nil {
		return err
	}
	return os.RemoveAll(backup)
}

func (s *hostSetup) restoreKubeletState(ctx context.Context, backup string, managed bool) (bool, error) {
	saved, err := fileExists(filepath.Join(backup, "kubelet-config.yaml"))
	if err != nil {
		return false, err
	}
	registered, err := kubelet.BootResetRegistered(s.host)
	if err != nil {
		return false, err
	}
	managed = managed || saved || registered
	if !managed {
		return false, nil
	}
	if err := s.systemctl(ctx, "stop", "kubelet"); err != nil {
		return false, err
	}
	if err := s.unmountOwned(ctx, s.host.RootPath("numa/kubelet-config.yaml"), s.host.HostPath(kubeletConfig)); err != nil {
		return false, err
	}
	if saved {
		if err := s.restoreCheckpoints(backup); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *hostSetup) finishRestore(ctx context.Context, managed bool) error {
	if !managed {
		return nil
	}
	if err := s.systemctl(ctx, "start", "kubelet"); err != nil {
		return err
	}
	if err := s.waitHealthy(ctx); err != nil {
		return err
	}
	return kubelet.RemoveBootReset(ctx, s.host, s.systemctl)
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
	backup := s.host.RunPath("mokka/numa/original")
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

func (s *hostSetup) apply(ctx context.Context, managed bool) error {
	desired, applied, err := s.prepareApply(managed)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}
	if err := fsutil.Write(s.host.RunPath(pendingRel), desired, 0600); err != nil {
		return err
	}
	zap.L().Info("applying node NUMA setup", zap.Bool("configure_kubelet", managed))
	applyErr := s.applySetup(ctx, managed)
	if applyErr == nil {
		applyErr = fsutil.Write(s.host.RunPath(appliedRel), desired, 0o600)
		if applyErr == nil {
			applyErr = fsutil.Remove(s.host.RunPath(pendingRel))
		}
	}
	if applyErr != nil {
		zap.L().Warn("NUMA setup failed; restoring original node setup", zap.Error(applyErr))
		// The failed setup may have consumed its deadline. Give rollback its own
		// bounded window so cancellation does not prevent recovery.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		restoreErr := s.restore(rollbackCtx, managed)
		if restoreErr != nil {
			return errors.Join(applyErr, fmt.Errorf("NUMA rollback failed; inspect %s: %w", s.host.RunPath("mokka/numa/original"), restoreErr))
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
	// A pending marker can mean kubelet was stopped before an interruption.
	// Only a completed setup may skip startup and its health check.
	applied, err := os.ReadFile(s.host.RunPath(appliedRel))
	if err != nil && !os.IsNotExist(err) {
		return nil, false, err
	}
	if bytes.Equal(applied, desired) {
		current, err := s.currentMounts(managed)
		if err != nil {
			return nil, false, err
		}
		if current {
			return desired, true, nil
		}
	}
	if managed {
		if _, err := os.Stat(s.host.RootPath("numa/kubelet-config.yaml")); err != nil {
			return nil, false, err
		}
	}
	return desired, false, nil
}

func (s *hostSetup) applySetup(ctx context.Context, managed bool) error {
	if managed {
		if err := s.prepareKubelet(ctx); err != nil {
			return err
		}
	}
	if err := s.mountTree(ctx); err != nil {
		return err
	}
	if !managed {
		return nil
	}
	return s.startSimulatedKubelet(ctx)
}

func (s *hostSetup) prepareKubelet(ctx context.Context) error {
	if err := s.systemctl(ctx, "stop", "kubelet"); err != nil {
		return err
	}
	if err := s.backupKubelet(); err != nil {
		return err
	}
	return kubelet.InstallBootReset(ctx, s.host, kubelet.BootResetPlan{NUMA: true}, s.systemctl)
}

func (s *hostSetup) startSimulatedKubelet(ctx context.Context) error {
	wasMounted, err := s.mountedFrom(s.host.RootPath("numa/kubelet-config.yaml"), s.host.HostPath(kubeletConfig))
	if err != nil {
		return err
	}
	if err := s.mountConfig(ctx); err != nil {
		return err
	}
	if !wasMounted {
		for _, name := range checkpointFiles {
			if err := fsutil.Remove(s.host.HostPath("/var/lib/kubelet/" + name)); err != nil {
				return err
			}
		}
	}
	if err := s.systemctl(ctx, "start", "kubelet"); err != nil {
		return err
	}
	return s.waitHealthy(ctx)
}

func (s *hostSetup) currentMounts(managed bool) (bool, error) {
	for _, mount := range topologyMounts {
		present, err := s.mountedFrom(s.host.RootPath(mount.source), s.host.SysPath(mount.target))
		if err != nil || !present {
			return false, err
		}
	}
	if !managed {
		return true, nil
	}
	return s.mountedFrom(s.host.RootPath("numa/kubelet-config.yaml"), s.host.HostPath(kubeletConfig))
}

func (s *hostSetup) restoreCheckpoints(backup string) error {
	for _, name := range checkpointFiles {
		if err := s.restoreCheckpoint(backup, name); err != nil {
			return err
		}
	}
	return nil
}
