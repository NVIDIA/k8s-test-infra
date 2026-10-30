//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// These scenarios drive clock tuning the way an operator does, with
// nvidia-smi rather than nvml-mock-ctl, and read the effect back from
// `nvidia-smi -q -x` in the same pod. Each command is its own nvidia-smi
// process, so reading a set back proves it reached the state every consumer on
// the node shares. A set must also stay on the GPU it targeted and be undone by
// the matching reset.

// smiClockTuning runs one clock-tuning command against a single GPU and
// returns its combined output. nvidia-smi exits 0 when it refuses a clock
// combination ("Treating as warning and moving on"), so the exit code alone
// cannot tell a refusal from a write; callers match the output.
func smiClockTuning(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int, args ...string) string {
	GinkgoHelper()
	full := append([]string{"nvidia-smi", "-i", strconv.Itoa(idx)}, args...)
	res, err := h.Kube.Exec(ctx, pod, full...)
	Expect(err).NotTo(HaveOccurred(), "%v: %s", full, res.Combined())
	return res.Combined()
}

// resetOverridesAround clears the runtime overrides on the consumer's node
// before the spec and again after it, pass or fail. Overrides are staged per
// node, so an unpinned reset can clear another node's file and leave the tuned
// clocks live for every later spec on this one.
func resetOverridesAround(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	node := podNode(ctx, h, consumer)
	resetRuntimeOverridesOnNode(ctx, h, node)
	DeferCleanup(func(ctx SpecContext) { resetRuntimeOverridesOnNode(ctx, h, node) })
}

// smiCurrentClocks is the clocks in effect as (graphics, SM, memory).
func smiCurrentClocks(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) [3]int {
	GinkgoHelper()
	gpu := smiGPU(ctx, h, pod, idx)
	gfx, gok := gpu.GraphicsClockMHz()
	sm, sok := gpu.SMClockMHz()
	mem, mok := gpu.MemClockMHz()
	Expect(gok && sok && mok).To(BeTrue(), "nvidia-smi -q -x should report numeric current clocks for GPU %d", idx)
	return [3]int{gfx, sm, mem}
}

// assertApplicationsClocksViaNvidiaSmi checks how the bundled nvidia-smi
// treats applications clocks. nvidia-smi 615 deprecates them outright: `-ac`
// and `-rac` print "The requested functionality has been deprecated." without
// calling NVML, and every applications-clocks row of `-q -x` reads the same.
// The NVML setters behind them still answer; the engine and the bridge
// integration tests cover those.
func assertApplicationsClocksViaNvidiaSmi(ctx SpecContext, h *harness.Harness, consumer kube.PodRef, p profile.Profile) {
	GinkgoHelper()
	mem, gfx := p.DefaultApplicationsClocksMHz()
	for _, args := range [][]string{{"-ac", fmt.Sprintf("%d,%d", mem, gfx)}, {"-rac"}} {
		By("nvidia-smi " + strings.Join(args, " ") + " reports the functionality as deprecated")
		Expect(smiClockTuning(ctx, h, consumer, 0, args...)).
			To(ContainSubstring("The requested functionality has been deprecated."))
	}
	gpu := smiGPU(ctx, h, consumer, 0)
	_, _, ok := gpu.ApplicationsClocksMHz()
	Expect(ok).To(BeFalse(), "nvidia-smi 615 renders no applications clocks")
}

// assertLockedClocksViaNvidiaSmi locks the GPU clock with `-lgc` and the memory
// clock with `-lmc`, reads both back as the clocks in effect, and unlocks them
// with `-rgc` and `-rmc`. The memory half needs a second supported memory
// clock to lock to, which a100 does not have.
func assertLockedClocksViaNvidiaSmi(ctx SpecContext, h *harness.Harness, consumer kube.PodRef, p profile.Profile) {
	GinkgoHelper()
	table := p.SupportedClocks()
	if len(table) == 0 {
		Skip("profile " + p.Name + " declares no supported_clocks to lock to")
	}
	resetOverridesAround(ctx, h, consumer)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible
	baseline := smiCurrentClocks(ctx, h, consumer, target)

	lockGfx := lockTarget(table[0].GraphicsMHz, baseline[0])
	By(fmt.Sprintf("nvidia-smi -i %d -lgc %d,%d", target, lockGfx, lockGfx))
	Expect(smiClockTuning(ctx, h, consumer, target, "-lgc", fmt.Sprintf("%d,%d", lockGfx, lockGfx))).
		To(ContainSubstring(fmt.Sprintf(`GPU clocks set to "(gpuClkMin %d, gpuClkMax %d)"`, lockGfx, lockGfx)))
	Eventually(func() [3]int {
		return smiCurrentClocks(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal([3]int{lockGfx, lockGfx, baseline[2]}),
			"GPU %d graphics and SM clocks should sit at the lock, memory untouched", target)

	if count > 1 {
		By("verify the lock is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiCurrentClocks(ctx, h, consumer, 0)[0]).NotTo(Equal(lockGfx), "GPU 0 must keep its graphics clock")
	}

	if len(table) > 1 {
		memClocks := make([]int, 0, len(table))
		for _, mc := range table {
			memClocks = append(memClocks, mc.MemoryMHz)
		}
		lockMem := lockTarget(memClocks, baseline[2])
		By(fmt.Sprintf("nvidia-smi -i %d -lmc %d", target, lockMem))
		Expect(smiClockTuning(ctx, h, consumer, target, "-lmc", strconv.Itoa(lockMem))).
			To(ContainSubstring(fmt.Sprintf(`Memory clocks set to "(memClkMin %d, memClkMax %d)"`, lockMem, lockMem)))
		Eventually(func() int {
			return smiCurrentClocks(ctx, h, consumer, target)[2]
		}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
			Should(Equal(lockMem), "GPU %d memory clock should sit at the lock", target)

		By("nvidia-smi -rmc unlocks the memory clock")
		smiClockTuning(ctx, h, consumer, target, "-rmc")
		Eventually(func() int {
			return smiCurrentClocks(ctx, h, consumer, target)[2]
		}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
			Should(Equal(baseline[2]), "GPU %d memory clock should return to baseline after -rmc", target)
	}

	By("nvidia-smi -rgc unlocks the GPU clock")
	smiClockTuning(ctx, h, consumer, target, "-rgc")
	Eventually(func() [3]int {
		return smiCurrentClocks(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU %d clocks should return to baseline after -rgc", target)
}

// lockTarget picks the clock halfway down a supported list, moving on if that
// is the reading already in effect, so the lock is observable.
func lockTarget(clocks []int, current int) int {
	for i := len(clocks) / 2; i < len(clocks); i++ {
		if clocks[i] != current {
			return clocks[i]
		}
	}
	return clocks[0]
}
