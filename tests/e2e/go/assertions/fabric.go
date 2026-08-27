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

// FabricManagerGate waits until fabric readiness is published on the node, and
// reports whether fabricmanager is deployed at all.
//
// Enablement is read off the DEPLOYED daemonset rather than a profile->FM table,
// because the chart derives it from the profile's fabric.state and nvlink
// switches. The readiness signal is the marker file itself: that is what the
// mock NVML engine reads to move a GPU from IN_PROGRESS to COMPLETED.
//
// Callers MUST invoke this BEFORE the NV# topology assertion, so the real
// HGX/GB200 ordering (fabric ready -> NV# links) is preserved.
func FabricManagerGate(ctx context.Context, k *kube.Client, ns, dsName string, pod kube.PodRef, timeout, poll time.Duration) bool {
	ginkgo.GinkgoHelper()
	stateDir, _, err := k.DaemonSetContainerEnv(ctx, ns, dsName, "MOCK_FABRICMANAGER_STATE_DIR")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading MOCK_FABRICMANAGER_STATE_DIR off daemonset %s/%s", ns, dsName)
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "fabricmanager not enabled (no state dir); skipping readiness gate\n")
		return false
	}

	marker := stateDir + "/fabricmanager.ready"
	ginkgo.By("waiting for the fabric readiness marker at " + marker)
	gomega.Eventually(func() error {
		_, err := k.ExecSh(ctx, pod, "test -f "+marker)
		return err
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.Succeed(), "fabric readiness marker never appeared at %s", marker)
	return true
}
