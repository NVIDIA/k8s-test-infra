//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// These scenarios exercise every command documented in docs/nvml-mock-ctl.md
// end to end: each mutates the runtime overlay via nvml-mock-ctl on the running
// DaemonSet pod (no Helm upgrade, no pod delete) and then validates the effect
// through nvidia-smi in that same pod. The consumer is never restarted between
// mutate and assert — that is the whole point of the runtime override path.
//
// Fields are only asserted through nvidia-smi when they are actually
// hot-reloadable AND observable under the e2e chart's dynamic-metrics config
// (see demoRelease): failure injection, ECC counters/mode, the enforced power
// limit and temperature all flow through. GPU temperature is driven by the
// dynamic-metrics simulator under this chart, so the temperature scenario pins
// it deterministically by overriding dynamic_metrics.temperature (base_c with
// ramp_c=0/variance_c=0) and reads it back via temperature.gpu. GPU utilization
// is likewise simulator-driven but is left unasserted (its value oscillates by
// pattern). Lost / fallen_off_bus GPUs are detected via the ECC counter query,
// which returns "[GPU is lost]" for a tripped device — nvidia-smi -L keeps
// listing lost GPUs, so it is not a reliable failure signal.

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

// smiGPUValue returns the trimmed nvidia-smi --query-gpu value for a single GPU,
// asserting the query succeeds (use smiGPUValueRaw for GPUs that may be lost).
func smiGPUValue(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int, field string) string {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, pod, "nvidia-smi",
		"--id="+strconv.Itoa(idx),
		"--query-gpu="+field,
		"--format=csv,noheader,nounits")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -i %d --query-gpu=%s: %s", idx, field, res.Combined())
	return strings.TrimSpace(res.Stdout)
}

// smiGPUInt is smiGPUValue parsed as an integer.
func smiGPUInt(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int, field string) int {
	GinkgoHelper()
	v := smiGPUValue(ctx, h, pod, idx, field)
	n, err := strconv.Atoi(strings.TrimSpace(v))
	Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi %s for gpu %d: %q", field, idx, v)
	return n
}

// smiGPUPowerLimitW returns the integer-watt nvidia-smi power.limit for a single
// GPU. enforced_limit_mw is configured in milliwatts; nvidia-smi reports the
// limit in watts (e.g. "500.00"), which this truncates to whole watts.
func smiGPUPowerLimitW(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int) int {
	GinkgoHelper()
	v := smiGPUValue(ctx, h, pod, idx, "power.limit")
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi power.limit for gpu %d: %q", idx, v)
	return int(f)
}

// smiGPUFloat is smiGPUValue parsed as a float (e.g. power.draw "600.00").
func smiGPUFloat(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int, field string) float64 {
	GinkgoHelper()
	v := smiGPUValue(ctx, h, pod, idx, field)
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi %s for gpu %d: %q", field, idx, v)
	return f
}

// absInt returns the absolute value of an int.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// smiGPUValueRaw queries a single GPU without asserting success — a lost GPU
// makes nvidia-smi exit non-zero, and the caller inspects the combined output
// for failure markers.
func smiGPUValueRaw(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int, field string) string {
	GinkgoHelper()
	res, _ := h.Kube.Exec(ctx, pod, "nvidia-smi",
		"--id="+strconv.Itoa(idx),
		"--query-gpu="+field,
		"--format=csv,noheader,nounits")
	return res.Combined()
}

