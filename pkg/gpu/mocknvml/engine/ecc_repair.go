// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Memory repair state and the ECC setters.
//
// A health agent deciding between a reset, a row remap and a replacement reads
// the repair status, the unrepairable-memory flag and the SRAM error counts
// together. They are all derived from the ECC, row-remap and SRAM state the
// profile and the failure injector already describe, so a fault moves every
// one of them consistently and nothing here keeps state of its own.

import (
	"hash/crc32"
	"math"
	"strings"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// The unit index NVML reports an SRAM entry under. The driver's numbering is
// not public, so these name the source buckets ecc.sram.uncorrectable_sources
// declares, which are the only attribution the mock has.
const (
	sramUnitL2 uint32 = iota
	sramUnitSM
	sramUnitMicrocontroller
	sramUnitPCIe
	sramUnitOther
)

// EccCounterScope names the ECC counter set a clear applies to: the key it is
// stored under in ecc.errors and ecc.sram.
type EccCounterScope string

const (
	// EccScopeVolatile counts since the last driver reload.
	EccScopeVolatile EccCounterScope = "volatile"
	// EccScopeAggregate counts for the life of the board, as persisted in the
	// InfoROM.
	EccScopeAggregate EccCounterScope = "aggregate"
)

func (d *ConfigurableDevice) archAtLeast(floor gpuarch.Arch) bool {
	return gpuarch.Arch(d.Config.Architecture).AtLeast(floor)
}

// repairState is what the remapper and the SRAM counters say about the memory,
// before any architecture gate: a repair is pending when the profile says so or
// an injected uncorrectable error has just queued one, and the memory is
// unrepairable once the remapper has failed or the SRAM passed its threshold,
// the fault class a row remap cannot fix.
func (d *ConfigurableDevice) repairState() (repairPending, unrepairable bool) {
	cfg := d.cfg()
	if rows := cfg.RemappedRows; rows != nil {
		repairPending = rows.Pending
		unrepairable = rows.FailureOccurred
	}
	if fi := d.failureInjector(); fi != nil && fi.IsECCUncorrectable() {
		repairPending = true
		unrepairable = true
	}
	if cfg.ECC != nil && cfg.ECC.SRAM != nil && cfg.ECC.SRAM.ThresholdExceeded {
		unrepairable = true
	}
	return repairPending, unrepairable
}

// GetRepairStatus reports whether a memory repair is waiting for the next GPU
// reset. The mock models channel repair through the row remapper; no profile
// field describes TPC repair, so it is never pending.
func (d *ConfigurableDevice) GetRepairStatus() (nvml.RepairStatus, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.RepairStatus{}, ret
	}
	if !d.archAtLeast(gpuarch.Ampere) {
		debugLog("[NVML] nvmlDeviceGetRepairStatus -> NOT_SUPPORTED (before Ampere)\n")
		return nvml.RepairStatus{}, nvml.ERROR_NOT_SUPPORTED
	}
	pending, _ := d.repairState()
	var status nvml.RepairStatus
	if pending {
		status.BChannelRepairPending = 1
	}
	debugLog("[NVML] nvmlDeviceGetRepairStatus -> channelPending=%d\n", status.BChannelRepairPending)
	return status, nvml.SUCCESS
}

// GetUnrepairableMemoryFlag reports whether the memory has a fault only a
// replacement fixes. The returned struct carries no version: the caller's ABI
// is the bridge's concern.
func (d *ConfigurableDevice) GetUnrepairableMemoryFlag() (nvml.UnrepairableMemoryStatus_v1, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.UnrepairableMemoryStatus_v1{}, ret
	}
	if !d.archAtLeast(gpuarch.Hopper) {
		debugLog("[NVML] nvmlDeviceGetUnrepairableMemoryFlag -> NOT_SUPPORTED (before Hopper)\n")
		return nvml.UnrepairableMemoryStatus_v1{}, nvml.ERROR_NOT_SUPPORTED
	}
	_, unrepairable := d.repairState()
	var status nvml.UnrepairableMemoryStatus_v1
	if unrepairable {
		status.BUnrepairableMemory = 1
	}
	debugLog("[NVML] nvmlDeviceGetUnrepairableMemoryFlag -> %d\n", status.BUnrepairableMemory)
	return status, nvml.SUCCESS
}

