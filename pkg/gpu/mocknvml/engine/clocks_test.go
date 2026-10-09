// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// clockedDevice is a two-memory-clock board shaped like the H100 capture,
// trimmed so a failure prints a table a reader can check by eye.
func clockedDevice(t *testing.T, mutate ...func(*DeviceConfig)) *ConfigurableDevice {
	t.Helper()
	cfg := &DeviceConfig{
		Clocks: &ClocksConfig{
			GraphicsCurrent: 345, GraphicsMax: 1980, GraphicsApp: 1980, GraphicsAppDefault: 1980,
			SMCurrent: 345, SMMax: 1980,
			MemoryCurrent: 2619, MemoryMax: 2619, MemoryApp: 2619, MemoryAppDefault: 2619,
			VideoCurrent: 765, VideoMax: 1545,
		},
		SupportedClocks: &SupportedClocksConfig{MemoryClocks: []MemoryClockConfig{
			{FreqMHz: 2619, GraphicsClocks: []uint32{1980, 1965, 1500, 345}},
			{FreqMHz: 1593, GraphicsClocks: []uint32{1980, 1410, 345}},
		}},
		Utilization: &UtilizationConfig{GPU: 42, Memory: 17},
	}
	for _, m := range mutate {
		m(cfg)
	}
	return newTestDeviceWithConfig(t, cfg)
}

func TestSupportedClocks_ServeTheProfileTable(t *testing.T) {
	t.Parallel()
	dev := clockedDevice(t)

	mem, ret := dev.SupportedMemoryClocks()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{2619, 1593}, mem, "memory clocks in the order NVML returns them")

	gfx, ret := dev.SupportedGraphicsClocks(1593)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{1980, 1410, 345}, gfx, "graphics clocks belong to the memory clock asked for")

	_, ret = dev.SupportedGraphicsClocks(1000)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "nvml.h reports an unlisted memory clock as NOT_FOUND")

	gfx[0] = 1
	again, _ := dev.SupportedGraphicsClocks(1593)
	require.Equal(t, uint32(1980), again[0], "a caller mutating the result must not reach the profile")
}

func TestSupportedClocks_NotSupportedWithoutATable(t *testing.T) {
	t.Parallel()
	dev := clockedDevice(t, func(c *DeviceConfig) { c.SupportedClocks = nil })

	_, ret := dev.SupportedMemoryClocks()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = dev.SupportedGraphicsClocks(2619)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, _, ret = dev.GetMinMaxClockOfPState(nvml.CLOCK_GRAPHICS, nvml.PSTATE_0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = dev.PerformanceModes()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestPerformanceStates_ModelOneState(t *testing.T) {
	t.Parallel()
	dev := clockedDevice(t, func(c *DeviceConfig) { c.PerformanceState = "P2" })

	states, ret := dev.GetSupportedPerformanceStates()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []nvml.Pstates{nvml.PSTATE_2}, states)

	cases := []struct {
		name     string
		domain   nvml.ClockType
		lo, hi   uint32
		expected nvml.Return
	}{
		{"graphics spans the whole table", nvml.CLOCK_GRAPHICS, 345, 1980, nvml.SUCCESS},
		{"SM follows graphics", nvml.CLOCK_SM, 345, 1980, nvml.SUCCESS},
		{"memory spans the memory clocks", nvml.CLOCK_MEM, 1593, 2619, nvml.SUCCESS},
		{"video has no table to derive from", nvml.CLOCK_VIDEO, 0, 0, nvml.ERROR_NOT_SUPPORTED},
	}
	for _, tc := range cases {
		lo, hi, ret := dev.GetMinMaxClockOfPState(tc.domain, nvml.PSTATE_2)
		require.Equal(t, tc.expected, ret, tc.name)
		require.Equal(t, [2]uint32{tc.lo, tc.hi}, [2]uint32{lo, hi}, tc.name)
	}

	_, _, ret = dev.GetMinMaxClockOfPState(nvml.CLOCK_GRAPHICS, nvml.PSTATE_0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "a P-state the device is not modelled in has no envelope")
	_, _, ret = dev.GetMinMaxClockOfPState(nvml.CLOCK_GRAPHICS, nvml.PSTATE_UNKNOWN)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

func TestClockStrings_FollowTheHeaderTokenOrder(t *testing.T) {
	t.Parallel()
	dev := clockedDevice(t)

	modes, ret := dev.PerformanceModes()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "perf=0, nvclock=345, nvclockmin=345, nvclockmax=1980, nvclockeditable=0, "+
		"memclock=1593, memclockmin=1593, memclockmax=2619, memclockeditable=0, "+
		"memtransferrate=3186, memtransferratemin=3186, memtransferratemax=5238, memtransferrateeditable=0 ;", modes)

	current, ret := dev.CurrentClockFreqs()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "nvclock=345, nvclockmin=345, nvclockmax=1980, nvclockeditable=0, "+
		"memclock=2619, memclockmin=1593, memclockmax=2619, memclockeditable=0, "+
		"memtransferrate=5238, memtransferratemin=3186, memtransferratemax=5238, memtransferrateeditable=0 ;", current)
}

