// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"errors"
	"fmt"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// hostLinks opens a netlink handle onto the network namespace at nsPath.
//
// The links have to exist in the node's namespace, not the agent pod's: the
// consumers that resolve them run with host networking, and a link created in
// the pod's namespace is invisible to everything outside it.
//
// Reaching that namespace takes two capabilities, and the DaemonSet grants both
// alongside the host procfs mount nsPath resolves through. Entering it needs
// CAP_SYS_ADMIN. Merely opening the link that names it needs CAP_SYS_PTRACE,
// because the process it belongs to lives outside the agent's PID namespace and
// the kernel gates that read behind ptrace access — without it this fails at
// GetFromPath with EACCES, which reads as a mount problem and is not one.
func hostLinks(nsPath string) (*netlink.Handle, error) {
	ns, err := netns.GetFromPath(nsPath)
	if err != nil {
		return nil, fmt.Errorf("open network namespace %s: %w", nsPath, err)
	}

	defer func() { _ = ns.Close() }() // the handle keeps its own reference

	handle, err := netlink.NewHandleAt(ns)
	if err != nil {
		return nil, fmt.Errorf("enter network namespace %s: %w", nsPath, err)
	}

	return handle, nil
}

// addDummyLinks creates the interfaces and brings them up, leaving existing
// ones alone: reconciles re-run, and a link an earlier pass created is the
// steady state rather than a conflict. One namespace entry serves the whole
// set, since every link belongs to the same node.
//
// Consumers read an interface's operational state to decide the HCA behind it
// is usable, so a link left down is as good as absent. A name that fails is
// reported without abandoning the rest: the HCAs are independent, and one
// unusable link should not decide the others.
func addDummyLinks(nsPath string, names []string) error {
	handle, err := hostLinks(nsPath)
	if err != nil {
		return err
	}

	defer handle.Close()

	errs := make([]error, 0, len(names))

	for _, name := range names {
		if err := addDummyLink(handle, name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func addDummyLink(handle *netlink.Handle, name string) error {
	attrs := netlink.NewLinkAttrs()
	attrs.Name = name

	err := handle.LinkAdd(&netlink.Dummy{LinkAttrs: attrs})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create netdev %s: %w", name, err)
	}

	link, err := handle.LinkByName(name)
	if err != nil {
		return fmt.Errorf("look up netdev %s: %w", name, err)
	}

	if err := handle.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring netdev %s up: %w", name, err)
	}

	return nil
}

// deleteLinks removes the interfaces, treating absent ones as done.
func deleteLinks(nsPath string, names []string) error {
	handle, err := hostLinks(nsPath)
	if err != nil {
		return err
	}

	defer handle.Close()

	errs := make([]error, 0, len(names))

	for _, name := range names {
		if err := deleteLink(handle, name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func deleteLink(handle *netlink.Handle, name string) error {
	link, err := handle.LinkByName(name)
	if err != nil {
		if errors.As(err, new(netlink.LinkNotFoundError)) {
			return nil
		}

		return fmt.Errorf("look up netdev %s: %w", name, err)
	}

	if err := handle.LinkDel(link); err != nil {
		return fmt.Errorf("remove netdev %s: %w", name, err)
	}

	return nil
}
