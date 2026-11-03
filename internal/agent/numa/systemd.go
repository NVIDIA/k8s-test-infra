// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cri"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// SystemdRunner runs the host helper through a linked oneshot service.
type SystemdRunner struct{ host *host.Host }

// NewSystemdRunner constructs a runner without changing the node.
func NewSystemdRunner(h *host.Host) *SystemdRunner { return &SystemdRunner{host: h} }

// Run executes the requested NUMA action in the node's mount namespace.
func (r *SystemdRunner) Run(ctx context.Context, action HostAction, managed bool) error {
	// systemctl uses PID 1's private socket, including on nodes without D-Bus.
	// A linked oneshot runs the mount in the node's mount namespace.
	const unit = "mokka-numa-setup.service"
	if err := fsutil.Write(r.host.RootPath("numa", unit), []byte(setupService(action, managed)), 0644); err != nil {
		return err
	}
	for _, command := range [][]string{{"link", "--force", hostDir + "/" + unit}, {"daemon-reload"}, {"start", unit}} {
		if err := cri.Systemctl(ctx, cri.Chroot{Root: r.host.Prefix}, command...); err != nil {
			return err
		}
	}
	return nil
}

var _ HostRunner = (*SystemdRunner)(nil)
