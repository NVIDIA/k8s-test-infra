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

// PCIDevicesDir is the fake /sys/bus/pci/devices tree the pcibus simulator
// materializes from the profile's pcie_topology block (consumed by the NVIDIA
// DRA driver's dra.k8s.io/pcieRoot resolution and device-plugin NUMA hints).
const PCIDevicesDir = "/var/lib/nvml-mock/sys/bus/pci/devices"

// PCISysfs ports demo.sh step 9. From inside a pod it asserts:
//   - exactly gpuCount device symlinks under /sys/bus/pci/devices,
//   - the first symlink resolves to a RELATIVE ../../../devices/pci.../<bdf>
//     target (the contract deviceattribute readlink()s for the PCIe root),
//   - that device's numa_node is an integer,
//   - that device carries a non-zero PCI subsystem identity,
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

	ginkgo.By("device carries a non-zero PCI subsystem identity")
	// An identity word dropped between the profile and the renderer surfaces as
	// 0x0000 rather than an error, so lspci prints a board with no subsystem
	// name. Every shipped profile sets device_defaults.pci.subsystem_id, so any
	// zero here means the value was lost in transit.
	sub, err := k.ExecSh(ctx, pod, "cat "+PCIDevicesDir+"/"+dev+"/subsystem_device")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading subsystem_device for %s", dev)
	subDev := strings.TrimSpace(sub.Stdout)
	gomega.Expect(subDev).To(gomega.MatchRegexp(`^0x[0-9a-f]{4}$`),
		"subsystem_device for %s is malformed: %q", dev, sub.Stdout)
	gomega.Expect(subDev).NotTo(gomega.Equal("0x0000"),
		"subsystem_device for %s is 0x0000; the profile's subsystem_id never reached the renderer", dev)

	ginkgo.By(fmt.Sprintf("devices span %d distinct PCI root complexes", expectedRoots))
	// readlink target shape: "../../../devices/pciDDDD:BB/<bdf>" -> field 5 is
	// the root complex when split on "/".
	roots, err := k.ExecSh(ctx, pod,
		"for d in "+PCIDevicesDir+"/*; do readlink \"$d\"; done | awk -F/ '{print $5}' | sort -u | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(atoiTrim(roots.Stdout)).To(gomega.Equal(expectedRoots),
		"distinct PCI root complexes\n%s", roots.Combined())
}

// KernelPCIDevicesDir is where the kernel keeps the flat PCI lookup directory,
// and so where a consumer that reads sysfs directly looks. Unlike PCIDevicesDir
// this path owes nothing to the mock's layout: it is what has to be true inside
// a container for a Go consumer to see the mock GPUs at all.
const KernelPCIDevicesDir = "/sys/bus/pci/devices"

// PCISysfsAtKernelPaths asserts, from inside a container the mock serves, that
// the rendered tree is readable at the kernel paths.
//
// The overlay path the standalone scenario checks proves the renderer ran; it
// says nothing about whether anything reaches a consumer. libpcisysfs.so covers
// libc consumers such as lspci, but Go's os package issues openat directly, so
// the shim never sees the open and the process reads the node's real /sys —
// which is how GPU Feature Discovery came to label a mock node
// nvidia.com/gpu.mode=unknown (#673).
func PCISysfsAtKernelPaths(ctx context.Context, k *kube.Client, pod kube.PodRef, gpuCount int) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("%d mock GPUs visible at %s", gpuCount, KernelPCIDevicesDir))
	// Exact, not "at least": the mount replaces the directory outright, so a
	// higher count means the host's real devices are showing through or a
	// previous profile's render was never pruned.
	res, err := k.ExecSh(ctx, pod, "ls "+KernelPCIDevicesDir+" 2>/dev/null | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "listing %s: %s", KernelPCIDevicesDir, res.Combined())
	gomega.Expect(atoiTrim(res.Stdout)).To(gomega.Equal(gpuCount),
		"mock GPUs served at the kernel path\n%s", res.Combined())

	first, err := k.ExecSh(ctx, pod, "ls "+KernelPCIDevicesDir+" | sort | head -1")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	dev := strings.TrimSpace(first.Stdout)
	gomega.Expect(dev).NotTo(gomega.BeEmpty(), "no PCI devices under %s", KernelPCIDevicesDir)

	ginkgo.By("vendor resolves through the symlink into the served /sys/devices")
	// This is what separates delivery from coincidence, and why the two mounts
	// are one feature. The entries are relative symlinks into
	// ../../../devices/pciDDDD:BB, so this read succeeds only when that half is
	// served too — with the lookup directory alone the listing above still
	// passes and every attribute read returns ENOENT.
	vendor, err := k.ExecSh(ctx, pod, "cat "+KernelPCIDevicesDir+"/"+dev+"/vendor")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading vendor for %s: %s", dev, vendor.Combined())
	gomega.Expect(strings.TrimSpace(vendor.Stdout)).To(gomega.Equal("0x10de"),
		"vendor for %s did not resolve to the mock tree", dev)

	ginkgo.By("class is the 3D-controller value gpu.mode is derived from")
	// Naming the value here is what makes the gpu.mode assertion elsewhere
	// attributable: the label reads "compute" because GFD read this class out
	// of the served tree, not because it defaulted to it.
	class, err := k.ExecSh(ctx, pod, "cat "+KernelPCIDevicesDir+"/"+dev+"/class")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading class for %s", dev)
	gomega.Expect(strings.TrimSpace(class.Stdout)).To(gomega.Equal("0x030200"),
		"class for %s", dev)
}

func atoiTrim(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
