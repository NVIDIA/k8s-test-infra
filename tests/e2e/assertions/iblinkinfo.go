//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/profile"
)

var guid16RE = regexp.MustCompile(`(?i)0x[0-9a-f]{16}`)

func normGUID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ":", "")
	s = strings.TrimPrefix(s, "0x")
	return s
}

// IBLinkInfo ports validate-iblinkinfo.sh: the fabric scan from the local pod
// must surface at least one cross-pod (non-local) port GUID. The `fabric ready`
// log line is a soft signal (warning only). Skips for IB-disabled profiles.
func IBLinkInfo(ctx context.Context, k *kube.Client, local, peer kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()
	if p.ExpectedHCAs() == 0 {
		ginkgo.Skip("IB disabled for profile " + p.Name)
	}

	// Soft: warn (do not fail) if a pod has not logged 'fabric ready' yet.
	for _, pod := range []kube.PodRef{local, peer} {
		logRes, _ := k.ExecSh(ctx, pod, "cat /tmp/mock-ib.log 2>/dev/null || true")
		if !strings.Contains(logRes.Combined(), "fabric ready") {
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "WARN: pod %s log does not show 'fabric ready' yet\n", pod.Pod)
		}
	}

	localGUIDs := readLocalGUIDs(ctx, k, local)
	gomega.Expect(localGUIDs).NotTo(gomega.BeEmpty(),
		"could not enumerate local port GUIDs from sysfs on %s", local.Pod)

	ginkgo.By("iblinkinfo fabric scan finds a cross-pod peer")
	out, err := k.ExecSh(ctx, local, "iblinkinfo 2>&1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "iblinkinfo failed: %s", out.Combined())

	found := map[string]struct{}{}
	for _, m := range guid16RE.FindAllString(out.Combined(), -1) {
		found[normGUID(m)] = struct{}{}
	}
	gomega.Expect(found).NotTo(gomega.BeEmpty(), "iblinkinfo on %s printed no port GUIDs", local.Pod)

	var cross string
	for g := range found {
		if _, isLocal := localGUIDs[g]; !isLocal {
			cross = g
			break
		}
	}
	gomega.Expect(cross).NotTo(gomega.BeEmpty(),
		"iblinkinfo on %s found only local GUIDs, no cross-pod peer", local.Pod)
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "iblinkinfo cross-pod peer GUID: %s\n", cross)
}

func readLocalGUIDs(ctx context.Context, k *kube.Client, pod kube.PodRef) map[string]struct{} {
	res, _ := k.ExecSh(ctx, pod, `for f in /var/lib/nvml-mock/ib/sys/class/infiniband/*/ports/1/port_guid; do [ -r "$f" ] && cat "$f"; done 2>/dev/null`)
	set := map[string]struct{}{}
	for _, line := range strings.Split(res.Combined(), "\n") {
		g := normGUID(line)
		if g != "" {
			set[g] = struct{}{}
		}
	}
	return set
}
