// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import "fmt"

// HostAction identifies a supported operation of the node-side NUMA helper.
type HostAction string

// Supported node-side NUMA operations.
const (
	ActionApply   HostAction = "apply"
	ActionRestore HostAction = "restore"
	ActionMount   HostAction = "mount"
)

// ParseHostAction validates an action received from the helper command line.
func ParseHostAction(value string) (HostAction, error) {
	action := HostAction(value)
	switch action {
	case ActionApply, ActionRestore, ActionMount:
		return action, nil
	default:
		return "", fmt.Errorf("unknown NUMA host action %q", value)
	}
}
