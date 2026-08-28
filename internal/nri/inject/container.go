// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import "strings"

// Container is the subset of container and pod state the steps read.
type Container struct {
	Namespace      string
	PodAnnotations map[string]string
	Env            []string
	Mounts         []Mount

	// Devices and CDIDevices are what the container already carries when the
	// runtime asks the plugin to adjust it. The kubelet applies the device
	// plugin's Allocate response before this point, so a non-empty NVIDIA entry
	// here means the device plugin already served this container. See MEP-0002.
	Devices    []Device
	CDIDevices []string
}

// annotated reports whether the pod set annotation to value, case-insensitively.
func (c Container) annotated(annotation, value string) bool {
	return strings.EqualFold(c.PodAnnotations[annotation], value)
}

// Adjustment is the mount/env/device delta that a runtime plugin applies.
type Adjustment struct {
	Mounts  []Mount
	Env     []string
	Devices []Device
	// CDIDevices are fully-qualified CDI device references the runtime resolves
	// itself. Devices and CDIDevices are alternatives for the GPU tree, never
	// both: emitting the same GPUs twice would widen the container and defeat
	// the mock engine's detectVisibleDevices filter.
	CDIDevices []string
}

// Mount describes a bind mount in a runtime-neutral form.
type Mount struct {
	Source      string
	Destination string
	Type        string
	Options     []string
}

// Device describes a host device node made visible in the container.
type Device struct {
	HostPath string
	Path     string
}
