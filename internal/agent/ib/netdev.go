// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"errors"
	"fmt"
)

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

// ensureNetdevs brings the interfaces into existence. An HCA is only a
// candidate to a consumer that can resolve its interface, and that lookup goes
// over rtnetlink — no rendered sysfs file can answer it. The links are plain
// dummy netdevs: nothing here needs RDMA kernel support, only a name the
// kernel knows.
func ensureNetdevs(prefix string, count int) error {
	errs := make([]error, 0, count)

	for _, name := range netdevNames(prefix, count) {
		if err := addDummyLink(name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// removeNetdevs takes the interfaces down again, tolerating any that are
// already gone so a repeated teardown stays quiet.
func removeNetdevs(prefix string, count int) error {
	errs := make([]error, 0, count)

	for _, name := range netdevNames(prefix, count) {
		if err := deleteLink(name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
