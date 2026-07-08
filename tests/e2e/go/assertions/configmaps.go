//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// ProfileConfigMaps ports the demo's "Profile ConfigMaps" step: when the fake
// GPU operator integration is enabled the chart renders one per-profile
// ConfigMap (labeled run.ai/gpu-profile=true) for fake-gpu-operator discovery.
// The demo asserts at least `atLeast` (6); the chart ships 7.
func ProfileConfigMaps(ctx context.Context, k *kube.Client, ns, selector string, atLeast int) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("at least %d ConfigMaps labeled %q in %s", atLeast, selector, ns))
	n, err := k.CountConfigMaps(ctx, ns, selector)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "counting profile ConfigMaps")
	gomega.Expect(n).To(gomega.BeNumerically(">=", atLeast),
		"expected >= %d profile ConfigMaps (%s), found %d", atLeast, selector, n)
}
