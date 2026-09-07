// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import "fmt"

// netdevNames lists the interfaces the HCAs are associated with. The PCI
// renderer writes its net/<ifname> entries from the same prefix and count, so
// the sysfs association and the kernel links cannot drift apart.
func netdevNames(prefix string, count int) []string {
	names := make([]string, 0, count)

	for i := range count {
		names = append(names, fmt.Sprintf("%s%d", prefix, i))
	}

	return names
}

// hostNetnsRelPath locates the node's network namespace through the host
// procfs the agent mounts. PID 1 is the node's init, so its namespace is the
// one every host-networked consumer resolves interfaces in.
const hostNetnsRelPath = "1/ns/net"

// ensureNetdevs brings the interfaces into existence in the node's network
// namespace. An HCA is only a candidate to a consumer that can resolve its
// interface, and that lookup goes over rtnetlink — no rendered sysfs file can
// answer it. The links are plain dummy netdevs: nothing here needs RDMA kernel
// support, only a name the kernel knows.
func ensureNetdevs(nsPath, prefix string, count int) error {
	names := netdevNames(prefix, count)
	if len(names) == 0 {
		// A profile with no HCAs asks for no interfaces, so there is no reason
		// to enter the node's namespace — nor to fail where entering it cannot
		// work at all, as on a developer's workstation.
		return nil
	}

	return addDummyLinks(nsPath, names)
}

// removeNetdevs takes the interfaces down again, tolerating any that are
// already gone so a repeated teardown stays quiet.
func removeNetdevs(nsPath, prefix string, count int) error {
	names := netdevNames(prefix, count)
	if len(names) == 0 {
		return nil
	}

	return deleteLinks(nsPath, names)
}