// sramUniqueEntries describes the aggregate uncorrectable SRAM errors as one
// entry per unit that reported any. The profile carries per-unit totals and no
// addresses, so an entry names its unit and count and leaves the location
// fields zero.
func (d *ConfigurableDevice) sramUniqueEntries() []nvml.EccSramUniqueUncorrectedErrorEntry_v1 {
	cfg := d.cfg()
	if cfg.ECC == nil || cfg.ECC.SRAM == nil || cfg.ECC.SRAM.UncorrectableSources == nil {
		return nil
	}
	s := cfg.ECC.SRAM.UncorrectableSources
	var entries []nvml.EccSramUniqueUncorrectedErrorEntry_v1
	for _, src := range []struct {
		unit  uint32
		count uint64
	}{
		{sramUnitL2, s.L2},
		{sramUnitSM, s.SM},
		{sramUnitMicrocontroller, s.Microcontroller},
		{sramUnitPCIe, s.PCIe},
		{sramUnitOther, s.Other},
	} {
		if src.count == 0 {
			continue
		}
		entries = append(entries, nvml.EccSramUniqueUncorrectedErrorEntry_v1{
			Unit:  src.unit,
			Count: uint32(min(src.count, math.MaxUint32)),
		})
	}
	return entries
}

// GetSramUniqueUncorrectedEccErrorCounts follows NVML's two-call protocol: a
// nil Entries asks for the size, and a buffer too small for the current errors
// gets INSUFFICIENT_SIZE with the size it needs, since errors can arrive
// between the two calls.
func (d *ConfigurableDevice) GetSramUniqueUncorrectedEccErrorCounts(counts *nvml.EccSramUniqueUncorrectedErrorCounts) nvml.Return {
	if counts == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if !d.archAtLeast(gpuarch.Blackwell) {
		debugLog("[NVML] nvmlDeviceGetSramUniqueUncorrectedEccErrorCounts -> NOT_SUPPORTED (before Blackwell)\n")
		return nvml.ERROR_NOT_SUPPORTED
	}
	// As for the SRAM status, counters of a GPU with ECC off would claim
	// healthy SRAM the driver is not watching.
	if current, _, _ := d.GetEccMode(); current != nvml.FEATURE_ENABLED {
		debugLog("[NVML] nvmlDeviceGetSramUniqueUncorrectedEccErrorCounts -> NOT_SUPPORTED (ECC disabled)\n")
		return nvml.ERROR_NOT_SUPPORTED
	}

	entries := d.sramUniqueEntries()
	needed := uint32(len(entries))
	capacity := counts.EntryCount
	counts.EntryCount = needed
	if counts.Entries == nil {
		return nvml.SUCCESS
	}
	if capacity < needed {
		return nvml.ERROR_INSUFFICIENT_SIZE
	}
	if needed > 0 {
		copy(unsafe.Slice(counts.Entries, needed), entries)
	}
	debugLog("[NVML] nvmlDeviceGetSramUniqueUncorrectedEccErrorCounts -> %d entries\n", needed)
	return nvml.SUCCESS
}

// ValidateInforom reports the InfoROM as intact whenever the profile has one.
// Content beyond that is not modelled, so corruption cannot be injected here.
func (d *ConfigurableDevice) ValidateInforom() nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if d.cfg().InfoROM == nil {
		debugLog("[NVML] nvmlDeviceValidateInforom -> NOT_SUPPORTED (no inforom configured)\n")
		return nvml.ERROR_NOT_SUPPORTED
	}
	return nvml.SUCCESS
}

// GetInforomConfigurationChecksum returns a checksum of the InfoROM versions the
// profile declares: stable for one profile, and different for another, which
// is all a consumer comparing checksums across GPUs or reboots relies on.
func (d *ConfigurableDevice) GetInforomConfigurationChecksum() (uint32, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, ret
	}
	rom := d.cfg().InfoROM
	if rom == nil {
		debugLog("[NVML] nvmlDeviceGetInforomConfigurationChecksum -> NOT_SUPPORTED (no inforom configured)\n")
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	// The separator keeps ("ab", "") and ("a", "b") apart.
	sum := crc32.ChecksumIEEE([]byte(strings.Join(
		[]string{rom.ImageVersion, rom.OEMObject, rom.ECCObject, rom.PWRObject}, "\x00")))
	debugLog("[NVML] nvmlDeviceGetInforomConfigurationChecksum -> %#x\n", sum)
	return sum, nvml.SUCCESS
}

// dramEncryption returns the configured DRAM encryption modes, disabled unless
// the profile says otherwise.
func (d *ConfigurableDevice) dramEncryption() (current, pending nvml.EnableState) {
	current, pending = nvml.FEATURE_DISABLED, nvml.FEATURE_DISABLED
	if c := d.cfg(); c.ECC != nil && c.ECC.DRAMEncryption != nil {
		if c.ECC.DRAMEncryption.ModeCurrent == "enabled" {
			current = nvml.FEATURE_ENABLED
		}
		if c.ECC.DRAMEncryption.ModePending == "enabled" {
			pending = nvml.FEATURE_ENABLED
		}
	}
	return current, pending
}

