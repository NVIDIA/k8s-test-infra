//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"context"
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// Running nvidia-smi in a pod and asserting through Gomega. Every helper is a
// Ginkgo helper so failures point at the calling spec line. ExecQuiet keeps the
// ~90 KB document out of the Ginkgo log; only the decoded problems are
// attached to a failure.

// Inventory asserts nvidia-smi runs in the pod at all, and that the `-q -x`
// document describes the profile's full device name and exactly ExpectedGPUs
// devices with no processes on them.
func Inventory(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	// The bare invocation renders the human table, a code path -q -x never
	// exercises. Only its exit status is checked; everything below reads the
	// machine-readable document instead.
	ginkgo.By("nvidia-smi default output")
	res, err := k.Exec(ctx, pod, "nvidia-smi")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi exited with error: %s", res.Combined())

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x describes %d GPUs named %q", p.ExpectedGPUs(), p.DisplayName))
	out := query(ctx, k, pod)

	problems := InventoryProblems(out, p.DisplayName, p.ExpectedGPUs())
	gomega.Expect(problems).To(gomega.BeEmpty(), "nvidia-smi inventory wrong:\n%s",
		strings.Join(problems, "\n"))

	ginkgo.By("nvidia-smi -q -x reports no phantom processes")
	problems = PhantomProcessProblems(out)
	gomega.Expect(problems).To(gomega.BeEmpty(), "phantom processes:\n%s",
		strings.Join(problems, "\n"))
}

// JpgOfaUtilization asserts nvidia-smi -q -x reports the given JPEG and OFA
// percentages on every GPU. Both elements read N/A until the NVML getters
// existed, so the configured values were silently dropped. See issue #637.
func JpgOfaUtilization(ctx context.Context, k *kube.Client, pod kube.PodRef, wantJPEG, wantOFA int) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x reports jpeg_util %d %% / ofa_util %d %%", wantJPEG, wantOFA))
	problems := JpgOfaUtilizationProblems(query(ctx, k, pod), wantJPEG, wantOFA)
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"JPEG/OFA utilization wrong:\n%s", strings.Join(problems, "\n"))
}

// TemperatureThresholds asserts nvidia-smi -q -x uses the
// architecture-correct threshold presentation for the profile: absolute
// elements on pre-Ada, *_tlimit_threshold elements on Ada and later. See issue
// #635.
func TemperatureThresholds(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x temperature thresholds on %s (arch=%s, tlimit=%v)",
		p.Name, p.Architecture(), p.ReportsTLimitTemp()))
	problems := TemperatureProblems(query(ctx, k, pod), p.ReportsTLimitTemp(),
		p.ShutdownThresholdC(), p.SlowdownThresholdC(), p.MaxOperatingC())
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"temperature threshold presentation wrong for profile %s:\n%s",
		p.Name, strings.Join(problems, "\n"))
}

// query execs `nvidia-smi -q -x` and asserts it succeeded, returning stdout.
func query(ctx context.Context, k *kube.Client, pod kube.PodRef) string {
	ginkgo.GinkgoHelper()

	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"nvidia-smi -q -x exited with error: %s", res.Combined())
	return res.Stdout
}

// SnapshotFromPod execs `nvidia-smi -q -x` in pod and decodes it. It returns an
// error rather than asserting, so pollers can retry; the combined output is
// folded into the error because a failed exec has no document to report.
func SnapshotFromPod(ctx context.Context, k *kube.Client, pod kube.PodRef) (Snapshot, error) {
	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	if err != nil {
		return Snapshot{}, fmt.Errorf("nvidia-smi -q -x: %w: %s", err, res.Combined())
	}
	return ParseSnapshot(res.Stdout)
}
