// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"strings"
)

// A decoded `nvidia-smi -q -x` document, the readings assertions take from it,
// and the checks over those readings. One document answers every question the
// suite used to ask through `-L` and a series of --query-gpu calls, so a
// scenario that read four fields now makes one exec.
//
// The schema itself lives in nvidiasmi_schema.go; the temperature-threshold
// check is large enough to warrant nvidiasmi_temperature.go.
//
// No build tag: decoding and comparing is pure. The exec wrapper is
// GPUSnapshotFromPod in nvidiasmi.go, under //go:build e2e.

// GPUSnapshot is one decoded document.
type GPUSnapshot struct {
	log nvidiaSMILog
}

// ParseGPUSnapshot decodes `nvidia-smi -q -x` output.
func ParseGPUSnapshot(out string) (GPUSnapshot, error) {
	log, err := parseNvidiaSMIXML(out)
	if err != nil {
		return GPUSnapshot{}, err
	}
	return GPUSnapshot{log: log}, nil
}

// AttachedGPUs is the <attached_gpus> reading, nvidia-smi's own count. It is
// reported separately from Count so a truncated document is detectable.
func (s GPUSnapshot) AttachedGPUs() (int, bool) { return s.log.AttachedGPUs.intValue() }

// Count is the number of <gpu> elements in the document.
func (s GPUSnapshot) Count() int { return len(s.log.GPUs) }

// ProductNames lists <product_name> in the order nvidia-smi emits GPUs, which
// is the order it indexes them by.
func (s GPUSnapshot) ProductNames() []string {
	names := make([]string, 0, len(s.log.GPUs))
	for _, gpu := range s.log.GPUs {
		names = append(names, strings.TrimSpace(string(gpu.ProductName)))
	}
	return names
}

// UUIDs lists <uuid> in nvidia-smi's GPU order.
func (s GPUSnapshot) UUIDs() []string {
	uuids := make([]string, 0, len(s.log.GPUs))
	for _, gpu := range s.log.GPUs {
		uuids = append(uuids, strings.TrimSpace(string(gpu.UUID)))
	}
	return uuids
}

// GPU returns the readings for the GPU at index, in nvidia-smi's order — the
// same index `--id=N` takes.
func (s GPUSnapshot) GPU(index int) (GPUReadings, error) {
	if index < 0 || index >= len(s.log.GPUs) {
		return GPUReadings{}, fmt.Errorf(
			"nvidia-smi XML reported %d GPUs, want an entry at index %d", len(s.log.GPUs), index)
	}
	return GPUReadings{gpu: s.log.GPUs[index], index: index}, nil
}

// GPUReadings exposes one GPU's readings.
type GPUReadings struct {
	gpu   nvidiaSMIGPU
	index int
}

// Label names the GPU in assertion output.
func (g GPUReadings) Label() string { return g.gpu.label(g.index) }

// UUID is the <uuid> body.
func (g GPUReadings) UUID() string { return strings.TrimSpace(string(g.gpu.UUID)) }

// failureProbes are the elements checked for an NVML error body. A failed
// device renders the string in place of nearly every reading, so any one of
// these is enough; several are read because which ones appear varies with the
// failure mode (a lost device loses all of them, a fallen-off-bus device is
// queried through a different NVML path).
func (g GPUReadings) failureProbes() []reading {
	return []reading{
		g.gpu.Temperature.GPUTemp,
		g.gpu.Utilization.GPU,
		g.gpu.PowerReadings.InstantPowerDraw,
		g.gpu.Clocks.SMClock,
		g.gpu.ECCErrors.Aggregate.DRAMUncorrectable,
	}
}

// Failed reports whether nvidia-smi substituted an NVML error string for this
// GPU's readings. It is deliberately not satisfied by "N/A": the readings a
// healthy GPU legitimately does not support (fan_speed on a passively-cooled
// board) would otherwise mark every such device failed.
func (g GPUReadings) Failed() bool {
	for _, r := range g.failureProbes() {
		if r.failed() {
			return true
		}
	}
	return false
}

