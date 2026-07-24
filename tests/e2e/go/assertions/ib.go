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

// IBCharDevDir is where setup.sh backs /dev/infiniband with real character
// nodes on the node (MOCK_IB_ROOT/dev/infiniband). Inside the nvml-mock pod
// the node's /var/lib/nvml-mock is bind-mounted at the canonical path, so the
// char nodes are visible here without the /host prefix.
const IBCharDevDir = "/var/lib/nvml-mock/ib/dev/infiniband"

// NFDFeatureFile is the NFD "local" source feature file setup.sh writes when
// infiniband.nfd.publishNicLabel is enabled. NFD turns each name=value line
// into a feature.node.kubernetes.io/<name> label; here pci-15b3.present=true.
const NFDFeatureFile = "/host-nfd-features/nvml-mock-ib.features"

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

// IBCharDevices asserts that setup.sh materialized real character devices under
// /dev/infiniband (not the 0-byte placeholder regular files mock-ib stages):
// uverbsN / umadN / issmN per HCA plus a single rdma_cm. RDMA tooling and the
// operator's plugins open these device nodes by path, so their being real char
// nodes (test -c) is the contract. Skips for IB-disabled profiles.
func IBCharDevices(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()
	expected := p.ExpectedHCAs()
	if expected == 0 {
		ginkgo.Skip("IB disabled for profile " + p.Name)
	}

	// per HCA: uverbsN, umadN, issmN (3) — plus one shared rdma_cm.
	wantChar := expected*3 + 1
	ginkgo.By(fmt.Sprintf("%d real char devices under %s (%d HCAs x 3 + rdma_cm)", wantChar, IBCharDevDir, expected))
	script := fmt.Sprintf(
		`d=%s; n=%d; ok=0; i=0; `+
			`while [ "$i" -lt "$n" ]; do for f in uverbs$i umad$i issm$i; do [ -c "$d/$f" ] && ok=$((ok+1)); done; i=$((i+1)); done; `+
			`[ -c "$d/rdma_cm" ] && ok=$((ok+1)); echo "$ok"`,
		IBCharDevDir, expected)
	res, err := k.ExecSh(ctx, pod, script)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "counting IB char devices: %s", res.Combined())
	gomega.Expect(atoiTrim(res.Stdout)).To(gomega.Equal(wantChar),
		"real char devices under %s\n%s", IBCharDevDir, listCombined(ctx, k, pod, IBCharDevDir))
}

// NFDNicFeatureFile asserts setup.sh wrote the NFD local-source feature file
// advertising the mock Mellanox NIC (infiniband.nfd.publishNicLabel, on by
// default for IB profiles). NFD is not installed in this scenario, so we assert
// the file content directly — NFD would derive
// feature.node.kubernetes.io/pci-15b3.present=true from it. Skips when IB is
// disabled (the chart does not mount the features dir there).
func NFDNicFeatureFile(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()
	if !p.IBEnabled() {
		ginkgo.Skip("IB disabled for profile " + p.Name)
	}

	ginkgo.By("NFD feature file " + NFDFeatureFile + " advertises pci-15b3.present=true")
	res, err := k.ExecSh(ctx, pod, "cat "+NFDFeatureFile+" 2>&1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading %s: %s", NFDFeatureFile, res.Combined())
	gomega.Expect(strings.TrimSpace(res.Stdout)).To(gomega.Equal("pci-15b3.present=true"),
		"NFD feature file content\n%s", res.Combined())
}

// listCombined is a best-effort `ls -l` used only to enrich failure messages;
// it never fails the spec itself.
func listCombined(ctx context.Context, k *kube.Client, pod kube.PodRef, dir string) string {
	res, err := k.ExecSh(ctx, pod, "ls -l "+dir+" 2>&1 || true")
	if err != nil {
		return ""
	}
	return res.Combined()
}
