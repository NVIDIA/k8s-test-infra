// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// DialContext connects to a literal IP address in the node's network namespace.
// Only socket creation needs that namespace; HTTP can use the connection from
// ordinary pod threads without changing the agent's network configuration.
func (h *Host) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var conn net.Conn
	err := runOnDisposableThread(func() error {
		ns, err := os.Open(h.ProcPath("1/ns/net"))
		if err != nil {
			return fmt.Errorf("open node network namespace: %w", err)
		}
		defer func() { _ = ns.Close() }() // Read-only namespace descriptor.
		if err := unix.Setns(int(ns.Fd()), unix.CLONE_NEWNET); err != nil {
			return fmt.Errorf("enter node network namespace: %w", err)
		}
		conn, err = (&net.Dialer{}).DialContext(ctx, network, address)
		return err
	})
	return conn, err
}
