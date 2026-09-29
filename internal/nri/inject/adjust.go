// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject decides what the nvml-mock overlay adds to a container. It is
// the runtime-neutral half of the NRI plugin: no containerd types cross this
// boundary, so the decision is exercisable as a plain table test.
//
// Every step fails open. A missing surface degrades injection instead of
// failing container creation. The runtime-facing plugin separately gates
// adjustments while the node agent is staging or unavailable.
package inject

import (
	"path/filepath"

	"go.uber.org/zap"
)

// Adjust returns what to add to a container, or ok=false when the container
// should be left exactly as authored.
//
// The steps run in a fixed order, each contributing to the same adjustment.
// None of them can fail, which is why there is no error to return.
func Adjust(cfg Config, container Container) (Adjustment, bool) {
	cfg = withDefaults(cfg)
	sel, reason, skipped := decide(cfg, container)
	if skipped {
		zap.L().Debug("skipping container injection", zap.String("namespace", container.Namespace), zap.String("reason", reason))
		return Adjustment{}, false
	}
	if sel.allocated && container.annotated(cfg.DeviceAnnotation, "true") {
		zap.L().Debug("GPU allocation takes precedence over the devices annotation",
			zap.String("namespace", container.Namespace))
	}

	// GPU and IMEX containers receive the same shared overlay and environment.
	var adjustment Adjustment
	mountOverlay(cfg, &adjustment)
	setEnvironment(cfg, container, &adjustment)
	if sel.management {
		attachGPUs(cfg, &adjustment)
	}
	attachIMEXChannels(cfg, container, &adjustment)

	zap.L().Debug("injecting container",
		zap.String("namespace", container.Namespace),
		zap.Int("mounts", len(adjustment.Mounts)),
		zap.Int("devices", len(adjustment.Devices)))

	return adjustment, true
}

// Skip reports whether Adjust would leave the container exactly as authored,
// and why. It reads only the container, so the plugin can ask before it
// consults the node agent.
func Skip(cfg Config, container Container) (reason string, skipped bool) {
	_, reason, skipped = decide(withDefaults(cfg), container)
	return reason, skipped
}

// selection records which surfaces a container asked for. It is derived from
// the container alone, never from the node's staged tree.
type selection struct {
	// allocated: the container holds a device-plugin or DRA GPU allocation,
	// which it keeps exactly.
	allocated bool
	// management: the devices annotation without an allocation, which
	// delivers every staged GPU.
	management bool
	imex       bool
}

// decide is the single source of truth shared by Adjust and Skip.
//
// The mount check is what makes re-adjustment safe: a container that already
// carries the overlay at its destination has been through here before, and
// injecting a second time would stack duplicate LD_PRELOAD entries.
func decide(cfg Config, container Container) (sel selection, reason string, skipped bool) {
	if reason, skipped := skip(cfg, container); skipped {
		return selection{}, reason, true
	}
	allocated := hasGPUAllocation(container)
	sel = selection{
		allocated:  allocated,
		management: container.annotated(cfg.DeviceAnnotation, "true") && !allocated,
		imex:       container.annotated(cfg.ImexChannelAnnotation, "true"),
	}
	if !sel.allocated && !sel.management && !sel.imex {
		return sel, "no GPU allocation and no opt-in annotation", true
	}
	if hasOverlay(cfg, container) {
		return sel, "overlay already mounted", true
	}
	return sel, "", false
}

// skip applies the explicit exclusions: the opt-out annotation and excluded
// namespaces.
func skip(cfg Config, container Container) (reason string, ok bool) {
	if container.annotated(cfg.OptOutAnnotation, "false") {
		return "opt-out annotation", true
	}
	for _, namespace := range cfg.ExcludedNamespaces {
		if container.Namespace == namespace {
			return "excluded namespace", true
		}
	}
	return "", false
}

func hasOverlay(cfg Config, container Container) bool {
	for _, mount := range container.Mounts {
		if mount.Destination == cfg.ContainerOverlayPath {
			return true
		}
	}
	return false
}

// mountOverlay binds the staged mock driver tree into the container, read-only
// except for the config directory.
//
// Selected GPU and IMEX containers receive this whole shared tree.
//
// The config directory is writable because the container writes back through
// it: the mock serves `nvidia-smi --gpu-reset` by clearing the device's bucket
// from overrides.yaml, under the same flock every other writer takes. With the
// whole overlay read-only that write failed with EROFS, so a reset reported
// "GPU Reset couldn't run" on exactly the GPUs that had state to clear, while
// healthy ones reported success by skipping the write. Layering a narrower
// writable bind over the read-only tree keeps the mock library and nvidia-smi
// below it immutable; the order matters, since the overlay would cover this
// mount if it came second.
func mountOverlay(cfg Config, adjustment *Adjustment) {
	adjustment.Mounts = append(adjustment.Mounts,
		Mount{
			Source:      cfg.HostOverlayPath,
			Destination: cfg.ContainerOverlayPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev"},
		},
		Mount{
			Source:      filepath.Join(cfg.HostOverlayPath, configRelPath),
			Destination: filepath.Join(cfg.ContainerOverlayPath, configRelPath),
			Type:        "bind",
			Options:     []string{"rbind", "rw", "nosuid", "nodev"},
		},
	)
}
