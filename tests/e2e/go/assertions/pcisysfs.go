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

// NICRootComplex is the synthetic PCI root render.SynthesizeNICs places the
// mock Mellanox (15b3) NICs under (bus 0xe0), kept well clear of the profile
// GPU BDF ranges. Mirrors pkg/system/mockpcisysfs/render.nicRootComplex.
const NICRootComplex = "pci0000:e0"

// PCISysfs ports demo.sh step 9. From inside a pod it asserts:
//   - exactly gpuCount+nicCount device symlinks under /sys/bus/pci/devices
//     (nicCount is the synthesized 15b3 NIC entries this chart renders for
//     IB-enabled profiles; pass 0 when the profile has no InfiniBand),
//   - the first symlink resolves to a RELATIVE ../../../devices/pci.../<bdf>
//     target (the contract deviceattribute readlink()s for the PCIe root),
//   - that device's numa_node is an integer,
//   - the devices span exactly expectedRoots(+1 when NICs are present, since
//     they live on their own NICRootComplex) distinct PCIe root complexes.
func PCISysfs(ctx context.Context, k *kube.Client, pod kube.PodRef, gpuCount, nicCount, expectedRoots int) {
	ginkgo.GinkgoHelper()

	totalDevices := gpuCount + nicCount
	ginkgo.By(fmt.Sprintf("%d PCI device symlinks present (%d GPU + %d NIC)", totalDevices, gpuCount, nicCount))
	res, err := k.ExecSh(ctx, pod, "ls "+PCIDevicesDir+" 2>/dev/null | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "listing %s: %s", PCIDevicesDir, res.Combined())
	gomega.Expect(atoiTrim(res.Stdout)).To(gomega.Equal(totalDevices),
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

	// Synthesized NICs sit on their own NICRootComplex, so they add one more
	// distinct root on top of the profile's GPU roots.
	wantRoots := expectedRoots
	if nicCount > 0 {
		wantRoots++
	}
	ginkgo.By(fmt.Sprintf("devices span %d distinct PCI root complexes", wantRoots))
	// readlink target shape: "../../../devices/pciDDDD:BB/<bdf>" -> field 5 is
	// the root complex when split on "/".
	roots, err := k.ExecSh(ctx, pod,
		"for d in "+PCIDevicesDir+"/*; do readlink \"$d\"; done | awk -F/ '{print $5}' | sort -u | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(atoiTrim(roots.Stdout)).To(gomega.Equal(wantRoots),
		"distinct PCI root complexes\n%s", roots.Combined())
}

// NICSysfs asserts the synthesized mock Mellanox (15b3) NIC PCI devices this
// chart renders for IB-enabled profiles (render.SynthesizeNICs). From inside
// the nvml-mock pod it checks that exactly nicCount device symlinks resolve to
// the NIC root complex and that the first NIC carries the 15b3 vendor /
// subsystem identity and a NIC PCI class — the attribute files NFD's pci.device
// source (and the operator's nvidia-nics-rules NodeFeatureRule) match on.
// Skips when nicCount is 0 (IB-disabled profiles synthesize no NICs).
func NICSysfs(ctx context.Context, k *kube.Client, pod kube.PodRef, nicCount int) {
	ginkgo.GinkgoHelper()
	if nicCount == 0 {
		ginkgo.Skip("InfiniBand disabled; no synthesized 15b3 NICs to assert")
	}

	ginkgo.By(fmt.Sprintf("%d 15b3 NIC device symlinks under %s", nicCount, NICRootComplex))
	// readlink target shape: "../../../devices/pci0000:e0/<bdf>" -> field 5 is
	// the root complex; count only the symlinks that land on the NIC root.
	cnt, err := k.ExecSh(ctx, pod,
		"for d in "+PCIDevicesDir+"/*; do readlink \"$d\"; done 2>/dev/null | awk -F/ '$5==\""+NICRootComplex+"\"' | wc -l")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(atoiTrim(cnt.Stdout)).To(gomega.Equal(nicCount),
		"synthesized 15b3 NIC device count\n%s", cnt.Combined())

	// render.SynthesizeNICs assigns NIC BDFs deterministically as
	// 0000:e0:NN.0, so the first NIC is always 0000:e0:00.0.
	const firstNIC = "0000:e0:00.0"
	ginkgo.By("NIC " + firstNIC + " carries the 15b3 vendor/class/subsystem identity")

	vendor, err := k.ExecSh(ctx, pod, "cat "+PCIDevicesDir+"/"+firstNIC+"/vendor")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading vendor for %s", firstNIC)
	gomega.Expect(strings.TrimSpace(vendor.Stdout)).To(gomega.Equal("0x15b3"),
		"NIC %s vendor\n%s", firstNIC, vendor.Combined())

	class, err := k.ExecSh(ctx, pod, "cat "+PCIDevicesDir+"/"+firstNIC+"/class")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading class for %s", firstNIC)
	// 0x0207xx = InfiniBand controller, 0x0200xx = Ethernet controller — the
	// two link layers render.SynthesizeNICs emits.
	gomega.Expect(strings.TrimSpace(class.Stdout)).To(gomega.MatchRegexp(`^0x020[07]00$`),
		"NIC %s class should be an IB (0x020700) or Ethernet (0x020000) controller\n%s", firstNIC, class.Combined())

	subVendor, err := k.ExecSh(ctx, pod, "cat "+PCIDevicesDir+"/"+firstNIC+"/subsystem_vendor")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading subsystem_vendor for %s", firstNIC)
	gomega.Expect(strings.TrimSpace(subVendor.Stdout)).To(gomega.Equal("0x15b3"),
		"NIC %s subsystem_vendor\n%s", firstNIC, subVendor.Combined())
}

func atoiTrim(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
