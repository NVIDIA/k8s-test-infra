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

// nvTokens applies a column-windowed parse to `nvidia-smi topo -m`: in a
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

// NVLink asserts the NVLink topology the mock renders. fabricmanager readiness
// must already be gated by the caller (FabricManagerGate) BEFORE this
// assertion, matching the real HGX/GB200 ordering.
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
		nvlinkBwMode(ctx, k, pod, p)
		return
	}

	// Negative control: no NV# links may leak (b200 standalone, t4, l40s).
	gomega.Expect(distinct).To(gomega.BeEmpty(),
		"non-NVLink profile %q leaked NV# links: %v", p.Name, distinct)

	// The bandwidth-mode gate is architecture-based, not fabric-based, so it
	// applies even to profiles with no declared links — b200 standalone is
	// Blackwell and must still report a mode.
	nvlinkBwMode(ctx, k, pod, p)
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

// hopperPlusProfiles are the profiles whose architecture clears the engine's
// Hopper gate. That is the gate that matters for `nvidia-smi nvlink -gBwMode`
// and `-sBwMode`, because those route to nvmlSystemGet/SetNvlinkBwMode rather
// than to the per-device trio — so h100 belongs here, and a pre-Hopper profile
// is the only honest negative control. The per-device bandwidth-mode calls are
// not reachable through this nvidia-smi at all.
var hopperPlusProfiles = map[string]struct{}{
	"h100": {}, "b200": {}, "gb200": {}, "gb300": {},
}

// bwModeNameRE matches the bundled nvidia-smi's own mode-name table. Matching
// the name set rather than pinning one value keeps the assertion from breaking
// when the default mode changes, while still failing loudly if the mock
// answers NOT_SUPPORTED.
var bwModeNameRE = regexp.MustCompile(`bandwidth mode: (FULL|OFF|MIN|HALF|3QUARTER)`)

// bwModeNames is the whole table. Setting each one pins the name-to-index
// mapping end to end, since nvidia-smi is what resolves a name to the index it
// sends down.
var bwModeNames = []string{"FULL", "OFF", "MIN", "HALF", "3QUARTER"}

// nvlinkBwMode asserts the NVLink bandwidth-mode, NVLE and low-power surfaces
// of `nvidia-smi nvlink`. Below the gate it asserts the negative direction,
// which is what keeps the architecture gate from silently degrading into a
// blanket success.
func nvlinkBwMode(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	// nvidia-smi exits non-zero after printing "not supported", which is the
	// expected outcome below the gate, so the output is the signal, not err.
	ginkgo.By("nvidia-smi nvlink -gBwMode")
	res, _ := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-gBwMode")
	out := res.Combined()

	if _, ok := hopperPlusProfiles[p.Name]; !ok {
		gomega.Expect(out).To(gomega.MatchRegexp(`(?i)not supported`),
			"pre-Hopper profile %q must not claim bandwidth-mode support:\n%s", p.Name, out)
		return
	}

	gomega.Expect(out).To(gomega.MatchRegexp(bwModeNameRE.String()),
		"profile %q did not report a named bandwidth mode:\n%s", p.Name, out)

	for _, mode := range bwModeNames {
		ginkgo.By("nvidia-smi nvlink -sBwMode " + mode)
		setRes, _ := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-sBwMode", mode)
		gomega.Expect(setRes.Combined()).To(gomega.MatchRegexp(`(?i)Successfully set nvlink bandwidth mode`),
			"profile %q rejected supported bandwidth mode %q:\n%s", p.Name, mode, setRes.Combined())
	}

	// The round trip across two nvidia-smi invocations is the point of
	// persisting the mode: each invocation dlopens its own copy of the mock, so
	// a mode held in process memory would be gone by this next call.
	ginkgo.By("nvidia-smi nvlink -sBwMode survives into a separate process")
	_, _ = k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-sBwMode", "HALF")
	backRes, _ := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-gBwMode")
	gomega.Expect(backRes.Combined()).To(gomega.MatchRegexp(`(?i)bandwidth mode:\s*HALF`),
		"profile %q lost a bandwidth mode set by a previous process:\n%s", p.Name, backRes.Combined())

	// Leave the node on the default, so a later assertion (or a re-run against
	// the same cluster) starts from FULL rather than inheriting HALF.
	_, _ = k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-sBwMode", "FULL")

	// nvlink --info is per-device, so it needs Blackwell AND a real fabric.
	// h100 is Hopper, and b200 is standalone with no links at all, so neither
	// prints an NVLE row — below Blackwell the driver-version registry answers
	// FUNCTION_NOT_FOUND before the architecture gate is even reached.
	ginkgo.By("nvidia-smi nvlink --info reports NVLE")
	infoRes, _ := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "--info")
	info := infoRes.Combined()
	if p.Name == "h100" || p.ExpectedNV() == 0 {
		gomega.Expect(info).NotTo(gomega.ContainSubstring("NVLE:"),
			"profile %q must not report NVLE:\n%s", p.Name, info)
		return
	}
	gomega.Expect(info).To(gomega.ContainSubstring("NVLE:"),
		"profile %q did not report the NVLE row:\n%s", p.Name, info)

	ginkgo.By("nvidia-smi nvlink -sLowPwrThres round trip")
	lowRes, _ := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-sLowPwrThres", "500")
	gomega.Expect(lowRes.Combined()).To(gomega.MatchRegexp(`(?i)Low Power Threshold set to`),
		"profile %q rejected an in-range low-power threshold:\n%s", p.Name, lowRes.Combined())

	// `default` sends NVML_NVLINK_LOW_POWER_THRESHOLD_RESET (0xFFFFFFFF), which
	// must clear the override rather than fail range validation.
	resetRes, _ := k.ExecTruncated(ctx, pod, nvlinkLogOutputLines, "nvidia-smi", "nvlink", "-sLowPwrThres", "default")
	gomega.Expect(resetRes.Combined()).To(gomega.MatchRegexp(`(?i)reset successfully`),
		"profile %q did not reset the low-power threshold:\n%s", p.Name, resetRes.Combined())
}
