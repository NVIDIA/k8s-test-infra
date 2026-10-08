// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cri sets up a node's container runtime for Mokka's CDI specs, as
// MEP-0006 describes: it installs the NVIDIA Container Toolkit's runtime and
// hook on the node, and has the toolkit's nvidia-ctk register the nvidia
// handler with the container runtime. Each container runtime implements
// Runtime in a subpackage; containerd is the first.
package cri

import (
	"context"
	"errors"
	"fmt"
)

const (
	// HandlerName is the runtime handler Mokka registers: the name of the GPU
	// Operator's RuntimeClass.
	HandlerName = "nvidia"
	// BinaryName is the runtime the handler runs, at the path a package
	// install uses.
	BinaryName = "/usr/bin/nvidia-container-runtime"
)

// Runtime registers Mokka's runtime handler with one container runtime, as
// nvidia-ctk-installer's runtime packages do for the toolkit's.
type Runtime interface {
	// Installed returns nil when the runtime is installed on this node, and
	// otherwise an error saying what is missing. A node without it is left
	// alone.
	Installed() error
	// Setup makes the runtime serve the handler, restarting it when its
	// configuration changed.
	Setup(ctx context.Context) error
	// Cleanup removes Mokka's configuration and restarts the runtime when it
	// removed anything.
	Cleanup(ctx context.Context) error
}

// ErrRestartPending means the configuration is written but, with restart
// mode none, the running daemon does not use it until someone restarts the
// runtime.
var ErrRestartPending = errors.New("configuration written; restart the container runtime to apply it")

// RestartMode is how a runtime picks up a configuration change.
type RestartMode string

const (
	// RestartSystemd restarts the runtime's unit through the host's systemd.
	RestartSystemd RestartMode = "systemd"
	// RestartNone writes the configuration and leaves the restart to the
	// operator.
	RestartNone RestartMode = "none"
)

// ParseRestartMode validates a restart mode from configuration.
func ParseRestartMode(s string) (RestartMode, error) {
	switch m := RestartMode(s); m {
	case RestartSystemd, RestartNone:
		return m, nil
	default:
		return "", fmt.Errorf("unknown restart mode %q (want %q or %q)", s, RestartSystemd, RestartNone)
	}
}

// RuntimeOptions are what every Runtime shares, as nvidia-ctk-installer's
// container.Options are. Paths are host paths. The chart mounts the runtime's
// configuration directory at the same path in this container, so the imports
// nvidia-ctk writes resolve on the host. An empty one takes the runtime's
// default.
type RuntimeOptions struct {
	// NvidiaCTK is the nvidia-ctk binary that writes the configuration.
	NvidiaCTK string
	// Chroot runs systemctl on the host.
	Chroot Chroot

	ConfigPath  string
	ConfigDir   string
	Unit        string
	RestartMode RestartMode
}
