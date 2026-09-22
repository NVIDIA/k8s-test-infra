// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package fsutil

import "errors"

// bindMount and lazyUnmount are Linux-only: mokka's node-agent only ever
// runs there. This file exists solely so the package still builds on a
// contributor's non-Linux dev machine.

func bindMount(_, _ string) error {
	return errors.New("fsutil: bind mounts are only supported on linux")
}

func lazyUnmount(_ string) error {
	return errors.New("fsutil: bind mounts are only supported on linux")
}
