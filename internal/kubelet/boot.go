// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package kubelet manages Mokka-owned kubelet integration on the node.
package kubelet

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"go.uber.org/zap"
)

const (
	planPath = "kubelet/boot-reset.json"
	donePath = "mokka/kubelet-reset.done"
)

// BootResetPlan identifies the features whose persistent state needs resetting.
type BootResetPlan struct {
	NUMA bool `json:"numa"`
}

// ArmBootReset registers cleanup before any simulation changes kubelet state.
func ArmBootReset(h *host.Host, plan BootResetPlan) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	// Kubelet restarts during installation must not trigger boot cleanup.
	if err := markBootResetCompleted(h); err != nil {
		return err
	}
	return fsutil.Write(h.RootPath(planPath), data, 0600)
}

// ResetForBoot prepares kubelet to start with its original, unmounted config.
// The node's /run is reset on boot; an ordinary pod or kubelet restart keeps it.
func ResetForBoot(h *host.Host) error {
	plan, err := readBootResetPlan(h)
	if err != nil || plan == nil {
		return err
	}
	completed, err := bootResetCompleted(h)
	if err != nil || completed {
		return err
	}
	if err := resetNUMAState(h, plan.NUMA); err != nil {
		return err
	}
	return markBootResetCompleted(h)
}

func readBootResetPlan(h *host.Host) (*BootResetPlan, error) {
	data, err := os.ReadFile(h.RootPath(planPath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan BootResetPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("decode kubelet boot reset plan: %w", err)
	}
	return &plan, nil
}

func bootResetCompleted(h *host.Host) (bool, error) {
	_, err := os.Stat(h.RunPath(donePath))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func resetNUMAState(h *host.Host, enabled bool) error {
	if !enabled {
		return nil
	}
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
	return fsutil.Remove(h.RootPath(planPath))
}

// BootResetRegistered reports whether Mokka has registered persistent cleanup.
func BootResetRegistered(h *host.Host) (bool, error) {
	plan, err := readBootResetPlan(h)
	return plan != nil, err
}
