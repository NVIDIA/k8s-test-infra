//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// These scenarios exercise every command documented in docs/nvml-mock-ctl.md
// end to end: each mutates the runtime config override via nvml-mock-ctl on the running
// DaemonSet pod (no Helm upgrade, no pod delete) and then validates the effect
// through nvidia-smi in that same pod. The consumer is never restarted between
// mutate and assert — that is the whole point of the runtime override path.
//
// Every reading comes from `nvidia-smi -q -x`, the machine-readable form of
// -q: one exec per observation carries all of them, and the element bodies are
// tri-state, so an unsupported N/A reading is never confused with a failed
// device.
//
// Fields are only asserted through nvidia-smi when they are actually
// hot-reloadable AND observable under the e2e chart's dynamic-metrics config
// (see demoRelease): failure injection, ECC counters/mode, the enforced power
// limit and temperature all flow through. GPU temperature is driven by the
// dynamic-metrics simulator under this chart, so the temperature scenario pins
// it deterministically by overriding dynamic_metrics.temperature (base_c with
// ramp_c=0/variance_c=0) and reads it back from <gpu_temp>. Lost /
// fallen_off_bus GPUs are detected by the NVML error body nvidia-smi renders in
// place of their readings — the document keeps describing lost GPUs, so their
// presence is not a failure signal.

const (
	runtimeTTLTimeout = 30 * time.Second
	runtimeTTLPoll    = 2 * time.Second
)

// nvmlMockCtl execs `nvml-mock-ctl <args...>` inside the nvml-mock DaemonSet pod
// and returns its stdout, asserting the command succeeded.
func nvmlMockCtl(ctx SpecContext, h *harness.Harness, args ...string) string {
	GinkgoHelper()
	pod := firstNvmlPod(ctx, h)
	full := append([]string{"nvml-mock-ctl"}, args...)
	res, err := h.Kube.Exec(ctx, pod, full...)
	Expect(err).NotTo(HaveOccurred(), "nvml-mock-ctl %v: %s", args, res.Combined())
	return res.Stdout
}

// nvmlMockCtlTry is the non-asserting variant, used where a command may
// legitimately fail (e.g. UUID targeting against auto-generated UUIDs that the
// v1 CLI cannot resolve). It returns combined output and the exec error.
func nvmlMockCtlTry(ctx SpecContext, h *harness.Harness, args ...string) (string, error) {
	GinkgoHelper()
	pod := firstNvmlPod(ctx, h)
	full := append([]string{"nvml-mock-ctl"}, args...)
	res, err := h.Kube.Exec(ctx, pod, full...)
	return res.Combined(), err
}

// resetRuntimeOverrides clears every override for the node's pod. Used to
// isolate the runtime-control scenarios from one another.
func resetRuntimeOverrides(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}

// smiGPU returns one GPU's readings from a fresh `nvidia-smi -q -x` document.
// A single exec carries every field these scenarios read, and the readings are
// tri-state, so a lost GPU reads as lost rather than as a plausible zero.
//
// It asserts the exec and the decode, which is safe inside an Eventually: both
// only fail when nvidia-smi itself is broken, and that is not something the
// scenarios wait for.
func smiGPU(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) nvidiasmi.GPU {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, pod)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x")
	gpu, err := snap.GPU(idx)
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -q -x should describe GPU %d", idx)
	return gpu
}

// smiGPUTempC is temperature.gpu.
func smiGPUTempC(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	c, ok := smiGPU(ctx, h, pod, idx).TemperatureC()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report a numeric temperature for GPU %d", idx)
	return c
}

// smiGPUUtilPercent is utilization.gpu.
func smiGPUUtilPercent(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	pct, ok := smiGPU(ctx, h, pod, idx).UtilizationGPUPercent()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report a numeric GPU utilization for GPU %d", idx)
	return pct
}

// smiGPUSMClockMHz is clocks.sm.
func smiGPUSMClockMHz(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	mhz, ok := smiGPU(ctx, h, pod, idx).SMClockMHz()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report a numeric SM clock for GPU %d", idx)
	return mhz
}

// smiGPUMemoryUsedMiB is memory.used, the used framebuffer.
func smiGPUMemoryUsedMiB(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	mib, ok := smiGPU(ctx, h, pod, idx).MemoryUsedMiB()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report numeric used memory for GPU %d", idx)
	return mib
}

// smiGPUECCTotal is ecc.errors.uncorrected.aggregate.total.
func smiGPUECCTotal(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	total, ok := smiGPU(ctx, h, pod, idx).UncorrectedECCAggregate()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report countable ECC totals for GPU %d", idx)
	return total
}

