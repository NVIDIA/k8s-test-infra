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
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/nodes"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// NvidiaSMI ports validate-nvidia-smi.sh: nvidia-smi must run, and `-L` must
// list the profile's full device name and exactly ExpectedGPUs entries.
func NvidiaSMI(ctx context.Context, n nodes.Docker, node string, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By("nvidia-smi default output")
	res, err := n.Exec(ctx, node, DriverNvidiaSMI)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi exited with error: %s", res.Combined())

	ginkgo.By(fmt.Sprintf("nvidia-smi -L lists %d GPUs named %q", p.ExpectedGPUs(), p.DisplayName))
	res, err = n.Exec(ctx, node, DriverNvidiaSMI, "-L")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi -L exited with error: %s", res.Combined())
	out := res.Combined()
	gomega.Expect(out).To(gomega.ContainSubstring(p.DisplayName),
		"GPU name %q not found in nvidia-smi -L", p.DisplayName)

	count := countLinesWithPrefix(out, "GPU")
	gomega.Expect(count).To(gomega.Equal(p.ExpectedGPUs()),
		"nvidia-smi -L GPU count\n%s", strings.TrimSpace(out))
}

// NvidiaSMIPod ports demo.sh step 7's `kubectl exec <pod> -- nvidia-smi`. Unlike
// NvidiaSMI (which runs the host-driver-root binary on the node via docker
// exec), this exercises the IN-POD path: the mock libnvidia-ml.so injected into
// the container, resolved off PATH. It asserts the same device name + count, so
// a regression in the container library-injection path is caught independently
// of the node-level driver root.
func NvidiaSMIPod(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By("in-pod nvidia-smi default output (injected libnvidia-ml)")
	res, err := k.ExecSh(ctx, pod, "nvidia-smi")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "in-pod nvidia-smi exited with error: %s", res.Combined())

	ginkgo.By(fmt.Sprintf("in-pod nvidia-smi -L lists %d GPUs named %q", p.ExpectedGPUs(), p.DisplayName))
	res, err = k.ExecSh(ctx, pod, "nvidia-smi -L")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "in-pod nvidia-smi -L exited with error: %s", res.Combined())
	out := res.Combined()
	gomega.Expect(out).To(gomega.ContainSubstring(p.DisplayName),
		"GPU name %q not found in in-pod nvidia-smi -L", p.DisplayName)

	count := countLinesWithPrefix(out, "GPU")
	gomega.Expect(count).To(gomega.Equal(p.ExpectedGPUs()),
		"in-pod nvidia-smi -L GPU count\n%s", strings.TrimSpace(out))
}