// gpuCount reports how many GPUs the running pod exposes via nvidia-smi -L.
func gpuCount(ctx SpecContext, h *harness.Harness, pod kube.PodRef) int {
	return nvidiaSMILCount(ctx, h, pod)
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
		return smiGPUInt(ctx, h, consumer, 0, "ecc.errors.uncorrected.aggregate.total")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeNumerically(">", 0), "running consumer should observe injected ECC errors on GPU 0 within the TTL")

	if gpuCount(ctx, h, consumer) > 1 {
		By("verify the failure is scoped to GPU 0 (GPU 1 stays healthy)")
		Consistently(func() int {
			return smiGPUInt(ctx, h, consumer, 1, "ecc.errors.uncorrected.aggregate.total")
		}).WithContext(ctx).WithTimeout(6*time.Second).WithPolling(runtimeTTLPoll).
			Should(Equal(0), "GPU 1 must not report ECC errors when only GPU 0 was targeted")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUInt(ctx, h, consumer, 0, "ecc.errors.uncorrected.aggregate.total")
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

	// A lost GPU returns GPU_IS_LOST from every guarded getter; the ECC
	// counter query surfaces that as a "[GPU is lost]" marker for every GPU.
	Eventually(func() bool {
		return hasFailureMarker(eccQuery(ctx, h, consumer))
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeTrue(), "nvidia-smi should surface lost-GPU markers after fail --gpu all --mode lost")

	By("reset runtime overrides and confirm every GPU recovers")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() bool {
		return hasFailureMarker(eccQuery(ctx, h, consumer))
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeFalse(), "lost-GPU markers should clear within the TTL after reset")
	Expect(nvidiaSMILCount(ctx, h, consumer)).To(Equal(expectedGPUs),
		"all GPUs should still be enumerable after reset")
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
// rebuilds the simulator on overlay refresh, so the running consumer observes
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

	baseline := smiGPUInt(ctx, h, consumer, target, "temperature.gpu")
	Expect(baseline).NotTo(Equal(overrideC), "baseline temperature must differ from the override for a meaningful assertion")

	By(fmt.Sprintf("pin temperature to %dC on GPU %d via nvml-mock-ctl set", overrideC, target))
	nvmlMockCtl(ctx, h, "set", "--gpu", strconv.Itoa(target),
		"dynamic_metrics.temperature.base_c="+strconv.Itoa(overrideC),
		"dynamic_metrics.temperature.ramp_c=0",
		"dynamic_metrics.temperature.variance_c=0")

	Eventually(func() int {
		return smiGPUInt(ctx, h, consumer, target, "temperature.gpu")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideC), "GPU %d temperature should reflect the runtime override", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUInt(ctx, h, consumer, 0, "temperature.gpu")).
			NotTo(Equal(overrideC), "GPU 0 must keep its baseline (simulator-driven) temperature")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUInt(ctx, h, consumer, target, "temperature.gpu")
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

	baseline := smiGPUInt(ctx, h, consumer, target, "temperature.gpu")
	Expect(baseline).NotTo(Equal(overrideC), "baseline temperature must differ from the override for a meaningful assertion")

	By(fmt.Sprintf("pin temperature to %dC on GPU %d via nvml-mock-ctl temp", overrideC, target))
	nvmlMockCtl(ctx, h, "temp", "--gpu", strconv.Itoa(target), strconv.Itoa(overrideC))

	Eventually(func() int {
		return smiGPUInt(ctx, h, consumer, target, "temperature.gpu")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideC), "GPU %d temperature should reflect the temp command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUInt(ctx, h, consumer, 0, "temperature.gpu")).
			NotTo(Equal(overrideC), "GPU 0 must keep its baseline (simulator-driven) temperature")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return smiGPUInt(ctx, h, consumer, target, "temperature.gpu")
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

	minW := int(smiGPUFloat(ctx, h, consumer, target, "power.min_limit"))
	maxW := int(smiGPUFloat(ctx, h, consumer, target, "power.max_limit"))
	Expect(maxW).To(BeNumerically(">", minW), "profile must advertise a usable power envelope")
	baseline := int(smiGPUFloat(ctx, h, consumer, target, "power.draw"))

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
		return int(smiGPUFloat(ctx, h, consumer, target, "power.draw"))
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideW), "GPU %d power draw should reflect the power command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(int(smiGPUFloat(ctx, h, consumer, 0, "power.draw"))).
			NotTo(Equal(overrideW), "GPU 0 must keep its baseline (simulator-driven) power draw")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return int(smiGPUFloat(ctx, h, consumer, target, "power.draw"))
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(And(BeNumerically(">=", minW), BeNumerically("<=", maxW), Not(Equal(overrideW))),
			"GPU %d power draw should return to the simulator baseline after reset", target)
}

// assertRuntimeFanCommand covers the `fan` convenience command: pin a GPU's fan
// speed and read it back through fan.speed. Liquid/passively-cooled profiles
// ship fan.count: 0 (fan.speed reports "[N/A]"); the command forces the count
// to at least 1 so the pinned speed becomes observable, and reset returns it to
// the profile baseline.
func assertRuntimeFanCommand(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible

	baseline := smiGPUValue(ctx, h, consumer, target, "fan.speed")
	overridePct := 57 // uncommon value unlikely to match a profile default
	if baseline == strconv.Itoa(overridePct) {
		overridePct = 43
	}
	overrideStr := strconv.Itoa(overridePct)

	By(fmt.Sprintf("pin fan speed to %d%% on GPU %d via nvml-mock-ctl fan", overridePct, target))
	nvmlMockCtl(ctx, h, "fan", "--gpu", strconv.Itoa(target), overrideStr)

	Eventually(func() string {
		return smiGPUValue(ctx, h, consumer, target, "fan.speed")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(overrideStr), "GPU %d fan speed should reflect the fan command", target)

	if count > 1 {
		By("verify the override is scoped to the target GPU (GPU 0 unchanged)")
		Expect(smiGPUValue(ctx, h, consumer, 0, "fan.speed")).
			NotTo(Equal(overrideStr), "GPU 0 must keep its baseline fan reading")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() string {
		return smiGPUValue(ctx, h, consumer, target, "fan.speed")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU %d fan speed should return to the profile baseline after reset", target)
}

// assertRuntimeApplyPatch covers docs example #4: apply a multi-field YAML
// snippet to one GPU. The e2e chart drives GPU utilization from the
// dynamic-metrics simulator, so we assert the deterministic, hot-reloadable ECC
// mode field through nvidia-smi (utilization is included in the patch to mirror
// the docs but is simulator-driven and not asserted).
func assertRuntimeApplyPatch(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	baseline := smiGPUValue(ctx, h, consumer, 0, "ecc.mode.current")
	// Flip to the opposite of the baseline so the change is observable
	// regardless of the profile's default ECC mode.
	wantCfg, wantSMI := "disabled", "Disabled"
	if strings.EqualFold(baseline, "Disabled") {
		wantCfg, wantSMI = "enabled", "Enabled"
	}

	patch := fmt.Sprintf("ecc:\n  mode_current: %s\nutilization:\n  gpu: 100\n", wantCfg)
	pod := firstNvmlPod(ctx, h)
	By("stage a patch file inside the pod and apply it to GPU 0")
	writePatch, err := h.Kube.ExecSh(ctx, pod, "cat > /tmp/nvml-mock-ctl-patch.yaml <<'EOF'\n"+patch+"EOF")
	Expect(err).NotTo(HaveOccurred(), "write patch file: %s", writePatch.Combined())
	nvmlMockCtl(ctx, h, "apply", "--gpu", "0", "-f", "/tmp/nvml-mock-ctl-patch.yaml")

	Eventually(func() string {
		return smiGPUValue(ctx, h, consumer, 0, "ecc.mode.current")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(wantSMI), "GPU 0 ECC mode should reflect the applied patch")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() string {
		return smiGPUValue(ctx, h, consumer, 0, "ecc.mode.current")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "GPU 0 ECC mode should return to baseline after reset")
}

// assertRuntimeUUIDTargeting covers docs example #5: target a GPU by its UUID.
// The v1 CLI only resolves UUIDs declared explicitly in the profile; when the
// profile uses auto-generated UUIDs the command cannot resolve them and the
// scenario is skipped (documented limitation).
func assertRuntimeUUIDTargeting(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	uuid := smiGPUValue(ctx, h, consumer, 0, "uuid")
	Expect(uuid).NotTo(BeEmpty(), "nvidia-smi should report a UUID for GPU 0")

	By("target GPU 0 by UUID with fallen_off_bus via nvml-mock-ctl")
	out, err := nvmlMockCtlTry(ctx, h, "fail", "--gpu", uuid, "--mode", "fallen_off_bus")
	if err != nil {
		Skip(fmt.Sprintf("profile uses UUIDs nvml-mock-ctl cannot resolve (v1 limitation): %s", strings.TrimSpace(out)))
	}

	const lostSignal = "ecc.errors.uncorrected.aggregate.total"
	Eventually(func() bool {
		return hasFailureMarker(smiGPUValueRaw(ctx, h, consumer, 0, lostSignal))
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeTrue(), "GPU targeted by UUID should surface failure markers")

	if gpuCount(ctx, h, consumer) > 1 {
		By("verify a non-targeted GPU stays healthy")
		Expect(hasFailureMarker(smiGPUValueRaw(ctx, h, consumer, 1, lostSignal))).
			To(BeFalse(), "GPU 1 must stay healthy when only GPU 0's UUID was targeted")
	}

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() bool {
		return hasFailureMarker(smiGPUValueRaw(ctx, h, consumer, 0, lostSignal))
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
		return smiGPUInt(ctx, h, consumer, 0, "ecc.errors.uncorrected.aggregate.total")
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
		return smiGPUInt(ctx, h, consumer, 0, "ecc.errors.uncorrected.aggregate.total")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeNumerically(">", 0), "GPU 0 should trip before recovery")

	By("recover GPU 0 with fail --mode healthy (no reset)")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "0", "--mode", "healthy")
	Eventually(func() int {
		return smiGPUInt(ctx, h, consumer, 0, "ecc.errors.uncorrected.aggregate.total")
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(0), "GPU 0 should recover after fail --mode healthy")

	By("final reset")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}
