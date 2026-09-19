// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject decides what the nvml-mock overlay adds to a container. It is
// the runtime-neutral half of the NRI plugin: no containerd types cross this
// boundary, so the decision is exercisable as a plain table test.
//
// Every step fails open. A surface the node agent has not staged yet degrades
// the injection instead of failing container creation, because nothing orders
// the plugin's DaemonSet after the agent's.
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
	if reason, skipped := skip(cfg, container); skipped {
		zap.L().Debug("skipping container injection", zap.String("namespace", container.Namespace), zap.String("reason", reason))
		return Adjustment{}, false
	}

	selected := selectSurfaces(cfg, container)
	if !selected.any() {
		zap.L().Debug("skipping container injection",
			zap.String("namespace", container.Namespace),
			zap.String("reason", "no allocated or explicitly requested devices"))
		return Adjustment{}, false
	}

	var adjustment Adjustment
	adjustSelectedSurfaces(cfg, container, selected, &adjustment)
	attachIMEXChannels(cfg, container, &adjustment)

	zap.L().Debug("injecting container",
		zap.String("namespace", container.Namespace),
		zap.Int("mounts", len(adjustment.Mounts)),
		zap.Int("devices", len(adjustment.Devices)))

	return adjustment, true
}

type selectedSurfaces struct {
	gpuAllocated bool
	allGPUs      bool
	infiniBand   bool
	imexChannels bool
}

func selectSurfaces(cfg Config, container Container) selectedSurfaces {
	return selectedSurfaces{
		gpuAllocated: hasAllocatedGPU(container),
		allGPUs:      container.annotated(cfg.DeviceAnnotation, "true"),
		infiniBand: hasAllocatedInfiniBand(container) ||
			container.annotated(cfg.InfiniBandAnnotation, "true"),
		imexChannels: container.annotated(cfg.ImexChannelAnnotation, "true"),
	}
}

func (s selectedSurfaces) gpu() bool {
	return s.gpuAllocated || s.allGPUs
}

func (s selectedSurfaces) any() bool {
	return s.gpu() || s.infiniBand || s.imexChannels
}

func adjustSelectedSurfaces(cfg Config, container Container, selected selectedSurfaces, adjustment *Adjustment) {
	// IMEX is independently gated. A channel-only container needs the channel
	// nodes, not the GPU driver overlay: mounting the overlay would make mock
	// NVML interpret the absence of /dev/nvidiaN as permission to enumerate the
	// entire node.
	if selected.gpu() || selected.infiniBand {
		mountOverlay(cfg, adjustment)
		setEnvironment(cfg, container, selected.gpu(), selected.infiniBand, adjustment)
	}
	if selected.gpu() {
		attachGPUs(cfg, container, adjustment)
		attachAllocatedGPUControls(cfg, container, adjustment)
	}
}

func hasAllocatedInfiniBand(container Container) bool {
	for _, device := range container.Devices {
		if filepath.Dir(device.Path) == "/dev/infiniband" {
			return true
		}
	}
	return false
}

// skip reports whether the container must be left exactly as authored, and why.
//
// The mount check is what makes re-adjustment safe: a container that already
// carries the overlay at its destination has been through here before, and
// injecting a second time would stack duplicate LD_PRELOAD entries.
func skip(cfg Config, container Container) (reason string, ok bool) {
	if container.annotated(cfg.OptOutAnnotation, "false") {
		return "opt-out annotation", true
	}
	for _, namespace := range cfg.ExcludedNamespaces {
		if container.Namespace == namespace {
			return "excluded namespace", true
		}
	}
	for _, mount := range container.Mounts {
		if mount.Destination == cfg.ContainerOverlayPath {
			return "overlay already mounted", true
		}
	}
	return "", false
}

// mountOverlay binds the staged mock driver tree into the container, read-only
// except for the config directory.
//
// Eligibility is decided before this function runs. The overlay is mounted for
// a selected GPU or InfiniBand surface, never merely because the container
// happens to run on a simulated node.
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
