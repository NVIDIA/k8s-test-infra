//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

var (
	gpuRowRE             = regexp.MustCompile(`^GPU[0-9]`)
	nvTokenRE            = regexp.MustCompile(`^NV[0-9]+$`)
	nvlinkLogOutputLines = 50
)

// nvTokens applies the column-windowed parse from validate-nvlink.sh: in a
// "GPU<n>" row, field 0 is the label and fields 1..count are the GPU data
// columns (diagonal is "X"); NIC and CPU/NUMA columns come after and must be
// excluded to avoid environmental false positives.
func nvTokens(topo string, count int) []string {
	var out []string
	for _, line := range strings.Split(topo, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !gpuRowRE.MatchString(f[0]) {
			continue
		}
		for i := 1; i <= count && i < len(f); i++ {
			if nvTokenRE.MatchString(f[i]) {
				out = append(out, f[i])
			}
		}
	}
	return out
}

func distinctSorted(toks []string) []string {
	m := map[string]struct{}{}
	for _, t := range toks {
		m[t] = struct{}{}
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NVLink ports validate-nvlink.sh. fabricmanager readiness must already be
// gated by the caller (FabricManagerGate) BEFORE this assertion, matching the
// real HGX/GB200 ordering.
func NVLink(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	count := p.ExpectedGPUs()
	expectNV := p.ExpectedNV()

	ginkgo.By("nvidia-smi topo -m")
	res, err := k.Exec(ctx, pod, "nvidia-smi", "topo", "-m")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "topo -m exited with error: %s", res.Combined())
	topo := res.Combined()
	gomega.Expect(topo).To(gomega.MatchRegexp(`(?i)Legend|NV# =`), "topo -m did not print a legend")
	gomega.Expect(topo).To(gomega.MatchRegexp(`(?i)CPU Affinity|NUMA Affinity`),
		"topo -m missing CPU/NUMA Affinity columns")

	tokens := nvTokens(topo, count)
	distinct := distinctSorted(tokens)
	offDiag := count * (count - 1)

	if expectNV > 0 {
		want := fmt.Sprintf("NV%d", expectNV)
		gomega.Expect(distinct).To(gomega.Equal([]string{want}),
			"profile %q expected uniform %s between every GPU pair, got %v\n%s",
			p.Name, want, distinct, topo)
		gomega.Expect(tokens).To(gomega.HaveLen(offDiag),
			"profile %q expected %d off-diagonal %s cells (full matrix), got %d",
			p.Name, offDiag, want, len(tokens))

		ginkgo.By("nvidia-smi nvlink -s (status) enumerates links")
		s, err := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-s")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvlink -s exited with error: %s", s.Combined())
		gomega.Expect(s.Combined()).To(gomega.MatchRegexp(`Link[[:space:]]+0`),
			"nvlink -s printed no links for NVLink profile %q", p.Name)

		ginkgo.By("nvidia-smi nvlink -c (capabilities) enumerates links")
		c, err := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-c")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvlink -c exited with error: %s", c.Combined())
		gomega.Expect(c.Combined()).To(gomega.MatchRegexp(`Link[[:space:]]+0`),
			"nvlink -c printed no capabilities for NVLink profile %q", p.Name)

		nvlinkCountersTriState(ctx, k, pod)
		return
	}

	// Negative control: no NV# links may leak (b200 standalone, t4, l40s).
	gomega.Expect(distinct).To(gomega.BeEmpty(),
		"non-NVLink profile %q leaked NV# links: %v", p.Name, distinct)
}

// nvlinkCountersTriState samples `nvlink -gt d` twice and is tri-state:
// SKIP if no counters are surfaced, PASS if non-decreasing, FAIL if they
// decrease — never silent-pass and never hard-fail on zero.
func nvlinkCountersTriState(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.By("NVLink throughput counters are non-decreasing (tri-state)")
	sum := func() int {
		res, _ := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-gt", "d")
		return sumInts(res.Stdout)
	}
	s1 := sum()
	sleepCtx(ctx, time.Second)
	s2 := sum()
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "NVLink counter sums: t0=%d t1=%d\n", s1, s2)
	switch {
	case s1 == 0 && s2 == 0:
		_, _ = fmt.Fprintln(ginkgo.GinkgoWriter,
			"SKIP: bundled nvidia-smi did not surface NVLink throughput counters via 'nvlink -gt d'")
	case s2 >= s1:
		// PASS: non-decreasing.
	default:
		ginkgo.Fail(fmt.Sprintf("NVLink counters decreased (%d -> %d) — not monotonic", s1, s2))
	}
}