// smiGPUPowerLimitW is power.limit truncated to whole watts.
// power.enforced_limit_mw is configured in milliwatts and nvidia-smi renders
// watts with decimals ("500.00 W").
func smiGPUPowerLimitW(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	w, ok := smiGPU(ctx, h, pod, idx).PowerLimitW()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report a numeric power limit for GPU %d", idx)
	return int(w)
}

// smiGPUPowerDrawW is power.draw truncated to whole watts.
func smiGPUPowerDrawW(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	w, ok := smiGPU(ctx, h, pod, idx).PowerDrawW()
	Expect(ok).To(BeTrue(), "nvidia-smi -q -x should report a numeric power draw for GPU %d", idx)
	return int(w)
}

// absInt returns the absolute value of an int.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// gpuFailed reports whether one GPU renders an NVML error body in place of its
// readings. N/A is not a failure, so a passively-cooled GPU reporting fan_speed
// N/A stays healthy.
func gpuFailed(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) bool {
	GinkgoHelper()
	return smiGPU(ctx, h, pod, idx).Failed()
}

// gpuFailedList names the GPUs reporting an NVML error body, as
// "label: reason". The list rather than a boolean so a scoped injection can be
// told from an all-GPU one, and so a failure message says which device.
func gpuFailedList(ctx SpecContext, h *harness.Harness, pod kube.PodRef) []string {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, pod)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x")
	return snap.FailedGPUs()
}

// gpuCount reports how many GPUs the running pod describes in `-q -x`.
func gpuCount(ctx SpecContext, h *harness.Harness, pod kube.PodRef) int {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, pod)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x")
	return snap.Count()
}

// assertRuntimeECCInjection covers docs example #1: force uncorrectable ECC on a
// single GPU and deliver Xid 79, verify only the target GPU trips, then reset
// and verify recovery — all without restarting the consumer.
func assertRuntimeECCInjection(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	By("inject ecc_uncorrectable on GPU 0 at runtime via nvml-mock-ctl")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable", "--after-calls", "1", "--xid", "79")

	Eventually(func() int {
		return smiGPUECCTotal(ctx, h, consumer, 0)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeNumerically(">", 0), "running consumer should observe injected ECC errors on GPU 0 within the TTL")

	if gpuCount(ctx, h, consumer) > 1 {
		By("verify the failure is scoped to GPU 0 (GPU 1 stays healthy)")
		Consistently(func() int {
			return smiGPUECCTotal(ctx, h, consumer, 1)
		}).WithContext(ctx).WithTimeout(6*time.Second).WithPolling(runtimeTTLPoll).
			Should(Equal(0), "GPU 1 must not report ECC errors when only GPU 0 was targeted")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUECCTotal(ctx, h, consumer, 0)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(0), "GPU 0 should return to healthy after reset")
}

// assertRuntimeFailAllLost covers docs example #2: mark ALL GPUs lost, verify
// nvidia-smi surfaces failure markers, then reset and verify every GPU becomes
// addressable again.
func assertRuntimeFailAllLost(ctx SpecContext, h *harness.Harness, consumer kube.PodRef, expectedGPUs int) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	By("mark all GPUs lost at runtime via nvml-mock-ctl")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "all", "--mode", "lost")

	// A lost GPU returns GPU_IS_LOST from every guarded getter, which nvidia-smi
	// renders as the error body in place of each reading. Every GPU was
	// targeted, so every GPU must show it.
	Eventually(func() int {
		return len(gpuFailedList(ctx, h, consumer))
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(expectedGPUs), "every GPU should report lost after fail --gpu all --mode lost")

	By("reset runtime overrides and confirm every GPU recovers")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() []string {
		return gpuFailedList(ctx, h, consumer)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "lost-GPU readings should clear within the TTL after reset")
	Expect(gpuCount(ctx, h, consumer)).To(Equal(expectedGPUs),
		"all GPUs should still be described after reset")
}

