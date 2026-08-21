// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import "fmt"

// blackwellRemovedFields are the rows a real Blackwell tray answers N/A because
// the hardware feature behind each was removed or never existed: GPU Operation
// Mode is GK110-era, page retirement gave way to row remapping, the target
// temperature and its supported range come from acoustic thresholds Blackwell
// does not implement, and Sparse Operation Mode arrives through an internal
// export-table slot the hardware fails. Older hardware does report them, so
// they are an architecture axis and checked in both directions.
//
// The direction of this check is the inverse of most: it fails a reading that
// is present. A fabricated answer is worse than a missing one, because N/A
// prompts a consumer to look elsewhere while "85 C" or a retired-page count of
// 0 is believed (#679).
func blackwellRemovedFields(gpu gpuElement) []namedReading {
	return []namedReading{
		{"gpu_operation_mode/current_gom", gpu.OperationMode.Current},
		{"gpu_operation_mode/pending_gom", gpu.OperationMode.Pending},
		{"sparse_operation_mode", gpu.SparseOperationMode},
		{"retired_pages/multiple_single_bit_retirement/retired_count", gpu.RetiredPages.SingleBit.Count},
		{"retired_pages/double_bit_retirement/retired_count", gpu.RetiredPages.DoubleBit.Count},
		{"retired_pages/pending_blacklist", gpu.RetiredPages.PendingBlacklist},
		{"retired_pages/pending_retirement", gpu.RetiredPages.PendingRetirement},
		{"temperature/gpu_target_temperature", gpu.Temperature.TargetTemperature},
		{"supported_gpu_target_temp/gpu_target_temp_min", gpu.SupportedTargetTemp.Min},
		{"supported_gpu_target_temp/gpu_target_temp_max", gpu.SupportedTargetTemp.Max},
	}
}

// blackwellAbsentBoardData are rows Blackwell must report N/A that are board
// data rather than an architecture feature. The Power Management Object is
// absent from a Blackwell inforom, but whether an older board carries one is a
// property of that board -- every non-Blackwell profile the mock ships also
// reports N/A -- so the pre-Blackwell direction asserts nothing about it.
func blackwellAbsentBoardData(gpu gpuElement) []namedReading {
	return []namedReading{
		{"inforom_version/pwr_object", gpu.InforomVersion.PWRObject},
	}
}

// BlackwellRemovedFieldProblems checks the architecture presentation of the
// fields Blackwell removed, for every GPU in a `nvidia-smi -q -x` document:
//
//   - Blackwell (preBlackwell=false): every field is absent or N/A. Both are
//     correct — nvidia-smi drops the Supported GPU Target Temp section from the
//     human table once the query fails but keeps the elements with N/A bodies
//     in the XML — so the check rejects a reading rather than requiring one.
//   - pre-Blackwell (preBlackwell=true): every field still carries a reading.
//
// The second half is what makes this a fidelity check rather than a blanket
// "must be N/A": disabling the surfaces everywhere would satisfy the first half
// while silently regressing the Turing and Ampere profiles, whose hardware does
// report them.
func BlackwellRemovedFieldProblems(out string, preBlackwell bool) []string {
	doc, err := parse(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range doc.GPUs {
		name := gpu.label(i)
		fields := blackwellRemovedFields(gpu)
		if preBlackwell {
			for _, field := range fields {
				if !field.raw.present() || field.raw.unsupported() {
					problems = append(problems, fmt.Sprintf(
						"%s: no longer reports %s (body %q)",
						name, field.element, string(field.raw)))
				}
			}
			continue
		}
		for _, field := range append(fields, blackwellAbsentBoardData(gpu)...) {
			if field.raw.present() && !field.raw.unsupported() {
				problems = append(problems, fmt.Sprintf(
					"%s: %s = %q, want N/A on Blackwell",
					name, field.element, string(field.raw)))
			}
		}
	}
	return problems
}