func TestDynamicPstatesInfo_ReportsUtilizationDomains(t *testing.T) {
	t.Parallel()
	info, ret := clockedDevice(t).GetDynamicPstatesInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.GpuDynamicPstatesInfoUtilization{BIsPresent: 1, Percentage: 42},
		info.Utilization[nvml.GPU_UTILIZATION_DOMAIN_GPU])
	require.Equal(t, nvml.GpuDynamicPstatesInfoUtilization{BIsPresent: 1, Percentage: 17},
		info.Utilization[nvml.GPU_UTILIZATION_DOMAIN_FB])
	require.Zero(t, info.Utilization[nvml.GPU_UTILIZATION_DOMAIN_VID].BIsPresent,
		"a domain with no simulated load must not claim to be present")
}

func TestAdaptiveClocking_FollowsProfile(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]nvml.Return{"enabled": nvml.SUCCESS, "disabled": nvml.SUCCESS, "": nvml.ERROR_NOT_SUPPORTED} {
		dev := clockedDevice(t, func(c *DeviceConfig) { c.Clocks.AdaptiveClocking = value })
		status, ret := dev.GetAdaptiveClockInfoStatus()
		require.Equal(t, want, ret, "adaptive_clocking=%q", value)
		if value == "enabled" {
			require.Equal(t, uint32(nvml.ADAPTIVE_CLOCKING_INFO_STATUS_ENABLED), status)
		}
	}
}

// TestSupportedClocksEventReasons_MatchesThrottleName pins the two names of
// one mask together: dcgm-exporter interprets the current reasons against the
// supported mask, so the two names must not disagree about which bits exist.
func TestSupportedClocksEventReasons_MatchesThrottleName(t *testing.T) {
	t.Parallel()
	dev := clockedDevice(t)
	event, ret := dev.GetSupportedClocksEventReasons()
	require.Equal(t, nvml.SUCCESS, ret)
	throttle, _ := dev.GetSupportedClocksThrottleReasons()
	require.Equal(t, throttle, event)
}

func TestApplicationsClocks_RoundTripAndReset(t *testing.T) {
	persistSetterWrites(t)
	dev := clockedDevice(t)

	require.Equal(t, nvml.SUCCESS, dev.SetApplicationsClocks(1593, 1410))
	requireAppClocks(t, dev, 1593, 1410)

	require.Equal(t, nvml.SUCCESS, dev.ResetApplicationsClocks())
	requireAppClocks(t, dev, 2619, 1980)
	def, _ := dev.GetDefaultApplicationsClock(nvml.CLOCK_GRAPHICS)
	require.Equal(t, uint32(1980), def, "the defaults are a board property and never move")
}

func TestApplicationsClocks_RejectUnsupportedPairs(t *testing.T) {
	t.Parallel()
	dev := clockedDevice(t)
	for _, pair := range [][2]uint32{
		{1000, 1980}, // memory clock not in the table
		{1593, 1965}, // graphics clock listed only under the other memory clock
	} {
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetApplicationsClocks(pair[0], pair[1]), "%v", pair)
	}
	requireAppClocks(t, dev, 2619, 1980)

	noApp := clockedDevice(t, func(c *DeviceConfig) { c.Clocks.GraphicsAppDefault = 0 })
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, noApp.SetApplicationsClocks(2619, 1980),
		"a board that reports no applications clocks must decline the setter as it declines the getter")
}

func requireAppClocks(t *testing.T, dev *ConfigurableDevice, mem, gfx uint32) {
	t.Helper()
	gotMem, ret := dev.GetApplicationsClock(nvml.CLOCK_MEM)
	require.Equal(t, nvml.SUCCESS, ret)
	gotGfx, ret := dev.GetApplicationsClock(nvml.CLOCK_GRAPHICS)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, [2]uint32{mem, gfx}, [2]uint32{gotMem, gotGfx}, "applications clocks (mem, graphics)")
}

func TestGpuLockedClocks_PinTheCurrentClocks(t *testing.T) {
	persistSetterWrites(t)
	dev := clockedDevice(t)

	require.Equal(t, nvml.SUCCESS, dev.SetGpuLockedClocks(1500, 1500))
	requireClock(t, dev, nvml.CLOCK_GRAPHICS, 1500)
	requireClock(t, dev, nvml.CLOCK_SM, 1500)
	requireClock(t, dev, nvml.CLOCK_MEM, 2619)

	require.Equal(t, nvml.SUCCESS, dev.SetGpuLockedClocks(1900, 5000))
	requireClock(t, dev, nvml.CLOCK_GRAPHICS, 1900)

	require.Equal(t, nvml.SUCCESS, dev.SetGpuLockedClocks(3000, 5000))
	requireClock(t, dev, nvml.CLOCK_GRAPHICS, 1980)

	require.Equal(t, nvml.SUCCESS, dev.ResetGpuLockedClocks())
	requireClock(t, dev, nvml.CLOCK_GRAPHICS, 345)
	requireClock(t, dev, nvml.CLOCK_SM, 345)
}

