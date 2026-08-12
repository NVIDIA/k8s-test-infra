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

	// Regression guard: with no processes configured, the process-detail-list
	// path (used by `-q` and `--query-compute-apps`) must report none. A prior
	// bug had the internal export-table stub return SUCCESS without zeroing the
	// caller's count, so nvidia-smi rendered its uninitialized buffer as
	// hundreds of phantom processes (PID 0). --format=csv,noheader yields one
	// line per process, so a clean GPU produces empty output.
	ginkgo.By("nvidia-smi --query-compute-apps reports no phantom processes")
	res, err = k.Exec(ctx, pod, "nvidia-smi", "--query-compute-apps=pid,used_memory", "--format=csv,noheader")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi --query-compute-apps exited with error: %s", res.Combined())
	gomega.Expect(strings.TrimSpace(res.Combined())).To(gomega.BeEmpty(),
		"expected no compute-apps, got phantom processes:\n%s", res.Combined())

	ginkgo.By("nvidia-smi -q reports no phantom processes")
	res, err = k.Exec(ctx, pod, "nvidia-smi", "-q")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi -q exited with error: %s", res.Combined())
	gomega.Expect(countMatches(res.Combined(), `(?m)^\s*Process ID\b`)).To(gomega.Equal(0),
		"expected no per-process entries in nvidia-smi -q")
}

// NvidiaSMITemperatureThresholds asserts nvidia-smi -q -d TEMPERATURE uses the
// architecture-correct threshold presentation for the profile: absolute rows
// on pre-Ada, T.Limit rows on Ada and later. See issue #635.
func NvidiaSMITemperatureThresholds(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -d TEMPERATURE on %s (arch=%s, tlimit=%v)",
		p.Name, p.Architecture(), p.ReportsTLimitTemp()))
	res, err := k.Exec(ctx, pod, "nvidia-smi", "-q", "-d", "TEMPERATURE")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"nvidia-smi -q -d TEMPERATURE exited with error: %s", res.Combined())
	out := res.Combined()
	problems := DiffTemperatureQuery(out, p.ReportsTLimitTemp(),
		p.ShutdownThresholdC(), p.SlowdownThresholdC(), p.MaxOperatingC())
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"temperature threshold presentation wrong for profile %s:\n%s\n%s",
		p.Name, strings.Join(problems, "\n"), strings.TrimSpace(out))
}
