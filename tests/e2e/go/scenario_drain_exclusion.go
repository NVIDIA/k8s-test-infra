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
)

// Driver state the GPU Operator's driver upgrade relies on: excluded GPUs,
// draining GPUs, and persistence mode, which blocks a drain. Every reading
// comes from a fresh nvidia-smi process, so each assertion also proves the
// state crossed processes through the runtime override document.
//
// The bundled nvidia-smi has no drain subcommand, so drain is set through
// nvml-mock-ctl; nvmlDeviceModifyDrainState itself is covered by the C-ABI
// harness in tests/mocknvml.

// smiUUIDs lists the GPUs a new nvidia-smi process enumerates, in its order.
func smiUUIDs(ctx SpecContext, h *harness.Harness, pod kube.PodRef) []string {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, pod)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x")
	return snap.UUIDs()
}

// smiExcludedUUIDs lists the GPUs `nvidia-smi -B` reports as excluded.
func smiExcludedUUIDs(ctx SpecContext, h *harness.Harness, pod kube.PodRef) []string {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, pod, "nvidia-smi", "-B")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -B: %s", res.Combined())
	gpus, err := nvidiasmi.ParseExcludedGPUs(res.Stdout)
	Expect(err).NotTo(HaveOccurred())
	uuids := make([]string, 0, len(gpus))
	for _, g := range gpus {
		uuids = append(uuids, g.UUID)
	}
	return uuids
}

// smiPersistenceModes lists persistence mode ("Enabled"/"Disabled") per GPU.
func smiPersistenceModes(ctx SpecContext, h *harness.Harness, pod kube.PodRef) []string {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, pod, "nvidia-smi", "--query-gpu=persistence_mode", "--format=csv,noheader")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi --query-gpu=persistence_mode: %s", res.Combined())
	return strings.Fields(res.Stdout)
}

// assertRuntimeExclusionAndDrain excludes the last GPU and drains the first,
// then checks what a new nvidia-smi process enumerates: neither GPU, the rest
// renumbered in order, and only the excluded one listed by `nvidia-smi -B`.
// A reset brings both back.
func assertRuntimeExclusionAndDrain(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	all := smiUUIDs(ctx, h, consumer)
	if len(all) < 3 {
		Skip(fmt.Sprintf("needs three GPUs to exclude one, drain one and keep one; profile has %d", len(all)))
	}
	excluded := len(all) - 1
	Expect(smiExcludedUUIDs(ctx, h, consumer)).To(BeEmpty(), "no GPU is excluded on a clean node")

	By(fmt.Sprintf("exclude GPU %d via nvml-mock-ctl set excluded=true", excluded))
	nvmlMockCtl(ctx, h, "set", "--gpu", strconv.Itoa(excluded), "excluded=true")
	Eventually(func() []string {
		return smiUUIDs(ctx, h, consumer)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(all[:excluded]), "nvidia-smi should not enumerate the excluded GPU")
	Expect(smiExcludedUUIDs(ctx, h, consumer)).To(Equal([]string{all[excluded]}),
		"nvidia-smi -B should list the excluded GPU")

	By("drain GPU 0 via nvml-mock-ctl set draining=true")
	nvmlMockCtl(ctx, h, "set", "--gpu", "0", "draining=true")
	Eventually(func() []string {
		return smiUUIDs(ctx, h, consumer)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(all[1:excluded]), "a new nvidia-smi process should not enumerate the draining GPU")
	Expect(smiExcludedUUIDs(ctx, h, consumer)).To(Equal([]string{all[excluded]}),
		"a draining GPU is hidden, not excluded")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
	Eventually(func() []string {
		return smiUUIDs(ctx, h, consumer)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(all), "every GPU should be enumerated again after reset")
	Expect(smiExcludedUUIDs(ctx, h, consumer)).To(BeEmpty(), "no GPU stays excluded after reset")
}

// assertPersistenceModeAcrossProcesses turns persistence mode off for one GPU
// with `nvidia-smi -pm 0` and reads it back from a second nvidia-smi process,
// which is the step that clears the way for a drain. A reset restores it.
func assertPersistenceModeAcrossProcesses(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	baseline := smiPersistenceModes(ctx, h, consumer)
	target := len(baseline) - 1
	for i, mode := range baseline {
		Expect(mode).To(Equal("Enabled"), "GPU %d must start in persistence mode for a meaningful assertion", i)
	}
	want := append([]string(nil), baseline...)
	want[target] = "Disabled"

	By(fmt.Sprintf("disable persistence mode on GPU %d with nvidia-smi -pm 0", target))
	res, err := h.Kube.Exec(ctx, consumer, "nvidia-smi", "-pm", "0", "-i", strconv.Itoa(target))
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -pm 0: %s", res.Combined())
	Eventually(func() []string {
		return smiPersistenceModes(ctx, h, consumer)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(want), "a second nvidia-smi process should read GPU %d out of persistence mode, and only it", target)

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
	Eventually(func() []string {
		return smiPersistenceModes(ctx, h, consumer)
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(Equal(baseline), "persistence mode should return to the profile baseline after reset")
}
