// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Where the writes NVML setters make are recorded.
//
// On real hardware a cap or a profile request is driver state: set it in one
// process and every other process on the node sees it. Each consumer loads its
// own copy of the mock, so state kept on ConfigurableDevice was private to the
// writer and a read-after-write across two processes reported the old value.
// The override document is the only state those processes share, so that is
// where the writes go.
//
// The engine declares the port and the bridge installs the implementation,
// rather than calling the writer directly, because the writer lives in mockctl
// and mockctl imports this package.

import (
	"sync/atomic"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// OverrideWriter records setter writes in the shared override document.
type OverrideWriter interface {
	// SetPowerLimit records a power cap, in milliwatts, for one device.
	SetPowerLimit(index int, milliwatts uint32) error

	// SetPersistenceMode records whether persistence mode is enabled.
	SetPersistenceMode(index int, enabled bool) error

	// SetDrainState records whether the device is draining.
	SetDrainState(index int, draining bool) error

	// SetRemoved records whether the device was removed from the driver.
	SetRemoved(index int, removed bool) error

	// UpdateWorkloadProfiles replaces a device's requested profile list with
	// the one apply computes from the list already recorded.
	//
	// The caller passes apply rather than a finished list because the NVML
	// operations are read-modify-write, and the base has to be read under the
	// same lock as the write or concurrent callers lose an update. present
	// separates "nothing has been written" from "written, then cleared": only
	// the first may fall back to the profile's configured request.
	UpdateWorkloadProfiles(index int, apply func(base []uint32, present bool) ([]uint32, error)) error

	// SetNvlinkBwMode records an NVLink Reduced Bandwidth Mode. allDevices
	// selects the `all:` bucket, which is how the node-wide NVML pair writes
	// a value that is not about one device; otherwise it applies to index.
	SetNvlinkBwMode(index int, mode uint8, allDevices bool) error

	// SetNvlinkLowPowerThreshold records an NVLink low-power threshold for one
	// device. A nil threshold clears the recorded value, which is what the
	// setter's reset sentinel asks for — the field has a driver default to
	// fall back to, so clearing it is meaningful rather than ambiguous.
	SetNvlinkLowPowerThreshold(index int, threshold *uint32) error

	// SetApplicationsClocks records the memory and graphics applications
	// clocks for one device.
	SetApplicationsClocks(index int, memMHz, graphicsMHz uint32) error

	// SetLockedClocks records the range one clock domain is locked to. A nil
	// range clears the lock, which is what the reset setters ask for: unlocked
	// is the absence of a range, not a range of its own.
	SetLockedClocks(index int, domain ClockDomain, r *ClockRangeConfig) error

	// SetClockOffset records the offset applied to one clock domain.
	SetClockOffset(index int, domain ClockDomain, offsetMHz int32) error

	// UpdateFanControl replaces a device's per-fan control state with the one
	// apply computes from the state already recorded.
	//
	// apply is passed the whole list rather than one fan's entry because the
	// fan setters address a single fan while the recorded state covers every
	// one, so the fold has to happen under the write's lock for the same
	// reason UpdateWorkloadProfiles does. present separates "nothing has been
	// written" from "written, then emptied"; only the first falls back to the
	// control state the profile declares.
	UpdateFanControl(index int, apply func(base []FanControlConfig, present bool) ([]FanControlConfig, error)) error

	// SetPowerMizerMode records the PowerMizer mode, by its config name, for
	// one device.
	SetPowerMizerMode(index int, mode string) error

	// SetPowerSmoothingState records whether power smoothing is enabled.
	SetPowerSmoothingState(index int, enabled bool) error

	// SetPowerSmoothingActiveProfile records the preset profile in force.
	SetPowerSmoothingActiveProfile(index int, profileID uint32) error

	// SetPowerSmoothingProfileParam records one parameter of one preset
	// profile. The value has already been validated for param.
	SetPowerSmoothingProfileParam(index int, profileID uint32, param PowerSmoothingParam, value float64) error

	// SetAccountingMode records whether per-process accounting is enabled.
	// Disabling also empties the terminated records, because turning
	// accounting off clears every record the driver held.
	SetAccountingMode(index int, enabled bool) error

	// ClearAccountingPids empties the records of processes that have exited.
	ClearAccountingPids(index int) error
}

// ClockDomain names a clock domain the locked-clock and offset setters write
// under: the key of its locked_<domain> range and of its offsets.<domain> entry.
type ClockDomain string

// The clock domains a setter can write. Graphics covers the SM clock too,
// which is derived from the GPC clock it names.
const (
	ClockDomainGraphics ClockDomain = "graphics"
	ClockDomainMemory   ClockDomain = "memory"
)

// overrideWriterRef is atomic because it is installed once at load time but
// read by every setter call, which NVML makes from arbitrary consumer threads.
var overrideWriterRef atomic.Pointer[OverrideWriter]

// SetOverrideWriter installs the writer the setters persist through. A nil
// writer uninstalls, which is what a test restoring the previous one passes.
func SetOverrideWriter(w OverrideWriter) {
	if w == nil {
		overrideWriterRef.Store(nil)
		return
	}
	overrideWriterRef.Store(&w)
}

// overrideWriter returns the installed writer, or nil when a consumer links the
// engine without the bridge that installs one.
func overrideWriter() OverrideWriter {
	if w := overrideWriterRef.Load(); w != nil {
		return *w
	}
	return nil
}

// recordSetterWrite runs one setter write against the override writer and
// invalidates the cache so the write is visible to the next read in this
// process. A setter with nowhere to record has failed and must say so.
func recordSetterWrite(op string, write func(OverrideWriter) error) nvml.Return {
	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] %s -> NO_PERMISSION (no override writer)\n", op)
		return nvml.ERROR_NO_PERMISSION
	}
	if err := write(w); err != nil {
		warnLog("[NVML] %s -> NO_PERMISSION: %v\n", op, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] %s\n", op)
	return nvml.SUCCESS
}
