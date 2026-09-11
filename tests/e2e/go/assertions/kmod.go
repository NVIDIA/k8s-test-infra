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
)

const moduleRefcntPath = "/sys/module/nvidia/refcnt"

const driverProcVersionPath = "/var/lib/nvml-mock/driver/proc/driver/nvidia/version"

func lsmodSizes(out string) map[string]string {
	sizes := make(map[string]string)

	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "Module" {
			continue
		}
		sizes[fields[0]] = fields[1]
	}

	return sizes
}

// KernelModules checks the simulated module surface.
func KernelModules(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.GinkgoHelper()

	ginkgo.By("checking whether the node loads a real nvidia module")
	res, err := k.ExecSh(ctx, pod,
		`if MOCK_PCI_ROOT= test -d /sys/module/nvidia; then echo host; else echo none; fi`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "listing node modules\n%s", res.Combined())
	gomega.Expect(strings.TrimSpace(res.Stdout)).To(gomega.BeElementOf("host", "none"),
		"the host-driver probe must answer\n%s", res.Combined())
	hostDriver := strings.TrimSpace(res.Stdout) == "host"

	ginkgo.By("lsmod reports the simulated NVIDIA modules")
	res, err = k.ExecSh(ctx, pod, "lsmod")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "lsmod failed\n%s", res.Combined())
	if !hostDriver {
		gomega.Expect(res.Stdout).To(gomega.MatchRegexp(`(?m)^nvidia\s+\d+\s+1\s+nvidia_uvm`),
			"nvidia must be held by nvidia_uvm\n%s", res.Combined())
		gomega.Expect(res.Stdout).To(gomega.MatchRegexp(`(?m)^nvidia_uvm\s+\d+\s+0`),
			"nvidia_uvm must report no holders\n%s", res.Combined())
	}

	ginkgo.By("a host module keeps its own size in the mirror")
	mirrored := lsmodSizes(res.Stdout)
	served := res.Stdout
	res, err = k.ExecSh(ctx, pod, "MOCK_PCI_ROOT= lsmod")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "node lsmod failed\n%s", res.Combined())

	shared := 0
	for name, size := range lsmodSizes(res.Stdout) {
		got, ok := mirrored[name]
		if !ok {
			continue
		}
		shared++
		gomega.Expect(got).To(gomega.Equal(size),
			"the mirror altered the size of host module %s. Only the size is compared: the "+
				"kernel orders the Used-by holders by dependency in /proc/modules, and a "+
				"holders/ directory cannot reproduce that order.\nnode:\n%s\nserved:\n%s",
			name, res.Stdout, served)
	}

	gomega.Expect(shared).To(gomega.BeNumerically(">", 0),
		"no host module reached the mirror, so the served tree hides the node's modules"+
			"\nnode:\n%s\nserved:\n%s", res.Stdout, served)

	ginkgo.By("a mirrored parameter reads as it does on the node")
	res, err = k.ExecSh(ctx, pod,
		`p=$(MOCK_PCI_ROOT= sh -c 'ls /sys/module/*/parameters/* 2>/dev/null | head -1'); `+
			`[ -n "$p" ] || { echo none; exit 0; }; `+
			`[ -r "$p" ] || { echo absent; exit 0; }; `+
			`[ "$(MOCK_PCI_ROOT= cat "$p")" = "$(cat "$p")" ] && echo same || echo differ`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading a mirrored parameter\n%s", res.Combined())
	gomega.Expect(strings.TrimSpace(res.Stdout)).NotTo(gomega.Equal("differ"),
		"the mirror altered a parameter value\n%s", res.Combined())

	ginkgo.By("the driver module reports a refcount the validator can read")
	res, err = k.ExecSh(ctx, pod, "cat "+moduleRefcntPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading %s\n%s", moduleRefcntPath, res.Combined())
	gomega.Expect(strings.TrimSpace(res.Stdout)).To(gomega.MatchRegexp(`^\d+$`),
		"the validator parses this as an integer\n%s", res.Combined())
	if !hostDriver {
		gomega.Expect(strings.TrimSpace(res.Stdout)).To(gomega.Equal("1"))
	}

	if !hostDriver {
		ginkgo.By("the driver module version agrees with the procfs driver version")
		res, err = k.ExecSh(ctx, pod,
			"cat /sys/module/nvidia/version; sed -n 's/.*Kernel Module *\\([0-9.]*\\).*/\\1/p' "+driverProcVersionPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading versions\n%s", res.Combined())

		versions := strings.Fields(res.Stdout)
		gomega.Expect(versions).To(gomega.HaveLen(2), "expected both versions\n%s", res.Combined())
		gomega.Expect(versions[0]).To(gomega.Equal(versions[1]),
			"the module version and the procfs version describe the same driver")
	}

	ginkgo.By("/proc/modules lists both modules")
	res, err = k.ExecSh(ctx, pod, "cat /proc/modules")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading /proc/modules\n%s", res.Combined())
	gomega.Expect(res.Stdout).To(gomega.MatchRegexp(`(?m)^nvidia_uvm\s`),
		"nvidia_uvm needs its own line\n%s", res.Combined())
	if !hostDriver {
		gomega.Expect(res.Stdout).To(gomega.ContainSubstring("nvidia_uvm,"),
			"the nvidia line must name its holder\n%s", res.Combined())
	}
}
