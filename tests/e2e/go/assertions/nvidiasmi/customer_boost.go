// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strings"
)

// MaxCustomerBoostClockProblems checks the "Max Customer Boost Clocks" row of
// every GPU two ways: against wantMHz, the profile's clocks.graphics_max, and
// against the <max_clocks> graphics reading beside it, which it must equal.
//
// N/A is the defect this exists to catch (#712). Both NVML getters that can
// answer the row — nvmlDeviceGetMaxCustomerBoostClock and the clock-id form of
// nvmlDeviceGetClock — were generated stubs, so the mock said the driver could
// not report an OEM boost ceiling where every real board reports one.
//
// The second comparison is what keeps the first from being a restatement of the
// config. All seven captures in testdata/hardware report
// max_customer_boost_clocks equal to max_clocks, which is why the mock resolves
// the ceiling from clocks.graphics_max rather than from a key of its own; nvml.h
// permits an OEM to set it lower, so pinning the equality here means a profile
// that ever needs the two to differ has to change this check and say why.
func MaxCustomerBoostClockProblems(out string, wantMHz int) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i := range snap.doc.GPUs {
		gpu := snap.gpu(i)
		name := gpu.Label()
		problems = append(problems, intReadingProblems(
			name+" max_customer_boost_clocks/graphics_clock",
			snap.doc.GPUs[i].MaxCustomerBoostClocks.GraphicsClock, wantMHz, " MHz")...)
		problems = append(problems, boostCeilingAgreementProblems(name, gpu)...)
	}
	return problems
}

// boostCeilingAgreementProblems compares the OEM ceiling against the boost
// maximum beside it. An unreadable max_clocks is reported rather than skipped:
// silently dropping the comparison is how the check would rot into a
// restatement of the config if the driver renamed the element.
func boostCeilingAgreementProblems(name string, gpu GPU) []string {
	boostMax, ok := gpu.MaxGraphicsClockMHz()
	if !ok {
		return []string{fmt.Sprintf(
			"%s max_clocks/graphics_clock = %q, want a MHz reading to compare the OEM ceiling against",
			name, strings.TrimSpace(string(gpu.element.MaxClocks.GraphicsClock)))}
	}
	// A non-numeric ceiling is already reported by the caller as the missing
	// getter it is; reporting it twice would say nothing new.
	ceiling, ok := gpu.MaxCustomerBoostGraphicsClockMHz()
	if ok && ceiling != boostMax {
		return []string{fmt.Sprintf(
			"%s max_customer_boost_clocks/graphics_clock = %d MHz but max_clocks/graphics_clock = %d MHz; "+
				"no captured board sets the OEM ceiling apart from the boost maximum",
			name, ceiling, boostMax)}
	}
	return nil
}
