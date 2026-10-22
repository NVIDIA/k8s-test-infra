// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// accountingConfig runs one compute and one graphics process and remembers one
// that has already exited, so every query below has a running and a terminated
// record to tell apart. The numbers are distinct per field so a getter reading
// a neighbouring one fails rather than coincidentally agreeing.
func accountingConfig(mode string) *DeviceConfig {
	return &DeviceConfig{
		Accounting: &AccountingConfig{
			Mode:       mode,
			BufferSize: 4000,
			Terminated: []AccountedProcessConfig{
				{PID: 300, GPUUtil: 62, MemUtil: 31, MaxMemoryMiB: 1024, TimeMS: 120000},
			},
		},
		Processes: []ProcessConfig{
			{PID: 100, Type: "C", UsedMemoryMiB: 2048, SmUtil: 70, MemUtil: 30},
			{PID: 200, Type: "G", UsedMemoryMiB: 128, SmUtil: 5, MemUtil: 2},
		},
	}
}

// TestAccounting_DisabledModeRefusesTheQueries is what every shipped profile
// answers, and what `nvidia-smi --query-accounted-apps` renders as accounting
// being off rather than as an empty list.
func TestAccounting_DisabledModeRefusesTheQueries(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, accountingConfig("disabled"))

	_, ret := dev.GetAccountingPids()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetAccountingPids")
	_, ret = dev.GetAccountingStats(100)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetAccountingStats")
	_, ret = dev.GetAccountingStats_v2(100)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetAccountingStats_v2")
}

func TestGetAccountingPids_ListsTerminatedThenRunningProcesses(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	pids, ret := dev.GetAccountingPids()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []int{300, 100, 200}, pids,
		"the buffer runs oldest to newest, and a process still running is newer than one that exited")
}

// TestGetAccountingStats_RunningProcessMirrorsTheProcessList keeps accounting
// consistent with what pmon and the running-process lists report for the same
// pid: its lifetime utilization is the utilization configured for it.
func TestGetAccountingStats_RunningProcessMirrorsTheProcessList(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	stats, ret := dev.GetAccountingStats(100)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.AccountingStats{
		GpuUtilization:    70,
		MemoryUtilization: 30,
		MaxMemoryUsage:    2048 * oneMiB,
		Time:              0,
		StartTime:         uint64(dev.driverLoadedAt().UnixMicro()),
		IsRunning:         1,
	}, stats, "time stays 0 until the process exits")
}

func TestGetAccountingStats_TerminatedProcessReportsItsRunTime(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	stats, ret := dev.GetAccountingStats(300)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(0), stats.IsRunning)
	require.Equal(t, uint64(120000), stats.Time)
	require.Equal(t, uint32(62), stats.GpuUtilization)
	require.Equal(t, uint32(31), stats.MemoryUtilization)
	require.Equal(t, uint64(1024*oneMiB), stats.MaxMemoryUsage)
}

func TestGetAccountingStats_UnknownPidIsNotFound(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	_, ret := dev.GetAccountingStats(999)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// TestGetAccountingStats_V2SumsAgreeWithTheAverage pins the relationship a
// consumer recomputing the average from the v2 sums depends on: the sums are
// in pct*100 per sample, so sum / count / 100 is the lifetime utilization.
func TestGetAccountingStats_V2SumsAgreeWithTheAverage(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	stats, ret := dev.GetAccountingStats_v2(300)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(300), stats.Pid)
	require.Equal(t, uint32(0), stats.IsRunning)
	require.Equal(t, uint32(120), stats.SampleCount, "one sample a second over a 120 s run")
	require.Equal(t, uint64(62*100*120), stats.SumGpuUtil)
	require.Equal(t, uint64(31*100*120), stats.SumFbUtil)
	require.Equal(t, uint64(120000), stats.Time)
}

// TestGetAccountingPids_BufferDropsTheOldestRecords covers the circular
// buffer: once it is full, the records that go are the oldest terminated ones.
func TestGetAccountingPids_BufferDropsTheOldestRecords(t *testing.T) {
	t.Parallel()
	cfg := accountingConfig("enabled")
	cfg.Accounting.BufferSize = 3
	cfg.Accounting.Terminated = []AccountedProcessConfig{{PID: 301}, {PID: 302}}
	dev := newTestDeviceWithConfig(t, cfg)

	pids, ret := dev.GetAccountingPids()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []int{302, 100, 200}, pids)

	_, ret = dev.GetAccountingStats(301)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "an overwritten record is gone")
}