// FailureReason is the NVML error body behind Failed, for assertion messages.
func (g GPUReadings) FailureReason() string {
	for _, r := range g.failureProbes() {
		if r.failed() {
			return strings.TrimSpace(string(r))
		}
	}
	return ""
}

// HasFailedGPU reports whether any GPU in the document failed. Failure
// injection is scoped to one device, so the healthy siblings must not mask it.
func (s GPUSnapshot) HasFailedGPU() bool {
	for i := range s.log.GPUs {
		if (GPUReadings{gpu: s.log.GPUs[i], index: i}).Failed() {
			return true
		}
	}
	return false
}

// FailedGPUs lists the failed GPUs as "label: reason", for assertion messages.
func (s GPUSnapshot) FailedGPUs() []string {
	var failed []string
	for i := range s.log.GPUs {
		g := GPUReadings{gpu: s.log.GPUs[i], index: i}
		if g.Failed() {
			failed = append(failed, fmt.Sprintf("%s: %s", g.Label(), g.FailureReason()))
		}
	}
	return failed
}

// UncorrectedECCAggregate is the total of this GPU's aggregate uncorrectable
// counters — the XML equivalent of --query-gpu=ecc.errors.uncorrected.aggregate.total.
// Counters reading N/A are skipped, because SRAM counters are unsupported on
// these profiles and dropping the whole total for them would hide the DRAM
// errors the assertion is looking for. false means no counter was numeric,
// which is what a failed device reports.
func (g GPUReadings) UncorrectedECCAggregate() (int, bool) {
	total, counted := 0, false
	for _, r := range []reading{
		g.gpu.ECCErrors.Aggregate.SRAMUncorrectableParity,
		g.gpu.ECCErrors.Aggregate.SRAMUncorrectableSECDED,
		g.gpu.ECCErrors.Aggregate.DRAMUncorrectable,
	} {
		if v, ok := r.intValue(); ok {
			total += v
			counted = true
		}
	}
	return total, counted
}

