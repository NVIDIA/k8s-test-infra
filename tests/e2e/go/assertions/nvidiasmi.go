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
// must run in the nvml-mock pod, and the `-q -x` document must describe the
// profile's full device name and exactly ExpectedGPUs entries.
func NvidiaSMI(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	// The bare invocation renders the human table, a code path -q -x never
	// exercises. Only its exit status is checked; everything below reads the
	// machine-readable document instead.
	ginkgo.By("nvidia-smi default output")
	res, err := k.Exec(ctx, pod, "nvidia-smi")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi exited with error: %s", res.Combined())

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x describes %d GPUs named %q", p.ExpectedGPUs(), p.DisplayName))
	res, err = k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi -q -x exited with error: %s", res.Combined())
	out := res.Stdout

	problems := DiffInventoryXML(out, p.DisplayName, p.ExpectedGPUs())
	gomega.Expect(problems).To(gomega.BeEmpty(), "nvidia-smi inventory wrong:\n%s",
		strings.Join(problems, "\n"))

	ginkgo.By("nvidia-smi -q -x reports no phantom processes")
	problems = DiffNoProcessesXML(out)
	gomega.Expect(problems).To(gomega.BeEmpty(), "phantom processes:\n%s",
		strings.Join(problems, "\n"))
}

// NvidiaSMIJpgOfaUtilization asserts nvidia-smi -q -x reports the given JPEG
// and OFA percentages on every GPU. Both elements read N/A until the NVML
// getters existed, so the configured values were silently dropped. See issue
// #637.
func NvidiaSMIJpgOfaUtilization(ctx context.Context, k *kube.Client, pod kube.PodRef, wantJPEG, wantOFA int) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x reports jpeg_util %d %% / ofa_util %d %%", wantJPEG, wantOFA))
	// -x rather than `-q -d UTILIZATION`: the XML is nvidia-smi's
	// machine-readable contract, so the assertion keys off DTD element names
	// instead of the human table's column layout. -d cannot be combined with -x.
	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"nvidia-smi -q -x exited with error: %s", res.Combined())
	out := res.Stdout
	problems := DiffJpgOfaUtilizationXML(out, wantJPEG, wantOFA)
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"JPEG/OFA utilization wrong:\n%s", strings.Join(problems, "\n"))
}

// NvidiaSMITemperatureThresholds asserts nvidia-smi -q -x uses the
// architecture-correct threshold presentation for the profile: absolute
// elements on pre-Ada, *_tlimit_threshold elements on Ada and later. See issue
// #635. -x rather than `-q -d TEMPERATURE` keys the check off DTD element names
// instead of the human table's row labels; -d cannot be combined with -x.
func NvidiaSMITemperatureThresholds(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x temperature thresholds on %s (arch=%s, tlimit=%v)",
		p.Name, p.Architecture(), p.ReportsTLimitTemp()))
	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"nvidia-smi -q -x exited with error: %s", res.Combined())

	// The document is ~90 KB, so only the decoded problems are attached.
	problems := DiffTemperatureXML(res.Stdout, p.ReportsTLimitTemp(),
		p.ShutdownThresholdC(), p.SlowdownThresholdC(), p.MaxOperatingC())
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"temperature threshold presentation wrong for profile %s:\n%s",
		p.Name, strings.Join(problems, "\n"))
}

// NvidiaSMIEncoderFBCAccounting asserts nvidia-smi -q -x reports the configured
// Encoder Stats / FBC Stats numbers and a numeric Accounting Mode Buffer Size
// (issue #636 — these used to be silently stubbed as N/A).
func NvidiaSMIEncoderFBCAccounting(ctx context.Context, k *kube.Client, pod kube.PodRef, encoder, fbc EncoderFBCStats, accountingBufferSize int) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x encoder/fbc/accounting (sessions=%d fps=%d latency=%d buffer=%d)",
		encoder.SessionCount, encoder.AverageFPS, encoder.AverageLatencyUS, accountingBufferSize))
	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"nvidia-smi -q -x exited with error: %s", res.Combined())

	problems := DiffEncoderFBCXML(res.Stdout, encoder, fbc, accountingBufferSize)
	gomega.Expect(problems).To(gomega.BeEmpty(), "encoder/FBC/accounting readings wrong:\n%s",
		strings.Join(problems, "\n"))
}

// GPUSnapshotFromPod execs `nvidia-smi -q -x` in pod and decodes it. It returns
// an error rather than asserting, so pollers can retry; the combined output is
// folded into the error because a failed exec has no document to report.
// ExecQuiet keeps the ~90 KB document out of the Ginkgo log on every poll.
func GPUSnapshotFromPod(ctx context.Context, k *kube.Client, pod kube.PodRef) (GPUSnapshot, error) {
	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	if err != nil {
		return GPUSnapshot{}, fmt.Errorf("nvidia-smi -q -x: %w: %s", err, res.Combined())
	}
	return ParseGPUSnapshot(res.Stdout)
}
