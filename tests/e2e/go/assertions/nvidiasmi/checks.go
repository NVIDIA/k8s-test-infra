// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import "fmt"

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

func statsBlockProblems(name string, got statsBlock, want EncoderFBCStats) []string {
	var problems []string
	problems = append(problems, intReadingProblems(name+" session_count", got.SessionCount, want.SessionCount, "")...)
	problems = append(problems, intReadingProblems(name+" average_fps", got.AverageFPS, want.AverageFPS, "")...)
	return append(problems,
		intReadingProblems(name+" average_latency", got.AverageLatency, want.AverageLatencyUS, " us")...)
}
