//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// NvidiaSMI ports validate-nvidia-smi.sh through `kubectl exec`: nvidia-smi
// must run in the nvml-mock pod, and `-L` must list the profile's full device
// name and exactly ExpectedGPUs entries.
func NvidiaSMI(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By("nvidia-smi default output")
	res, err := k.Exec(ctx, pod, "nvidia-smi")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi exited with error: %s", res.Combined())

	ginkgo.By(fmt.Sprintf("nvidia-smi -L lists %d GPUs named %q", p.ExpectedGPUs(), p.DisplayName))
	res, err = k.Exec(ctx, pod, "nvidia-smi", "-L")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi -L exited with error: %s", res.Combined())
	out := res.Combined()
	gomega.Expect(out).To(gomega.ContainSubstring(p.DisplayName),
		"GPU name %q not found in nvidia-smi -L", p.DisplayName)

	count := countLinesWithPrefix(out, "GPU")
	gomega.Expect(count).To(gomega.Equal(p.ExpectedGPUs()),
		"nvidia-smi -L GPU count\n%s", strings.TrimSpace(out))
}
