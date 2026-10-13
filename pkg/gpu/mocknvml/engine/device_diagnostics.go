// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// GetClkMonStatus reports a healthy clock monitor: no global fault and no
// faulted clock domain. Clock monitors arrived with Ampere, so earlier GPUs
// decline.
func (d *ConfigurableDevice) GetClkMonStatus() (nvml.ClkMonStatus, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.ClkMonStatus{}, ret
	}
	if !gpuarch.Arch(d.Config.Architecture).AtLeast(gpuarch.Ampere) {
		return nvml.ClkMonStatus{}, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlDeviceGetClkMonStatus -> no faults\n")
	return nvml.ClkMonStatus{}, nvml.SUCCESS
}

// GetBBXTimeData_v1 reports how long the driver has been loaded, read off the
// shared epoch NVLink counters accrue from, so every process agrees on it and
// it grows between invocations. Real hardware accumulates this over the
// board's whole life; the mock's board was new when the driver loaded.
func (d *ConfigurableDevice) GetBBXTimeData_v1() (nvml.BBXTimeData_v1, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.BBXTimeData_v1{}, ret
	}
	epoch, now := d.driverLoadedAt(), time.Now()
	if d.fabric != nil && d.fabric.now != nil {
		now = d.fabric.now()
	}
	seconds := max(now.Sub(epoch), 0) / time.Second
	debugLog("[NVML] nvmlDeviceGetBBXTimeData_v1 -> timeRun=%d\n", seconds)
	return nvml.BBXTimeData_v1{TimeRun: uint32(seconds)}, nvml.SUCCESS
}

func (d *ConfigurableDevice) driverLoadedAt() time.Time {
	if d.fabric != nil && !d.fabric.epoch.IsZero() {
		return d.fabric.epoch
	}
	return resolveCounterEpoch()
}

// GetLastBBXFlushTime answers NOT_READY: the black box has not been flushed
// during this driver run. Real hardware reports the same until its first
// flush, which is what the freshly booted A100, B200, L40S and T4 captures
// show; modelling a later flush would mean inventing the driver's schedule.
func (d *ConfigurableDevice) GetLastBBXFlushTime() (uint64, uint, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, 0, ret
	}
	return 0, 0, nvml.ERROR_NOT_READY
}

// ReadPRMCounters_v1 declines. PRM registers expose the physical layer of the
// GPU's NVLink ports (PPCNT and its siblings), which the mock has no model of.
func (d *ConfigurableDevice) ReadPRMCounters_v1([]nvml.PRMCounterId, int) ([]nvml.PRMCounter_v1, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	return nil, nvml.ERROR_NOT_SUPPORTED
}

// ReadWritePRM_v1 declines for the reason ReadPRMCounters_v1 does.
func (d *ConfigurableDevice) ReadWritePRM_v1(*nvml.PRMTLV_v1) nvml.Return {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return ret
	}
	return nvml.ERROR_NOT_SUPPORTED
}

// ClearFieldValues declines each field on its own while the call succeeds, the
// per-value contract nvml.h documents. The mock derives its counters from the
// profile and the shared epoch instead of accumulating them, so none has state
// a caller could reset.
func (d *ConfigurableDevice) ClearFieldValues(values []nvml.FieldValue) nvml.Return {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return ret
	}
	ts := time.Now().UnixMicro()
	for i := range values {
		values[i].Timestamp = ts
		values[i].NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
	}
	debugLog("[NVML] nvmlDeviceClearFieldValues(count=%d) -> each NOT_SUPPORTED\n", len(values))
	return nvml.SUCCESS
}
