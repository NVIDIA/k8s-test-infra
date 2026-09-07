// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"errors"
	"fmt"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// addDummyLink creates the interface and brings it up, leaving an existing one
// alone: reconciles re-run, and a link an earlier pass created is the steady
// state rather than a conflict.
//
// Consumers read the interface's operational state to decide the HCA behind it
// is usable, so a link left down is as good as absent.
func addDummyLink(name string) error {
	attrs := netlink.NewLinkAttrs()
	attrs.Name = name

	err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: attrs})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create netdev %s: %w", name, err)
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("look up netdev %s: %w", name, err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring netdev %s up: %w", name, err)
	}

	return nil
}

// deleteLink removes the interface, treating an absent one as done.
func deleteLink(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		if errors.As(err, new(netlink.LinkNotFoundError)) {
			return nil
		}

		return fmt.Errorf("look up netdev %s: %w", name, err)
	}

	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("remove netdev %s: %w", name, err)
	}

	return nil
}
