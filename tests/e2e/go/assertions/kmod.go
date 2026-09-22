//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"strconv"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

const moduleRefcntPath = "/sys/module/nvidia/refcnt"

const driverProcVersionPath = "/var/lib/nvml-mock/driver/proc/driver/nvidia/version"

// nodeView makes a command read the node's filesystem, not the served tree. A
// `MOCK_PCI_ROOT= cmd` prefix is not enough: it reaches libmockfs only when the
// shell execs cmd, so it is silently inert for a builtin such as `test`.
const nodeView = "env -u LD_PRELOAD -u MOCK_PCI_ROOT "

// lsmodRow is one module's columns: Size, Used-by count, Used-by names.
type lsmodRow struct {
	size    string
	refcnt  string
	holders []string
}

func parseLsmod(out string) map[string]lsmodRow {
	rows := make(map[string]lsmodRow)

	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "Module" {
			continue
		}

		row := lsmodRow{size: fields[1]}
		if len(fields) > 2 {
			row.refcnt = fields[2]
		}
		if len(fields) > 3 {
			row.holders = strings.Split(fields[3], ",")
		}

		rows[fields[0]] = row
	}

	return rows
}

// simulatedHolders, simulatedFabricHolders and simulatedFabricOnly are the
// modules the agent adds beside the node's own. mlx5_core is the one that needs
// a fabric without also holding nvidia.
var (
	simulatedHolders       = []string{"gdrdrv", "nvidia_fs", "nvidia_modeset", "nvidia_uvm"}
	simulatedFabricHolders = []string{"nvidia_peermem"}
	simulatedFabricOnly    = []string{"mlx5_core"}
)

func expectedHolders(ibEnabled bool) []string {
	holders := append([]string{}, simulatedHolders...)
	if ibEnabled {
		holders = append(holders, simulatedFabricHolders...)
	}

	return holders
}

func expectLsmodListsSimulatedModules(rows map[string]lsmodRow, combined string, ibEnabled bool) {
	ginkgo.GinkgoHelper()

	want := expectedHolders(ibEnabled)
	if ibEnabled {
		want = append(want, simulatedFabricOnly...)
	}

	for _, mod := range want {
		gomega.Expect(rows).To(gomega.HaveKey(mod),
			"%s needs its own lsmod line\n%s", mod, combined)
	}

	nvidia, ok := rows["nvidia"]
	gomega.Expect(ok).To(gomega.BeTrue(), "nvidia needs an lsmod line\n%s", combined)
	gomega.Expect(nvidia.holders).To(gomega.ConsistOf(expectedHolders(ibEnabled)),
		"nvidia's Used-by column must name every module that holds it\n%s", combined)

	// The count and the names come from different files: refcnt from
	// /sys/module/nvidia/refcnt, Used-by from holders/. They must agree.
	gomega.Expect(nvidia.refcnt).To(gomega.Equal(strconv.Itoa(len(nvidia.holders))),
		"nvidia's refcount must match its holder count\n%s", combined)

	gomega.Expect(rows["nvidia_uvm"].refcnt).To(gomega.Equal("0"),
		"nvidia_uvm must report no holders\n%s", combined)
}

// KernelModules checks the simulated module surface.
func KernelModules(ctx context.Context, k *kube.Client, pod kube.PodRef, ibEnabled bool) {
	ginkgo.GinkgoHelper()

	ginkgo.By("checking whether the node loads a real nvidia module")
	res, err := k.ExecSh(ctx, pod,
		"if "+nodeView+"test -d /sys/module/nvidia; then echo host; else echo none; fi")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "listing node modules\n%s", res.Combined())
	gomega.Expect(strings.TrimSpace(res.Stdout)).To(gomega.BeElementOf("host", "none"),
		"the host-driver probe must answer\n%s", res.Combined())
	hostDriver := strings.TrimSpace(res.Stdout) == "host"
	if hostDriver {
		ginkgo.AddReportEntry("kmod", "the node loads a real nvidia module, "+
			"so the checks that pin the simulated values do not run here")
	}

	ginkgo.By("lsmod reports the simulated NVIDIA modules")
	res, err = k.ExecSh(ctx, pod, "lsmod")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "lsmod failed\n%s", res.Combined())
	mirrored := parseLsmod(res.Stdout)
	if !hostDriver {
		expectLsmodListsSimulatedModules(mirrored, res.Combined(), ibEnabled)
	}

	// lsmod takes Size from /sys/module/<name>/coresize, not from the
	// /proc/modules line, so this compares the mirror, not the copied host text.
	ginkgo.By("a host module keeps its own size in the mirror")
	served := res.Stdout
	res, err = k.ExecSh(ctx, pod, nodeView+"lsmod")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "node lsmod failed\n%s", res.Combined())

	shared := 0
	for name, node := range parseLsmod(res.Stdout) {
		got, ok := mirrored[name]
		if !ok {
			continue
		}
		shared++
		gomega.Expect(got.size).To(gomega.Equal(node.size),
			"the mirror altered the size of host module %s. Only size is compared: a "+
				"holders/ directory cannot reproduce the dependency order the kernel "+
				"prints in Used by.\nnode:\n%s\nserved:\n%s",
			name, res.Stdout, served)
	}

	gomega.Expect(shared).To(gomega.BeNumerically(">", 0),
		"no host module reached the mirror, so the served tree hides the node's modules"+
			"\nnode:\n%s\nserved:\n%s", res.Stdout, served)

	ginkgo.By("a mirrored parameter reads as it does on the node")
	res, err = k.ExecSh(ctx, pod,
		`p=$(`+nodeView+`sh -c 'ls /sys/module/*/parameters/* 2>/dev/null | head -1'); `+
			`[ -n "$p" ] || { echo none; exit 0; }; `+
			`[ -r "$p" ] || { echo absent; exit 0; }; `+
			`[ "$(`+nodeView+`cat "$p")" = "$(cat "$p")" ] && echo same || echo differ`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading a mirrored parameter\n%s", res.Combined())
	gomega.Expect(strings.TrimSpace(res.Stdout)).NotTo(gomega.Equal("differ"),
		"the mirror altered a parameter value\n%s", res.Combined())

	ginkgo.By("the driver module reports a refcount the validator can read")
	res, err = k.ExecSh(ctx, pod, "cat "+moduleRefcntPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading %s\n%s", moduleRefcntPath, res.Combined())
	gomega.Expect(strings.TrimSpace(res.Stdout)).To(gomega.MatchRegexp(`^\d+$`),
		"the validator parses this as an integer\n%s", res.Combined())
	if !hostDriver {
		gomega.Expect(strings.TrimSpace(res.Stdout)).
			To(gomega.Equal(strconv.Itoa(len(expectedHolders(ibEnabled)))),
				"the served refcount must match the simulated holder count\n%s", res.Combined())
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
