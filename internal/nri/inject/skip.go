// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

// skip reports whether the container must be left exactly as authored.
//
// The mount check is what makes re-adjustment safe: a container that already
// carries the overlay at its destination has been through here before, and
// injecting a second time would stack duplicate LD_PRELOAD entries.
func skip(cfg Config, container Container) bool {
	if container.annotated(cfg.OptOutAnnotation, "false") {
		return true
	}
	for _, namespace := range cfg.ExcludedNamespaces {
		if container.Namespace == namespace {
			return true
		}
	}
	for _, mount := range container.Mounts {
		if mount.Destination == cfg.ContainerOverlayPath {
			return true
		}
	}
	return false
}
