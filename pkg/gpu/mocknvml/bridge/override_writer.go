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

// SetPersistenceMode records the mode nvmlDeviceSetPersistenceMode applied.
func (overrideWriter) SetPersistenceMode(index int, enabled bool) error {
	return mockctl.SetPersistenceMode(engine.ConfigOverridePath(), index, enabled)
}

// SetDrainState records the state nvmlDeviceModifyDrainState applied.
func (overrideWriter) SetDrainState(index int, draining bool) error {
	return mockctl.SetDrainState(engine.ConfigOverridePath(), index, draining)
}

// SetRemoved records a GPU nvmlDeviceRemoveGpu detached or
// nvmlDeviceDiscoverGpus brought back.
func (overrideWriter) SetRemoved(index int, removed bool) error {
	return mockctl.SetRemoved(engine.ConfigOverridePath(), index, removed)
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

// SetApplicationsClocks records the pair nvmlDeviceSetApplicationsClocks or
// nvmlDeviceResetApplicationsClocks applied.
func (overrideWriter) SetApplicationsClocks(index int, memMHz, graphicsMHz uint32) error {
	return mockctl.SetApplicationsClocks(engine.ConfigOverridePath(), index, memMHz, graphicsMHz)
}

// SetLockedClocks records or clears the range a locked-clock setter applied.
func (overrideWriter) SetLockedClocks(index int, domain engine.ClockDomain, r *engine.ClockRangeConfig) error {
	return mockctl.SetLockedClocks(engine.ConfigOverridePath(), index, domain, r)
}

// SetClockOffset records the offset nvmlDeviceSetClockOffsets applied.
func (overrideWriter) SetClockOffset(index int, domain engine.ClockDomain, offsetMHz int32) error {
	return mockctl.SetClockOffset(engine.ConfigOverridePath(), index, domain, offsetMHz)
}

// UpdateFanControl records the policy and duty cycle the fan setters left
// behind, folding the update in under the same lock that guards the write.
func (overrideWriter) UpdateFanControl(
	index int, apply func(base []engine.FanControlConfig, present bool) ([]engine.FanControlConfig, error),
) error {
	return mockctl.UpdateFanControl(engine.ConfigOverridePath(), index, apply)
}

// SetPowerMizerMode records the mode nvmlDeviceSetPowerMizerMode_v1 applied.
func (overrideWriter) SetPowerMizerMode(index int, mode string) error {
	return mockctl.SetPowerMizerMode(engine.ConfigOverridePath(), index, mode)
}

// SetPowerSmoothingState records the state nvmlDevicePowerSmoothingSetState
// applied.
func (overrideWriter) SetPowerSmoothingState(index int, enabled bool) error {
	return mockctl.SetPowerSmoothingState(engine.ConfigOverridePath(), index, enabled)
}

// SetPowerSmoothingActiveProfile records the preset
// nvmlDevicePowerSmoothingActivatePresetProfile made active.
func (overrideWriter) SetPowerSmoothingActiveProfile(index int, profileID uint32) error {
	return mockctl.SetPowerSmoothingActiveProfile(engine.ConfigOverridePath(), index, profileID)
}

// SetPowerSmoothingProfileParam records the preset parameter
// nvmlDevicePowerSmoothingUpdatePresetProfileParam tuned.
func (overrideWriter) SetPowerSmoothingProfileParam(
	index int, profileID uint32, param engine.PowerSmoothingParam, value float64,
) error {
	return mockctl.SetPowerSmoothingProfileParam(engine.ConfigOverridePath(), index, profileID, param, value)
}

// SetAccountingMode records the mode nvmlDeviceSetAccountingMode applied.
func (overrideWriter) SetAccountingMode(index int, enabled bool) error {
	return mockctl.SetAccountingMode(engine.ConfigOverridePath(), index, enabled)
}

// ClearAccountingPids records the clear nvmlDeviceClearAccountingPids applied.
func (overrideWriter) ClearAccountingPids(index int) error {
	return mockctl.ClearAccountingPids(engine.ConfigOverridePath(), index)
}
