// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strconv"
	"strings"
)

// The checks over a decoded document. Each is named *Problems because it
// returns what it found instead of failing: that reports every wrong reading in
// one pass rather than one per fix-and-rerun cycle, lets a caller poll the check
// inside an Eventually until the mock converges, and keeps the comparison
// unit-testable without a cluster. exec.go turns them into Gomega assertions for
// specs that want to fail immediately.
//
// The architecture-dependent threshold check is large enough to live in
// temperature.go.

// InventoryProblems checks the document describes exactly wantGPUs devices, all
// named wantProductName. product_name carries the profile's DisplayName
// verbatim, so this is an equality check rather than a substring search.
func InventoryProblems(out, wantProductName string, wantGPUs int) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	attached, ok := snap.AttachedGPUs()
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("attached_gpus = %q, want %d",
			string(snap.doc.AttachedGPUs), wantGPUs))
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

// PhantomProcessProblems checks every GPU reports an empty <processes> block. With no
// processes configured the process-detail-list path must report none: a prior
// bug had the internal export-table stub return SUCCESS without zeroing the
// caller's count, so nvidia-smi rendered its uninitialised buffer as hundreds of
// phantom PID 0 entries.
func PhantomProcessProblems(out string) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		for _, p := range gpu.Processes.Infos {
			problems = append(problems, fmt.Sprintf("%s reports pid %d (%s), want no processes",
				gpu.label(i), p.PID, p.Name))
		}
	}
	return problems
}

// intReadingProblems compares one element body against the number the mock was
// configured with. A non-numeric body is reported as a missing getter rather
// than as a wrong value, because that is the shape both #636 and #637 took: the
// value was parsed from the config and nvidia-smi still rendered N/A.
func intReadingProblems(name string, got reading, want int, unit string) []string {
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

// JpgOfaUtilizationProblems checks the jpeg_util and ofa_util elements against the
// configured utilization.jpeg and utilization.ofa percentages, for every GPU in
// the output. A GPU-scoped getter that answers for only one device is therefore
// caught. An "N/A" reading is the defect this exists to catch (#637).
func JpgOfaUtilizationProblems(out string, wantJPEG, wantOFA int) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		name := gpu.label(i)
		problems = append(problems, intReadingProblems(name+" jpeg_util", gpu.Utilization.JPEG, wantJPEG, " %")...)
		problems = append(problems, intReadingProblems(name+" ofa_util", gpu.Utilization.OFA, wantOFA, " %")...)
	}
	return problems
}

// C2CModeProblems checks the c2c_mode element of every GPU against the
// profile's declared nvlink.c2c_enabled. wantEnabled selects the expected body:
// "Enabled" on a Grace board, "N/A" on every other one. "Disabled" satisfies
// neither, and that is deliberate — the engine answers
// NVML_ERROR_NOT_SUPPORTED rather than a false reading, which nvidia-smi
// renders as N/A. Every profile read N/A while the NVML entry point was a
// generated stub (#639).
//
// A failed GPU is skipped instead of compared. c2c_mode is answered from a
// handle-lookup path that deliberately does not tick the failure injector, so a
// lost device reports either its board's real C2C state or an NVML error body
// depending on which element nvidia-smi asked for first; neither says anything
// about this fix. Skipping every GPU is itself reported, so a document in which
// they all failed cannot pass by having nothing left to check.
func C2CModeProblems(out string, wantEnabled bool) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	want := "N/A"
	if wantEnabled {
		want = "Enabled"
	}

	var problems []string
	compared := 0
	for i := range snap.doc.GPUs {
		gpu := snap.gpu(i)
		if gpu.Failed() {
			continue
		}
		compared++
		got := gpu.C2CMode()
		switch {
		case !gpu.element.C2CMode.present():
			problems = append(problems, fmt.Sprintf(
				"%s emits no c2c_mode element, want %q; the driver may have renamed it", gpu.Label(), want))
		case got != want:
			problems = append(problems, fmt.Sprintf("%s c2c_mode = %q, want %q", gpu.Label(), got, want))
		}
	}
	if compared == 0 {
		return []string{"no GPU had a comparable c2c_mode reading: every device in the document failed"}
	}
	return problems
}

// bareMetalVirtualizationMode is what nvidia-smi renders for
// NVML_GPU_VIRTUALIZATION_MODE_NONE.
const bareMetalVirtualizationMode = "None"