// GetDramEncryptionMode returns the current and pending DRAM encryption modes.
// Only Blackwell and newer boards have the feature.
func (d *ConfigurableDevice) GetDramEncryptionMode() (nvml.DramEncryptionInfo, nvml.DramEncryptionInfo, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.DramEncryptionInfo{}, nvml.DramEncryptionInfo{}, ret
	}
	if !d.archAtLeast(gpuarch.Blackwell) {
		debugLog("[NVML] nvmlDeviceGetDramEncryptionMode -> NOT_SUPPORTED (before Blackwell)\n")
		return nvml.DramEncryptionInfo{}, nvml.DramEncryptionInfo{}, nvml.ERROR_NOT_SUPPORTED
	}
	current, pending := d.dramEncryption()
	debugLog("[NVML] nvmlDeviceGetDramEncryptionMode -> current=%d pending=%d\n", current, pending)
	return nvml.DramEncryptionInfo{EncryptionState: uint32(current)},
		nvml.DramEncryptionInfo{EncryptionState: uint32(pending)}, nvml.SUCCESS
}

// SetDramEncryptionMode records the mode the next reboot applies. The current
// mode stays what it is.
func (d *ConfigurableDevice) SetDramEncryptionMode(info *nvml.DramEncryptionInfo) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if !d.archAtLeast(gpuarch.Blackwell) {
		debugLog("[NVML] nvmlDeviceSetDramEncryptionMode -> NOT_SUPPORTED (before Blackwell)\n")
		return nvml.ERROR_NOT_SUPPORTED
	}
	if info == nil || !validEnableState(nvml.EnableState(info.EncryptionState)) {
		debugLog("[NVML] nvmlDeviceSetDramEncryptionMode -> INVALID_ARGUMENT\n")
		return nvml.ERROR_INVALID_ARGUMENT
	}
	enabled := nvml.EnableState(info.EncryptionState) == nvml.FEATURE_ENABLED
	return recordSetterWrite("nvmlDeviceSetDramEncryptionMode",
		func(w OverrideWriter) error { return w.SetDramEncryptionPending(d.PhysicalIndex(), enabled) })
}

// SetEccMode records the ECC mode the next GPU reset applies. The current mode
// stays what it is, as on hardware.
func (d *ConfigurableDevice) SetEccMode(mode nvml.EnableState) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if !validEnableState(mode) {
		debugLog("[NVML] nvmlDeviceSetEccMode(%d) -> INVALID_ARGUMENT\n", mode)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return recordSetterWrite("nvmlDeviceSetEccMode",
		func(w OverrideWriter) error {
			return w.SetEccModePending(d.PhysicalIndex(), mode == nvml.FEATURE_ENABLED)
		})
}

// ClearEccErrorCounts zeroes one scope of ECC counters. The SRAM counters go
// with the DRAM ones because both are error counters of the same scope. Clearing
// the aggregate scope also drops the unit attribution and the SRAM threshold
// flag: both are derived from the counters, so they cannot outlive them.
func (d *ConfigurableDevice) ClearEccErrorCounts(counterType nvml.EccCounterType) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	var scope EccCounterScope
	switch counterType {
	case nvml.VOLATILE_ECC:
		scope = EccScopeVolatile
	case nvml.AGGREGATE_ECC:
		scope = EccScopeAggregate
	default:
		debugLog("[NVML] nvmlDeviceClearEccErrorCounts(%d) -> INVALID_ARGUMENT\n", counterType)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return recordSetterWrite("nvmlDeviceClearEccErrorCounts",
		func(w OverrideWriter) error { return w.ClearEccErrorCounts(d.PhysicalIndex(), scope) })
}

// EccClearPatch is the override a counter clear writes. The zeros are explicit
// because the counters may come from the profile, and only a written zero
// outranks them in the merge.
func EccClearPatch(scope EccCounterScope) map[string]any {
	location := func() map[string]any {
		return map[string]any{
			"device_memory": 0, "l1_cache": 0, "l2_cache": 0,
			"register_file": 0, "texture_memory": 0, "total": 0,
		}
	}
	sram := map[string]any{
		string(scope): map[string]any{
			"correctable": 0, "uncorrectable_parity": 0, "uncorrectable_secded": 0,
		},
	}
	if scope == EccScopeAggregate {
		sram["uncorrectable_sources"] = map[string]any{
			"l2": 0, "sm": 0, "microcontroller": 0, "pcie": 0, "other": 0,
		}
		sram["threshold_exceeded"] = false
	}
	return map[string]any{"ecc": map[string]any{
		"errors": map[string]any{
			string(scope): map[string]any{"single_bit": location(), "double_bit": location()},
		},
		"sram": sram,
	}}
}

func validEnableState(s nvml.EnableState) bool {
	return s == nvml.FEATURE_ENABLED || s == nvml.FEATURE_DISABLED
}
