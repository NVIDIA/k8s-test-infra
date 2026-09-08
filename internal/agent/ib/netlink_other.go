// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package ib

import "errors"

// The links are kernel netdevs in another network namespace, which only Linux
// has. Stubbing them keeps the rest of the package building and testing on a
// developer's workstation instead of hiding the whole simulator behind a build
// tag.

var errNetdevsRequireLinux = errors.New("netdevs require Linux")

func addDummyLinks(_ string, _ []string) error {
	return errNetdevsRequireLinux
}

func deleteLinks(_ string, _ []string) error {
	return errNetdevsRequireLinux
}