// assertRuntimeSetField covers docs example #3: set an arbitrary scalar field
// (the enforced power limit) on one GPU and read it back through nvidia-smi,
// confirming the target changed, its neighbour did not, and reset restores the
// baseline. power.enforced_limit_mw is a static, hot-reloadable scalar that
// nvidia-smi reports reliably as power.limit.
func assertRuntimeSetField(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible
	// enforced_limit_mw is milliwatts; nvidia-smi reports power.limit in watts.
	const overrideMW = 500000
	const overrideW = 500

	baseline := smiGPUPowerLimitW(ctx, h, consumer, target)
	Expect(baseline).NotTo(Equal(overrideW), "baseline power limit must differ from the override for a meaningful assertion")

	By(fmt.Sprintf("set power.enforced_limit_mw=%d on GPU %d via nvml-mock-ctl", overrideMW, target))
	nvmlMockCtl(ctx, h, "set", "--gpu", strconv.Itoa(target),
		"power.enforced_limit_mw="+strconv.Itoa(overrideMW))

	Eventually(func() int {
		return smiGPUPowerLimitW(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideW), "GPU %d power limit should reflect the runtime override", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUPowerLimitW(ctx, h, consumer, 0)).
			NotTo(Equal(overrideW), "GPU 0 must keep its baseline power limit")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUPowerLimitW(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU %d power limit should return to baseline after reset", target)
}

// assertRuntimeSetTemperature covers runtime temperature control: pin a GPU's
// temperature to a fixed value and read it back through nvidia-smi. The e2e
// chart runs the dynamic-metrics simulator, which drives temperature.gpu and
// masks the static thermal block, so we override dynamic_metrics.temperature
// with ramp_c=0/variance_c=0 to get a deterministic reading. The engine
// rebuilds the simulator on config override refresh, so the running consumer observes
// the change without a restart; reset returns temperature to the (varying)
// simulator baseline.
func assertRuntimeSetTemperature(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible
	// Distinct from the ~55-70 dynamic baseline and below every profile's
	// shutdown threshold (min 92), so nvidia-smi never clamps the reading.
	const overrideC = 85

	baseline := smiGPUTempC(ctx, h, consumer, target)
	Expect(baseline).NotTo(Equal(overrideC), "baseline temperature must differ from the override for a meaningful assertion")

	By(fmt.Sprintf("pin temperature to %dC on GPU %d via nvml-mock-ctl set", overrideC, target))
	nvmlMockCtl(ctx, h, "set", "--gpu", strconv.Itoa(target),
		"dynamic_metrics.temperature.base_c="+strconv.Itoa(overrideC),
		"dynamic_metrics.temperature.ramp_c=0",
		"dynamic_metrics.temperature.variance_c=0")

	Eventually(func() int {
		return smiGPUTempC(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideC), "GPU %d temperature should reflect the runtime override", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUTempC(ctx, h, consumer, 0)).
			NotTo(Equal(overrideC), "GPU 0 must keep its baseline (simulator-driven) temperature")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUTempC(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(And(BeNumerically(">", 0), BeNumerically("<", overrideC)),
			"GPU %d temperature should return to the simulator baseline after reset", target)
}

// assertRuntimeTempCommand covers the `temp` convenience command: pin a GPU's
// temperature to a fixed value with a single positional argument and read it
// back through nvidia-smi. Unlike assertRuntimeSetTemperature (which exercises
// the raw `set` path against dynamic_metrics.temperature), this validates that
// the convenience wrapper writes both the static and zero-variation dynamic
// blocks so the reading is deterministic under the e2e chart's dynamic-metrics
// simulator, without the caller spelling out the dynamic keys.
func assertRuntimeTempCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible
	// Distinct from the dynamic baseline and below every profile's shutdown
	// threshold (min 92), so nvidia-smi never clamps the reading.
	const overrideC = 84

	baseline := smiGPUTempC(ctx, h, consumer, target)
	Expect(baseline).NotTo(Equal(overrideC), "baseline temperature must differ from the override for a meaningful assertion")

	By(fmt.Sprintf("pin temperature to %dC on GPU %d via nvml-mock-ctl temp", overrideC, target))
	nvmlMockCtl(ctx, h, "temp", "--gpu", strconv.Itoa(target), strconv.Itoa(overrideC))

	Eventually(func() int {
		return smiGPUTempC(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideC), "GPU %d temperature should reflect the temp command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUTempC(ctx, h, consumer, 0)).
			NotTo(Equal(overrideC), "GPU 0 must keep its baseline (simulator-driven) temperature")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUTempC(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(And(BeNumerically(">", 0), BeNumerically("<", overrideC)),
			"GPU %d temperature should return to the simulator baseline after reset", target)
}

// assertRuntimePowerCommand covers the `power` convenience command: pin a GPU's
// power draw (in watts, the unit nvidia-smi displays) and read it back through
// power.draw. The command writes both the static and zero-variation dynamic
// power blocks, so the reading is deterministic. The target watts is chosen
// inside the profile's [min_limit, max_limit] envelope (queried at runtime so
// the test is profile-agnostic) and far from the dynamic baseline.
func assertRuntimePowerCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	envelope := smiGPU(ctx, h, consumer, target)
	minF, minOK := envelope.PowerMinLimitW()
	maxF, maxOK := envelope.PowerMaxLimitW()
	Expect(minOK && maxOK).To(BeTrue(), "profile must report a numeric power envelope")
	minW, maxW := int(minF), int(maxF)
	Expect(maxW).To(BeNumerically(">", minW), "profile must advertise a usable power envelope")
	baseline := smiGPUPowerDrawW(ctx, h, consumer, target)

	// Pick whichever of the 25%/75% marks sits farther from the (varying)
	// baseline, so the override is unambiguously observable and stays inside
	// [min_limit, max_limit] where the engine won't clamp it.
	lo := minW + (maxW-minW)/4
	hi := minW + (maxW-minW)*3/4
	overrideW := lo
	if absInt(hi-baseline) > absInt(lo-baseline) {
		overrideW = hi
	}

	By(fmt.Sprintf("pin power draw to %dW on GPU %d via nvml-mock-ctl power", overrideW, target))
	nvmlMockCtl(ctx, h, "power", "--gpu", strconv.Itoa(target), strconv.Itoa(overrideW))

	Eventually(func() int {
		return smiGPUPowerDrawW(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideW), "GPU %d power draw should reflect the power command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUPowerDrawW(ctx, h, consumer, 0)).
			NotTo(Equal(overrideW), "GPU 0 must keep its baseline (simulator-driven) power draw")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUPowerDrawW(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(And(BeNumerically(">=", minW), BeNumerically("<=", maxW), Not(Equal(overrideW))),
			"GPU %d power draw should return to the simulator baseline after reset", target)
}

// assertRuntimeFanCommand covers the `fan` convenience command: pin a GPU's fan
// speed and read it back through <fan_speed>. Liquid/passively-cooled profiles
// ship fan.count: 0, so the baseline reads N/A; the command forces the count to
// at least 1 so the pinned speed becomes observable, and reset returns it to the
// profile baseline. The element body is compared rather than a number, because
// N/A is a legitimate baseline that has to round-trip as itself.
func assertRuntimeFanCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	gpu := smiGPU(ctx, h, consumer, target)
	baseline := gpu.FanSpeed()
	overridePct := 57 // uncommon value unlikely to match a profile default
	if pct, ok := gpu.FanSpeedPercent(); ok && pct == overridePct {
		overridePct = 43
	}
	overrideBody := fmt.Sprintf("%d %%", overridePct)

	By(fmt.Sprintf("pin fan speed to %d%% on GPU %d via nvml-mock-ctl fan", overridePct, target))
	nvmlMockCtl(ctx, h, "fan", "--gpu", strconv.Itoa(target), strconv.Itoa(overridePct))

	Eventually(func() string {
		return smiGPU(ctx, h, consumer, target).FanSpeed()
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideBody), "GPU %d fan speed should reflect the fan command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPU(ctx, h, consumer, 0).FanSpeed()).
			NotTo(Equal(overrideBody), "GPU 0 must keep its baseline fan reading")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() string {
		return smiGPU(ctx, h, consumer, target).FanSpeed()
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU %d fan speed should return to the profile baseline after reset", target)
}

// assertRuntimeUtilCommand covers the `util` convenience command: pin a GPU's
// GPU and memory utilization to a fixed percent and read both back through
// nvidia-smi. Utilization is simulator-driven under the e2e chart, so the
// command disables the dynamic utilization sub-simulator (rather than zeroing
// its variation) to make the reading deterministic for any percent. reset lets
// the simulator resume.
func assertRuntimeUtilCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	// Pick an override far from the (oscillating) dynamic baseline so it is
	// unambiguously observable and the simulator never emits it on its own.
	baseline := smiGPUUtilPercent(ctx, h, consumer, target)
	overridePct := 90
	if baseline >= 50 {
		overridePct = 10
	}

	By(fmt.Sprintf("pin utilization to %d%% on GPU %d via nvml-mock-ctl util", overridePct, target))
	nvmlMockCtl(ctx, h, "util", "--gpu", strconv.Itoa(target), strconv.Itoa(overridePct))

	Eventually(func() int {
		return smiGPUUtilPercent(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overridePct), "GPU %d GPU utilization should reflect the util command", target)
	memUtil, ok := smiGPU(ctx, h, consumer, target).UtilizationMemoryPercent()
	Expect(ok).To(BeTrue(), "GPU %d should report a numeric memory utilization", target)
	Expect(memUtil).To(Equal(overridePct), "GPU %d memory utilization should also be pinned", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUUtilPercent(ctx, h, consumer, 0)).
			NotTo(Equal(overridePct), "GPU 0 must keep its baseline (simulator-driven) utilization")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUUtilPercent(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Not(Equal(overridePct)), "GPU %d utilization should resume varying after reset", target)
}

// assertJpgOfaUtilizationOverride pins distinct non-zero JPEG and OFA
// utilization through `set` and reads both back from the jpeg_util and ofa_util
// elements of nvidia-smi -q -x. The shipped profiles configure 0 % for both, so
// the deployed config on its own cannot tell a working getter apart from the
// dropped-field bug (#637) — and the two values must differ, or a getter reading
// the other field would pass.
func assertJpgOfaUtilizationOverride(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	const wantJPEG, wantOFA = 35, 12

	By(fmt.Sprintf("set utilization.jpeg=%d utilization.ofa=%d on every GPU via nvml-mock-ctl", wantJPEG, wantOFA))
	nvmlMockCtl(ctx, h, "set", "--gpu", "all",
		"utilization.jpeg="+strconv.Itoa(wantJPEG), "utilization.ofa="+strconv.Itoa(wantOFA))

	Eventually(func() []string {
		res, err := h.Kube.Exec(ctx, consumer, "nvidia-smi", "-q", "-x")
		if err != nil {
			return []string{"nvidia-smi -q -x failed: " + res.Combined()}
		}
		return nvidiasmi.JpgOfaUtilizationProblems(res.Stdout, wantJPEG, wantOFA)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "JPEG/OFA utilization should reflect the runtime override")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}

// assertRuntimeClocksCommand covers the `clocks` convenience command: pin a
// GPU's SM/graphics clocks and read clocks.sm back. Clocks are static (no
// dynamic simulator), so the reading is exact both after the pin and after
// reset returns it to the profile baseline.
func assertRuntimeClocksCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	baseline := smiGPUSMClockMHz(ctx, h, consumer, target)
	overrideMHz := 1410
	if baseline == overrideMHz {
		overrideMHz = 1215
	}

	By(fmt.Sprintf("pin SM/graphics clocks to %d MHz on GPU %d via nvml-mock-ctl clocks", overrideMHz, target))
	nvmlMockCtl(ctx, h, "clocks", "--gpu", strconv.Itoa(target), strconv.Itoa(overrideMHz))

	Eventually(func() int {
		return smiGPUSMClockMHz(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideMHz), "GPU %d SM clock should reflect the clocks command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUSMClockMHz(ctx, h, consumer, 0)).
			NotTo(Equal(overrideMHz), "GPU 0 must keep its baseline SM clock")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUSMClockMHz(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU %d SM clock should return to the profile baseline after reset", target)
}

// assertRuntimeThrottleCommand covers the `throttle` convenience command: set
// the hw_thermal_slowdown reason on a GPU and read it back via
// <clocks_event_reason_hw_thermal_slowdown> ("Active"/"Not Active"), then let
// reset restore the profile baseline. Profiles ship this reason off, so the
// transition is observable.
func assertRuntimeThrottleCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	baseline := smiGPU(ctx, h, consumer, target).ThermalSlowdownState()
	Expect(baseline).To(Equal("Not Active"), "profile must ship hw_thermal_slowdown off for a meaningful assertion")

	By(fmt.Sprintf("set the thermal throttle reason on GPU %d via nvml-mock-ctl throttle", target))
	nvmlMockCtl(ctx, h, "throttle", "--gpu", strconv.Itoa(target), "thermal")

	Eventually(func() string {
		return smiGPU(ctx, h, consumer, target).ThermalSlowdownState()
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal("Active"), "GPU %d hw_thermal_slowdown should be active after the throttle command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPU(ctx, h, consumer, 0).ThermalSlowdownState()).
			To(Equal("Not Active"), "GPU 0 must keep its baseline throttle state")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() string {
		return smiGPU(ctx, h, consumer, target).ThermalSlowdownState()
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU %d throttle reason should return to the profile baseline after reset", target)
}

// assertRuntimePStateCommand covers the `pstate` convenience command: pin a
// GPU's performance state and read pstate back ("P0".."P15"). The value is
// static, so it is exact after the pin and after reset restores the baseline.
func assertRuntimePStateCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	baseline := smiGPU(ctx, h, consumer, target).PerformanceState()
	overrideN := 8
	if baseline == "P8" {
		overrideN = 5
	}
	overrideStr := fmt.Sprintf("P%d", overrideN)

	By(fmt.Sprintf("pin performance state to %s on GPU %d via nvml-mock-ctl pstate", overrideStr, target))
	nvmlMockCtl(ctx, h, "pstate", "--gpu", strconv.Itoa(target), strconv.Itoa(overrideN))

	Eventually(func() string {
		return smiGPU(ctx, h, consumer, target).PerformanceState()
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideStr), "GPU %d pstate should reflect the pstate command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPU(ctx, h, consumer, 0).PerformanceState()).
			NotTo(Equal(overrideStr), "GPU 0 must keep its baseline pstate")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() string {
		return smiGPU(ctx, h, consumer, target).PerformanceState()
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU %d pstate should return to the profile baseline after reset", target)
}

// assertRuntimeUUIDTargeting covers docs example #5: target a GPU by its UUID.
// The v1 CLI only resolves UUIDs declared explicitly in the profile; when the
// profile uses auto-generated UUIDs the command cannot resolve them and the
// scenario is skipped (documented limitation).
func assertRuntimeUUIDTargeting(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	uuid := smiGPU(ctx, h, consumer, 0).UUID()
	Expect(uuid).NotTo(BeEmpty(), "nvidia-smi should report a UUID for GPU 0")

	By("target GPU 0 by UUID with fallen_off_bus via nvml-mock-ctl")
	out, err := nvmlMockCtlTry(ctx, h, "fail", "--gpu", uuid, "--mode", "fallen_off_bus")
	if err != nil {
		// Only the documented v1 limitation (the profile's UUID isn't in the
		// config the CLI can read, so ResolveTarget can't map it) is a valid
		// skip. Any other exec failure is a real regression and must fail.
		if strings.Contains(out, "cannot resolve") {
			Skip("profile uses UUIDs nvml-mock-ctl cannot resolve (v1 limitation): " + strings.TrimSpace(out))
		}
		Expect(err).NotTo(HaveOccurred(), "nvml-mock-ctl fail --gpu <uuid> failed unexpectedly: %s", strings.TrimSpace(out))
	}

	Eventually(func() bool {
		return gpuFailed(ctx, h, consumer, 0)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeTrue(), "GPU targeted by UUID should render NVML error bodies")

	if gpuCount(ctx, h, consumer) > 1 {
		By("verify a non-targeted GPU stays healthy")
		Expect(gpuFailed(ctx, h, consumer, 1)).
			To(BeFalse(), "GPU 1 must stay healthy when only GPU 0's UUID was targeted")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() bool {
		return gpuFailed(ctx, h, consumer, 0)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeFalse(), "GPU 0 should recover after reset")
}

// assertRuntimeStatus covers docs example #6: inspect active overrides via
// `status` and `status --gpu <idx>`, and cross-checks the effect via nvidia-smi.
func assertRuntimeStatus(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	By("no overrides are reported on a clean node")
	Expect(nvmlMockCtl(ctx, h, "status")).To(ContainSubstring("no active overrides"))

	By("inject ecc_uncorrectable on GPU 0 and confirm it via nvidia-smi")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable", "--after-calls", "1")
	Eventually(func() int {
		return smiGPUECCTotal(ctx, h, consumer, 0)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeNumerically(">", 0))

	By("status reports the GPU 0 override")
	Expect(nvmlMockCtl(ctx, h, "status")).To(ContainSubstring("ecc_uncorrectable"))
	Expect(nvmlMockCtl(ctx, h, "status", "--gpu", "0")).To(ContainSubstring("ecc_uncorrectable"))

	if gpuCount(ctx, h, consumer) > 1 {
		By("status --gpu 1 reports no override for the untouched GPU")
		Expect(nvmlMockCtl(ctx, h, "status", "--gpu", "1")).
			To(ContainSubstring("no active overrides for gpu 1"))
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
	Expect(nvmlMockCtl(ctx, h, "status")).To(ContainSubstring("no active overrides"))
}

// assertRuntimeHealthyRecovery covers docs example #7: recover a single GPU with
// `fail --mode healthy` (without touching other overrides), verified through
// nvidia-smi, then a final reset.
func assertRuntimeHealthyRecovery(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	By("inject ecc_uncorrectable on GPU 0")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable", "--after-calls", "1")
	Eventually(func() int {
		return smiGPUECCTotal(ctx, h, consumer, 0)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeNumerically(">", 0), "GPU 0 should trip before recovery")

	By("recover GPU 0 with fail --mode healthy (no reset)")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "0", "--mode", "healthy")
	Eventually(func() int {
		return smiGPUECCTotal(ctx, h, consumer, 0)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(0), "GPU 0 should recover after fail --mode healthy")

	By("final reset")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}

// smiProcesses returns the processes `nvidia-smi -q -x` reports for one GPU.
func smiProcesses(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) []nvidiasmi.Process {
	GinkgoHelper()
	procs, err := smiGPU(ctx, h, pod, idx).Processes()
	Expect(err).NotTo(HaveOccurred(), "decode GPU %d processes", idx)
	return procs
}

// setRuntimeProcesses writes a `processes:` list onto one GPU via
// `nvml-mock-ctl set`. There is no dedicated process subcommand: `set` accepts
// any DeviceConfig path, and the value is parsed as YAML, so the whole list is
// replaced in one call (an empty list clears it).
func setRuntimeProcesses(ctx SpecContext, h *harness.Harness, idx int, procs []nvidiasmi.Process) {
	GinkgoHelper()
	entries := make([]string, 0, len(procs))
	for _, p := range procs {
		entries = append(entries, fmt.Sprintf("{pid: %d, type: C, name: %s, used_memory_mib: %d}",
			p.PID, p.Name, p.MemoryMiB))
	}
	nvmlMockCtl(ctx, h, "set", "--gpu", strconv.Itoa(idx),
		"processes=["+strings.Join(entries, ", ")+"]")
}

// assertRuntimeProcesses covers driving a GPU's running-process list at runtime
// with `nvml-mock-ctl set --gpu <idx> 'processes=[...]'` and reading it back
// from the <processes> block.
//
// This is a regression guard for two bugs that only appear with real
// nvidia-smi. nvidia-smi enumerates processes through the internal export
// table, whose entry is a 4128-byte struct carrying an inline name buffer, not
// the public 24-byte nvmlProcessInfo_t:
//
//   - a wrong stride silently renders every process after the FIRST as
//     PID 0 / [N/A] / 0 MiB, so this deliberately configures more than one
//     process and compares the whole list;
//   - an empty inline name is dropped rather than rendered, so <process_name>
//     is compared too — the name reaches nvidia-smi only through that buffer,
//     it does not call nvmlSystemGetProcessName on this path.
func assertRuntimeProcesses(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	// Modest memory values so the numbers stay plausible on every profile.
	want := []nvidiasmi.Process{
		{PID: 4201, Name: "train.py", MemoryMiB: 1024},
		{PID: 4202, Name: "infer.py", MemoryMiB: 512},
		{PID: 4203, Name: "jupyter", MemoryMiB: 64},
	}

	By("baseline: the GPU reports no running processes")
	Expect(smiProcesses(ctx, h, consumer, target)).To(BeEmpty(),
		"GPU %d must start with no processes for a meaningful assertion", target)

	By(fmt.Sprintf("configure %d processes on GPU %d via nvml-mock-ctl set", len(want), target))
	setRuntimeProcesses(ctx, h, target, want)

	// -q -x is unscoped, so nvidia-smi walks every GPU's process list in one
	// run. That is the shape that faulted while a stray write in the internal
	// export-table shim was reachable from calls that never carried a process
	// buffer.
	Eventually(func() []nvidiasmi.Process {
		return smiProcesses(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(want), "GPU %d should report every configured process with its pid, name and memory", target)

	if count > 1 {
		By("verify the process list is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiProcesses(ctx, h, consumer, 0)).To(BeEmpty(),
			"GPU 0 must report no processes when only GPU %d was targeted", target)
	}

	By("clearing the list with an empty processes value removes them")
	setRuntimeProcesses(ctx, h, target, nil)
	Eventually(func() []nvidiasmi.Process {
		return smiProcesses(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "GPU %d should report no processes after processes=[]", target)

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
	Eventually(func() []nvidiasmi.Process {
		return smiProcesses(ctx, h, consumer, target)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "GPU %d should still report no processes after reset", target)
}

// nvlinkErrorSum sums the Replay/Recovery/CRC error counters nvidia-smi reports
// for a single GPU via `nvlink -e`. The second return is false when the bundled
// nvidia-smi does not surface per-link error counters at all, so callers can
// tri-state (SKIP) rather than hard-fail — the same philosophy as
// assertions.nvlinkCountersTriState for throughput counters.
func nvlinkErrorSum(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) (int, bool) {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, pod, "nvidia-smi", "nvlink", "-e", "-i", strconv.Itoa(idx))
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi nvlink -e -i %d: %s", idx, res.Combined())
	sum, surfaced := 0, false
	for _, line := range strings.Split(res.Stdout, "\n") {
		// e.g. "\t Link 0: Replay Errors: 12"
		if !strings.Contains(line, "Errors:") {
			continue
		}
		n, perr := strconv.Atoi(strings.TrimSpace(line[strings.LastIndex(line, ":")+1:]))
		if perr != nil {
			continue
		}
		surfaced = true
		sum += n
	}
	return sum, surfaced
}

// assertRuntimeNVLinkErrorInjection covers the `nvlink-error` command: inject a
// rising NVLink DL error rate on a GPU's switch-attached links and observe the
// per-link error counters climb through nvidia-smi nvlink -e, confirm the
// injection is scoped to the target GPU, then heal with rate 0 (no reset) and a
// final reset, verifying the counters return to the healthy baseline. The
// injected errors accrue monotonically off the fabric epoch, which is exactly
// the rising rate DCGM's delta-based NVLink health watch consumes. Requires an
// NVLink (switch-attached) profile — the caller gates on ExpectedNV()>0.
func assertRuntimeNVLinkErrorInjection(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	baseSum, surfaced := nvlinkErrorSum(ctx, h, consumer, target)
	if !surfaced {
		Skip("bundled nvidia-smi did not surface NVLink error counters via 'nvlink -e'")
	}
	// The recovery half of this scenario asserts the counters return to their
	// pre-injection value; that is only unambiguous when the profile ships a
	// static-zero NVLink error baseline (every shipped profile does — none set
	// nvlink error_rate). Skip rather than fail if a profile pins a rising
	// baseline, so the test stays portable across E2E_PROFILES selections.
	if baseSum != 0 {
		Skip(fmt.Sprintf("profile ships a non-zero NVLink error baseline (%d); nvlink-error recovery needs a static-zero baseline", baseSum))
	}

	const rate = 500 // errors/second — climbs fast enough to observe within the TTL

	By(fmt.Sprintf("inject NVLink DL errors at %d/s on GPU %d via nvml-mock-ctl nvlink-error", rate, target))
	nvmlMockCtl(ctx, h, "nvlink-error", "--gpu", strconv.Itoa(target), strconv.Itoa(rate))

	Eventually(func() int {
		s, _ := nvlinkErrorSum(ctx, h, consumer, target)
		return s
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeNumerically(">", 0), "GPU %d NVLink error counters should climb after injection", target)

	if count > 1 {
		By("verify the injection is scoped to the target GPU (GPU 0 unchanged)")
		s0, _ := nvlinkErrorSum(ctx, h, consumer, 0)
		Expect(s0).To(Equal(0), "GPU 0 must not accrue NVLink errors when only GPU %d was targeted", target)
	}

	By("heal the GPU with nvlink-error rate 0 (no reset)")
	nvmlMockCtl(ctx, h, "nvlink-error", "--gpu", strconv.Itoa(target), "0")

	Eventually(func() int {
		s, _ := nvlinkErrorSum(ctx, h, consumer, target)
		return s
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(0), "GPU %d NVLink error counters should return to the healthy baseline after rate 0", target)

	By("final reset")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}

// assertEncoderFBCAccounting covers issue #636: pin non-zero encoder_stats and
// fbc_stats via nvml-mock-ctl, then assert nvidia-smi -q -x surfaces those exact
// numbers (and a numeric Accounting Mode Buffer Size) instead of N/A stubs.
func assertEncoderFBCAccounting(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	const (
		sessions = 2
		fps      = 30
		latency  = 1500
		buffer   = 4000
	)
	stats := nvidiasmi.EncoderFBCStats{
		SessionCount:     sessions,
		AverageFPS:       fps,
		AverageLatencyUS: latency,
	}

	By("pin non-zero encoder_stats and fbc_stats via nvml-mock-ctl set")
	nvmlMockCtl(ctx, h, "set", "--gpu", "all",
		"encoder_stats.session_count="+strconv.Itoa(sessions),
		"encoder_stats.average_fps="+strconv.Itoa(fps),
		"encoder_stats.average_latency_us="+strconv.Itoa(latency),
		"fbc_stats.session_count="+strconv.Itoa(sessions),
		"fbc_stats.average_fps="+strconv.Itoa(fps),
		"fbc_stats.average_latency_us="+strconv.Itoa(latency),
	)

	// ExecQuiet keeps the ~90 KB document out of the Ginkgo log on every poll.
	Eventually(func() error {
		res, err := h.Kube.ExecQuiet(ctx, consumer, "nvidia-smi", "-q", "-x")
		if err != nil {
			return fmt.Errorf("nvidia-smi -q -x: %w: %s", err, res.Combined())
		}
		if problems := nvidiasmi.EncoderFBCProblems(res.Stdout, stats, stats, buffer); len(problems) > 0 {
			return errors.New(strings.Join(problems, "\n"))
		}
		return nil
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Succeed(), "encoder/FBC/accounting must reflect the runtime override")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}
