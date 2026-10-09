// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Supported-clock tables, performance states and the clock tuning setters.
//
// The supported-clock table is the profile's supported_clocks block, copied
// from a real board, and everything else here is checked against it: the
// application clocks must be a pair it lists, and the P-state envelope is its
// lowest and highest entry. The mock models a single performance state — the
// one the profile declares — because no capture records a board's P-state
// table, and inventing one would put clocks in front of consumers that no
// board reports.
//
// The setters persist through the override document, like the power cap, so
// `nvidia-smi -ac` followed by a separate `nvidia-smi -q` reports the new
// value the way it does against a real driver.

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// maxGpuPerfPstates is NVML_MAX_GPU_PERF_PSTATES, the size of the array
// nvmlDeviceGetSupportedPerformanceStates fills.
const maxGpuPerfPstates = 16

// SupportedMemoryClocks returns the memory clocks the device supports, in the
// order the profile lists them — highest first, as NVML returns them.
func (d *ConfigurableDevice) SupportedMemoryClocks() ([]uint32, nvml.Return) {
	sc := d.cfg().SupportedClocks
	if sc == nil || len(sc.MemoryClocks) == 0 {
		debugLog("[NVML] nvmlDeviceGetSupportedMemoryClocks -> NOT_SUPPORTED (no supported_clocks)\n")
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	clocks := make([]uint32, 0, len(sc.MemoryClocks))
	for _, mc := range sc.MemoryClocks {
		clocks = append(clocks, mc.FreqMHz)
	}
	debugLog("[NVML] nvmlDeviceGetSupportedMemoryClocks -> %d clocks\n", len(clocks))
	return clocks, nvml.SUCCESS
}

// SupportedGraphicsClocks returns the graphics clocks the device supports at
// one memory clock. A memory clock the table does not list is NOT_FOUND, which
// is how nvml.h tells a caller the frequency itself is the problem.
func (d *ConfigurableDevice) SupportedGraphicsClocks(memMHz uint32) ([]uint32, nvml.Return) {
	sc := d.cfg().SupportedClocks
	if sc == nil || len(sc.MemoryClocks) == 0 {
		debugLog("[NVML] nvmlDeviceGetSupportedGraphicsClocks(%d) -> NOT_SUPPORTED (no supported_clocks)\n", memMHz)
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	i := slices.IndexFunc(sc.MemoryClocks, func(mc MemoryClockConfig) bool { return mc.FreqMHz == memMHz })
	if i < 0 {
		debugLog("[NVML] nvmlDeviceGetSupportedGraphicsClocks(%d) -> NOT_FOUND\n", memMHz)
		return nil, nvml.ERROR_NOT_FOUND
	}
	clocks := slices.Clone(sc.MemoryClocks[i].GraphicsClocks)
	debugLog("[NVML] nvmlDeviceGetSupportedGraphicsClocks(%d) -> %d clocks\n", memMHz, len(clocks))
	return clocks, nvml.SUCCESS
}

// GetSupportedPerformanceStates returns the one P-state the mock models.
func (d *ConfigurableDevice) GetSupportedPerformanceStates() ([]nvml.Pstates, nvml.Return) {
	pstate, ret := d.GetPerformanceState()
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return []nvml.Pstates{pstate}, nvml.SUCCESS
}

// GetMinMaxClockOfPState returns the clock envelope of the modelled P-state:
// the lowest and highest supported clock of the domain. Video has no table to
// derive an envelope from, so it answers NOT_SUPPORTED rather than a guess.
func (d *ConfigurableDevice) GetMinMaxClockOfPState(clockType nvml.ClockType, pstate nvml.Pstates) (uint32, uint32, nvml.Return) {
	if clockType >= nvml.CLOCK_COUNT || pstate >= maxGpuPerfPstates {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	if current, ret := d.GetPerformanceState(); ret != nvml.SUCCESS || pstate != current {
		debugLog("[NVML] nvmlDeviceGetMinMaxClockOfPState(type=%d, P%d) -> NOT_SUPPORTED (unmodelled P-state)\n",
			clockType, pstate)
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}
	env, ok := d.clockEnvelope()
	if !ok {
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}
	var lo, hi uint32
	switch clockType {
	case nvml.CLOCK_GRAPHICS, nvml.CLOCK_SM:
		lo, hi = env.graphicsMin, env.graphicsMax
	case nvml.CLOCK_MEM:
		lo, hi = env.memoryMin, env.memoryMax
	default:
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlDeviceGetMinMaxClockOfPState(type=%d, P%d) -> %d-%d MHz\n", clockType, pstate, lo, hi)
	return lo, hi, nvml.SUCCESS
}

// clockEnvelope is the lowest and highest graphics and memory clock across the
// whole supported-clock table.
type clockEnvelope struct {
	graphicsMin, graphicsMax uint32
	memoryMin, memoryMax     uint32
}

func (d *ConfigurableDevice) clockEnvelope() (clockEnvelope, bool) {
	sc := d.cfg().SupportedClocks
	if sc == nil || len(sc.MemoryClocks) == 0 {
		return clockEnvelope{}, false
	}
	var env clockEnvelope
	var graphics, memory []uint32
	for _, mc := range sc.MemoryClocks {
		memory = append(memory, mc.FreqMHz)
		graphics = append(graphics, mc.GraphicsClocks...)
	}
	if len(graphics) == 0 {
		return clockEnvelope{}, false
	}
	env.memoryMin, env.memoryMax = slices.Min(memory), slices.Max(memory)
	env.graphicsMin, env.graphicsMax = slices.Min(graphics), slices.Max(graphics)
	return env, true
}

// PerformanceModes renders the nvmlDeviceGetPerformanceModes string: one
// token set per P-state, in the token order and " ;" terminator of the nvml.h
// example. With a single modelled P-state there is one set, at perf level 0.
func (d *ConfigurableDevice) PerformanceModes() (string, nvml.Return) {
	env, ok := d.clockEnvelope()
	if !ok {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	s := "perf=0, " + clockFreqTokens(env.graphicsMin, env.graphicsMin, env.graphicsMax,
		env.memoryMin, env.memoryMin, env.memoryMax)
	debugLog("[NVML] nvmlDeviceGetPerformanceModes -> %q\n", s)
	return s, nvml.SUCCESS
}

// CurrentClockFreqs renders the nvmlDeviceGetCurrentClockFreqs string: the
// clocks in effect now against the envelope they may move within.
func (d *ConfigurableDevice) CurrentClockFreqs() (string, nvml.Return) {
	env, ok := d.clockEnvelope()
	if !ok {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	graphics, ret := d.GetClockInfo(nvml.CLOCK_GRAPHICS)
	if ret != nvml.SUCCESS {
		return "", ret
	}
	memory, ret := d.GetClockInfo(nvml.CLOCK_MEM)
	if ret != nvml.SUCCESS {
		return "", ret
	}
	s := clockFreqTokens(graphics, env.graphicsMin, env.graphicsMax, memory, env.memoryMin, env.memoryMax)
	debugLog("[NVML] nvmlDeviceGetCurrentClockFreqs -> %q\n", s)
	return s, nvml.SUCCESS
}

// clockFreqTokens renders the token list both clock strings share. Neither
// domain is reported editable, and the transfer rate is twice the memory clock,
// both as in the nvml.h examples.
func clockFreqTokens(nv, nvMin, nvMax, mem, memMin, memMax uint32) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nvclock=%d, nvclockmin=%d, nvclockmax=%d, nvclockeditable=0, ", nv, nvMin, nvMax)
	fmt.Fprintf(&b, "memclock=%d, memclockmin=%d, memclockmax=%d, memclockeditable=0, ", mem, memMin, memMax)
	fmt.Fprintf(&b, "memtransferrate=%d, memtransferratemin=%d, memtransferratemax=%d, memtransferrateeditable=0 ;",
		2*mem, 2*memMin, 2*memMax)
	return b.String()
}

// GetDynamicPstatesInfo reports the utilization the driver's P-state governor
// samples. Only the graphics and frame-buffer domains are present, from the
// same readings as nvmlDeviceGetUtilizationRates; the video and bus domains
// have no simulated load behind them. The governor's thresholds are not
// modelled and read as 0.
func (d *ConfigurableDevice) GetDynamicPstatesInfo() (nvml.GpuDynamicPstatesInfo, nvml.Return) {
	util, ret := d.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return nvml.GpuDynamicPstatesInfo{}, ret
	}
	var info nvml.GpuDynamicPstatesInfo
	info.Utilization[nvml.GPU_UTILIZATION_DOMAIN_GPU] = nvml.GpuDynamicPstatesInfoUtilization{
		BIsPresent: 1, Percentage: util.Gpu,
	}
	info.Utilization[nvml.GPU_UTILIZATION_DOMAIN_FB] = nvml.GpuDynamicPstatesInfoUtilization{
		BIsPresent: 1, Percentage: util.Memory,
	}
	return info, nvml.SUCCESS
}

// GetAdaptiveClockInfoStatus reports clocks.adaptive_clocking. No capture
// records it, so a profile that does not declare it answers NOT_SUPPORTED.
func (d *ConfigurableDevice) GetAdaptiveClockInfoStatus() (uint32, nvml.Return) {
	c := d.cfg()
	if c.Clocks == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	switch c.Clocks.AdaptiveClocking {
	case "enabled":
		return nvml.ADAPTIVE_CLOCKING_INFO_STATUS_ENABLED, nvml.SUCCESS
	case "disabled":
		return nvml.ADAPTIVE_CLOCKING_INFO_STATUS_DISABLED, nvml.SUCCESS
	default:
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
}

// GetSupportedClocksEventReasons is the newer name of
// GetSupportedClocksThrottleReasons, and answers the same mask so that the
// current-reasons getter is read against the same set under either name.
func (d *ConfigurableDevice) GetSupportedClocksEventReasons() (uint64, nvml.Return) {
	return d.GetSupportedClocksThrottleReasons()
}

// SetApplicationsClocks applies `nvidia-smi -ac <mem>,<graphics>`. The pair
// must be one the supported-clock table lists; a board that reports no
// applications clocks declines the setter as it declines the getter.
func (d *ConfigurableDevice) SetApplicationsClocks(memMHz, graphicsMHz uint32) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if c := d.cfg(); c.Clocks == nil || c.Clocks.MemoryAppDefault == 0 || c.Clocks.GraphicsAppDefault == 0 {
		debugLog("[NVML] nvmlDeviceSetApplicationsClocks(%d, %d) -> NOT_SUPPORTED (no applications clocks)\n",
			memMHz, graphicsMHz)
		return nvml.ERROR_NOT_SUPPORTED
	}
	graphics, ret := d.SupportedGraphicsClocks(memMHz)
	if ret == nvml.ERROR_NOT_FOUND || (ret == nvml.SUCCESS && !slices.Contains(graphics, graphicsMHz)) {
		debugLog("[NVML] nvmlDeviceSetApplicationsClocks(%d, %d) -> INVALID_ARGUMENT (unsupported pair)\n",
			memMHz, graphicsMHz)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if ret != nvml.SUCCESS {
		return ret
	}
	return recordClockWrite(fmt.Sprintf("nvmlDeviceSetApplicationsClocks(%d, %d)", memMHz, graphicsMHz),
		func(w OverrideWriter) error { return w.SetApplicationsClocks(d.PhysicalIndex(), memMHz, graphicsMHz) })
}

// ResetApplicationsClocks applies `nvidia-smi -rac`: the applications clocks
// return to the board defaults. The defaults are written rather than the
// override cleared, because a profile may model a node whose clocks were
// already set away from them.
func (d *ConfigurableDevice) ResetApplicationsClocks() nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	c := d.cfg()
	if c.Clocks == nil || c.Clocks.MemoryAppDefault == 0 || c.Clocks.GraphicsAppDefault == 0 {
		return nvml.ERROR_NOT_SUPPORTED
	}
	memMHz, graphicsMHz := c.Clocks.MemoryAppDefault, c.Clocks.GraphicsAppDefault
	return recordClockWrite("nvmlDeviceResetApplicationsClocks",
		func(w OverrideWriter) error { return w.SetApplicationsClocks(d.PhysicalIndex(), memMHz, graphicsMHz) })
}

// SetGpuLockedClocks applies `nvidia-smi -lgc <min>,<max>`. The current
// graphics and SM clocks then read inside the range.
func (d *ConfigurableDevice) SetGpuLockedClocks(minMHz, maxMHz uint32) nvml.Return {
	return d.setLockedClocks("nvmlDeviceSetGpuLockedClocks", ClockDomainGraphics, minMHz, maxMHz)
}

// ResetGpuLockedClocks applies `nvidia-smi -rgc`.
func (d *ConfigurableDevice) ResetGpuLockedClocks() nvml.Return {
	return d.clearLockedClocks("nvmlDeviceResetGpuLockedClocks", ClockDomainGraphics)
}

// SetMemoryLockedClocks applies `nvidia-smi -lmc <min>,<max>`. The current
// memory clock then reads inside the range.
func (d *ConfigurableDevice) SetMemoryLockedClocks(minMHz, maxMHz uint32) nvml.Return {
	return d.setLockedClocks("nvmlDeviceSetMemoryLockedClocks", ClockDomainMemory, minMHz, maxMHz)
}

// ResetMemoryLockedClocks applies `nvidia-smi -rmc`.
func (d *ConfigurableDevice) ResetMemoryLockedClocks() nvml.Return {
	return d.clearLockedClocks("nvmlDeviceResetMemoryLockedClocks", ClockDomainMemory)
}

// Symbolic bounds nvmlDeviceSetGpuLockedClocks accepts in place of MHz
// (nvmlClockLimitId_t).
const (
	clockLimitIDRangeStart = 0xffffff00
	clockLimitIDTDP        = clockLimitIDRangeStart + 1
	clockLimitIDUnlimited  = clockLimitIDRangeStart + 2
)

// setLockedClocks validates and records a lock. nvml.h allows a pair of
// symbolic bounds instead of MHz: unlimited on both ends is a reset, while a
// TDP bound needs the board's TDP clock, which no profile declares, so it is
// declined rather than locked to a made-up value. Mixing a symbolic bound with
// a numeric one is the INVALID_ARGUMENT the header documents.
func (d *ConfigurableDevice) setLockedClocks(op string, domain ClockDomain, minMHz, maxMHz uint32) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if d.cfg().Clocks == nil {
		return nvml.ERROR_NOT_SUPPORTED
	}
	minSymbolic, maxSymbolic := minMHz >= clockLimitIDRangeStart, maxMHz >= clockLimitIDRangeStart
	switch {
	case minSymbolic != maxSymbolic:
		debugLog("[NVML] %s(%#x, %#x) -> INVALID_ARGUMENT (mixed symbolic and MHz bounds)\n", op, minMHz, maxMHz)
		return nvml.ERROR_INVALID_ARGUMENT
	case minSymbolic && minMHz == clockLimitIDUnlimited && maxMHz == clockLimitIDUnlimited:
		return d.clearLockedClocks(op, domain)
	case minSymbolic:
		debugLog("[NVML] %s(%#x, %#x) -> NOT_SUPPORTED (no TDP clock modelled)\n", op, minMHz, maxMHz)
		return nvml.ERROR_NOT_SUPPORTED
	case minMHz > maxMHz:
		debugLog("[NVML] %s(%d, %d) -> INVALID_ARGUMENT (min above max)\n", op, minMHz, maxMHz)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	r := &ClockRangeConfig{MinMHz: minMHz, MaxMHz: maxMHz}
	return recordClockWrite(fmt.Sprintf("%s(%d, %d)", op, minMHz, maxMHz),
		func(w OverrideWriter) error { return w.SetLockedClocks(d.PhysicalIndex(), domain, r) })
}

func (d *ConfigurableDevice) clearLockedClocks(op string, domain ClockDomain) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if d.cfg().Clocks == nil {
		return nvml.ERROR_NOT_SUPPORTED
	}
	return recordClockWrite(op,
		func(w OverrideWriter) error { return w.SetLockedClocks(d.PhysicalIndex(), domain, nil) })
}

// lockedClock is the reading a lock lets through: the unlocked clock pulled
// into the locked range, and never above what the domain can reach — a lock
// asks the driver for a range, it does not raise the board's maximum.
func lockedClock(current uint32, lock *ClockRangeConfig, maxMHz uint32) uint32 {
	if lock == nil {
		return current
	}
	clock := min(max(current, lock.MinMHz), lock.MaxMHz)
	if maxMHz > 0 {
		clock = min(clock, maxMHz)
	}
	return clock
}

// ClockOffset returns the offset applied to one domain at one P-state, with
// the range the board accepts. Offsets exist only for the modelled P-state and
// for the domains clocks.offsets declares. They are recorded and reported, but
// do not shift the simulated clocks: no capture shows how a real board's
// readings move under an offset.
func (d *ConfigurableDevice) ClockOffset(clockType nvml.ClockType, pstate nvml.Pstates) (nvml.ClockOffset, nvml.Return) {
	off, ret := d.clockOffsetConfig(clockType, pstate)
	if ret != nvml.SUCCESS {
		return nvml.ClockOffset{}, ret
	}
	return nvml.ClockOffset{
		Type:              uint32(clockType),
		Pstate:            uint32(pstate),
		ClockOffsetMHz:    off.OffsetMHz,
		MinClockOffsetMHz: off.MinMHz,
		MaxClockOffsetMHz: off.MaxMHz,
	}, nvml.SUCCESS
}

// SetClockOffsets records the offset info asks for, within the declared range.
func (d *ConfigurableDevice) SetClockOffsets(info nvml.ClockOffset) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	clockType, pstate := nvml.ClockType(info.Type), nvml.Pstates(info.Pstate)
	off, ret := d.clockOffsetConfig(clockType, pstate)
	if ret != nvml.SUCCESS {
		return ret
	}
	if info.ClockOffsetMHz < off.MinMHz || info.ClockOffsetMHz > off.MaxMHz {
		debugLog("[NVML] nvmlDeviceSetClockOffsets(type=%d, %d MHz) -> INVALID_ARGUMENT (range %d..%d)\n",
			clockType, info.ClockOffsetMHz, off.MinMHz, off.MaxMHz)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	domain := ClockDomainGraphics
	if clockType == nvml.CLOCK_MEM {
		domain = ClockDomainMemory
	}
	return recordClockWrite(fmt.Sprintf("nvmlDeviceSetClockOffsets(type=%d, %d MHz)", clockType, info.ClockOffsetMHz),
		func(w OverrideWriter) error { return w.SetClockOffset(d.PhysicalIndex(), domain, info.ClockOffsetMHz) })
}

// clockOffsetConfig resolves the offset block one (domain, P-state) reads. The
// graphics offset is the GPC clock's, so CLOCK_SM shares it; video has none.
func (d *ConfigurableDevice) clockOffsetConfig(clockType nvml.ClockType, pstate nvml.Pstates) (*ClockOffsetConfig, nvml.Return) {
	if clockType >= nvml.CLOCK_COUNT || pstate >= maxGpuPerfPstates {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	if current, ret := d.GetPerformanceState(); ret != nvml.SUCCESS || pstate != current {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	c := d.cfg()
	if c.Clocks == nil {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	if off := c.Clocks.Offsets.domain(clockType); off != nil {
		return off, nvml.SUCCESS
	}
	return nil, nvml.ERROR_NOT_SUPPORTED
}

// domain returns the offset block a clock type reads, nil when it has none.
func (o *ClockOffsetsConfig) domain(clockType nvml.ClockType) *ClockOffsetConfig {
	if o == nil {
		return nil
	}
	switch clockType {
	case nvml.CLOCK_GRAPHICS, nvml.CLOCK_SM:
		return o.Graphics
	case nvml.CLOCK_MEM:
		return o.Memory
	default:
		// CLOCK_VIDEO has no offset to report.
		return nil
	}
}

// The four VF-offset functions are the deprecated, single-domain form of the
// ClockOffsets pair, reading and writing the modelled P-state.

// GetGpcClkVfOffset returns the graphics clock offset.
func (d *ConfigurableDevice) GetGpcClkVfOffset() (int, nvml.Return) {
	return d.vfOffset(nvml.CLOCK_GRAPHICS)
}

// GetMemClkVfOffset returns the memory clock offset.
func (d *ConfigurableDevice) GetMemClkVfOffset() (int, nvml.Return) {
	return d.vfOffset(nvml.CLOCK_MEM)
}

// GetGpcClkMinMaxVfOffset returns the graphics clock offset range.
func (d *ConfigurableDevice) GetGpcClkMinMaxVfOffset() (int, int, nvml.Return) {
	return d.vfOffsetRange(nvml.CLOCK_GRAPHICS)
}

// GetMemClkMinMaxVfOffset returns the memory clock offset range.
func (d *ConfigurableDevice) GetMemClkMinMaxVfOffset() (int, int, nvml.Return) {
	return d.vfOffsetRange(nvml.CLOCK_MEM)
}

// SetGpcClkVfOffset applies a graphics clock offset.
func (d *ConfigurableDevice) SetGpcClkVfOffset(offset int) nvml.Return {
	return d.setVfOffset(nvml.CLOCK_GRAPHICS, offset)
}

// SetMemClkVfOffset applies a memory clock offset.
func (d *ConfigurableDevice) SetMemClkVfOffset(offset int) nvml.Return {
	return d.setVfOffset(nvml.CLOCK_MEM, offset)
}

func (d *ConfigurableDevice) vfOffset(clockType nvml.ClockType) (int, nvml.Return) {
	pstate, ret := d.GetPerformanceState()
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	off, ret := d.ClockOffset(clockType, pstate)
	return int(off.ClockOffsetMHz), ret
}

func (d *ConfigurableDevice) vfOffsetRange(clockType nvml.ClockType) (int, int, nvml.Return) {
	pstate, ret := d.GetPerformanceState()
	if ret != nvml.SUCCESS {
		return 0, 0, ret
	}
	off, ret := d.ClockOffset(clockType, pstate)
	return int(off.MinClockOffsetMHz), int(off.MaxClockOffsetMHz), ret
}

func (d *ConfigurableDevice) setVfOffset(clockType nvml.ClockType, offset int) nvml.Return {
	pstate, ret := d.GetPerformanceState()
	if ret != nvml.SUCCESS {
		return ret
	}
	// An int that does not fit the int32 offset field cannot be in any range a
	// board declares; clamping it would record a different request.
	if offset < math.MinInt32 || offset > math.MaxInt32 {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return d.SetClockOffsets(nvml.ClockOffset{
		Type: uint32(clockType), Pstate: uint32(pstate), ClockOffsetMHz: int32(offset),
	})
}

// SetAutoBoostedClocksEnabled declines, as the getter does. nvml.h moves
// auto boost under the applications clocks from Pascal on, every board the
// mock models is newer, and every capture reports auto boost as N/A.
func (d *ConfigurableDevice) SetAutoBoostedClocksEnabled(nvml.EnableState) nvml.Return {
	return nvml.ERROR_NOT_SUPPORTED
}

// SetDefaultAutoBoostedClocksEnabled declines for the same reason.
func (d *ConfigurableDevice) SetDefaultAutoBoostedClocksEnabled(nvml.EnableState, uint32) nvml.Return {
	return nvml.ERROR_NOT_SUPPORTED
}

// recordClockWrite runs one setter write against the override writer and
// invalidates the cache so the write is visible to the next read in this
// process. A setter with nowhere to record has failed and must say so.
func recordClockWrite(op string, write func(OverrideWriter) error) nvml.Return {
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
