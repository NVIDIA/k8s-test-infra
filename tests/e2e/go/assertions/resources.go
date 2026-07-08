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

// WaitDaemonSetReady polls until the DaemonSet has all desired pods ready.
func WaitDaemonSetReady(ctx context.Context, k *kube.Client, ns, name string, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for daemonset %s/%s ready", ns, name))
	gomega.Eventually(func() (bool, error) {
		return k.DaemonSetReady(ctx, ns, name)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeTrue(), "daemonset %s/%s not ready", ns, name)
}

// WaitNodeReady polls until a node's Ready condition is True (used after a
// containerd restart, replacing the bare `sleep 5`).
func WaitNodeReady(ctx context.Context, k *kube.Client, node string, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By("waiting for node " + node + " Ready")
	gomega.Eventually(func() (bool, error) {
		return k.NodeReady(ctx, node)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeTrue(), "node %s not Ready", node)
}

// WaitAllocatableGPU polls until a node reports exactly want allocatable GPUs.
func WaitAllocatableGPU(ctx context.Context, k *kube.Client, node string, want int, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for allocatable %s=%d on %s", kube.GPUResourceName, want, node))
	gomega.Eventually(func() (int, error) {
		return k.AllocatableGPU(ctx, node)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.Equal(want), "node %s allocatable GPUs", node)
}

// WaitResourceSliceTotal polls until the summed ResourceSlice device count
// equals want (DRA driver published the GPUs). Pinned to v1beta1 in kube.
func WaitResourceSliceTotal(ctx context.Context, k *kube.Client, want int, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for ResourceSlice device total = %d", want))
	gomega.Eventually(func() (int, error) {
		return k.ResourceSliceGPUTotal(ctx)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.Equal(want), "ResourceSlice GPU total")
}

// WaitPodPhase polls until a pod reaches the given phase.
func WaitPodPhase(ctx context.Context, k *kube.Client, ns, name, phase string, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for pod %s/%s phase=%s", ns, name, phase))
	gomega.Eventually(func() (string, error) {
		return k.PodPhase(ctx, ns, name)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.Equal(phase), "pod %s/%s phase", ns, name)
}

// NodeLabelEquals asserts a node label has an exact value (HARD check, e.g.
// nvidia.com/gpu.present=true in the device-plugin job).
func NodeLabelEquals(ctx context.Context, k *kube.Client, node, key, want string) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("node %s label %s=%s", node, key, want))
	v, ok, err := k.NodeLabel(ctx, node, key)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ok).To(gomega.BeTrue(), "node %s missing label %s", node, key)
	gomega.Expect(v).To(gomega.Equal(want), "node %s label %s", node, key)
}

// NodeLabelSoft reports (warning-only) whether a GFD/operator label is present.
// Kept soft to preserve current strictness (the operator job only WARNs).
func NodeLabelSoft(ctx context.Context, k *kube.Client, node, key string) {
	v, ok, err := k.NodeLabel(ctx, node, key)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "WARN: reading label %s on %s: %v\n", key, node, err)
	case !ok || v == "":
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "WARNING: label %s not set on %s (soft)\n", key, node)
	default:
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "  %s=%s\n", key, v)
	}
}

// DRAEmptyDeviceEdits classifies a stuck gpu-test-pod: if the pod events show
// the "empty device edits" string the failure is the dev-node layout
// regression (preserved from the bash diagnosis). Returns true if seen.
func DRAEmptyDeviceEdits(ctx context.Context, k *kube.Client, ns, pod string) bool {
	out, _ := k.DescribePod(ctx, ns, pod)
	return strings.Contains(out, "empty device edits")
}
