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

// WaitNodeLabelsPresent polls until every requested node label is set to a
// non-empty value.
func WaitNodeLabelsPresent(ctx context.Context, k *kube.Client, node string, labels []string, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for node %s labels %s", node, strings.Join(labels, ", ")))
	gomega.Eventually(func() ([]string, error) {
		var missing []string
		for _, label := range labels {
			v, ok, err := k.NodeLabel(ctx, node, label)
			if err != nil {
				return nil, err
			}
			if !ok || v == "" {
				missing = append(missing, label)
			}
		}
		return missing, nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeEmpty(), "node %s missing labels", node)
}

// WaitResourceSlicePerNode polls until at least one ResourceSlice exists and
// every published ResourceSlice reports exactly want devices. The DRA driver
// publishes one ResourceSlice per node with the mock's advertised GPU count,
// so this is the load-bearing invariant on any cluster shape (1-node harness
// or 1-CP + N-worker Tilt-managed clusters): asserting per-slice equality
// catches a single node with a broken mock, whereas a summed check would only
// notice if the grand total moved.
func WaitResourceSlicePerNode(ctx context.Context, k *kube.Client, want int, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for every ResourceSlice to publish %d devices", want))
	gomega.Eventually(func() ([]int, error) {
		return k.ResourceSliceDeviceCounts(ctx)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.SatisfyAll(
			gomega.Not(gomega.BeEmpty()),
			gomega.HaveEach(gomega.Equal(want)),
		), "per-node ResourceSlice device count")
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

// WaitJobComplete polls until a Job records at least one successful completion.
func WaitJobComplete(ctx context.Context, k *kube.Client, ns, name string, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for job %s/%s complete", ns, name))
	gomega.Eventually(func() (bool, error) {
		return k.JobComplete(ctx, ns, name)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeTrue(), "job %s/%s did not complete", ns, name)
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

// NodeLabelAbsent asserts a node label is not set at all. This is the guard
// that proves provenance: with NFD absent, nothing in nvml-mock may create
// feature.node.kubernetes.io/pci-10de.present. Reinstating a direct
// `kubectl label` for that key turns this red.
func NodeLabelAbsent(ctx context.Context, k *kube.Client, node, key string) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("node %s has no label %s", node, key))
	v, ok, err := k.NodeLabel(ctx, node, key)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ok).To(gomega.BeFalse(),
		"node %s unexpectedly carries %s=%s — nvml-mock must not write this label", node, key, v)
}

// NodeAnnotationListContains asserts a node annotation holds a comma-separated
// list with item as one of its ELEMENTS. This is the ownership half of the
// provenance guard: NFD records every label it owns in
// nfd.node.kubernetes.io/feature-labels, and a label written by anything else
// (a `kubectl label` in setup.sh, say) never appears there — so presence of the
// label plus membership here is the direct discriminator, independent of any
// ordering argument.
//
// Membership, never strings.Contains on the raw value: a substring match would
// also accept a neighbouring entry that merely contains item (`xpci-10de.presentx`).
func NodeAnnotationListContains(ctx context.Context, k *kube.Client, node, key, item string) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("node %s annotation %s lists %s", node, key, item))
	v, ok, err := k.NodeAnnotation(ctx, node, key)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ok).To(gomega.BeTrue(), "node %s missing annotation %s", node, key)
	items := strings.Split(v, ",")
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	gomega.Expect(items).To(gomega.ContainElement(item),
		"node %s annotation %s=%q does not list %s", node, key, v, item)
}

// DRAEmptyDeviceEdits classifies a stuck gpu-test-pod: if the pod events show
// the "empty device edits" string the failure is the dev-node layout
// regression (preserved from the bash diagnosis). Returns true if seen.
func DRAEmptyDeviceEdits(ctx context.Context, k *kube.Client, ns, pod string) bool {
	out, _ := k.DescribePod(ctx, ns, pod)
	return strings.Contains(out, "empty device edits")
}
