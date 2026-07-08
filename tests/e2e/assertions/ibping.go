//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/ibutil"
)

const (
	// These embed the literal `${MOCK_IB_ROOT:-...}` so they expand pod-side
	// inside `sh -c`, against the pod's env (matching validate-ibping.sh).
	ibLIDPath    = `${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib}/sys/class/infiniband/mlx5_0/ports/1/lid`
	ibGUIDPath   = `${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib}/sys/class/infiniband/mlx5_0/ports/1/port_guid`
	mockIBSock   = "/run/mock-ib.sock"
	ibPingRecvRE = `[0-9]+ packets transmitted, [1-9][0-9]* received`
)

// ibForbidden are the substrings that mark an ibping failure even if the
// command exits zero (ported from ibping_fail_patterns).
var ibForbidden = []string{
	"client_register for mgmt 3 failed",
	"iberror:",
	"can't open UMAD port",
	"ibwarn:",
	"mad_rpc",
	"Resource temporarily unavailable",
	"can't serve class",
	"100% packet loss",
	", 0 received",
}

var ibRecvRE = regexp.MustCompile(ibPingRecvRE)

func ibHasForbidden(out string) (string, bool) {
	for _, p := range ibForbidden {
		if strings.Contains(out, p) {
			return p, true
		}
	}
	return "", false
}

// ibSuccess requires at least one received reply (not merely "transmitted").
func ibSuccess(out string) bool {
	if ibRecvRE.MatchString(out) {
		return true
	}
	return strings.Contains(out, "0% packet loss")
}

func readSysfs(ctx context.Context, k *kube.Client, pod kube.PodRef, path string) string {
	res, _ := k.ExecSh(ctx, pod, "tr -d '[:space:]' < "+path)
	return strings.TrimSpace(res.Stdout)
}

// IBPing ports validate-ibping.sh: read the server LID/GUID from sysfs, wait
// for both mock-ib sockets, one-shot REGISTER peers (a non-idempotent step run
// ONCE before polling), then exercise both LID and GUID modes with bounded
// retries, asserting the forbidden-pattern allowlist AND a positive-received
// predicate.
func IBPing(ctx context.Context, k *kube.Client, server, client kube.PodRef, mode string, retries int, retrySleep time.Duration) {
	ginkgo.GinkgoHelper()
	if mode == "" {
		mode = "both"
	}
	if retries < 1 {
		retries = 3
	}

	lidRaw := readSysfs(ctx, k, server, ibLIDPath)
	guidRaw := readSysfs(ctx, k, server, ibGUIDPath)
	gomega.Expect(lidRaw).NotTo(gomega.BeEmpty(), "empty LID from server sysfs")
	gomega.Expect(guidRaw).NotTo(gomega.BeEmpty(), "empty port_guid from server sysfs")

	lid, err := ibutil.NormalizeLID(lidRaw)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "normalize LID %q", lidRaw)
	guid, err := ibutil.NormalizeGUID(guidRaw)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "normalize GUID %q", guidRaw)
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "Server LID (sysfs=%s ibping=%s) GUID (sysfs=%s ibping=%s)\n",
		lidRaw, lid, guidRaw, guid)

	waitMockIBSocket(ctx, k, server)
	waitMockIBSocket(ctx, k, client)

	// One-shot REGISTER (NOT idempotent) — once, before the polling loop.
	registerPeersOnce(ctx, k, server, client)
	// Allow registerWithPeersLoop / DNS discovery to populate registries.
	sleepCtx(ctx, 5*time.Second)

	switch mode {
	case "lid":
		runIBPingCase(ctx, k, client, "LID", fmt.Sprintf("ibping -c 3 %s", lid), retries, retrySleep)
	case "guid":
		runIBPingCase(ctx, k, client, "GUID", fmt.Sprintf("ibping -G -c 3 %s", guid), retries, retrySleep)
	default: // both
		runIBPingCase(ctx, k, client, "LID", fmt.Sprintf("ibping -c 3 %s", lid), retries, retrySleep)
		runIBPingCase(ctx, k, client, "GUID", fmt.Sprintf("ibping -G -c 3 %s", guid), retries, retrySleep)
	}
}

func waitMockIBSocket(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.By("waiting for mock-ib socket on " + pod.Pod)
	gomega.Eventually(func() error {
		_, err := k.Exec(ctx, pod, "test", "-S", mockIBSock)
		return err
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(time.Second).
		Should(gomega.Succeed(), "mock-ib socket not ready on %s", pod.Pod)
}

// registerPeersOnce performs the cross-release one-shot REGISTER on both pods.
// Best-effort: peer discovery within a single release happens automatically; a
// repeated register is harmless but is intentionally run once (not polled).
func registerPeersOnce(ctx context.Context, k *kube.Client, server, client kube.PodRef) {
	sIP, _ := k.PodIP(ctx, server.Namespace, server.Pod)
	cIP, _ := k.PodIP(ctx, client.Namespace, client.Pod)
	if sIP == "" || cIP == "" {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "register-peers: could not resolve pod IPs (server=%q client=%q); skipping\n", sIP, cIP)
		return
	}
	reg := func(pod kube.PodRef, podIP, peers string) {
		cmd := fmt.Sprintf(
			"env POD_IP=%s MOCK_IB_PEERS=%s "+
				"MOCK_IB_ROOT=${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib} "+
				"MOCK_IB_PING_PORT=${MOCK_IB_PING_PORT:-18515} "+
				"/usr/local/bin/mock-ib -register-peers "+
				"-ib-root ${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib} "+
				"-port ${MOCK_IB_PING_PORT:-18515} -fabric",
			podIP, peers)
		_, _ = k.ExecSh(ctx, pod, cmd)
	}
	reg(server, sIP, cIP)
	reg(client, cIP, sIP)
}

func runIBPingCase(ctx context.Context, k *kube.Client, client kube.PodRef, label, cmd string, retries int, retrySleep time.Duration) {
	ginkgo.By("ibping case: " + label)
	var last string
	for attempt := 1; attempt <= retries; attempt++ {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "--- ibping (%s) attempt %d/%d ---\n", label, attempt, retries)
		res, _ := k.ExecSh(ctx, client, cmd+" 2>&1") // tolerate non-zero, like the bash `|| true`
		last = res.Combined()
		if p, bad := ibHasForbidden(last); bad {
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "forbidden pattern %q present, retrying\n", p)
		} else if ibSuccess(last) {
			return
		}
		if attempt < retries {
			sleepCtx(ctx, retrySleep)
		}
	}
	ginkgo.Fail(fmt.Sprintf("ibping (%s) did not report success after %d attempts:\n%s", label, retries, last))
}
