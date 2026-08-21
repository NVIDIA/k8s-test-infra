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

// Runtime fabric health scenarios for issue #677.
//
// Before #677 every element of the `nvidia-smi -q -x` fabric health block read
// N/A, so a consumer could not tell a healthy fabric from an unknown one and
// degraded fabric handling could not be exercised at all. The deployed
// profile's healthy baseline is asserted by nvidiasmi.FabricHealth from the
// profile's own spec; what these scenarios add is the fabric degrading under a
// running workload, which is the case worth simulating:
//
//   - one injected condition flips its own element and only its own element, on
//     the targeted device and only that device — a mask applied wholesale, or a
//     summary pinned without decoding the conditions, would pass a weaker
//     assertion;
//   - clearing the injection returns the block to healthy, without restarting
//     the consumer.
//
// One `-q -x` document carries every GPU, so the scoping assertions compare the
// target and its neighbours from the same observation rather than racing
// separate execs against the override TTL.

// assertRuntimeFabricHealthInjection covers acceptance criteria 2 and 3:
// degrade one specific condition through nvml-mock-ctl while the consumer keeps
// running, assert only that condition moved and only on the target GPU, then
// clear it and assert the fabric reports healthy again.
func assertRuntimeFabricHealthInjection(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	healthy := nvidiasmi.HealthyFabricBlock()
	target := gpuCount(ctx, h, consumer) - 1 // exercise a non-zero index where possible

	// Route Unhealthy specifically, not "some fault": its neighbours must stay
	// False, which is what distinguishes decoding the conditions from applying
	// a mask wholesale.
	degraded := healthy
	degraded.RouteUnhealthy = "True"
	degraded.Summary = "Unhealthy"

	By("degrade the fabric route on GPU " + strconv.Itoa(target) + " via nvml-mock-ctl fabric-health")
	nvmlMockCtl(ctx, h, "fabric-health", "--gpu", strconv.Itoa(target), "route_unhealthy")

	Eventually(func() []string {
		return fabricHealthProblems(ctx, h, consumer, func(out string) []string {
			return nvidiasmi.FabricHealthProblemsAt(out, target, degraded, healthy)
		})
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "GPU %d should report an unhealthy route while its neighbours stay healthy", target)

	// `fabric-health healthy` rather than `reset`: recovery must work without
	// discarding the node's other overrides, the same contract
	// `fail --mode healthy` has.
	By("clear the degradation with fabric-health healthy (no reset)")
	nvmlMockCtl(ctx, h, "fabric-health", "--gpu", strconv.Itoa(target), "healthy")

	Eventually(func() []string {
		return fabricHealthProblems(ctx, h, consumer, func(out string) []string {
			return nvidiasmi.FabricHealthProblems(out, healthy)
		})
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "every GPU should report a healthy fabric again")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}

// assertRuntimeFabricMisconfiguration covers the one condition that is not a
// boolean: the incorrect_configuration element carries which misconfiguration
// was detected, so a named value has to reach it rather than collapsing into a
// generic fault.
func assertRuntimeFabricMisconfiguration(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	resetRuntimeOverrides(ctx, h)

	misconfigured := nvidiasmi.HealthyFabricBlock()
	misconfigured.IncorrectConfiguration = "No Partition"
	misconfigured.Summary = "Unhealthy"

	By("inject a fabric misconfiguration on GPU 0 via nvml-mock-ctl fabric-health")
	nvmlMockCtl(ctx, h, "fabric-health", "--gpu", "0", "no_partition")

	Eventually(func() []string {
		return fabricHealthProblems(ctx, h, consumer, func(out string) []string {
			return nvidiasmi.FabricHealthProblemsAt(out, 0, misconfigured, nvidiasmi.HealthyFabricBlock())
		})
	}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
		Should(BeEmpty(), "GPU 0 should report the injected misconfiguration and nothing else")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")
}

// fabricHealthProblems reads a fresh `nvidia-smi -q -x` document from the
// consumer and hands it to check. A failed exec is returned as a problem rather
// than asserted, so a poll can ride out a consumer restart.
func fabricHealthProblems(ctx SpecContext, h *harness.Harness, consumer kube.PodRef,
	check func(out string) []string) []string {
	GinkgoHelper()
	res, err := h.Kube.ExecQuiet(ctx, consumer, "nvidia-smi", "-q", "-x")
	if err != nil {
		return []string{"nvidia-smi -q -x failed: " + res.Combined()}
	}
	return check(res.Stdout)
}
