//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// FabricManagerGate ports the "Verify fake fabricmanager readiness" step. It
// reads MOCK_FABRICMANAGER off the DEPLOYED daemonset (never a hardcoded
// profile->FM table); only when it is "on" does it Eventually-poll
// nv-fabricmanager-ctl -q for READY inside the pod. It returns whether FM was
// enabled. Callers MUST invoke this BEFORE the NV# topology assertion so the
// real HGX/GB200 ordering (fabric ready -> NV# links) is preserved.
func FabricManagerGate(ctx context.Context, k *kube.Client, ns, dsName string, pod kube.PodRef, timeout, poll time.Duration) bool {
	ginkgo.GinkgoHelper()
	val, _, err := k.DaemonSetContainerEnv(ctx, ns, dsName, "MOCK_FABRICMANAGER")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading MOCK_FABRICMANAGER off daemonset %s/%s", ns, dsName)
	if strings.ToLower(strings.TrimSpace(val)) != "on" {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "fabricmanager not enabled (MOCK_FABRICMANAGER=%q); skipping readiness gate\n", val)
		return false
	}

	ginkgo.By("waiting for fake nv-fabricmanager to report READY")
	gomega.Eventually(func() (string, error) {
		res, _ := k.ExecSh(ctx, pod, "/usr/bin/nv-fabricmanager-ctl -q 2>/dev/null || true")
		return res.Combined(), nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.ContainSubstring("READY"), "fake fabricmanager did not report READY")
	return true
}