// VirtualizationModeProblems checks every GPU reports the bare-metal
// virtualization mode the profiles configure, with no vGPU state alongside it.
// "N/A" is the defect this exists to catch (#640): it says the driver cannot
// tell whether the GPU is virtualized, where real hardware always answers.
func VirtualizationModeProblems(out string) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		name := gpu.label(i)
		if mode := strings.TrimSpace(string(gpu.Virtualization.Mode)); mode != bareMetalVirtualizationMode {
			problems = append(problems, fmt.Sprintf("%s virtualization_mode = %q, want %q",
				name, mode, bareMetalVirtualizationMode))
		}
		problems = append(problems, unsupportedReadingProblems(
			name+" host_vgpu_mode", gpu.Virtualization.HostVGPUMode)...)
		problems = append(problems, unsupportedReadingProblems(
			name+" vgpu_heterogeneous_mode", gpu.Virtualization.HeterogeneousMode)...)
	}
	return problems
}

// unsupportedReadingProblems checks an element the mock must leave unsupported.
// An absent element passes: nvidia-smi omits elements the driver never reports,
// which is a stronger form of the same claim.
func unsupportedReadingProblems(name string, got reading) []string {
	if got.present() && !got.unsupported() {
		return []string{fmt.Sprintf("%s = %q, want \"N/A\"", name, strings.TrimSpace(string(got)))}
	}
	return nil
}

// pmonAcceptableExitCodes are the outcomes of `nvidia-smi pmon` that are not a
// regression: 0 if the process-monitor path ever becomes supported, and the 255
// it exits with today after printing "Not supported on the device(s)". Anything
// else is abnormal — above all the 139 kubectl reports for a SIGSEGV, which is
// how the reverted attempt behind PR #630 failed.
var pmonAcceptableExitCodes = []int{0, 255}

// ProcessMonitorProblems judges an `nvidia-smi pmon` exit code. pmon refuses to
// run against the mock, so a non-zero exit is the baseline rather than the
// defect; the check exists to catch it dying instead of refusing.
func ProcessMonitorProblems(exitCode int, output string) []string {
	for _, ok := range pmonAcceptableExitCodes {
		if exitCode == ok {
			return nil
		}
	}
	return []string{fmt.Sprintf(
		"nvidia-smi pmon -c 1 exited %d, want one of %v (139 is a SIGSEGV through kubectl): %s",
		exitCode, pmonAcceptableExitCodes, output)}
}

// EncoderFBCStats are the non-default values issue #636 expects nvidia-smi to
// surface.
type EncoderFBCStats struct {
	SessionCount     int
	AverageFPS       int
	AverageLatencyUS int
}

// EncoderFBCProblems checks the encoder_stats, fbc_stats and
// accounting_mode_buffer_size elements of every GPU. All three read N/A while
// the NVML exports were generated stubs (#636).
func EncoderFBCProblems(out string, encoder, fbc EncoderFBCStats, accountingBufferSize int) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		name := gpu.label(i)
		problems = append(problems, statsBlockProblems(name+" encoder_stats", gpu.EncoderStats, encoder)...)
		problems = append(problems, statsBlockProblems(name+" fbc_stats", gpu.FBCStats, fbc)...)
		problems = append(problems, intReadingProblems(name+" accounting_mode_buffer_size",
			gpu.AccountingModeBufferSize, accountingBufferSize, "")...)
	}
	return problems
}

// PCIeIdentityProblems checks the per-GPU PCIe identity elements for values no
// real GPU can report (#638):
//
//   - max_link_gen, max_device_link_gen and max_host_link_gen all equal the
//     profile's configured pcie.max_link_gen. max_device_link_gen read N/A while
//     nvmlDeviceGetGpuMaxPcieLinkGeneration was a generated stub, and
//     max_host_link_gen read 0 while the internal export-table slot behind it
//     went unserved. Requiring the three to agree encodes the invariant a Gen0
//     host maximum violated: a link never negotiates higher than either
//     endpoint supports.
//   - board_id is non-zero and unique across the node. Every GPU reported 0x0,
//     leaving the GPUs of a multi-GPU node indistinguishable by board ID.
//
// wantGPUs guards against a truncated document silently passing every per-GPU
// check.
func PCIeIdentityProblems(out string, wantGPUs, wantMaxLinkGen int) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	if snap.Count() != wantGPUs {
		problems = append(problems, fmt.Sprintf("nvidia-smi XML reported %d GPUs, want %d",
			snap.Count(), wantGPUs))
	}
	boardIDs := make(map[uint64]string, snap.Count())
	for i, gpu := range snap.doc.GPUs {
		name := gpu.label(i)
		gen := gpu.PCI.GPULinkInfo.PCIeGen
		problems = append(problems,
			intReadingProblems(name+" max_link_gen", gen.Max, wantMaxLinkGen, "")...)
		problems = append(problems,
			intReadingProblems(name+" max_device_link_gen", gen.DeviceMax, wantMaxLinkGen, "")...)
		problems = append(problems,
			intReadingProblems(name+" max_host_link_gen", gen.HostMax, wantMaxLinkGen, "")...)
		problems = append(problems, boardIDProblems(name, gpu.BoardID, boardIDs)...)
	}
	return problems
}