// MaxUncorrectedECCAggregate is the largest per-GPU aggregate uncorrectable
// total in the document. ECC injection targets a single GPU, so the maximum is
// what tells a tripped counter from a clean cluster.
func (s GPUSnapshot) MaxUncorrectedECCAggregate() (int, bool) {
	maxVal, counted := 0, false
	for i := range s.log.GPUs {
		v, ok := (GPUReadings{gpu: s.log.GPUs[i], index: i}).UncorrectedECCAggregate()
		if !ok {
			continue
		}
		counted = true
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal, counted
}

// The scalar readings, one per --query-gpu field they replace. Each returns
// false when the element is absent, N/A or an NVML error body, so a caller
// polling a lost GPU cannot mistake a failure for a zero — which is what
// parsing "[GPU is lost]" out of the CSV used to do.

// TemperatureC is <gpu_temp>, i.e. temperature.gpu.
func (g GPUReadings) TemperatureC() (int, bool) { return g.gpu.Temperature.GPUTemp.intValue() }

// UtilizationGPUPercent is <gpu_util>, i.e. utilization.gpu.
func (g GPUReadings) UtilizationGPUPercent() (int, bool) { return g.gpu.Utilization.GPU.intValue() }

// UtilizationMemoryPercent is <memory_util>, i.e. utilization.memory.
func (g GPUReadings) UtilizationMemoryPercent() (int, bool) {
	return g.gpu.Utilization.Memory.intValue()
}

// SMClockMHz is <sm_clock> inside <clocks>, i.e. clocks.sm. The <max_clocks>
// sibling repeats the element name and is deliberately not read.
func (g GPUReadings) SMClockMHz() (int, bool) { return g.gpu.Clocks.SMClock.intValue() }

// MemoryUsedMiB is <used> inside <fb_memory_usage>, i.e. memory.used. The
// bar1_memory_usage sibling repeats the element name and is not read.
func (g GPUReadings) MemoryUsedMiB() (int, bool) { return g.gpu.FBMemoryUsage.Used.intValue() }

// MemoryTotalMiB is <total> inside <fb_memory_usage>, i.e. memory.total.
func (g GPUReadings) MemoryTotalMiB() (int, bool) { return g.gpu.FBMemoryUsage.Total.intValue() }

// PowerLimitW is <current_power_limit>, i.e. power.limit.
func (g GPUReadings) PowerLimitW() (float64, bool) {
	return g.gpu.PowerReadings.CurrentPowerLimit.floatValue()
}

// PowerMinLimitW is <min_power_limit>, i.e. power.min_limit.
func (g GPUReadings) PowerMinLimitW() (float64, bool) {
	return g.gpu.PowerReadings.MinPowerLimit.floatValue()
}

// PowerMaxLimitW is <max_power_limit>, i.e. power.max_limit.
func (g GPUReadings) PowerMaxLimitW() (float64, bool) {
	return g.gpu.PowerReadings.MaxPowerLimit.floatValue()
}

// PowerDrawW is power.draw. nvidia-smi splits the draw into an instant and an
// averaged element and which one plain `power.draw` aliases varies by driver
// release; the mock resolves both from nvmlDeviceGetPowerUsage, so either
// answers. Instant is preferred because it is the later sample.
func (g GPUReadings) PowerDrawW() (float64, bool) {
	if w, ok := g.gpu.PowerReadings.InstantPowerDraw.floatValue(); ok {
		return w, true
	}
	return g.gpu.PowerReadings.AveragePowerDraw.floatValue()
}

// FanSpeed is the <fan_speed> body as rendered — "N/A" on a passively-cooled
// board, "57 %" when a speed is reported. Callers compare the body rather than
// a number because N/A is a legitimate baseline they must round-trip.
func (g GPUReadings) FanSpeed() string { return strings.TrimSpace(string(g.gpu.FanSpeed)) }

// FanSpeedPercent is FanSpeed as a number, false when the board reports N/A.
func (g GPUReadings) FanSpeedPercent() (int, bool) { return g.gpu.FanSpeed.intValue() }

// PerformanceState is <performance_state>, i.e. pstate ("P0".."P15").
func (g GPUReadings) PerformanceState() string {
	return strings.TrimSpace(string(g.gpu.PerformanceState))
}

// ThermalSlowdownState is <clocks_event_reason_hw_thermal_slowdown>, i.e.
// clocks_throttle_reasons.hw_thermal_slowdown ("Active" / "Not Active").
func (g GPUReadings) ThermalSlowdownState() string {
	return strings.TrimSpace(string(g.gpu.ClocksEventReasons.HWThermalSlowdown))
}

// SMIProcess is one decoded <process_info> entry.
type SMIProcess struct {
	PID       int
	Name      string
	MemoryMiB int
}

// Processes decodes this GPU's <processes> block.
func (g GPUReadings) Processes() ([]SMIProcess, error) {
	infos := g.gpu.Processes.Infos
	processes := make([]SMIProcess, 0, len(infos))
	for _, info := range infos {
		mib, ok := info.UsedMemory.intValue()
		if !ok {
			return nil, fmt.Errorf("%s: pid %d used_memory = %q, want a MiB reading",
				g.Label(), info.PID, string(info.UsedMemory))
		}
		processes = append(processes, SMIProcess{PID: info.PID, Name: info.Name, MemoryMiB: mib})
	}
	return processes, nil
}

// DiffInventoryXML checks the document describes exactly wantGPUs devices, all
// named wantProductName. product_name carries the profile's DisplayName
// verbatim, so this is an equality check rather than a substring search.
func DiffInventoryXML(out, wantProductName string, wantGPUs int) []string {
	snap, err := ParseGPUSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	attached, ok := snap.AttachedGPUs()
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("attached_gpus = %q, want %d",
			string(snap.log.AttachedGPUs), wantGPUs))
	case attached != wantGPUs:
		problems = append(problems, fmt.Sprintf("attached_gpus = %d, want %d", attached, wantGPUs))
	}
	// nvidia-smi can report a count it then fails to describe; a mismatch means
	// the document is truncated and every later index is suspect.
	if ok && attached != snap.Count() {
		problems = append(problems, fmt.Sprintf(
			"attached_gpus = %d but the document carries %d <gpu> elements", attached, snap.Count()))
	}
	for i, name := range snap.ProductNames() {
		if name != wantProductName {
			problems = append(problems, fmt.Sprintf("GPU %d product_name = %q, want %q",
				i, name, wantProductName))
		}
	}
	return problems
}

