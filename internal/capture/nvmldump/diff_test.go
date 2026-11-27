// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package nvmldump

import (
	"bytes"
	"fmt"
	"regexp"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// verdicts compares two single-device dumps with the default rules.
func verdicts(t *testing.T, hw, mock map[string]call) map[string]verdict {
	t.Helper()
	got := map[string]verdict{}
	for _, f := range compare(dumpOf(hw), dumpOf(mock), regexp.MustCompile(defaultIgnore)) {
		got[f.name] = f.verdict
	}
	return got
}

func ok(v any) call { return call{Ret: "SUCCESS", Value: v} }

func TestDiffSkipsHostAndTimeReadings(t *testing.T) {
	t.Parallel()
	hw := map[string]call{
		"GetCpuAffinity(8)":              ok([]any{uint64(1<<64 - 1)}),
		"GetCpuAffinityWithinScope(8,0)": ok([]any{uint64(1<<64 - 1)}),
		"GetDynamicPstatesInfo":          ok(map[string]any{"Flags": 0, "Utilization": []any{map[string]any{"Percentage": 3}}}),
		"GetBBXTimeData_v1":              ok(map[string]any{"TimeRun": 7058642}),
		"GetLastBBXFlushTime":            ok([]any{1793762201439370, 36598}),
		"GetCurrentClockFreqs":           ok(map[string]any{"Str": "nvclock=120"}),
		"GetClock(0,0)":                  ok(120),
		"GetClock(0,1)":                  ok(2424),
	}
	mock := map[string]call{
		"GetCpuAffinity(8)":              ok([]any{uint64(1023)}),
		"GetCpuAffinityWithinScope(8,0)": ok([]any{uint64(1023)}),
		"GetDynamicPstatesInfo":          ok(map[string]any{"Flags": 0, "Utilization": []any{map[string]any{"Percentage": 9}}}),
		"GetBBXTimeData_v1":              ok(map[string]any{"TimeRun": 45182}),
		"GetLastBBXFlushTime":            ok([]any{1793986189000000, 1}),
		"GetCurrentClockFreqs":           ok(map[string]any{"Str": "nvclock=1980"}),
		"GetClock(0,0)":                  ok(1980),
		"GetClock(0,1)":                  ok(2400),
	}

	got := verdicts(t, hw, mock)
	for _, name := range []string{
		"GetCpuAffinity(8)", "GetCpuAffinityWithinScope(8,0)", "GetDynamicPstatesInfo",
		"GetBBXTimeData_v1", "GetLastBBXFlushTime", "GetCurrentClockFreqs", "GetClock(0,0)",
	} {
		require.Equal(t, ignoredValue, got[name], name)
	}
	require.Equal(t, valueDiffers, got["GetClock(0,1)"], "the application clock target describes the board")
}

func TestDiffComparesTheBoardPartsOfAnAnswer(t *testing.T) {
	t.Parallel()
	memory := func(free, used, reserved int) call {
		return ok(map[string]any{"Free": free, "Used": used, "Reserved": reserved, "Total": 300442189824, "Version": 33554472})
	}
	sensor := func(current, defaultMax int) call {
		return ok(map[string]any{"Count": 1, "Sensor": []any{map[string]any{"CurrentTemp": current, "DefaultMaxTemp": defaultMax}}})
	}
	hw := map[string]call{
		"GetMemoryInfo_v2":      memory(299103551488, 0, 1338638336),
		"GetMemoryInfo":         ok(map[string]any{"Free": 1, "Used": 2, "Total": 3}),
		"GetBAR1MemoryInfo":     ok(map[string]any{"Bar1Free": 1, "Bar1Used": 524288, "Bar1Total": 3}),
		"GetThermalSettings(0)": sensor(33, 127),
		"GetThermalSettings(1)": sensor(33, 127),
		"GetNvlinkBwMode":       ok(map[string]any{"BIsBest": 1, "BwMode": 0, "Version": 0}),
	}
	mock := map[string]call{
		"GetMemoryInfo_v2":      memory(299103158272, 0, 1339031552),
		"GetMemoryInfo":         ok(map[string]any{"Free": 9, "Used": 0, "Total": 3}),
		"GetBAR1MemoryInfo":     ok(map[string]any{"Bar1Free": 2, "Bar1Used": 0, "Bar1Total": 3}),
		"GetThermalSettings(0)": sensor(32, 127),
		"GetThermalSettings(1)": sensor(33, 90),
		"GetNvlinkBwMode":       ok(map[string]any{"BIsBest": 1, "BwMode": 0, "Version": 16777224}),
	}

	got := verdicts(t, hw, mock)
	require.Equal(t, valueDiffers, got["GetMemoryInfo_v2"], "the driver's reservation describes the board")
	require.Equal(t, match, got["GetMemoryInfo"], "memory in use is a reading; the total matches")
	require.Equal(t, match, got["GetBAR1MemoryInfo"])
	require.Equal(t, match, got["GetThermalSettings(0)"], "only the current temperature differs")
	require.Equal(t, valueDiffers, got["GetThermalSettings(1)"])
	require.Equal(t, match, got["GetNvlinkBwMode"], "the struct version belongs to the API")

	var out bytes.Buffer
	report(&out, compare(dumpOf(hw), dumpOf(mock), regexp.MustCompile(defaultIgnore)), 10)
	require.Contains(t, out.String(), `real={"Reserved":1338638336,"Total":300442189824}`,
		"a listed difference shows what was compared")
}

func TestDiffComparesBoardFieldValues(t *testing.T) {
	t.Parallel()
	field := func(id int) string { return fmt.Sprintf("GetFieldValues(id=%d)", id) }
	value := func(v string) call { return ok(map[string]any{"valueType": 5, "value": v}) }
	board := []int{
		nvml.FI_DEV_ECC_CURRENT, nvml.FI_DEV_NVLINK_LINK_COUNT, nvml.FI_DEV_POWER_MAX_LIMIT,
		nvml.FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT, nvml.FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT,
		nvml.FI_DEV_TEMPERATURE_GPU_MAX_TLIMIT,
	}
	live := []int{
		nvml.FI_DEV_ECC_SBE_VOL_TOTAL, nvml.FI_DEV_TOTAL_ENERGY_CONSUMPTION, nvml.FI_DEV_POWER_AVERAGE,
		nvml.FI_DEV_NVLINK_THROUGHPUT_DATA_TX, nvml.FI_DEV_NVLINK_COUNT_FEC_HISTORY_0,
		nvml.FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN,
	}
	hw, mock := map[string]call{}, map[string]call{}
	for _, id := range append(board, live...) {
		hw[field(id)] = value("feffffff00000000")
		mock[field(id)] = value("0000000000000000")
	}
	hw[field(nvml.FI_DEV_MEMORY_TEMP)] = call{Ret: "ERROR_NOT_SUPPORTED"}
	mock[field(nvml.FI_DEV_MEMORY_TEMP)] = value("2800000000000000")

	got := verdicts(t, hw, mock)
	for _, id := range board {
		require.Equal(t, valueDiffers, got[field(id)], "field %d describes the board", id)
	}
	for _, id := range live {
		require.Equal(t, ignoredValue, got[field(id)], "field %d is a reading", id)
	}
	require.Equal(t, retDiffers, got[field(nvml.FI_DEV_MEMORY_TEMP)], "a reading's support is still compared")
}

// The ranges are written with go-nvml's names, so a header that renumbers a
// field moves the range with it; a range that no longer runs upwards, or that
// overlaps the next, means the header changed shape under it.
func TestLiveFieldRangesFollowTheHeader(t *testing.T) {
	t.Parallel()
	last := 0
	for _, r := range liveFieldIDs {
		require.LessOrEqual(t, r[0], r[1], "range %v", r)
		require.Greater(t, r[0], last, "range %v overlaps or precedes the one before", r)
		last = r[1]
	}
	require.Less(t, last, fieldIDLimit, "every live field is probed")
}

func TestReportNamesEveryValueLeftOut(t *testing.T) {
	t.Parallel()
	hw := dumpOf(map[string]call{
		"GetCpuAffinity(8)": ok([]any{uint64(1<<64 - 1)}),
		"GetMemoryInfo_v2":  ok(map[string]any{"Free": 7, "Used": 1, "Reserved": 2, "Total": 10, "Version": 33554472}),
		"GetBrand":          ok(14),
		"GetTemperature(0)": call{Ret: "ERROR_NOT_SUPPORTED"},
	})
	mock := dumpOf(map[string]call{
		"GetCpuAffinity(8)": ok([]any{uint64(1023)}),
		"GetMemoryInfo_v2":  ok(map[string]any{"Free": 8, "Used": 0, "Reserved": 2, "Total": 10, "Version": 33554472}),
		"GetBrand":          ok(14),
		"GetTemperature(0)": call{Ret: "ERROR_NOT_SUPPORTED"},
	})

	var out bytes.Buffer
	report(&out, compare(hw, mock, regexp.MustCompile(defaultIgnore)), 10)

	require.Contains(t, out.String(), "value matches:         2 of 2 (100.0%)", "the board parts of the memory answer still count")
	require.Contains(t, out.String(), "not compared, a reading or an identifier (4):\n"+
		"  device 0  GetCpuAffinity(8)                        real=[18446744073709551615] mock=[1023]\n"+
		"  device 0  GetMemoryInfo_v2.Free                    real=7 mock=8\n"+
		"  device 0  GetMemoryInfo_v2.Used                    real=1 mock=0\n"+
		"  device 0  GetMemoryInfo_v2.Version                 real=33554472 mock=33554472\n")
	require.NotContains(t, out.String(), "GetTemperature(0)", "a call without an answer has no value to leave out")
}
