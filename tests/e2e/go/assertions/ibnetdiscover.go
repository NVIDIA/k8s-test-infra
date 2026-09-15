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
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// caGUIDsCmd reads the GUIDs the local CAs claim. It collects the CA-level
// node_guid and sys_image_guid as well as the port GUID because ibnetdiscover
// prints node GUIDs (caguid=) alongside port GUIDs, and the mock derives
// node_guid from port_guid by clearing the EUI-64 U/L bit
// (fabric.nodeGUIDFromPortGUID) — so the two differ. A local set built from
// port GUIDs alone would let the local CA's own node GUID pass as a cross-pod
// peer and false-pass the assertion below.
//
// MOCK_IB_ROOT expands pod-side inside `sh -c`, against the pod's env, the same
// way the ibping sysfs paths do.
const caGUIDsCmd = `for ca in ${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib}/sys/class/infiniband/*; do
  for f in "$ca/ports/1/port_guid" "$ca/node_guid" "$ca/sys_image_guid"; do
    [ -r "$f" ] && cat "$f"
  done
done 2>/dev/null`

// IBNetDiscover asserts the whole-fabric scan: the libibnetdisc directed-route
// walk from the local pod must print at least one Ca record and must reach at
// least one non-local GUID. It runs over the same NODE_INFO / NODE_DESC /
// PORT_INFO synthesis iblinkinfo uses, so it needs the mock-ib UMAD daemon and
// a peer pod. Skips for IB-disabled profiles.
func IBNetDiscover(ctx context.Context, k *kube.Client, local, peer kube.PodRef, p profile.Profile, retries int, retrySleep time.Duration) {
	ginkgo.GinkgoHelper()
	if p.ExpectedHCAs() == 0 {
		ginkgo.Skip("IB disabled for profile " + p.Name)
	}
	if retries < 1 {
		retries = 3
	}

	waitMockIBSocket(ctx, k, local)

	// Soft: warn (do not fail) if a pod has not logged 'fabric ready' yet. The
	// scan itself is the assertion; the log line only explains a failure.
	for _, pod := range []kube.PodRef{local, peer} {
		logs, _ := k.PodLogs(ctx, pod.Namespace, pod.Pod, "node-agent", 200)
		if !strings.Contains(logs, "fabric ready") {
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "WARN: pod %s log does not show 'fabric ready' yet\n", pod.Pod)
		}
	}

	res, _ := k.ExecSh(ctx, local, caGUIDsCmd)
	localGUIDs := map[string]struct{}{}
	for _, line := range strings.Split(res.Combined(), "\n") {
		if g := normGUID(line); g != "" {
			localGUIDs[g] = struct{}{}
		}
	}
	gomega.Expect(localGUIDs).NotTo(gomega.BeEmpty(),
		"could not enumerate local GUIDs (port/node/sys_image) from sysfs on %s", local.Pod)

	ginkgo.By("ibnetdiscover scan from " + local.Pod + " reaches a cross-pod peer")
	var last string
	for attempt := 1; attempt <= retries; attempt++ {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "--- ibnetdiscover attempt %d/%d ---\n", attempt, retries)
		out, _ := k.ExecSh(ctx, local, "ibnetdiscover 2>&1") // tolerate non-zero, like the bash `|| true`
		last = out.Combined()

		cross, why := ibNetDiscoverPeer(last, localGUIDs)
		if why == "" {
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "ibnetdiscover cross-pod peer GUID: %s\n", cross)
			return
		}
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "%s, retrying\n", why)
		if attempt < retries {
			sleepCtx(ctx, retrySleep)
		}
	}
	ginkgo.Fail(fmt.Sprintf("ibnetdiscover on %s did not reach a cross-pod peer after %d attempts:\n%s",
		local.Pod, retries, last))
}
