//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// IBClassDir is the sys/class root of the rendered IB tree. NICSysfs reads it
// directly, as a Go NIC monitor does: Go issues openat itself, so no LD_PRELOAD
// redirect stands between such a consumer and these files.
const IBClassDir = "/var/lib/nvml-mock/ib/sys/class"

// NICSysfs asserts the files a sysfs-based NIC health monitor reads to
// discover, place and watch each HCA, against the profile's PCI layout.
func NICSysfs(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By("every HCA carries the PCI identity, locality and counters a NIC monitor reads")
	globs := make([]string, len(hcaFiles))
	for i, f := range hcaFiles {
		globs[i] = "infiniband/*/" + f.path
	}
	// grep exits non-zero when a file is missing; the checks below name which.
	files, _ := k.ExecSh(ctx, pod, "cd "+IBClassDir+" && grep -H '' "+strings.Join(globs, " "))
	gomega.Expect(nicSysfsProblems(parseHCAFiles(files.Stdout),
		p.ExpectedHCAs(), p.GPUNUMANodes(), p.PCIAddresses())).To(gomega.BeEmpty(), files.Combined())

	ginkgo.By("sys/class/net lists one netdev per RoCE HCA")
	// Exact, and the directory must exist even when empty: a monitor lists it,
	// and a netdev on an InfiniBand profile would get its HCA treated as RoCE.
	net, err := k.ExecSh(ctx, pod, "ls "+IBClassDir+"/net")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "listing %s/net: %s", IBClassDir, net.Combined())
	gomega.Expect(strings.Fields(net.Stdout)).To(gomega.HaveLen(p.ExpectedNetdevs()), net.Stdout)
}
