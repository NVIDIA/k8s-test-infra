// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strings"
)

// A decoded document and the readings assertions take from it. One document
// answers every question about a GPU's state, so a scenario reading several
// fields costs one exec rather than one per field.

// Snapshot is one decoded document.
type Snapshot struct {
	doc document
}

// ParseSnapshot decodes `nvidia-smi -q -x` output.
func ParseSnapshot(out string) (Snapshot, error) {
	doc, err := parse(out)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{doc: doc}, nil
}

// AttachedGPUs is the <attached_gpus> reading, nvidia-smi's own count. It is
// reported separately from Count so a truncated document is detectable.
func (s Snapshot) AttachedGPUs() (int, bool) { return s.doc.AttachedGPUs.intValue() }

// Count is the number of <gpu> elements in the document.
func (s Snapshot) Count() int { return len(s.doc.GPUs) }

// ProductNames lists <product_name> in the order nvidia-smi emits GPUs, which
// is the order it indexes them by.
func (s Snapshot) ProductNames() []string {
	names := make([]string, 0, len(s.doc.GPUs))
	for _, gpu := range s.doc.GPUs {
		names = append(names, strings.TrimSpace(string(gpu.ProductName)))
	}
	return names
}

// UUIDs lists <uuid> in nvidia-smi's GPU order.
func (s Snapshot) UUIDs() []string {
	uuids := make([]string, 0, len(s.doc.GPUs))
	for _, gpu := range s.doc.GPUs {
		uuids = append(uuids, strings.TrimSpace(string(gpu.UUID)))
	}
	return uuids
}

// GPU returns the readings for the GPU at index, in nvidia-smi's order — the
// same index `--id=N` takes.
func (s Snapshot) GPU(index int) (GPU, error) {
	if index < 0 || index >= len(s.doc.GPUs) {
		return GPU{}, fmt.Errorf(
			"nvidia-smi XML reported %d GPUs, want an entry at index %d", len(s.doc.GPUs), index)
	}
	return s.gpu(index), nil
}

func (s Snapshot) gpu(index int) GPU {
	return GPU{element: s.doc.GPUs[index], index: index}
}

// GPU exposes one GPU's readings.
type GPU struct {
	element gpuElement
	index   int
}

// Label names the GPU in assertion output.
func (g GPU) Label() string { return g.element.label(g.index) }

// UUID is the <uuid> body.
func (g GPU) UUID() string { return strings.TrimSpace(string(g.element.UUID)) }

// failureProbes are the elements checked for an NVML error body. A failed
// device renders the string in place of nearly every reading, so any one of
// these is enough; several are read because which ones appear varies with the
// failure mode (a lost device loses all of them, a fallen-off-bus device is
// queried through a different NVML path).
func (g GPU) failureProbes() []reading {
	return []reading{
		g.element.Temperature.GPUTemp,
		g.element.Utilization.GPU,
		g.element.PowerReadings.InstantPowerDraw,
		g.element.Clocks.SMClock,
		g.element.ECCErrors.Aggregate.DRAMUncorrectable,
	}
}

// Failed reports whether nvidia-smi substituted an NVML error string for this
// GPU's readings. It is deliberately not satisfied by "N/A": the readings a
// healthy GPU legitimately does not support (fan_speed on a passively-cooled
// board) would otherwise mark every such device failed.
func (g GPU) Failed() bool {
	for _, r := range g.failureProbes() {
		if r.failed() {
			return true
		}
	}
	return false
}

// FailureReason is the NVML error body behind Failed, for assertion messages.
func (g GPU) FailureReason() string {
	for _, r := range g.failureProbes() {
		if r.failed() {
			return strings.TrimSpace(string(r))
		}
	}
	return ""
}

// HasFailedGPU reports whether any GPU in the document failed. Failure
// injection is scoped to one device, so the healthy siblings must not mask it.
func (s Snapshot) HasFailedGPU() bool {
	for i := range s.doc.GPUs {
		if s.gpu(i).Failed() {
			return true
		}
	}
	return false
}

// FailedGPUs lists the failed GPUs as "label: reason", for assertion messages.
func (s Snapshot) FailedGPUs() []string {
	var failed []string
	for i := range s.doc.GPUs {
		if g := s.gpu(i); g.Failed() {
			failed = append(failed, fmt.Sprintf("%s: %s", g.Label(), g.FailureReason()))
		}
	}
	return failed
}