// boardIDProblems validates one GPU's board_id and records it in seen so later
// GPUs are checked for duplicates. It keys on the parsed value rather than the
// rendered text, so two renderings of one board (0x700 and 0x0700) still
// collide.
func boardIDProblems(name string, got reading, seen map[uint64]string) []string {
	if !got.present() {
		return []string{name + " board_id is empty or absent"}
	}
	text := strings.TrimSpace(string(got))
	value, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(text), "0x"), 16, 64)
	if err != nil {
		return []string{fmt.Sprintf("%s board_id = %q, want a hex value", name, text)}
	}
	if value == 0 {
		return []string{fmt.Sprintf(
			"%s board_id = %s, want non-zero so the GPUs of a node are distinguishable", name, text)}
	}
	if prev, dup := seen[value]; dup {
		return []string{fmt.Sprintf("%s board_id = %s duplicates %s", name, text, prev)}
	}
	seen[value] = name
	return nil
}

// SramECCCounters are the three SRAM counters of one scope, volatile or
// aggregate.
type SramECCCounters struct {
	Correctable         int
	UncorrectableParity int
	UncorrectableSECDED int
}

// SramECCSources is the expected <aggregate_uncorrectable_sram_sources>
// breakdown: which unit reported the aggregate uncorrectable SRAM errors.
type SramECCSources struct {
	L2              int
	SM              int
	Microcontroller int
	PCIe            int
	Other           int
}

// SramECCLayout selects which of the two SRAM renderings nvidia-smi uses. Which
// one it picks depends on the GPU's architecture, so a spec asserting on the
// SRAM rows has to say which hardware it is looking at.
type SramECCLayout int

const (
	// SramECCDetailed is the Ampere-and-later rendering: the uncorrectable count
	// is split into parity and SEC-DED, and the aggregate scope carries the
	// per-unit source breakdown and the threshold flag. It is the zero value
	// because every profile the mock ships bar t4 is Ampere or later.
	SramECCDetailed SramECCLayout = iota
	// SramECCCombined is the pre-Ampere rendering: one SRAM Uncorrectable row
	// holding both uncorrectable flavours, with no source breakdown and no
	// threshold flag at all.
	SramECCCombined
)

// SramECCState is the whole SRAM ECC reporting a spec expects on every GPU.
type SramECCState struct {
	Volatile  SramECCCounters
	Aggregate SramECCCounters
	// Sources and ThresholdExceeded are only read in the detailed layout, since
	// pre-Ampere output has nowhere to report them.
	Sources           SramECCSources
	ThresholdExceeded bool
	Layout            SramECCLayout
}

// SramECCProblems checks the SRAM half of every GPU's <ecc_errors> block: the
// per-scope counters and, in the detailed layout, the aggregate uncorrectable
// source breakdown and sram_threshold_exceeded. Every one of them read N/A while
// nvmlDeviceGetSramEccErrorStatus was a stub and the DRAM counters answered for
// SRAM locations, so a health checker could not tell a GPU with no SRAM errors
// from one whose SRAM state is unknown (#641). Zero is a valid expectation and
// the point of the check: a healthy GPU must report 0, not N/A.
//
// want.Layout must match the architecture under test, because nvidia-smi renders
// the two differently and the elements of one are simply absent from the other.
func SramECCProblems(out string, want SramECCState) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		name := gpu.label(i)
		problems = append(problems,
			sramCountersProblems(name+" volatile", gpu.ECCErrors.Volatile, want.Volatile, want.Layout)...)
		problems = append(problems,
			sramCountersProblems(name+" aggregate", gpu.ECCErrors.Aggregate, want.Aggregate, want.Layout)...)
		if want.Layout == SramECCCombined {
			continue
		}
		problems = append(problems, sramSourcesProblems(name, gpu.ECCErrors.SRAMSources, want.Sources)...)
		problems = append(problems, yesNoReadingProblems(name+" aggregate sram_threshold_exceeded",
			gpu.ECCErrors.Aggregate.SRAMThresholdExceeded, want.ThresholdExceeded)...)
	}
	return problems
}

func sramCountersProblems(name string, got eccCounters, want SramECCCounters, layout SramECCLayout) []string {
	problems := intReadingProblems(name+" sram_correctable", got.SRAMCorrectable, want.Correctable, "")
	if layout == SramECCCombined {
		// Pre-Ampere reports one row for both uncorrectable flavours, so the
		// expectation is their sum.
		return append(problems, intReadingProblems(name+" sram_uncorrectable", got.SRAMUncorrectable,
			want.UncorrectableParity+want.UncorrectableSECDED, "")...)
	}
	problems = append(problems, intReadingProblems(name+" sram_uncorrectable_parity",
		got.SRAMUncorrectableParity, want.UncorrectableParity, "")...)
	return append(problems, intReadingProblems(name+" sram_uncorrectable_secded",
		got.SRAMUncorrectableSECDED, want.UncorrectableSECDED, "")...)
}

