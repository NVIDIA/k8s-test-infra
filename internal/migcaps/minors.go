// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package migcaps renders the MIG capability tables a MIG-enabled NVIDIA
// driver publishes, for a node that has no kernel driver at all.
//
// A consumer that has found a MIG device through NVML cannot use it until it
// can find the device node guarding it. nvidia-container-toolkit resolves that
// by reading /proc/driver/nvidia-caps/mig-minors, mapping a capability name
// such as "gpu0/gi1/ci0/access" to a minor, and injecting
// /dev/nvidia-caps/nvidia-cap<minor> into the container. This package owns the
// naming and the minor allocation for both halves so they cannot disagree.
//
// The consumer's parser is nvidia-container-toolkit's internal/nvcaps; its
// accepted grammar is what minors_test.go asserts against.
package migcaps

import (
	"fmt"
	"strings"
)

// Cap is one line of the mig-minors table: a capability name and the minor of
// the /dev/nvidia-caps node that guards it.
type Cap struct {
	Name  string
	Minor int
}

// GPU is one physical GPU's partitioning. Minor is the GPU's device-node minor
// (the N in /dev/nvidiaN), which is how the capability names identify it.
type GPU struct {
	Minor        int
	GPUInstances []GPUInstance
}

// GPUInstance is one MIG GPU instance and the compute instances inside it,
// identified by the IDs NVML reports for them.
type GPUInstance struct {
	ID                 uint32
	ComputeInstanceIDs []uint32
}

// The driver reserves the first two minors for the node-wide capabilities.
const (
	configMinor  = 1
	monitorMinor = 2
)

// Caps allocates a minor to every MIG capability on the node, in GPU then GPU
// instance then compute instance order.
//
// Only live partitions get an entry. A real driver's table also lists the slots
// a board could be partitioned into, but a consumer only ever looks up a
// capability for a MIG device NVML has already handed it, so the live set is
// what it can ask for.
//
// A node with nothing partitioned has no table at all, not an empty one: the
// file is absent on a driver where MIG was never enabled, and the consumer
// reads its absence as "not a MIG machine".
func Caps(gpus []GPU) []Cap {
	if !anyPartitioned(gpus) {
		return nil
	}

	caps := []Cap{{Name: "config", Minor: configMinor}, {Name: "monitor", Minor: monitorMinor}}
	next := monitorMinor + 1

	for _, gpu := range gpus {
		for _, gi := range gpu.GPUInstances {
			caps = append(caps, Cap{Name: gpuInstanceCap(gpu.Minor, gi.ID), Minor: next})
			next++
			for _, ci := range gi.ComputeInstanceIDs {
				caps = append(caps, Cap{Name: computeInstanceCap(gpu.Minor, gi.ID, ci), Minor: next})
				next++
			}
		}
	}
	return caps
}

// Minors renders the mig-minors file body.
func Minors(caps []Cap) string {
	var b strings.Builder
	for _, c := range caps {
		fmt.Fprintf(&b, "%s %d\n", c.Name, c.Minor)
	}
	return b.String()
}

// gpuInstanceCap and computeInstanceCap spell capability names the way
// nvidia-container-toolkit's NewGPUInstanceCap and NewComputeInstanceCap do.
func gpuInstanceCap(gpuMinor int, gi uint32) string {
	return fmt.Sprintf("gpu%d/gi%d/access", gpuMinor, gi)
}

func computeInstanceCap(gpuMinor int, gi, ci uint32) string {
	return fmt.Sprintf("gpu%d/gi%d/ci%d/access", gpuMinor, gi, ci)
}

func anyPartitioned(gpus []GPU) bool {
	for _, gpu := range gpus {
		if len(gpu.GPUInstances) > 0 {
			return true
		}
	}
	return false
}