// DiffNoProcessesXML checks every GPU reports an empty <processes> block. With
// no processes configured the process-detail-list path must report none: a prior
// bug had the internal export-table stub return SUCCESS without zeroing the
// caller's count, so nvidia-smi rendered its uninitialised buffer as hundreds of
// phantom PID 0 entries.
func DiffNoProcessesXML(out string) []string {
	snap, err := ParseGPUSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.log.GPUs {
		for _, p := range gpu.Processes.Infos {
			problems = append(problems, fmt.Sprintf("%s reports pid %d (%s), want no processes",
				gpu.label(i), p.PID, p.Name))
		}
	}
	return problems
}

// diffIntReading compares one element body against the number the mock was
// configured with. A non-numeric body is reported as a missing getter rather
// than as a wrong value, because that is the shape both #636 and #637 took: the
// value was parsed from the config and nvidia-smi still rendered N/A.
func diffIntReading(name string, got reading, want int, unit string) []string {
	value, ok := got.intValue()
	switch {
	case !ok:
		return []string{fmt.Sprintf(
			"%s = %q, want %d%s; a non-numeric reading means the NVML getter is missing or unimplemented",
			name, string(got), want, unit)}
	case value != want:
		return []string{fmt.Sprintf("%s = %d%s, want %d%s", name, value, unit, want, unit)}
	}
	return nil
}

// DiffJpgOfaUtilizationXML checks the jpeg_util and ofa_util elements against
// the configured utilization.jpeg and utilization.ofa percentages, for every GPU
// in the output. A GPU-scoped getter that answers for only one device is
// therefore caught. An "N/A" reading is the defect this exists to catch (#637).
func DiffJpgOfaUtilizationXML(out string, wantJPEG, wantOFA int) []string {
	snap, err := ParseGPUSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.log.GPUs {
		name := gpu.label(i)
		problems = append(problems, diffIntReading(name+" jpeg_util", gpu.Utilization.JPEG, wantJPEG, " %")...)
		problems = append(problems, diffIntReading(name+" ofa_util", gpu.Utilization.OFA, wantOFA, " %")...)
	}
	return problems
}

// EncoderFBCStats are the non-default values issue #636 expects nvidia-smi to
// surface.
type EncoderFBCStats struct {
	SessionCount     int
	AverageFPS       int
	AverageLatencyUS int
}

// DiffEncoderFBCXML checks the encoder_stats, fbc_stats and
// accounting_mode_buffer_size elements of every GPU. All three read N/A while
// the NVML exports were generated stubs (#636).
func DiffEncoderFBCXML(out string, encoder, fbc EncoderFBCStats, accountingBufferSize int) []string {
	snap, err := ParseGPUSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.log.GPUs {
		name := gpu.label(i)
		problems = append(problems, diffStatsBlock(name+" encoder_stats", gpu.EncoderStats, encoder)...)
		problems = append(problems, diffStatsBlock(name+" fbc_stats", gpu.FBCStats, fbc)...)
		problems = append(problems, diffIntReading(name+" accounting_mode_buffer_size",
			gpu.AccountingModeBufferSize, accountingBufferSize, "")...)
	}
	return problems
}

func diffStatsBlock(name string, got nvidiaSMIStatsBlock, want EncoderFBCStats) []string {
	var problems []string
	problems = append(problems, diffIntReading(name+" session_count", got.SessionCount, want.SessionCount, "")...)
	problems = append(problems, diffIntReading(name+" average_fps", got.AverageFPS, want.AverageFPS, "")...)
	return append(problems,
		diffIntReading(name+" average_latency", got.AverageLatency, want.AverageLatencyUS, " us")...)
}