// UncorrectedECCAggregate is the total of this GPU's aggregate uncorrectable
// counters — the XML equivalent of --query-gpu=ecc.errors.uncorrected.aggregate.total.
// Counters reading N/A are skipped so a GPU with ECC off, whose counters are all
// N/A, does not drop the whole total. false means no counter was numeric, which
// is what a failed device reports.
func (g GPU) UncorrectedECCAggregate() (int, bool) {
	total, counted := 0, false
	for _, r := range []reading{
		g.element.ECCErrors.Aggregate.SRAMUncorrectableParity,
		g.element.ECCErrors.Aggregate.SRAMUncorrectableSECDED,
		g.element.ECCErrors.Aggregate.DRAMUncorrectable,
	} {
		if v, ok := r.intValue(); ok {
			total += v
			counted = true
		}
	}
	return total, counted
}

// ECCEnabled reports whether <current_ecc> reads Enabled. The SRAM and DRAM
// counters only carry values in that mode, so checks over them gate on it
// instead of treating a profile with ECC off as a defect.
func (g GPU) ECCEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(string(g.element.ECCMode.Current)), "Enabled")
}

// ECCEnabled reports whether every GPU in the document has ECC on.
func (s Snapshot) ECCEnabled() bool {
	for i := range s.doc.GPUs {
		if !s.gpu(i).ECCEnabled() {
			return false
		}
	}
	return len(s.doc.GPUs) > 0
}

// RowRemapHistogramPopulated reports whether <row_remapper_histogram> carries
// bank counts rather than N/A, i.e. whether the GPU answered the histogram
// getter at all.
func (g GPU) RowRemapHistogramPopulated() bool {
	body := strings.TrimSpace(g.element.RemappedRows.Histogram.Body)
	return body != "" && !reading(body).unsupported()
}