func sramSourcesProblems(name string, got eccSRAMSources, want SramECCSources) []string {
	sources := []struct {
		element string
		got     reading
		want    int
	}{
		{"sram_l2", got.L2, want.L2},
		{"sram_sm", got.SM, want.SM},
		{"sram_microcontroller", got.Microcontroller, want.Microcontroller},
		{"sram_pcie", got.PCIe, want.PCIe},
		{"sram_other", got.Other, want.Other},
	}
	problems := make([]string, 0, len(sources))
	for _, source := range sources {
		problems = append(problems,
			intReadingProblems(name+" "+source.element, source.got, source.want, "")...)
	}
	return problems
}

// yesNoReadingProblems compares a reading nvidia-smi renders as Yes or No. An
// N/A body fails: it means the getter behind the flag is missing, which is the
// state a spec asserting on the flag exists to rule out.
func yesNoReadingProblems(name string, got reading, want bool) []string {
	body := strings.TrimSpace(string(got))
	wantBody := "No"
	if want {
		wantBody = "Yes"
	}
	switch {
	case strings.EqualFold(body, "Yes"), strings.EqualFold(body, "No"):
		if !strings.EqualFold(body, wantBody) {
			return []string{fmt.Sprintf("%s = %q, want %q", name, body, wantBody)}
		}
		return nil
	default:
		return []string{fmt.Sprintf(
			"%s = %q, want %q; a body that is neither Yes nor No means the NVML getter is missing",
			name, body, wantBody)}
	}
}

// RowRemapState is the <remapped_rows> state a spec expects on every GPU.
type RowRemapState struct {
	Correctable   int
	Uncorrectable int
	Pending       bool
	Failure       bool
	// HistogramBanks is the bank count the configured histogram puts in its
	// max-availability bucket. Zero means the GPU must report the histogram as
	// N/A, which is what pre-Ampere hardware without row remapping does.
	HistogramBanks int
}

// RowRemapProblems checks every GPU's <remapped_rows> block: the retired-row
// counters, the two flags, and the bank availability histogram. The histogram
// read N/A on every profile while nvmlDeviceGetRowRemapperHistogram was a stub,
// which on Ampere-and-later hardware means the GPU cannot report how much remap
// capacity is left (#641).
//
// The histogram is matched on its bank count appearing in the block rather than
// by decoding buckets: the bucket element names belong to the driver's DTD,
// while the count is what the profile configures and therefore what proves the
// configured value reached the caller.
func RowRemapProblems(out string, want RowRemapState) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i := range snap.doc.GPUs {
		gpu, rows := snap.gpu(i), snap.doc.GPUs[i].RemappedRows
		name := gpu.Label()
		problems = append(problems,
			intReadingProblems(name+" remapped_row_corr", rows.Correctable, want.Correctable, "")...)
		problems = append(problems,
			intReadingProblems(name+" remapped_row_unc", rows.Uncorrectable, want.Uncorrectable, "")...)
		problems = append(problems,
			yesNoReadingProblems(name+" remapped_row_pending", rows.Pending, want.Pending)...)
		problems = append(problems,
			yesNoReadingProblems(name+" remapped_row_failure", rows.Failure, want.Failure)...)
		problems = append(problems, histogramProblems(name, gpu, want.HistogramBanks)...)
	}
	return problems
}

func histogramProblems(name string, gpu GPU, wantBanks int) []string {
	body := strings.TrimSpace(gpu.element.RemappedRows.Histogram.Body)
	switch {
	case wantBanks == 0:
		if gpu.RowRemapHistogramPopulated() {
			return []string{fmt.Sprintf(
				"%s row_remapper_histogram = %q, want N/A: this profile configures no remap availability",
				name, body)}
		}
	case !gpu.RowRemapHistogramPopulated():
		return []string{fmt.Sprintf(
			"%s row_remapper_histogram = %q, want %d banks; N/A means the histogram getter is missing",
			name, body, wantBanks)}
	case !strings.Contains(body, strconv.Itoa(wantBanks)):
		return []string{fmt.Sprintf("%s row_remapper_histogram = %q, want it to report %d banks",
			name, body, wantBanks)}
	}
	return nil
}

func statsBlockProblems(name string, got statsBlock, want EncoderFBCStats) []string {
	var problems []string
	problems = append(problems, intReadingProblems(name+" session_count", got.SessionCount, want.SessionCount, "")...)
	problems = append(problems, intReadingProblems(name+" average_fps", got.AverageFPS, want.AverageFPS, "")...)
	return append(problems,
		intReadingProblems(name+" average_latency", got.AverageLatency, want.AverageLatencyUS, " us")...)
}
