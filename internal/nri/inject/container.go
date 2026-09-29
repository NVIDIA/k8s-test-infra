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

	// IncomingDevices and DeviceRules describe the runtime spec before Mokka's
	// adjustment. A numbered node alone is not allocation evidence: privileged
	// containers can inherit it without asking the scheduler for a GPU.
	IncomingDevices []RuntimeDevice
	DeviceRules     []DeviceRule
	CDIDevices      []string
}

// RuntimeDevice is a device already present in the container spec.
type RuntimeDevice struct {
	Path  string
	Type  string
	Major int64
	Minor int64
}

// DeviceRule is a pre-existing cgroup rule. Nil numbers mean wildcards.
type DeviceRule struct {
	Allow  bool
	Type   string
	Major  *int64
	Minor  *int64
	Access string
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
