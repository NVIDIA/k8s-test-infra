// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package kubelet manages Mokka-owned kubelet integration on the node.
package kubelet

import (
	"fmt"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"go.uber.org/zap"
)

const (
	enabledPath = "kubelet/boot-reset.enabled"
	donePath    = "mokka/kubelet-reset.done"
)

// ArmBootReset registers cleanup before any simulation changes kubelet state.
func ArmBootReset(h *host.Host) error {
	// Kubelet restarts during installation must not trigger boot cleanup.
	if err := markBootResetCompleted(h); err != nil {
		return err
	}
	return fsutil.Write(h.RootPath(enabledPath), nil, 0600)
}

// ResetForBoot prepares kubelet to start with its original, unmounted config.
// The node's /run is reset on boot; an ordinary pod or kubelet restart keeps it.
func ResetForBoot(h *host.Host) error {
	registered, err := BootResetRegistered(h)
	if err != nil || !registered {
		return err
	}
	completed, err := bootResetCompleted(h)
	if err != nil || completed {
		return err
	}
	if err := resetNUMAState(h); err != nil {
		return err
	}
	return markBootResetCompleted(h)
}

func bootResetCompleted(h *host.Host) (bool, error) {
	_, err := os.Stat(h.RunPath(donePath))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func resetNUMAState(h *host.Host) error {
	zap.L().Info("resetting NUMA resource-manager checkpoints for node boot")
	for _, name := range []string{"cpu_manager_state", "memory_manager_state"} {
		if err := fsutil.Remove(h.HostPath("/var/lib/kubelet/" + name)); err != nil {
			return fmt.Errorf("reset NUMA checkpoint %s: %w", name, err)
		}
	}
	return nil
}

func markBootResetCompleted(h *host.Host) error {
	return fsutil.Write(h.RunPath(donePath), []byte("done\n"), 0600)
}

// DisarmBootReset is called only after the original node setup is healthy.
func DisarmBootReset(h *host.Host) error {
	return fsutil.Remove(h.RootPath(enabledPath))
}

// BootResetRegistered reports whether Mokka has registered persistent cleanup.
func BootResetRegistered(h *host.Host) (bool, error) {
	info, err := os.Stat(h.RootPath(enabledPath))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect kubelet boot reset marker: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("kubelet boot reset marker is not a regular file: %s", h.RootPath(enabledPath))
	}
	return true, nil
}
