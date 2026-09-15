//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	imexassert "github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/imex"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/pod"
)

const (
	imexLifecycleConfig = "/tmp/imex.cfg"
	imexLifecycleNodes  = "/tmp/nodes.cfg"
)

// assertIMEXLifecycle turns the compute-domain guide's manual state machine
// into one bounded regression test: one locally-ready member is not a healthy
// domain, its second peer makes the domain healthy, and peer loss degrades it.
func assertIMEXLifecycle(ctx SpecContext, h *harness.Harness, workers []cluster.Node) {
	GinkgoHelper()
	Expect(len(workers)).To(BeNumerically(">=", 2))

	// Keep peer assignment stable even if a caller obtained the workers from a
	// Kubernetes collection without using Cluster.Workers, which sorts them.
	workers = append([]cluster.Node(nil), workers...)
	sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })

	peerA := applyIMEXLifecyclePod(ctx, h, "imex-lifecycle-a", workers[0].Name)
	peerB := applyIMEXLifecyclePod(ctx, h, "imex-lifecycle-b", workers[1].Name)
	peers := []kube.PodRef{peerA, peerB}
	DeferCleanup(func(cleanupCtx SpecContext) {
		for _, peer := range peers {
			res, _ := h.Kube.ExecSh(cleanupCtx, peer,
				`if test -f /tmp/imex.pid; then kill -TERM "$(cat /tmp/imex.pid)" 2>/dev/null || true; fi`)
			if CurrentSpecReport().Failed() {
				GinkgoWriter.Printf("\n%s IMEX diagnostics:\n%s\n", peer.Pod, res.Combined())
				logs, _ := h.Kube.ExecSh(cleanupCtx, peer,
					`tail -40 /tmp/nvidia-imex.log /tmp/imex.stdout 2>/dev/null || true`)
				GinkgoWriter.Printf("%s\n", logs.Combined())
			}
		}
	})

	ipA := waitForPodIP(ctx, h, peerA)
	ipB := waitForPodIP(ctx, h, peerB)
	for _, peer := range peers {
		configureIMEXPeer(ctx, h, peer, ipA, ipB)
	}

	By("starting peer A and waiting for its local readiness probe")
	startIMEXPeer(ctx, h, peerA)
	Eventually(func() (string, error) {
		res, err := h.Kube.ExecQuiet(ctx, peerA, "nvidia-imex-ctl", "-c", imexLifecycleConfig, "-q")
		return strings.TrimSpace(res.Stdout), err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		Should(Equal("READY"), "peer A never became locally ready")

	Eventually(func() (string, error) {
		status, statusErr := readIMEXStatus(ctx, h, peerA)
		return status.State, statusErr
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(Equal("UP"),
			"a domain with only one of its two configured peers must not report UP")

	By("starting peer B and waiting for both members to form an UP domain")
	startIMEXPeer(ctx, h, peerB)
	Eventually(func() (string, error) {
		status, statusErr := readIMEXStatus(ctx, h, peerA)
		return status.State, statusErr
	}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
		Should(Equal("UP"), "the two IMEX peers never formed a healthy domain")

	joined, err := readIMEXStatus(ctx, h, peerA)
	Expect(err).NotTo(HaveOccurred())
	Expect(joined.ReadyNodes()).To(Equal(2), "both domain members must report READY")
	Expect(joined.NoGPUNodes()).To(Equal(2), "both domain members must use the NO_GPU handshake")

	By("stopping peer B and waiting for peer A to detect the loss")
	res, err := h.Kube.ExecSh(ctx, peerB, `kill -TERM "$(cat /tmp/imex.pid)"`)
	Expect(err).NotTo(HaveOccurred(), "stop peer B: %s", res.Combined())
	Eventually(func() (string, error) {
		status, statusErr := readIMEXStatus(ctx, h, peerA)
		return status.State, statusErr
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(Equal("UP"), "peer A did not detect that peer B stopped")
}

func applyIMEXLifecyclePod(ctx context.Context, h *harness.Harness, name, node string) kube.PodRef {
	GinkgoHelper()
	spec := pod.Spec{
		Name:        name,
		Namespace:   nriWorkloadNS,
		Labels:      map[string]string{"app": name},
		Annotations: map[string]string{nriImexAnnotation: "true"},
		Image:       config.Image(),
		Node:        node,
		Command:     []string{"/bin/sh", "-c"},
		Args:        []string{"trap 'exit 0' TERM; sleep 3600 & wait"},
	}
	manifest := spec.Render()
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply %s", name)
	DeferCleanup(func(cleanupCtx SpecContext) { _ = h.Kube.Delete(cleanupCtx, manifest) }) //nolint:contextcheck // Ginkgo cleanup context is intentionally distinct.
	Eventually(func() (string, error) {
		return h.Kube.PodPhase(ctx, nriWorkloadNS, name)
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		Should(Equal("Running"), "%s never reached Running", name)
	return kube.PodRef{Namespace: nriWorkloadNS, Pod: name}
}

func waitForPodIP(ctx context.Context, h *harness.Harness, peer kube.PodRef) string {
	GinkgoHelper()
	var ip string
	Eventually(func() (string, error) {
		var err error
		ip, err = h.Kube.PodIP(ctx, peer.Namespace, peer.Pod)
		return ip, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "%s never received a pod IP", peer.Pod)
	return ip
}

func configureIMEXPeer(ctx context.Context, h *harness.Harness, peer kube.PodRef, ipA, ipB string) {
	GinkgoHelper()
	script := fmt.Sprintf(`printf '%%s\n%%s\n' %q %q > %s
sed -e 's/^DAEMONIZE=1/DAEMONIZE=0/' \
    -e 's|^IMEX_NODE_CONFIG_FILE=.*|IMEX_NODE_CONFIG_FILE=%s|' \
    -e 's|^LOG_FILE_NAME=.*|LOG_FILE_NAME=/tmp/nvidia-imex.log|' \
    /etc/nvidia-imex/config.cfg > %s`, ipA, ipB, imexLifecycleNodes,
		imexLifecycleNodes, imexLifecycleConfig)
	res, err := h.Kube.ExecSh(ctx, peer, script)
	Expect(err).NotTo(HaveOccurred(), "configure IMEX in %s: %s", peer.Pod, res.Combined())
}

func startIMEXPeer(ctx context.Context, h *harness.Harness, peer kube.PodRef) {
	GinkgoHelper()
	res, err := h.Kube.ExecSh(ctx, peer,
		`nvidia-imex -c `+imexLifecycleConfig+` >/tmp/imex.stdout 2>&1 & echo $! > /tmp/imex.pid`)
	Expect(err).NotTo(HaveOccurred(), "start IMEX in %s: %s", peer.Pod, res.Combined())
}

func readIMEXStatus(ctx context.Context, h *harness.Harness, peer kube.PodRef) (imexassert.Status, error) {
	res, err := h.Kube.ExecQuiet(ctx, peer, "nvidia-imex-ctl", "-c", imexLifecycleConfig, "-N", "-j")
	if err != nil {
		return imexassert.Status{}, fmt.Errorf("read IMEX status in %s: %w", peer.Pod, err)
	}
	status, err := imexassert.ParseStatus(res.Stdout)
	if err != nil {
		return imexassert.Status{}, fmt.Errorf("read IMEX status in %s: %w", peer.Pod, err)
	}
	return status, nil
}
