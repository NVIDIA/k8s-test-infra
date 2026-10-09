//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"slices"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// These scenarios drive clock tuning the way an operator does, with
// nvidia-smi rather than nvml-mock-ctl, and read the effect back from
// `nvidia-smi -q -x` in the same pod. Every setter used to be a generated stub,
// so each command was refused and nothing a tuning script did was observable.
// A set must persist across nvidia-smi processes, stay on the GPU it targeted,
// and be undone by the matching reset.

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

// smiAppClocks is the applications clocks pair as (memory, graphics).
func smiAppClocks(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) [2]int {
	GinkgoHelper()
	mem, gfx, ok := smiGPU(ctx, h, pod, idx).ApplicationsClocksMHz()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report numeric applications clocks for GPU %d", idx)
	return [2]int{mem, gfx}
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

// assertApplicationsClocksViaNvidiaSmi sets a supported non-default pair with
// `nvidia-smi -ac`, checks that a pair outside the supported table is refused
// without disturbing it, and restores the defaults with `-rac`.
func assertApplicationsClocksViaNvidiaSmi(ctx SpecContext, h *harness.Harness, consumer kube.PodRef, p profile.Profile) {
	GinkgoHelper()
	mem, gfx, ok := p.AlternateApplicationsClocksMHz()
	if !ok {
		Skip("profile " + p.Name + " has no supported clock pair other than the default")
	}
	resetRuntimeOverrides(ctx, h)
	DeferCleanup(resetRuntimeOverrides, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible
	defMem, defGfx := p.DefaultApplicationsClocksMHz()
	defaults := [2]int{defMem, defGfx}
	want := [2]int{mem, gfx}
	Expect(smiAppClocks(ctx, h, consumer, target)).To(Equal(defaults),
		"GPU %d must start at the profile's default applications clocks", target)

	By(fmt.Sprintf("nvidia-smi -i %d -ac %d,%d", target, mem, gfx))
	Expect(smiClockTuning(ctx, h, consumer, target, "-ac", fmt.Sprintf("%d,%d", mem, gfx))).
		To(ContainSubstring(fmt.Sprintf(`Applications clocks set to "(MEM %d, SM %d)"`, mem, gfx)))
	Eventually(func() [2]int {
		return smiAppClocks(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(want), "GPU %d applications clocks should follow -ac", target)

	if count > 1 {
		By("verify the write is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiAppClocks(ctx, h, consumer, 0)).To(Equal(defaults), "GPU 0 must keep its default applications clocks")
	}

	unsupported := unlistedGraphicsClock(p, mem, gfx)
	By(fmt.Sprintf("nvidia-smi -ac %d,%d is refused: the pair is not in the supported table", mem, unsupported))
	Expect(smiClockTuning(ctx, h, consumer, target, "-ac", fmt.Sprintf("%d,%d", mem, unsupported))).
		To(ContainSubstring("is not supported"))
	Expect(smiAppClocks(ctx, h, consumer, target)).To(Equal(want), "a refused pair must not replace the applied one")

	By(fmt.Sprintf("nvidia-smi -i %d -rac restores the defaults", target))
	smiClockTuning(ctx, h, consumer, target, "-rac")
	Eventually(func() [2]int {
		return smiAppClocks(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(defaults), "GPU %d applications clocks should return to the defaults after -rac", target)
}

// unlistedGraphicsClock returns a graphics clock the table does not pair with
// memMHz, starting just above near so the refusal is about the pair alone.
func unlistedGraphicsClock(p profile.Profile, memMHz, near int) int {
	var listed []int
	for _, mc := range p.SupportedClocks() {
		if mc.MemoryMHz == memMHz {
			listed = mc.GraphicsMHz
		}
	}
	mhz := near + 1
	for slices.Contains(listed, mhz) {
		mhz++
	}
	return mhz
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
	resetRuntimeOverrides(ctx, h)
	DeferCleanup(resetRuntimeOverrides, h)

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
