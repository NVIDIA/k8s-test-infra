// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package config holds the settings the node agent and the NRI plugin share.
// The chart passes both the same value, so both parse it here.
package config

import (
	"fmt"
	"strings"
)

// DriverMode is how a node's NVIDIA driver is simulated.
type DriverMode string

const (
	// DriverModeUserspace simulates the whole driver: the node agent stages
	// its userspace, fakes the files its kernel modules show, and publishes
	// the driver root at /run/nvidia/driver. Any node kernel will do.
	DriverModeUserspace DriverMode = "userspace"
	// DriverModeKernel has a driver installer, such as the GPU Operator's
	// driver container, load Mokka's simulated kernel modules, so the kernel
	// serves the driver's files itself. The installer owns the driver root,
	// and the node kernel must build out-of-tree modules.
	DriverModeKernel DriverMode = "kernel"
)

// ParseDriverMode validates a mode, case-insensitively. Empty means
// DriverModeUserspace; an unknown value is an error rather than a silent
// fallback.
func ParseDriverMode(s string) (DriverMode, error) {
	switch m := DriverMode(strings.ToLower(strings.TrimSpace(s))); m {
	case DriverModeUserspace, DriverModeKernel:
		return m, nil
	case "":
		return DriverModeUserspace, nil
	default:
		return "", fmt.Errorf("invalid driver mode %q: expected userspace or kernel", s)
	}
}