// TestGetAccountingStats_PidCollisionReportsTheLatestProcess follows the header:
// on a reused pid only the latest process is reported, and one still running
// is later than one that exited.
func TestGetAccountingStats_PidCollisionReportsTheLatestProcess(t *testing.T) {
	t.Parallel()
	cfg := accountingConfig("enabled")
	cfg.Accounting.Terminated = []AccountedProcessConfig{{PID: 100, GPUUtil: 1, TimeMS: 5000}}
	dev := newTestDeviceWithConfig(t, cfg)

	pids, ret := dev.GetAccountingPids()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []int{100, 200}, pids, "a reused pid is listed once")

	stats, ret := dev.GetAccountingStats(100)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), stats.IsRunning)
	require.Equal(t, uint32(70), stats.GpuUtilization)
}

// TestSetAccountingMode_RoundTripsThroughTheOverrideDocument separates a
// persisted write from one kept in the writing process: `nvidia-smi -am 1`
// runs as its own invocation, and the next one must see accounting on.
func TestSetAccountingMode_RoundTripsThroughTheOverrideDocument(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, accountingConfig("disabled"))

	require.Equal(t, nvml.SUCCESS, dev.SetAccountingMode(nvml.FEATURE_ENABLED))

	mode, ret := dev.GetAccountingMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FEATURE_ENABLED, mode)
	pids, ret := dev.GetAccountingPids()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []int{300, 100, 200}, pids)
}

// TestSetAccountingMode_DisablingClearsTheRecords follows the header: turning
// accounting off clears all accounting pids information, so turning it back on
// does not bring the exited processes back.
func TestSetAccountingMode_DisablingClearsTheRecords(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	require.Equal(t, nvml.SUCCESS, dev.SetAccountingMode(nvml.FEATURE_DISABLED))
	mode, ret := dev.GetAccountingMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FEATURE_DISABLED, mode)

	require.Equal(t, nvml.SUCCESS, dev.SetAccountingMode(nvml.FEATURE_ENABLED))
	pids, ret := dev.GetAccountingPids()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []int{100, 200}, pids, "only the processes still running are accounted again")
}

func TestSetAccountingMode_RejectsAnUnknownState(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, accountingConfig("disabled"))

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetAccountingMode(nvml.EnableState(7)))
}

// TestClearAccountingPids_KeepsRunningProcesses follows the header: the clear
// drops processes that have already terminated, and a running one stays
// queryable.
func TestClearAccountingPids_KeepsRunningProcesses(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	require.Equal(t, nvml.SUCCESS, dev.ClearAccountingPids())

	pids, ret := dev.GetAccountingPids()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []int{100, 200}, pids)
	_, ret = dev.GetAccountingStats(300)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// TestAccounting_UnavailableUnderMIG follows the header: on a MIG-enabled GPU
// accounting reads as disabled and cannot be turned on.
func TestAccounting_UnavailableUnderMIG(t *testing.T) {
	persistSetterWrites(t)
	cfg := a100MIGConfig()
	cfg.Accounting = accountingConfig("enabled").Accounting
	dev := newTestDeviceWithConfig(t, cfg)
	enableMIG(t, dev)

	mode, ret := dev.GetAccountingMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FEATURE_DISABLED, mode)
	_, ret = dev.GetAccountingPids()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetAccountingMode(nvml.FEATURE_ENABLED))
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.ClearAccountingPids())
}

// TestAccountingSetters_LostDevice pins the setters to the injected failure the
// other setters honour: a GPU that has fallen off the bus cannot have its
// accounting state changed, and a write here would outlive the failure.
func TestAccountingSetters_LostDevice(t *testing.T) {
	persistSetterWrites(t)
	cfg := accountingConfig("enabled")
	cfg.Failure = &FailureInjectionConfig{Mode: FailureModeLost}
	dev := newTestDeviceWithConfig(t, cfg)

	require.Equal(t, nvml.ERROR_GPU_IS_LOST, dev.SetAccountingMode(nvml.FEATURE_DISABLED))
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, dev.ClearAccountingPids())
}

// TestAccountingSetters_DeclineWithoutAWriter covers a consumer that links the
// engine without the bridge that installs a writer. Reporting success for a
// mode recorded nowhere is the failure this guards against.
func TestAccountingSetters_DeclineWithoutAWriter(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, accountingConfig("enabled"))

	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.SetAccountingMode(nvml.FEATURE_DISABLED))
	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.ClearAccountingPids())
}
