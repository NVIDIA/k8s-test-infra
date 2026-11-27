// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// GetBankRemapperStatus_v1 reports the DRAM bank remapper Rubin and later GPUs
// repair failing memory banks with. The state comes from the profile's
// bank_remapper block alone: an injected uncorrectable ECC error queues a row
// remap, which the row remapper already reports, and is not also counted as a
// bank remap. Without the block the device has a healthy remapper, nothing
// remapped and no pending remap, unless the profile marks absent features
// unsupported.
func (d *ConfigurableDevice) GetBankRemapperStatus_v1() (nvml.EccBankRemapperStatus_v1, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.EccBankRemapperStatus_v1{}, ret
	}
	if !d.archAtLeast(gpuarch.Rubin) {
		debugLog("[NVML] nvmlDeviceGetBankRemapperStatus_v1 -> NOT_SUPPORTED (before Rubin)\n")
		return nvml.EccBankRemapperStatus_v1{}, nvml.ERROR_NOT_SUPPORTED
	}
	cfg := d.cfg().BankRemapper
	if ret := d.unsupportedWhenAbsent("nvmlDeviceGetBankRemapperStatus_v1", cfg != nil); ret != nvml.SUCCESS {
		return nvml.EccBankRemapperStatus_v1{}, ret
	}
	var status nvml.EccBankRemapperStatus_v1
	if cfg != nil {
		status.ActiveRemappings = cfg.ActiveRemappings
		status.InactiveRemappings = cfg.InactiveRemappings
		status.BPending = uint32(boolField(cfg.Pending))
		if h := cfg.Histogram; h != nil {
			status.Histogram.MaxSpareGroupCount = h.MaxSpareGroups
			status.Histogram.NoSpareGroupCount = h.NoSpareGroups
		}
	}
	debugLog("[NVML] nvmlDeviceGetBankRemapperStatus_v1 -> active=%d inactive=%d pending=%d maxSpare=%d noSpare=%d\n",
		status.ActiveRemappings, status.InactiveRemappings, status.BPending,
		status.Histogram.MaxSpareGroupCount, status.Histogram.NoSpareGroupCount)
	return status, nvml.SUCCESS
}

func isBankRemapperField(fieldID uint32) bool {
	return fieldID >= fiActiveBankRemappings && fieldID <= fiPendingBankRemapping
}

// bankRemapperFieldValue resolves the NVML_FI_DEV_*BANK_REMAP* field ids from
// the same state as GetBankRemapperStatus_v1, so the two views cannot disagree.
// The driver reports every one of them as an unsigned long long.
func (d *ConfigurableDevice) bankRemapperFieldValue(fieldID uint32) (FieldValueType, uint64, nvml.Return, bool) {
	status, ret := d.GetBankRemapperStatus_v1()
	if ret != nvml.SUCCESS {
		return FieldValueUnsupported, 0, ret, true
	}
	var value uint32
	switch fieldID {
	case fiActiveBankRemappings:
		value = status.ActiveRemappings
	case fiInactiveBankRemappings:
		value = status.InactiveRemappings
	case fiBankRemapperHistogramMax:
		value = status.Histogram.MaxSpareGroupCount
	case fiBankRemapperHistogramNone:
		value = status.Histogram.NoSpareGroupCount
	default:
		value = status.BPending
	}
	return FieldValueUint64, uint64(value), nvml.SUCCESS, true
}
