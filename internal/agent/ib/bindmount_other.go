// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package ib

import "errors"

// The bind mounts exist to serve a node's sysfs into pods, which only Linux
// has. The rest of the package builds and tests on a developer's workstation,
// so these stubs keep it compiling there rather than hiding the whole
// simulator behind a build tag.

var errNotLinux = errors.New("sysfs class bind mounts require Linux")

func bindReadOnly(_, _ string) error { return errNotLinux }

func unmountAt(_ string) error { return errNotLinux }
