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

func statsBlockProblems(name string, got statsBlock, want EncoderFBCStats) []string {
	var problems []string
	problems = append(problems, intReadingProblems(name+" session_count", got.SessionCount, want.SessionCount, "")...)
	problems = append(problems, intReadingProblems(name+" average_fps", got.AverageFPS, want.AverageFPS, "")...)
	return append(problems,
		intReadingProblems(name+" average_latency", got.AverageLatency, want.AverageLatencyUS, " us")...)
}
