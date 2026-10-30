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

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// This scenario drives power smoothing the way an operator does, with
// `nvidia-smi power-smoothing`, and reads the effect back from `nvidia-smi -q
// -x` and `-ppd`. Every command is its own nvidia-smi process, so reading a
// write back proves it reached the state every consumer on the node shares.

// smoothingPreset is the preset the scenario tunes and activates. Any preset
// other than 0 works; shipped profiles start on 0, so activating this one is a
// change the readback can see.
const smoothingPreset = 2

// smoothingTuning is what the scenario writes into smoothingPreset, in the
// units nvidia-smi renders. The values are exact in binary, so the rendered
// two-decimal readback compares equal.
var smoothingTuning = nvidiasmi.PowerSmoothingPreset{
	PercentTMPFloor:      65.5,
	RampUpWPerS:          30,
	RampDownWPerS:        15,
	RampDownHysteresisMS: 250,
}

// smoothingRetune is what the scenario writes into preset 0 while it is active
// and smoothing is enabled: a high floor with slow ramps and a ten-minute
// hysteresis, the shape an operator uses to hold a training job's power steady.
var smoothingRetune = nvidiasmi.PowerSmoothingPreset{
	PercentTMPFloor:      95,
	RampUpWPerS:          20,
	RampDownWPerS:        20,
	RampDownHysteresisMS: 600000,
}

// presetWrite is one `nvidia-smi power-smoothing -p` parameter flag and the
// name nvidia-smi confirms it under.
type presetWrite struct{ flag, value, what string }

// presetWrites renders p as the flags that write it. nvidia-smi takes ramp
// rates in mW/s and renders them in W/s.
func presetWrites(p nvidiasmi.PowerSmoothingPreset) []presetWrite {
	format := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	return []presetWrite{
		{"-ptf", format(p.PercentTMPFloor), "Percent TMP Floor"},
		{"-rur", format(p.RampUpWPerS * 1000), "Ramp Up Rate"},
		{"-rdr", format(p.RampDownWPerS * 1000), "Ramp Down Rate"},
		{"-rdh", strconv.Itoa(p.RampDownHysteresisMS), "Ramp Down Hysteresis"},
	}
}

// smiPowerSmoothing runs one `nvidia-smi power-smoothing` command against a
// single GPU. The subcommand must come first: nvidia-smi rejects -i ahead of
// it. A refusal exits non-zero, so the caller asserts on the result rather
// than on the exec error.
func smiPowerSmoothing(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int, args ...string) (int, string) {
	GinkgoHelper()
	full := append([]string{"nvidia-smi", "power-smoothing", "-i", strconv.Itoa(idx)}, args...)
	res, _ := h.Kube.Exec(ctx, pod, full...)
	return res.ExitCode, res.Combined()
}

