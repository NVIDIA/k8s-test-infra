//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// ProcFSDeviceFileParams asserts that the params file staged on the node is one
// nvidia-modprobe can read, and that it reports the device-node ownership and
// permissions the deployed profile asked for.
//
// The check runs the consumer's own parse rather than grepping, because the
// defect this guards against was invisible to grep: the file listed all four
// keys, under names nvidia-modprobe does not match, below a line that ended its
// scan. What it does NOT prove is delivery — the tree stays at the overlay root
// for the reason given on ParamsPath — so this is the staging-path check, the
// counterpart to PCISysfs rather than to PCISysfsAtKernelPaths.
func ProcFSDeviceFileParams(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By("params staged at " + ParamsPath)
	res, err := k.Exec(ctx, pod, "cat", ParamsPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reading %s:\n%s", ParamsPath, res.Combined())

	want := p.DeviceFileParams()
	ginkgo.By(fmt.Sprintf("nvidia-modprobe reaches uid %d, gid %d, mode %#o, modify %t",
		want.UID, want.GID, want.Mode, want.ModifyDeviceFiles))
	gomega.Expect(ParamsProblems(res.Stdout, want)).To(gomega.BeEmpty(),
		"the staged params file does not work for nvidia-modprobe:\n%s", res.Stdout)
}
