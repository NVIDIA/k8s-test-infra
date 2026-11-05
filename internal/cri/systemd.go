// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cri

import "context"

// systemdEnv lets a chrooted systemctl act on the host's systemd instead of
// refusing to run "in a chroot": SYSTEMD_IN_CHROOT=0 since systemd 257, and
// SYSTEMD_IGNORE_CHROOT=1, which it deprecates, before.
var systemdEnv = []string{"SYSTEMD_IN_CHROOT=0", "SYSTEMD_IGNORE_CHROOT=1"}

// Systemctl runs the host's systemctl with args, as in
// Systemctl(ctx, c, "restart", "containerd").
func Systemctl(ctx context.Context, c Chroot, args ...string) error {
	_, err := c.Run(ctx, systemdEnv, "systemctl", args...)

	return err
}
