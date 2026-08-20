//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// PCIDevicesDir is the fake /sys/bus/pci/devices tree render-pci-sysfs
// materializes from the profile's pcie_topology block (consumed by the NVIDIA
// DRA driver's dra.k8s.io/pcieRoot resolution and device-plugin NUMA hints).
const PCIDevicesDir = "/var/lib/nvml-mock/sys/bus/pci/devices"

// PCISysfs ports demo.sh step 9. From inside a pod it asserts:
//   - exactly gpuCount device symlinks under /sys/bus/pci/devices,
//   - the first symlink resolves to a RELATIVE ../../../devices/pci.../<bdf>
//     target (the contract deviceattribute readlink()s for the PCIe root),
//   - that device's numa_node is an integer,
//   - the devices span exactly expectedRoots distinct PCIe root complexes.
func PCISysfs(ctx context.Context, k *kube.Client, pod kube.PodRef, gpuCount, expectedRoots int) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("%d PCI device symlinks present", gpuCount))
	res, err := k.ExecSh(ctx, pod, "ls "+PCIDevicesDir+" 2>/dev/null | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "listing %s: %s", PCIDevicesDir, res.Combined())
	gomega.Expect(atoiTrim(res.Stdout)).To(gomega.Equal(gpuCount),
		"rendered PCI device count\n%s", res.Combined())

	ginkgo.By("first device symlink resolves to a relative root-complex path")
	first, err := k.ExecSh(ctx, pod, "ls "+PCIDevicesDir+" | sort | head -1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	dev := strings.TrimSpace(first.Stdout)
	gomega.Expect(dev).NotTo(gomega.BeEmpty(), "no PCI devices under %s", PCIDevicesDir)

	target, err := k.ExecSh(ctx, pod, "readlink "+PCIDevicesDir+"/"+dev)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "readlink %s", dev)
	tgt := strings.TrimSpace(target.Stdout)
	gomega.Expect(tgt).To(gomega.MatchRegexp(`^\.\./\.\./\.\./devices/pci`),
		"expected relative ../../../devices/pciDDDD:BB/<bdf> target, got %q", tgt)

	ginkgo.By("device numa_node is an integer")
	numa, err := k.ExecSh(ctx, pod, "cat "+PCIDevicesDir+"/"+dev+"/numa_node")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading numa_node for %s", dev)
	gomega.Expect(strings.TrimSpace(numa.Stdout)).To(gomega.MatchRegexp(`^-?[0-9]+$`),
		"numa_node for %s is not a number: %q", dev, numa.Stdout)

	ginkgo.By(fmt.Sprintf("devices span %d distinct PCI root complexes", expectedRoots))
	// readlink target shape: "../../../devices/pciDDDD:BB/<bdf>" -> field 5 is
	// the root complex when split on "/".
	roots, err := k.ExecSh(ctx, pod,
		"for d in "+PCIDevicesDir+"/*; do readlink \"$d\"; done | awk -F/ '{print $5}' | sort -u | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(atoiTrim(roots.Stdout)).To(gomega.Equal(expectedRoots),
		"distinct PCI root complexes\n%s", roots.Combined())
}

// KernelPCIDevicesDir is where the kernel exposes PCI devices and where the
// mock tree must appear for consumers that cannot be redirected: Go binaries
// read sysfs with direct syscalls, so the libpcimocksys.so shim never sees
// their opens and MOCK_PCI_ROOT has no effect on them.
const KernelPCIDevicesDir = "/sys/bus/pci/devices"

// PCISysfsAtKernelPath asserts, from inside a container the mock serves (a GPU
// Operator operand, for instance), that the rendered tree arrived at the real
// kernel paths rather than only in the overlay:
//   - /sys/bus/pci/devices holds exactly the mock GPUs, so the host's own PCI
//     devices are masked and consumers enumerate the profile,
//   - reading a device's `vendor` yields NVIDIA, which only works when
//     /sys/devices is mounted too (the entries are relative symlinks into it).
func PCISysfsAtKernelPath(ctx context.Context, k *kube.Client, pod kube.PodRef, gpuCount int) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("%d mock PCI devices visible at %s", gpuCount, KernelPCIDevicesDir))
	res, err := k.ExecSh(ctx, pod, "ls "+KernelPCIDevicesDir+" 2>/dev/null | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "listing %s: %s", KernelPCIDevicesDir, res.Combined())
	gomega.Expect(atoiTrim(res.Stdout)).To(gomega.Equal(gpuCount),
		"device count at %s — the mock tree is not mounted there\n%s", KernelPCIDevicesDir, res.Combined())

	ginkgo.By("a device's vendor reads through the symlink into /sys/devices")
	first, err := k.ExecSh(ctx, pod, "ls "+KernelPCIDevicesDir+" | sort | head -1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	dev := strings.TrimSpace(first.Stdout)
	gomega.Expect(dev).NotTo(gomega.BeEmpty(), "no PCI devices at %s", KernelPCIDevicesDir)

	vendor, err := k.ExecSh(ctx, pod, "cat "+KernelPCIDevicesDir+"/"+dev+"/vendor")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"reading vendor for %s — a dangling symlink means /sys/devices is missing", dev)
	gomega.Expect(strings.TrimSpace(vendor.Stdout)).To(gomega.Equal("0x10de"),
		"vendor for %s\n%s", dev, vendor.Combined())
}

func atoiTrim(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
