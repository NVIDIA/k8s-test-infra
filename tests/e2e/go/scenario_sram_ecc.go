//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// SRAM ECC and row-remap availability (#641). Every SRAM row of nvidia-smi -q
// read N/A while nvmlDeviceGetSramEccErrorStatus and
// nvmlDeviceGetRowRemapperHistogram were generated stubs, so a consumer could
// not tell a GPU with no SRAM errors from one whose SRAM state is unknown — and
// no test could drive an SRAM fault at all.
//
// The distinction these scenarios rest on is that 0 and N/A are different
// answers: zero counters are the healthy baseline the injection then moves, so a
// spec that accepted N/A would pass against a mock that reports nothing.

// sramInjectedCount is the number of SRAM errors the runtime scenario injects.
// Small and non-zero: the assertion is that the exact configured count arrives,
// so any value distinguishable from the zero baseline works.
const sramInjectedCount = 4

// assertSramECCBaseline covers the healthy half of the acceptance criteria: a
// deployed profile must report 0 for every SRAM counter and a populated bank
// availability histogram (where the modelled architecture has row remapping),
// rather than N/A.
func assertSramECCBaseline(ctx SpecContext, h *harness.Harness, consumer kube.PodRef, p profile.Profile) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	By("verify every SRAM ECC counter reads 0 rather than N/A")
	healthy := nvidiasmi.SramECCState{Layout: sramECCLayout(p)}
	expectSmiEventually(ctx, h, consumer, "SRAM ECC counters must read 0, not N/A",
		func(out string) []string { return nvidiasmi.SramECCProblems(out, healthy) })

	// A profile without an availability_histogram block must keep reporting
	// N/A, so this expectation flips rather than being skipped: it is the
	// negative control that the histogram tracks config instead of always
	// answering.
	By(fmt.Sprintf("verify the %s bank remap availability histogram reports %d banks",
		p.Name, p.RowRemapHistogramBanks()))
	want := nvidiasmi.RowRemapState{HistogramBanks: p.RowRemapHistogramBanks()}
	expectSmiEventually(ctx, h, consumer, "remapped_rows must match the profile", func(out string) []string {
		return nvidiasmi.RowRemapProblems(out, want)
	})
}

// assertRuntimeSramECCInjection covers the injection half: drive SRAM ECC errors
// into the running pod with nvml-mock-ctl, confirm nvidia-smi reports the exact
// counts, the threshold flag and the source attribution, then clear them and
// confirm the GPU returns to the zero baseline. The consumer is never restarted,
// which is the capability being tested — a fault has to be injectable mid-test.
func assertRuntimeSramECCInjection(ctx SpecContext, h *harness.Harness, consumer kube.PodRef, p profile.Profile) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	counts := nvidiasmi.SramECCCounters{UncorrectableSECDED: sramInjectedCount}
	layout := sramECCLayout(p)
	injected := nvidiasmi.SramECCState{
		Volatile:  counts,
		Aggregate: counts,
		// The SM is the target because a wrong implementation is most likely to
		// report the total in the catch-all "other" bucket, which this catches.
		Sources:           nvidiasmi.SramECCSources{SM: sramInjectedCount},
		ThresholdExceeded: true,
		Layout:            layout,
	}

	By(fmt.Sprintf("inject %d uncorrectable SEC-DED SRAM errors on the SM of every GPU",
		sramInjectedCount))
	nvmlMockCtl(ctx, h, "sram-ecc", "--gpu", "all", "--type", "secded", "--source", "sm",
		"--threshold-exceeded", strconv.Itoa(sramInjectedCount))

	expectSmiEventually(ctx, h, consumer, "injected SRAM ECC errors must reach the running consumer",
		func(out string) []string { return nvidiasmi.SramECCProblems(out, injected) })

	By("heal the GPUs with sram-ecc count 0 and confirm the counters return to baseline")
	nvmlMockCtl(ctx, h, "sram-ecc", "--gpu", "all", "--type", "secded", "--source", "sm", "0")

	healed := nvidiasmi.SramECCState{Layout: layout}
	expectSmiEventually(ctx, h, consumer, "SRAM ECC counters must return to 0 after healing",
		func(out string) []string { return nvidiasmi.SramECCProblems(out, healed) })

	By("final reset")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}

// sramECCLayout picks the rendering nvidia-smi uses for the profile's
// architecture. Ampere and later split the uncorrectable count and report the
// source breakdown; pre-Ampere reports one combined row and omits the rest, so
// asserting on elements it never emits would fail on the mock's t4 profile even
// though the counters behind them are correct.
func sramECCLayout(p profile.Profile) nvidiasmi.SramECCLayout {
	if p.ReportsDetailedSramECC() {
		return nvidiasmi.SramECCDetailed
	}
	return nvidiasmi.SramECCCombined
}

// expectSmiEventually polls `nvidia-smi -q -x` in the consumer until check
// reports no problems, giving the runtime override TTL time to land. Every
// problem is reported at once so one poll names each wrong reading rather than
// one per fix-and-rerun cycle.
//
// ExecQuiet keeps the ~90 KB document out of the Ginkgo log on every poll.
func expectSmiEventually(
	ctx SpecContext, h *harness.Harness, consumer kube.PodRef, desc string,
	check func(out string) []string,
) {
	GinkgoHelper()
	Eventually(func() error {
		res, err := h.Kube.ExecQuiet(ctx, consumer, "nvidia-smi", "-q", "-x")
		if err != nil {
			return fmt.Errorf("nvidia-smi -q -x: %w: %s", err, res.Combined())
		}
		if problems := check(res.Stdout); len(problems) > 0 {
			return errors.New(strings.Join(problems, "\n"))
		}
		return nil
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Succeed(), desc)
}
