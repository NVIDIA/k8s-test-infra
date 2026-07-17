//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
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

// assertRuntimeECCInjection validates runtime failure control via nvml-mock-ctl:
// it injects ecc_uncorrectable on GPU 0 while the consumer keeps running (no
// Helm upgrade, no pod delete), observes the ECC counters rise within the TTL,
// then resets and observes them return to healthy. The consumer must NOT be
// restarted between inject and assert — that is the whole point of the runtime
// override path.
func assertRuntimeECCInjection(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	By("inject ecc_uncorrectable on GPU 0 at runtime via nvml-mock-ctl")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable", "--after-calls", "1", "--xid", "79")

	Eventually(func() int {
		return maxIntegerLine(eccQuery(ctx, h, consumer))
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(2*time.Second).
		Should(BeNumerically(">", 0), "running consumer should observe injected ECC errors within the TTL")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return maxIntegerLine(eccQuery(ctx, h, consumer))
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(2*time.Second).
		Should(Equal(0), "consumer should return to healthy after reset")
}
