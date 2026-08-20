//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// WaitGFDLabels polls until the node carries every expected GFD label with the
// expected value, and fails the spec otherwise.
//
// This replaces a warning-only check that could never fail: GPU Feature
// Discovery could have been removed entirely and the scenario would still have
// gone green. Labels are published asynchronously after the operator settles,
// hence the poll rather than a single read.
//
// The expectation and comparison it uses live in gfd_labels.go, which is
// untagged so they stay unit-testable without a cluster.
func WaitGFDLabels(ctx context.Context, k *kube.Client, node string, want map[string]string, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By("waiting for GFD labels on " + node)

	var last []string
	gomega.Eventually(func() ([]string, error) {
		got := make(map[string]string, len(want))
		for key := range want {
			value, ok, err := k.NodeLabel(ctx, node, key)
			if err != nil {
				return nil, err
			}
			if ok {
				got[key] = value
			}
		}
		last = DiffGFDLabels(want, got)
		return last, nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeEmpty(), "GFD labels on node %s did not match the profile: %v", node, &last)
}
