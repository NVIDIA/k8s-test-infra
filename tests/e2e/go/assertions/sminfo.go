//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// SMInfo asserts the master subnet manager: sminfo reads its local PortInfo
// (MasterSMLID=1) and issues a SubnGet(SMInfo) to that LID, which the mock-ib
// daemon answers from the elected master SM — the lowest port GUID in the
// fabric graph — with SMState=MASTER(3). Both pods must name the SAME master,
// which is the fabric-global "one SM identity" property a real subnet has and a
// per-pod mock would not. Needs the UMAD daemon, so it skips for IB-disabled
// profiles.
//
// A zero-value peer skips the cross-pod comparison and checks only pod.
func SMInfo(ctx context.Context, k *kube.Client, pod, peer kube.PodRef, p profile.Profile, retries int, retrySleep time.Duration) {
	ginkgo.GinkgoHelper()
	if p.ExpectedHCAs() == 0 {
		ginkgo.Skip("IB disabled for profile " + p.Name)
	}
	if retries < 1 {
		retries = 3
	}

	guid := runSMInfo(ctx, k, pod, retries, retrySleep)
	if peer.Pod == "" {
		return
	}

	peerGUID := runSMInfo(ctx, k, peer, retries, retrySleep)
	ginkgo.By("both pods agree on a single master SM")
	gomega.Expect(peerGUID).To(gomega.Equal(guid),
		"pods disagree on the master SM GUID (%s=%s, %s=%s)", pod.Pod, guid, peer.Pod, peerGUID)
}

// runSMInfo returns the master SM GUID the pod reports, failing the spec if it
// never reports a believable one.
func runSMInfo(ctx context.Context, k *kube.Client, pod kube.PodRef, retries int, retrySleep time.Duration) string {
	ginkgo.GinkgoHelper()
	waitMockIBSocket(ctx, k, pod)

	ginkgo.By("sminfo reports a master SM on " + pod.Pod)
	var last string
	for attempt := 1; attempt <= retries; attempt++ {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "--- sminfo on %s attempt %d/%d ---\n", pod.Pod, attempt, retries)
		out, _ := k.ExecSh(ctx, pod, "sminfo 2>&1") // tolerate non-zero, like the bash `|| true`
		last = out.Combined()

		guid, why := sminfoMaster(last)
		if why == "" {
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "%s reports master SM guid=%s state=MASTER\n", pod.Pod, guid)
			return guid
		}
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "%s, retrying\n", why)
		if attempt < retries {
			sleepCtx(ctx, retrySleep)
		}
	}
	ginkgo.Fail(fmt.Sprintf("sminfo did not report a master SM on %s after %d attempts:\n%s",
		pod.Pod, retries, last))
	return "" // unreachable: Fail panics.
}
