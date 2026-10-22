// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Per-process accounting.
//
// The records are derived rather than stored. A running process is accounted
// from the process list, so its lifetime numbers agree with what the process
// utilization and running-process queries report for the same pid; a process
// that has exited comes from accounting.terminated. The two setters write to
// the override document, the only state every process on the node shares, so
// `nvidia-smi -am 1` followed by a separate `nvidia-smi` sees accounting on.

import (
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// defaultAccountingBufferSize is the record count reported when the profile
// does not declare one.
const defaultAccountingBufferSize = 4000

// accountingSampleInterval is the rate behind the v2 sample count. NVML does not
// document the driver's; one a second keeps sum / count / 100 equal to the
// lifetime utilization, which is the relationship a consumer relies on.
const accountingSampleInterval = time.Second

// GetAccountingMode reports whether per-process accounting is on. A MIG-enabled
// GPU reports it off whatever the profile says, as the NVML header documents.
func (d *ConfigurableDevice) GetAccountingMode() (nvml.EnableState, nvml.Return) {
	mode := nvml.FEATURE_DISABLED
	if d.accountingEnabled() {
		mode = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetAccountingMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// GetAccountingBufferSize returns how many records the circular buffer holds.
func (d *ConfigurableDevice) GetAccountingBufferSize() (int, nvml.Return) {
	size := d.accountingBufferSize()
	debugLog("[NVML] nvmlDeviceGetAccountingBufferSize -> %d\n", size)
	return size, nvml.SUCCESS
}

// SetAccountingMode turns accounting on or off. Turning it off also drops the
// terminated records, because disabling clears all accounting information.
// Re-enabling accounts the processes still running again; the driver would
// only track processes started after that, which the mock has no way to tell.
func (d *ConfigurableDevice) SetAccountingMode(mode nvml.EnableState) nvml.Return {
	if mode != nvml.FEATURE_ENABLED && mode != nvml.FEATURE_DISABLED {
		debugLog("[NVML] nvmlDeviceSetAccountingMode(%d) -> INVALID_ARGUMENT\n", mode)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if d.migPartitioned() {
		debugLog("[NVML] nvmlDeviceSetAccountingMode(%d) -> NOT_SUPPORTED (MIG enabled)\n", mode)
		return nvml.ERROR_NOT_SUPPORTED
	}
	enabled := mode == nvml.FEATURE_ENABLED
	return recordSetterWrite("nvmlDeviceSetAccountingMode",
		func(w OverrideWriter) error { return w.SetAccountingMode(d.PhysicalIndex(), enabled) })
}

// ClearAccountingPids drops the records of processes that have exited. Running
// processes stay queryable.
func (d *ConfigurableDevice) ClearAccountingPids() nvml.Return {
	if d.migPartitioned() {
		debugLog("[NVML] nvmlDeviceClearAccountingPids -> NOT_SUPPORTED (MIG enabled)\n")
		return nvml.ERROR_NOT_SUPPORTED
	}
	return recordSetterWrite("nvmlDeviceClearAccountingPids",
		func(w OverrideWriter) error { return w.ClearAccountingPids(d.PhysicalIndex()) })
}

// GetAccountingPids lists the processes with an accounting record, oldest
// first.
func (d *ConfigurableDevice) GetAccountingPids() ([]int, nvml.Return) {
	records, ret := d.accountingRecordsIfEnabled("nvmlDeviceGetAccountingPids")
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	pids := make([]int, len(records))
	for i, r := range records {
		pids[i] = int(r.Pid)
	}
	debugLog("[NVML] nvmlDeviceGetAccountingPids -> %v\n", pids)
	return pids, nvml.SUCCESS
}

// GetAccountingStats returns one process's accounting record.
func (d *ConfigurableDevice) GetAccountingStats(pid uint32) (nvml.AccountingStats, nvml.Return) {
	r, ret := d.accountingRecord("nvmlDeviceGetAccountingStats", pid)
	if ret != nvml.SUCCESS {
		return nvml.AccountingStats{}, ret
	}
	return nvml.AccountingStats{
		GpuUtilization:    r.GpuUtilization,
		MemoryUtilization: r.MemoryUtilization,
		MaxMemoryUsage:    r.MaxMemoryUsage,
		Time:              r.Time,
		StartTime:         r.StartTime,
		IsRunning:         r.IsRunning,
	}, nvml.SUCCESS
}

// GetAccountingStats_v2 returns one process's accounting record with the
// per-sample sums the v2 struct adds.
func (d *ConfigurableDevice) GetAccountingStats_v2(pid uint32) (nvml.AccountingStats_v2, nvml.Return) {
	return d.accountingRecord("nvmlDeviceGetAccountingStats_v2", pid)
}

func (d *ConfigurableDevice) accountingRecord(funcName string, pid uint32) (nvml.AccountingStats_v2, nvml.Return) {
	records, ret := d.accountingRecordsIfEnabled(funcName)
	if ret != nvml.SUCCESS {
		return nvml.AccountingStats_v2{}, ret
	}
	for _, r := range records {
		if r.Pid == pid {
			debugLog("[NVML] %s(pid=%d) -> running=%d gpu=%d%% mem=%d%%\n",
				funcName, pid, r.IsRunning, r.GpuUtilization, r.MemoryUtilization)
			return r, nvml.SUCCESS
		}
	}
	debugLog("[NVML] %s(pid=%d) -> NOT_FOUND\n", funcName, pid)
	return nvml.AccountingStats_v2{}, nvml.ERROR_NOT_FOUND
}

func (d *ConfigurableDevice) accountingRecordsIfEnabled(funcName string) ([]nvml.AccountingStats_v2, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	if !d.accountingEnabled() {
		debugLog("[NVML] %s -> NOT_SUPPORTED (accounting disabled)\n", funcName)
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	return d.accountingRecords(), nvml.SUCCESS
}

// accountingRecords is the buffer's content, oldest first: the terminated
// records, then the running processes, which are newer than anything that has
// already exited. A reused pid keeps only its latest record, and a full buffer
// has overwritten its oldest ones.
func (d *ConfigurableDevice) accountingRecords() []nvml.AccountingStats_v2 {
	c := d.cfg()
	start := d.driverLoadedAt()

	var records []nvml.AccountingStats_v2
	if c.Accounting != nil {
		for _, p := range c.Accounting.Terminated {
			lifetime := time.Duration(p.TimeMS) * time.Millisecond
			records = append(records,
				newAccountingRecord(p.PID, false, p.GPUUtil, p.MemUtil, p.MaxMemoryMiB, start, lifetime))
		}
	}
	running := max(time.Since(start), 0)
	for _, p := range c.Processes {
		records = append(records,
			newAccountingRecord(p.PID, true, p.SmUtil, p.MemUtil, p.UsedMemoryMiB, start, running))
	}

	records = latestRecordPerPid(records)
	if size := d.accountingBufferSize(); len(records) > size {
		records = records[len(records)-size:]
	}
	return records
}

// newAccountingRecord builds one record. Every process is taken to have started
// when the driver loaded: the process list carries no start time, and that is
// the one moment every consumer of the mock agrees on.
func newAccountingRecord(
	pid uint32, running bool, gpuUtil, memUtil uint32, maxMemoryMiB uint64, start time.Time, lifetime time.Duration,
) nvml.AccountingStats_v2 {
	samples := uint64(lifetime / accountingSampleInterval)
	r := nvml.AccountingStats_v2{
		Pid:               pid,
		GpuUtilization:    gpuUtil,
		MemoryUtilization: memUtil,
		MaxMemoryUsage:    maxMemoryMiB * oneMiB,
		SampleCount:       uint32(samples),
		SumGpuUtil:        uint64(gpuUtil) * 100 * samples,
		SumFbUtil:         uint64(memUtil) * 100 * samples,
		StartTime:         uint64(start.UnixMicro()),
	}
	// The run time is only known once the process has exited; NVML reports 0
	// until then.
	if running {
		r.IsRunning = 1
	} else {
		r.Time = uint64(lifetime.Milliseconds())
	}
	return r
}

func latestRecordPerPid(records []nvml.AccountingStats_v2) []nvml.AccountingStats_v2 {
	latest := make(map[uint32]int, len(records))
	for i, r := range records {
		latest[r.Pid] = i
	}
	out := make([]nvml.AccountingStats_v2, 0, len(latest))
	for i, r := range records {
		if latest[r.Pid] == i {
			out = append(out, r)
		}
	}
	return out
}

func (d *ConfigurableDevice) accountingEnabled() bool {
	if d.migPartitioned() {
		return false
	}
	c := d.cfg()
	return c.Accounting != nil && c.Accounting.Mode == "enabled"
}

func (d *ConfigurableDevice) accountingBufferSize() int {
	if c := d.cfg(); c.Accounting != nil && c.Accounting.BufferSize > 0 {
		return c.Accounting.BufferSize
	}
	return defaultAccountingBufferSize
}
