// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Wiring the engine's setter writes to the override document, so a cap or a
// profile request set in one process is visible to every other one — which is
// how the same write behaves against a real driver.
//
// The engine declares the port and this installs the implementation because the
// writer lives in mockctl, and mockctl imports the engine. It is the same
// document, lock and writer `nvml-mock-ctl` and the GPU reset in gpu_reset.go
// use, rather than a second way of mutating one file.
package main

import (
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func init() {
	engine.SetOverrideWriter(overrideWriter{})
}

type overrideWriter struct{}

// SetPowerLimit records the cap nvmlDeviceSetPowerManagementLimit applied.
func (overrideWriter) SetPowerLimit(index int, milliwatts uint32) error {
	return mockctl.SetPowerLimit(engine.ConfigOverridePath(), index, milliwatts)
}

// UpdateWorkloadProfiles records the request the workload profile setters left
// behind, folding the update in under the same lock that guards the write.
func (overrideWriter) UpdateWorkloadProfiles(
	index int, apply func(base []uint32, present bool) ([]uint32, error),
) error {
	return mockctl.UpdateWorkloadProfiles(engine.ConfigOverridePath(), index, apply)
}

// SetNvlinkBwMode records the mode nvmlDeviceSetNvlinkBwMode or
// nvmlSystemSetNvlinkBwMode applied.
func (overrideWriter) SetNvlinkBwMode(index int, mode uint8, allDevices bool) error {
	return mockctl.SetNvlinkBwMode(engine.ConfigOverridePath(), index, mode, allDevices)
}

// SetNvlinkLowPowerThreshold records the threshold
// nvmlDeviceSetNvLinkDeviceLowPowerThreshold applied.
func (overrideWriter) SetNvlinkLowPowerThreshold(index int, threshold *uint32) error {
	return mockctl.SetNvlinkLowPowerThreshold(engine.ConfigOverridePath(), index, threshold)
}
