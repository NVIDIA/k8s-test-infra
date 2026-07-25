//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// GFD label keys asserted against GPU Feature Discovery.
const (
	GFDLabelProduct = "nvidia.com/gpu.product"
	GFDLabelMemory  = "nvidia.com/gpu.memory"
	GFDLabelCount   = "nvidia.com/gpu.count"
)

// ExpectedGFDLabels derives the GFD labels a node must carry from the profile,
// rather than from the node itself. Deriving them independently is what makes
// the assertion discriminating: if GFD stopped reading the mock NVML, the count
// would go absent or wrong and the comparison would catch it.
func ExpectedGFDLabels(product string, memoryMiB, gpuCount int) map[string]string {
	return map[string]string{
		GFDLabelProduct: product,
		GFDLabelMemory:  strconv.Itoa(memoryMiB),
		GFDLabelCount:   strconv.Itoa(gpuCount),
	}
}

// DiffGFDLabels returns one human-readable problem per expected label that is
// missing or carries the wrong value. An empty result means the node matches.
// It reports every problem rather than stopping at the first, so one run shows
// the whole picture.
func DiffGFDLabels(want, got map[string]string) []string {
	keys := make([]string, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var problems []string
	for _, key := range keys {
		actual, present := got[key]
		switch {
		case !present || actual == "":
			problems = append(problems, fmt.Sprintf("label %s missing (want %q)", key, want[key]))
		case actual != want[key]:
			problems = append(problems, fmt.Sprintf("label %s = %q, want %q", key, actual, want[key]))
		}
	}

	return problems
}

// WaitGFDLabels polls until the node carries every expected GFD label with the
// expected value, and fails the spec otherwise.
//
// This replaces a warning-only check that could never fail: GPU Feature
// Discovery could have been removed entirely and the scenario would still have
// gone green. Labels are published asynchronously after the operator settles,
// hence the poll rather than a single read.
func WaitGFDLabels(ctx context.Context, k *kube.Client, node string, want map[string]string, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("waiting for GFD labels on %s", node))

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