func TestGpuLockedClocks_SymbolicBounds(t *testing.T) {
	persistSetterWrites(t)
	dev := clockedDevice(t)

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetGpuLockedClocks(1500, clockLimitIDUnlimited),
		"nvml.h refuses a symbolic bound mixed with a MHz one")
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetGpuLockedClocks(clockLimitIDTDP, clockLimitIDTDP),
		"no profile declares a TDP clock to lock to")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetGpuLockedClocks(1600, 1500))

	require.Equal(t, nvml.SUCCESS, dev.SetGpuLockedClocks(1500, 1500))
	require.Equal(t, nvml.SUCCESS, dev.SetGpuLockedClocks(clockLimitIDUnlimited, clockLimitIDUnlimited),
		"unlimited on both ends is the documented reset")
	requireClock(t, dev, nvml.CLOCK_GRAPHICS, 345)
}

func TestMemoryLockedClocks_PinTheMemoryClock(t *testing.T) {
	persistSetterWrites(t)
	dev := clockedDevice(t)

	require.Equal(t, nvml.SUCCESS, dev.SetMemoryLockedClocks(1593, 1593))
	requireClock(t, dev, nvml.CLOCK_MEM, 1593)
	requireClock(t, dev, nvml.CLOCK_GRAPHICS, 345)

	require.Equal(t, nvml.SUCCESS, dev.ResetMemoryLockedClocks())
	requireClock(t, dev, nvml.CLOCK_MEM, 2619)
}

func requireClock(t *testing.T, dev *ConfigurableDevice, domain nvml.ClockType, want uint32) {
	t.Helper()
	got, ret := dev.GetClockInfo(domain)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, want, got, "clock domain %d", domain)
}

func offsetDevice(t *testing.T) *ConfigurableDevice {
	t.Helper()
	return clockedDevice(t, func(c *DeviceConfig) {
		c.Clocks.Offsets = &ClockOffsetsConfig{Graphics: &ClockOffsetConfig{MinMHz: -200, MaxMHz: 300}}
	})
}

func TestClockOffsets_RoundTripWithinRange(t *testing.T) {
	persistSetterWrites(t)
	dev := offsetDevice(t)

	require.Equal(t, nvml.SUCCESS, dev.SetClockOffsets(nvml.ClockOffset{
		Type: uint32(nvml.CLOCK_GRAPHICS), Pstate: uint32(nvml.PSTATE_0), ClockOffsetMHz: 150,
	}))
	off, ret := dev.ClockOffset(nvml.CLOCK_GRAPHICS, nvml.PSTATE_0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, [3]int32{150, -200, 300}, [3]int32{off.ClockOffsetMHz, off.MinClockOffsetMHz, off.MaxClockOffsetMHz})

	require.Equal(t, nvml.SUCCESS, dev.SetGpcClkVfOffset(-50), "the deprecated form writes the same offset")
	v, ret := dev.GetGpcClkVfOffset()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, -50, v)
	lo, hi, ret := dev.GetGpcClkMinMaxVfOffset()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, [2]int{-200, 300}, [2]int{lo, hi})

	requireClock(t, dev, nvml.CLOCK_GRAPHICS, 345)
}

func TestClockOffsets_Rejections(t *testing.T) {
	t.Parallel()
	dev := offsetDevice(t)

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetGpcClkVfOffset(301), "above the declared range")
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetMemClkVfOffset(0), "memory declares no offsets")
	_, ret := dev.GetMemClkVfOffset()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = dev.ClockOffset(nvml.CLOCK_GRAPHICS, nvml.PSTATE_8)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "offsets exist only for the modelled P-state")

	_, ret = clockedDevice(t).GetGpcClkVfOffset()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "a board that declares no offsets has none to report")
}

func TestAutoBoostSetters_AgreeWithGetter(t *testing.T) {
	t.Parallel()
	dev := clockedDevice(t)
	_, _, getRet := dev.GetAutoBoostedClocksEnabled()
	require.Equal(t, getRet, dev.SetAutoBoostedClocksEnabled(nvml.FEATURE_ENABLED))
	require.Equal(t, getRet, dev.SetDefaultAutoBoostedClocksEnabled(nvml.FEATURE_ENABLED, 0))
}

// TestClockSetters_DeclineWithoutAWriter covers a consumer that links the
// engine without the bridge. Reporting success for a write recorded nowhere is
// the failure this guards against.
func TestClockSetters_DeclineWithoutAWriter(t *testing.T) {
	dev := offsetDevice(t)
	for name, ret := range map[string]nvml.Return{
		"applications": dev.SetApplicationsClocks(1593, 1410),
		"reset apps":   dev.ResetApplicationsClocks(),
		"gpu lock":     dev.SetGpuLockedClocks(1500, 1500),
		"gpu unlock":   dev.ResetGpuLockedClocks(),
		"memory lock":  dev.SetMemoryLockedClocks(1593, 1593),
		"offset":       dev.SetGpcClkVfOffset(10),
	} {
		require.Equal(t, nvml.ERROR_NO_PERMISSION, ret, name)
	}
}
