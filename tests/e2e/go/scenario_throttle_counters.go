//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// Clocks-event counter scenario for issue #678.
//
// Before #678 all five counters in `nvidia-smi -q -x` read N/A, so the output
// contradicted itself: the flags above them stated confidently that no throttle
// reason was active, and the counters could not say whether one ever had been.
// The counters are how throttling is diagnosed after the fact — a workload that
// ran slow is investigated by reading accumulated power-capping or thermal
// time, not by sampling a flag and hoping to catch it.
//
// The healthy direction (five zeros on every GPU of every profile) is asserted
// by nvidiasmi.ThrottleCounters. What this scenario adds is a GPU carrying a
// history: one seeded cause reports its own total, its four siblings stay at
// zero, and the node's other GPUs stay at zero too. Answering a single counter
// for every field id is the cheapest way to stop the N/A and would pass a
// weaker check.

// throttleCounterSeedUS is the seeded power-capping total. It is deliberately
// unlike anything the mock produces on its own — no profile baseline, no
// accrued duration, no round number — so the assertion cannot pass by
// coincidence.
const throttleCounterSeedUS = 7654321

// assertRuntimeThrottleCounterSeeding seeds one cause's accrued time on one GPU
// through the generic `set` path and reads it back from nvidia-smi.
//
// sw_power_cap is the cause chosen because no shipped profile reports it as
// currently active: a cause that is active accrues further time on every read,
// which is correct behaviour but not something an exact assertion can pin. The
// zeroed baseline is asserted first, which is both the healthy expectation and
// the precondition that makes the exact comparison meaningful.
func assertRuntimeThrottleCounterSeeding(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	target := gpuCount(ctx, h, consumer) - 1 // exercise a non-zero index where possible

	By("every GPU starts with five zeroed clocks-event counters")
	Expect(throttleCounterProblems(ctx, h, consumer, func(out string) []string {
		return nvidiasmi.ThrottleCounterProblems(out, nvidiasmi.UnthrottledCounters())
	})).To(BeEmpty(), "the profile must report no accrued throttle time for an exact assertion to mean anything")

	seeded := nvidiasmi.UnthrottledCounters()
	seeded.SWPowerCapUS = throttleCounterSeedUS

	By("seed " + strconv.Itoa(throttleCounterSeedUS) + " us of power capping on GPU " +
		strconv.Itoa(target) + " via nvml-mock-ctl set")
	nvmlMockCtl(ctx, h, "set", "--gpu", strconv.Itoa(target),
		"clocks_throttle_reasons.counters.sw_power_cap_us="+strconv.Itoa(throttleCounterSeedUS))

	Eventually(func() []string {
		return throttleCounterProblems(ctx, h, consumer, func(out string) []string {
			return nvidiasmi.ThrottleCounterProblemsAt(out, target, seeded, nvidiasmi.UnthrottledCounters())
		})
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "GPU %d should report exactly the seeded power-capping time, and only that counter", target)

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() []string {
		return throttleCounterProblems(ctx, h, consumer, func(out string) []string {
			return nvidiasmi.ThrottleCounterProblems(out, nvidiasmi.UnthrottledCounters())
		})
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "every counter should return to the profile baseline after reset")
}

// throttleCounterProblems reads a fresh `nvidia-smi -q -x` document from the
// consumer and hands it to check. A failed exec is returned as a problem rather
// than asserted, so a poll can ride out a consumer restart.
func throttleCounterProblems(ctx SpecContext, h *harness.Harness, consumer kube.PodRef,
	check func(string) []string,
) []string {
	GinkgoHelper()
	res, err := h.Kube.ExecQuiet(ctx, consumer, "nvidia-smi", "-q", "-x")
	if err != nil {
		return []string{"nvidia-smi -q -x: " + err.Error() + ": " + res.Combined()}
	}
	return check(res.Stdout)
}
