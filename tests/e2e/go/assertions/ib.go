//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// IBStat ports validate-ibstat.sh: ibstat -l HCA count must equal ExpectedHCAs;
// for IB-enabled profiles every port must be ACTIVE and there must be one CA
// section per HCA. IB-disabled profiles (ExpectedHCAs==0) must report zero HCAs
// (negative control) and then short-circuit.
func IBStat(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()
	expected := p.ExpectedHCAs()

	ginkgo.By("ibstat -l lists the expected HCA count")
	list, _ := k.ExecSh(ctx, pod, "ibstat -l 2>&1 || true") // tolerate, like the bash `|| true`
	actual := countLinesWithPrefix(list.Combined(), "mlx")
	gomega.Expect(actual).To(gomega.Equal(expected),
		"ibstat -l reported %d HCAs, expected %d\n%s", actual, expected, list.Combined())

	if expected == 0 {
		return // IB disabled negative control passes here.
	}

	ginkgo.By("ibstatus shows every port ACTIVE")
	status, err := k.ExecSh(ctx, pod, "ibstatus 2>&1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "ibstatus failed: %s", status.Combined())
	active := countMatches(status.Combined(), `state:[[:space:]]+[0-9]+:[[:space:]]+ACTIVE`)
	gomega.Expect(active).To(gomega.Equal(expected),
		"ibstatus shows %d ACTIVE ports, expected %d", active, expected)

	ginkgo.By("ibstat shows one CA section per HCA")
	full, err := k.ExecSh(ctx, pod, "ibstat 2>&1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "ibstat failed: %s", full.Combined())
	cas := countMatches(full.Combined(), `(?m)^CA '`)
	gomega.Expect(cas).To(gomega.Equal(expected),
		"ibstat reported %d CA sections, expected %d\n%s", cas, expected, full.Combined())
}

// IBVDevinfo ports validate-ibv-devinfo.sh: libibverbs enumeration via
// ibv_devinfo -l / ibv_devices, plus per-port ACTIVE/LinkUp via ibstatus.
// Skips for IB-disabled profiles.
func IBVDevinfo(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()
	expected := p.ExpectedHCAs()
	if expected == 0 {
		ginkgo.Skip("IB disabled for profile " + p.Name)
	}

	ginkgo.By("ibv_devinfo -l enumerates HCAs")
	list, err := k.ExecSh(ctx, pod, "ibv_devinfo -l 2>&1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "ibv_devinfo -l failed: %s", list.Combined())
	actual := countMatches(list.Combined(), `(?m)^[[:space:]]+mlx5_`)
	gomega.Expect(actual).To(gomega.Equal(expected),
		"ibv_devinfo -l reported %d devices, expected %d\n%s", actual, expected, list.Combined())

	ginkgo.By("ibv_devices lists mlx5_0 with a node GUID")
	devs, err := k.ExecSh(ctx, pod, "ibv_devices 2>&1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "ibv_devices failed: %s", devs.Combined())
	gomega.Expect(devs.Combined()).To(gomega.MatchRegexp(`(?m)^[[:space:]]+mlx5_0[[:space:]]+[0-9a-f]{16}`),
		"ibv_devices did not list mlx5_0 with a node GUID")

	ginkgo.By("ibstatus reports ACTIVE / LinkUp ports")
	full, err := k.ExecSh(ctx, pod, "ibstatus 2>&1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "ibstatus failed: %s", full.Combined())
	activePorts := countMatches(full.Combined(), `state:[[:space:]]+4: ACTIVE`)
	gomega.Expect(activePorts).To(gomega.BeNumerically(">=", expected),
		"ibstatus reports %d ACTIVE ports, expected at least %d", activePorts, expected)
	gomega.Expect(full.Combined()).To(gomega.MatchRegexp(`phys state:[[:space:]]+5: LinkUp`),
		"ibstatus output missing 'phys state: 5: LinkUp'")
}
