// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kubelet

import (
	"context"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const (
	// HelperPath is staged on the node so boot cleanup needs no running Mokka pod.
	HelperPath  = "/var/lib/nvml-mock/kubelet/mokka-kubelet-helper"
	serviceName = "mokka-kubelet-reset.service"
	dropinPath  = "systemd/system/kubelet.service.d/30-mokka-reset.conf"
)

const resetService = `[Unit]
Description=Reset Mokka kubelet state before node startup
RequiresMountsFor=/var/lib/kubelet /var/lib/nvml-mock/kubelet
Before=kubelet.service
[Service]
Type=oneshot
ExecStart=` + HelperPath + ` reset-for-boot
RemainAfterExit=yes
`

const kubeletDependency = `[Unit]
Requires=mokka-kubelet-reset.service
After=mokka-kubelet-reset.service
`

// InstallBootReset registers the small boot task before simulation is applied.
func InstallBootReset(ctx context.Context, h *host.Host, plan BootResetPlan, systemctl func(context.Context, ...string) error) error {
	if _, err := os.Stat(h.HostPath(HelperPath)); err != nil {
		return err
	}
	if err := fsutil.Write(h.EtcPath("systemd/system/"+serviceName), []byte(resetService), 0644); err != nil {
		return err
	}
	if err := ArmBootReset(h, plan); err != nil {
		return err
	}
	if err := fsutil.Write(h.EtcPath(dropinPath), []byte(kubeletDependency), 0644); err != nil {
		return err
	}
	return systemctl(ctx, "daemon-reload")
}

// RemoveBootReset removes ordering only after restoration has succeeded.
func RemoveBootReset(ctx context.Context, h *host.Host, systemctl func(context.Context, ...string) error) error {
	if err := fsutil.Remove(h.EtcPath(dropinPath)); err != nil {
		return err
	}
	// Reload before stopping the dependency so kubelet is not stopped with it.
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	_, err := os.Stat(h.EtcPath("systemd/system/" + serviceName))
	if err == nil {
		if err := systemctl(ctx, "stop", serviceName); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := fsutil.Remove(h.EtcPath("systemd/system/" + serviceName)); err != nil {
		return err
	}
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	return DisarmBootReset(h)
}
