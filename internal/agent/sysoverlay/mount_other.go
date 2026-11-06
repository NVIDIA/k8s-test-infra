// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package sysoverlay

import "errors"

// overmount and detach are Linux-only: mokka's node-agent only ever runs
// there. This file exists solely so the package still builds on a
// contributor's non-Linux dev machine.

func overmount(_, _ string) error {
	return errors.New("sysoverlay: mounts are only supported on linux")
}

func detach(_ string) error {
	return errors.New("sysoverlay: mounts are only supported on linux")
}