// expectPowerSmoothing waits for GPU idx's Power Smoothing block to match want.
func expectPowerSmoothing(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int,
	want nvidiasmi.PowerSmoothingExpectation, why string,
) {
	GinkgoHelper()
	Eventually(func() []string {
		return nvidiasmi.PowerSmoothingGPUProblems(smiGPU(ctx, h, pod, idx), want)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "GPU %d %s", idx, why)
}

// assertPowerSmoothingViaNvidiaSmi tunes a preset, activates it and enables
// smoothing on one GPU, checks each write from a later process, checks a
// preset the device does not have is refused, retunes the default preset while
// it is active, and disables smoothing again. On
// a board without the feature it checks the first write is refused instead.
func assertPowerSmoothingViaNvidiaSmi(ctx SpecContext, h *harness.Harness, consumer kube.PodRef, p profile.Profile) {
	GinkgoHelper()
	if !p.SupportsPowerSmoothing() {
		By("nvidia-smi power-smoothing -s 1 is declined on " + p.Name)
		exit, out := smiPowerSmoothing(ctx, h, consumer, 0, "-s", "1")
		Expect(exit).NotTo(Equal(0), "power-smoothing -s 1 should fail on %s: %s", p.Name, out)
		Expect(out).To(ContainSubstring("Power Smoothing is not supported on this device"))
		return
	}
	if p.PowerSmoothingEnabled() || p.PowerSmoothingActiveProfile() == smoothingPreset {
		Skip("profile " + p.Name + " ships the state this spec writes, so a write would not be observable")
	}
	resetOverridesAround(ctx, h, consumer)

	count := gpuCount(ctx, h, consumer)
	target := count - 1 // exercise a non-zero index where possible
	shipped := nvidiasmi.PowerSmoothingExpectation{Supported: true}
	expectPowerSmoothing(ctx, h, consumer, target, shipped, "must start with smoothing off on preset 0")

	preset := strconv.Itoa(smoothingPreset)
	for _, w := range presetWrites(smoothingTuning) {
		By(fmt.Sprintf("nvidia-smi power-smoothing -i %d -p %s %s %s", target, preset, w.flag, w.value))
		exit, out := smiPowerSmoothing(ctx, h, consumer, target, "-p", preset, w.flag, w.value)
		Expect(exit).To(Equal(0), "%s %s: %s", w.flag, w.value, out)
		Expect(out).To(ContainSubstring("Successfully updated %s for Preset Profile ID %s", w.what, preset))
	}

	By("a separate nvidia-smi power-smoothing -ppd reports the tuned preset " + preset)
	Eventually(func() (nvidiasmi.PowerSmoothingPreset, error) {
		_, out := smiPowerSmoothing(ctx, h, consumer, target, "-ppd")
		presets, err := nvidiasmi.ParsePresetDefinitions(out)
		if err != nil {
			return nvidiasmi.PowerSmoothingPreset{}, err
		}
		if len(presets) != nvidiasmi.PowerSmoothingPresets {
			return nvidiasmi.PowerSmoothingPreset{}, fmt.Errorf("-ppd listed %d presets, want %d: %s",
				len(presets), nvidiasmi.PowerSmoothingPresets, out)
		}
		return presets[smoothingPreset], nil
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(smoothingTuning), "GPU %d preset %s should hold the tuned parameters", target, preset)

	By(fmt.Sprintf("nvidia-smi power-smoothing -i %d -spp %s, then -s 1", target, preset))
	exit, out := smiPowerSmoothing(ctx, h, consumer, target, "-spp", preset)
	Expect(exit).To(Equal(0), "-spp %s: %s", preset, out)
	Expect(out).To(ContainSubstring("Successfully Activated Preset Profile ID: %s", preset))
	exit, out = smiPowerSmoothing(ctx, h, consumer, target, "-s", "1")
	Expect(exit).To(Equal(0), "-s 1: %s", out)
	Expect(out).To(ContainSubstring("Successfully Enabled Power Smoothing"))

	tuned := nvidiasmi.PowerSmoothingExpectation{
		Supported: true, Enabled: true, ActiveProfile: smoothingPreset, Current: &smoothingTuning,
	}
	expectPowerSmoothing(ctx, h, consumer, target, tuned,
		"should report the activated preset and apply its floor once smoothing is enabled")

	// nvidia-smi reads the state before it writes, so this reply comes from
	// what the earlier process recorded rather than from the mock's answer.
	By("a second nvidia-smi power-smoothing -s 1 finds smoothing already enabled")
	exit, out = smiPowerSmoothing(ctx, h, consumer, target, "-s", "1")
	Expect(exit).To(Equal(0), "-s 1: %s", out)
	Expect(out).To(ContainSubstring("Power Smoothing is already enabled on this device"))

	if count > 1 {
		By("verify the writes are scoped to the target GPU (GPU 0 unchanged)")
		expectPowerSmoothing(ctx, h, consumer, 0, shipped, "must keep the shipped power smoothing state")
	}

	// nvidia-smi passes any preset id through, so the refusal is the mock's.
	unknown := strconv.Itoa(nvidiasmi.PowerSmoothingPresets + 2)
	By(fmt.Sprintf("nvidia-smi power-smoothing -spp %s is refused: the device has %d presets",
		unknown, nvidiasmi.PowerSmoothingPresets))
	exit, out = smiPowerSmoothing(ctx, h, consumer, target, "-spp", unknown)
	Expect(exit).NotTo(Equal(0), "-spp %s should fail: %s", unknown, out)
	Expect(out).To(ContainSubstring("Invalid Argument"))
	expectPowerSmoothing(ctx, h, consumer, target, tuned, "a refused preset must not replace the active one")

	retuned := assertActivePresetRetune(ctx, h, consumer, target)

	By(fmt.Sprintf("nvidia-smi power-smoothing -i %d -s 0 drops the floor", target))
	exit, out = smiPowerSmoothing(ctx, h, consumer, target, "-s", "0")
	Expect(exit).To(Equal(0), "-s 0: %s", out)
	Expect(out).To(ContainSubstring("Successfully Disabled Power Smoothing"))
	disabled := retuned
	disabled.Enabled = false
	expectPowerSmoothing(ctx, h, consumer, target, disabled,
		"should keep the active preset but report a 0 W floor once smoothing is disabled")
}

// assertActivePresetRetune switches smoothing back to preset 0 and tunes it in
// a single command while it is active, the way an operator adjusts a running
// node. The applied floor must follow without reactivating the preset.
func assertActivePresetRetune(ctx SpecContext, h *harness.Harness, pod kube.PodRef, idx int,
) nvidiasmi.PowerSmoothingExpectation {
	GinkgoHelper()
	By(fmt.Sprintf("nvidia-smi power-smoothing -i %d -spp 0 returns to the default preset", idx))
	exit, out := smiPowerSmoothing(ctx, h, pod, idx, "-spp", "0")
	Expect(exit).To(Equal(0), "-spp 0: %s", out)
	Expect(out).To(ContainSubstring("Successfully Activated Preset Profile ID: 0"))

	writes := presetWrites(smoothingRetune)
	args := make([]string, 0, 2+2*len(writes))
	args = append(args, "-p", "0")
	for _, w := range writes {
		args = append(args, w.flag, w.value)
	}
	cmd := strings.Join(args, " ")
	By(fmt.Sprintf("nvidia-smi power-smoothing -i %d %s retunes the active preset", idx, cmd))
	exit, out = smiPowerSmoothing(ctx, h, pod, idx, args...)
	Expect(exit).To(Equal(0), "%s: %s", cmd, out)
	for _, w := range writes {
		Expect(out).To(ContainSubstring("Successfully updated %s for Preset Profile ID 0", w.what))
	}

	retuned := nvidiasmi.PowerSmoothingExpectation{
		Supported: true, Enabled: true, ActiveProfile: 0, Current: &smoothingRetune,
	}
	expectPowerSmoothing(ctx, h, pod, idx, retuned,
		"should apply the retuned floor of the active preset without reactivating it")
	return retuned
}
