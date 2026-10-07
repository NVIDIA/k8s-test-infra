//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// NodeDMIDir is the node's DMI id directory as the node agent's container
// mounts it: the same files an NFD worker reads through its own /sys hostPath.
const NodeDMIDir = "/host/sys/devices/virtual/dmi/id"

// DMIAttributes are the world-readable attributes NFD v0.19's system source
// reads. It logs "failed to get DMI entry" for each one it cannot open and
// publishes the rest as the system.dmiid feature.
var DMIAttributes = []string{
	"bios_date", "bios_vendor", "bios_version",
	"board_asset_tag", "board_name", "board_vendor", "board_version",
	"chassis_asset_tag", "chassis_type", "chassis_vendor", "chassis_version",
	"product_family", "product_name", "product_sku", "product_version",
	"sys_vendor",
}

// nfdDMIFeature is the attribute feature NFD publishes DMIAttributes under.
const nfdDMIFeature = "system.dmiid"

// dmiSysVendor is the vendor the node agent simulates.
const dmiSysVendor = "NVIDIA"

// NodeDMI is the identity a node shows under NodeDMIDir.
type NodeDMI struct {
	// Simulated reports that the node agent serves the identity because the
	// kernel exposes none.
	Simulated bool
	// Attributes holds each of DMIAttributes the node shows, trimmed as NFD
	// trims it.
	Attributes map[string]string
}

// ReadNodeDMI reads the node's DMI identity from inside the node agent's
// container, failing the spec if the node shows none at all.
func ReadNodeDMI(ctx context.Context, k *kube.Client, agent kube.PodRef) NodeDMI {
	ginkgo.GinkgoHelper()

	// The kernel's attributes live on sysfs; the agent's on the node's own disk,
	// so the filesystem type tells the two apart without trusting any value.
	fs, err := k.ExecSh(ctx, agent, "stat -f -c %T "+NodeDMIDir)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"node shows no DMI at %s: %s", NodeDMIDir, fs.Combined())

	res, err := k.ExecSh(ctx, agent, "cd "+NodeDMIDir+" && for a in "+strings.Join(DMIAttributes, " ")+
		"; do if [ -r \"$a\" ]; then printf '%s=%s\\n' \"$a\" \"$(cat \"$a\")\"; fi; done")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading %s: %s", NodeDMIDir, res.Combined())

	attrs := make(map[string]string, len(DMIAttributes))
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			attrs[name] = strings.TrimSpace(value)
		}
	}

	return NodeDMI{
		Simulated:  strings.TrimSpace(fs.Stdout) != "sysfs",
		Attributes: attrs,
	}
}

// NodeDMIIdentity asserts the node shows the system vendor and product name
// every NFD version reads and, where the agent simulates them, that it shows
// all of DMIAttributes naming the system after the profile's GPU. It returns
// what the node shows, so a later spec can compare a consumer's view to it.
func NodeDMIIdentity(ctx context.Context, k *kube.Client, agent kube.PodRef, product string) NodeDMI {
	ginkgo.GinkgoHelper()

	ginkgo.By("node shows a DMI system vendor and product name")
	dmi := ReadNodeDMI(ctx, k, agent)
	gomega.Expect(dmi.Attributes).To(gomega.HaveKey("sys_vendor"), "node DMI: %v", dmi.Attributes)
	gomega.Expect(dmi.Attributes).To(gomega.HaveKey("product_name"), "node DMI: %v", dmi.Attributes)

	if !dmi.Simulated {
		ginkgo.AddReportEntry("DMI", "the kernel exposes DMI on this node, so the simulated identity is not exercised")
		return dmi
	}

	ginkgo.By("simulated identity names the profile's GPU")
	gomega.Expect(dmi.Attributes).To(gomega.HaveLen(len(DMIAttributes)),
		"simulated DMI must serve every attribute NFD reads: %v", dmi.Attributes)
	gomega.Expect(dmi.Attributes).To(gomega.HaveKeyWithValue("sys_vendor", dmiSysVendor))
	gomega.Expect(dmi.Attributes).To(gomega.HaveKeyWithValue("product_name", product))

	ginkgo.By("simulated identity serves no product_uuid")
	// kind's createContainer hook bind-mounts its own product_uuid into every
	// container once the node shows one, and a container's sysfs has no target
	// for it: serving one would stop every new container on the node.
	res, err := k.ExecSh(ctx, agent, "test ! -e "+NodeDMIDir+"/product_uuid")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "simulated DMI serves product_uuid: %s", res.Combined())

	return dmi
}

// WaitNFDRecordsNodeDMI polls until the NFD worker in ns publishes, for node,
// exactly the DMI attributes the node shows. A missing element is an attribute
// NFD failed to read, which is what it logs as "failed to get DMI entry".
func WaitNFDRecordsNodeDMI(
	ctx context.Context, k *kube.Client, ns, node string, want NodeDMI, timeout, poll time.Duration,
) {
	ginkgo.GinkgoHelper()
	ginkgo.By("waiting for NFD to publish " + nfdDMIFeature + " for " + node)

	gomega.Eventually(func() (map[string]string, error) {
		return k.NodeFeatureAttribute(ctx, ns, node, nfdDMIFeature)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.Equal(want.Attributes), "NFD's %s for node %s", nfdDMIFeature, node)
}
