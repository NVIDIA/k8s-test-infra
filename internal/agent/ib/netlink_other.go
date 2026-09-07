// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package ib

import "fmt"

// The links are kernel netdevs, which only Linux has. Stubbing them keeps the
// rest of the package building and testing on a developer's workstation
// instead of hiding the whole simulator behind a build tag.

func addDummyLink(name string) error {
	return fmt.Errorf("create netdev %s: netdevs require Linux", name)
}

func deleteLink(name string) error {
	return fmt.Errorf("remove netdev %s: netdevs require Linux", name)
}
