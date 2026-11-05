// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

// HostAction identifies a node operation performed by the NUMA simulator.
type HostAction string

// Host actions are executed by the privileged pod runner.
const (
	ActionApply   HostAction = "apply"
	ActionRestore HostAction = "restore"
)