// MaxUncorrectedECCAggregate is the largest per-GPU aggregate uncorrectable
// total in the document. ECC injection targets a single GPU, so the maximum is
// what tells a tripped counter from a clean cluster.
func (s Snapshot) MaxUncorrectedECCAggregate() (int, bool) {
	maxVal, counted := 0, false
	for i := range s.doc.GPUs {
		v, ok := s.gpu(i).UncorrectedECCAggregate()
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

// The scalar readings, each named after the --query-gpu field it corresponds
// to. All return false when the element is absent, N/A or an NVML error body,
// so a caller polling a lost GPU cannot mistake a failure for a zero.

// TemperatureC is <gpu_temp>, i.e. temperature.gpu.
func (g GPU) TemperatureC() (int, bool) { return g.element.Temperature.GPUTemp.intValue() }

// UtilizationGPUPercent is <gpu_util>, i.e. utilization.gpu.
func (g GPU) UtilizationGPUPercent() (int, bool) { return g.element.Utilization.GPU.intValue() }

// UtilizationMemoryPercent is <memory_util>, i.e. utilization.memory.
func (g GPU) UtilizationMemoryPercent() (int, bool) {
	return g.element.Utilization.Memory.intValue()
}

// SMClockMHz is <sm_clock> inside <clocks>, i.e. clocks.sm. The <max_clocks>
// sibling repeats the element name and is deliberately not read.
func (g GPU) SMClockMHz() (int, bool) { return g.element.Clocks.SMClock.intValue() }

// MaxGraphicsClockMHz is <graphics_clock> inside <max_clocks>, i.e.
// clocks.max.graphics — the boost maximum, not the clock in effect.
func (g GPU) MaxGraphicsClockMHz() (int, bool) {
	return g.element.MaxClocks.GraphicsClock.intValue()
}

// MaxCustomerBoostGraphicsClockMHz is <graphics_clock> inside
// <max_customer_boost_clocks>: the OEM-defined ceiling. false when the board
// reports N/A, so a caller cannot mistake an unanswered getter for 0 MHz.
func (g GPU) MaxCustomerBoostGraphicsClockMHz() (int, bool) {
	return g.element.MaxCustomerBoostClocks.GraphicsClock.intValue()
}

// MemoryUsedMiB is <used> inside <fb_memory_usage>, i.e. memory.used. The
// bar1_memory_usage sibling repeats the element name and is not read.
func (g GPU) MemoryUsedMiB() (int, bool) { return g.element.FBMemoryUsage.Used.intValue() }

// MemoryTotalMiB is <total> inside <fb_memory_usage>, i.e. memory.total.
func (g GPU) MemoryTotalMiB() (int, bool) { return g.element.FBMemoryUsage.Total.intValue() }

// PowerLimitW is <current_power_limit>, i.e. power.limit.
func (g GPU) PowerLimitW() (float64, bool) {
	return g.element.PowerReadings.CurrentPowerLimit.floatValue()
}

// PowerMinLimitW is <min_power_limit>, i.e. power.min_limit.
func (g GPU) PowerMinLimitW() (float64, bool) {
	return g.element.PowerReadings.MinPowerLimit.floatValue()
}

// PowerMaxLimitW is <max_power_limit>, i.e. power.max_limit.
func (g GPU) PowerMaxLimitW() (float64, bool) {
	return g.element.PowerReadings.MaxPowerLimit.floatValue()
}

// PowerDrawW is power.draw. nvidia-smi splits the draw into an instant and an
// averaged element and which one plain `power.draw` aliases varies by driver
// release; the mock resolves both from nvmlDeviceGetPowerUsage, so either
// answers. Instant is preferred because it is the later sample.
func (g GPU) PowerDrawW() (float64, bool) {
	if w, ok := g.element.PowerReadings.InstantPowerDraw.floatValue(); ok {
		return w, true
	}
	return g.element.PowerReadings.AveragePowerDraw.floatValue()
}

// FanSpeed is the <fan_speed> body as rendered — "N/A" on a passively-cooled
// board, "57 %" when a speed is reported. Callers compare the body rather than
// a number because N/A is a legitimate baseline they must round-trip.
func (g GPU) FanSpeed() string { return strings.TrimSpace(string(g.element.FanSpeed)) }

// FanSpeedPercent is FanSpeed as a number, false when the board reports N/A.
func (g GPU) FanSpeedPercent() (int, bool) { return g.element.FanSpeed.intValue() }

// PerformanceState is <performance_state>, i.e. pstate ("P0".."P15").
func (g GPU) PerformanceState() string {
	return strings.TrimSpace(string(g.element.PerformanceState))
}

// ThermalSlowdownState is <clocks_event_reason_hw_thermal_slowdown>, i.e.
// clocks_throttle_reasons.hw_thermal_slowdown ("Active" / "Not Active").
func (g GPU) ThermalSlowdownState() string {
	return strings.TrimSpace(string(g.element.ClocksEventReasons.HWThermalSlowdown))
}

// C2CMode is the <c2c_mode> body as rendered — "Enabled" on a board with an
// NVLink-C2C link to a host CPU, "N/A" on one without. Compared as a body
// rather than a bool because N/A is the correct reading for most profiles and a
// bool would flatten it into the same false as "Disabled", a third state the
// mock never reports.
func (g GPU) C2CMode() string { return strings.TrimSpace(string(g.element.C2CMode)) }

// PlatformInfo is the <platformInfo> block as rendered, each field kept as its
// body rather than a number: "N/A" is the correct reading on every board whose
// platform cannot report a location, and the assertions must round-trip it.
type PlatformInfo struct {
	ChassisSerialNumber string
	SlotNumber          string
	TrayIndex           string
	HostID              string
	PeerType            string
	ModuleID            string
}

// PlatformInfo decodes this GPU's <platformInfo> block.
func (g GPU) PlatformInfo() PlatformInfo {
	p := g.element.PlatformInfo
	return PlatformInfo{
		ChassisSerialNumber: strings.TrimSpace(string(p.ChassisSerialNumber)),
		SlotNumber:          strings.TrimSpace(string(p.SlotNumber)),
		TrayIndex:           strings.TrimSpace(string(p.TrayIndex)),
		HostID:              strings.TrimSpace(string(p.HostID)),
		PeerType:            strings.TrimSpace(string(p.PeerType)),
		ModuleID:            strings.TrimSpace(string(p.ModuleID)),
	}
}

// ModuleID is <module_id> inside <platformInfo>: which GPU this is within its
// node. false when the platform reports no location, so a caller deriving a
// physical position cannot mistake an unsupported board for module 0.
func (g GPU) ModuleID() (int, bool) { return g.element.PlatformInfo.ModuleID.intValue() }

// migDevicesNone is the body nvidia-smi writes into <mig_devices> for a GPU
// with no partitions, whether because MIG is off or because the board cannot
// partition at all. It is what tells that state from a block this schema can no
// longer read.
const migDevicesNone = "None"

// MIGPartition is one decoded <mig_device>.
//
// GPUInstanceID and ComputeInstanceID are the partition's identity in this
// document — the XML carries no MIG UUID and no profile name — and they are the
// keys the capability table under /dev/nvidia-caps is named by.
type MIGPartition struct {
	GPUInstanceID       int
	ComputeInstanceID   int
	MemoryTotalMiB      int
	MultiprocessorCount int
}

// MIGMode is the <current_mig> body as rendered: "Enabled" or "Disabled" on a
// MIG-capable board, "N/A" on one that cannot partition. Kept as a body rather
// than a bool so a caller can tell a board that answered "not partitioned" from
// one that could not answer at all.
func (g GPU) MIGMode() string { return strings.TrimSpace(string(g.element.MIGMode.Current)) }

// MIGModePending is <pending_mig>: the mode the GPU will report after its next
// reset. It differs from MIGMode only while a mode switch is outstanding, which
// is a state a caller asserting on the mode has to be able to exclude.
func (g GPU) MIGModePending() string { return strings.TrimSpace(string(g.element.MIGMode.Pending)) }

// MIGEnabled reports whether the GPU is partitioned and not mid-switch: both
// <current_mig> and <pending_mig> read Enabled.
func (g GPU) MIGEnabled() bool {
	return strings.EqualFold(g.MIGMode(), "Enabled") && strings.EqualFold(g.MIGModePending(), "Enabled")
}

// MIGPartitions decodes this GPU's <mig_devices> block.
//
// A GPU with no partitions yields an empty slice and no error; that is a real
// state, and the negative control the MIG scenario runs asserts on exactly it.
// A block this schema cannot read is an error instead, because a caller
// counting partitions would otherwise read a rename as a board with none.
func (g GPU) MIGPartitions() ([]MIGPartition, error) {
	block := g.element.MIGDevices
	body := strings.TrimSpace(block.Body)

	if len(block.Devices) == 0 {
		switch {
		case body == "":
			return nil, fmt.Errorf(
				"%s: the document carries no <mig_devices> block; nvidia-smi emits one on every GPU, MIG-capable or not",
				g.Label())
		case body != migDevicesNone:
			return nil, fmt.Errorf(
				"%s: <mig_devices> holds content this schema does not recognise, want %q or <mig_device> children: %s",
				g.Label(), migDevicesNone, firstLine(body))
		}
		return nil, nil
	}

	partitions := make([]MIGPartition, 0, len(block.Devices))
	for i, d := range block.Devices {
		var c migReadings
		partition := MIGPartition{
			GPUInstanceID:       c.value("gpu_instance_id", d.GPUInstanceID),
			ComputeInstanceID:   c.value("compute_instance_id", d.ComputeInstanceID),
			MemoryTotalMiB:      c.value("fb_memory_usage/total", d.FBMemoryUsage.Total),
			MultiprocessorCount: c.value("device_attributes/shared/multiprocessor_count", d.DeviceAttributes.Shared.MultiprocessorCount),
		}
		if c.missing != "" {
			return nil, fmt.Errorf("%s: MIG partition %d: %s, want a number", g.Label(), i, c.missing)
		}
		partitions = append(partitions, partition)
	}
	return partitions, nil
}

// MIGPartitionCount is the number of partitions across every GPU in the
// document — what a node-wide expectation compares against.
func (s Snapshot) MIGPartitionCount() (int, error) {
	total := 0
	for i := range s.doc.GPUs {
		partitions, err := s.gpu(i).MIGPartitions()
		if err != nil {
			return 0, err
		}
		total += len(partitions)
	}
	return total, nil
}

// migReadings collects the numeric bodies of one <mig_device>, holding the
// first element that did not answer a number rather than folding it into a
// zero. A partition reported with no memory or no SMs is a partition nothing
// can run on, so it must surface as an error and not as a plausible reading.
type migReadings struct{ missing string }

func (m *migReadings) value(element string, r reading) int {
	v, ok := r.intValue()
	if !ok && m.missing == "" {
		m.missing = fmt.Sprintf("%s = %q", element, strings.TrimSpace(string(r)))
	}
	return v
}

// Process is one decoded <process_info> entry.
type Process struct {
	PID       int
	Name      string
	MemoryMiB int
}

// Processes decodes this GPU's <processes> block.
func (g GPU) Processes() ([]Process, error) {
	infos := g.element.Processes.Infos
	processes := make([]Process, 0, len(infos))
	for _, info := range infos {
		mib, ok := info.UsedMemory.intValue()
		if !ok {
			return nil, fmt.Errorf("%s: pid %d used_memory = %q, want a MiB reading",
				g.Label(), info.PID, string(info.UsedMemory))
		}
		processes = append(processes, Process{PID: info.PID, Name: info.Name, MemoryMiB: mib})
	}
	return processes, nil
}
