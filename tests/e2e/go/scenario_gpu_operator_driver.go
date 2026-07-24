//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

// managedDriverProfile is the single profile the managed-driver lane runs. It
// is fixed (not driven by E2E_PROFILES) because the values overlays hardcode
// MOCK_GPU_PROFILE/MOCK_GPU_COUNT=a100/2 on BOTH the nvml-mock CDI side and the
// driver.env side, and this lane tests the operator's driver LIFECYCLE, not
// per-profile NVML behavior (that is the other jobs' matrices).
const (
	managedDriverProfile = "a100"
	managedDriverGPUs    = 2
)

// osSuffixedRef appends the operator's "-<osID><osVersionID>" tag suffix to an
// image ref. The GPU Operator resolves the driver image as
// <repository>/<image>:<version>-<osID><osVersionID> (from the node's NFD
// system-os_release labels) unless the reference is digest-pinned, so the
// scenario must load THIS ref into Kind for the operator to find it. Splitting
// on the tag (reusing splitImage) keeps a registry-host colon in the repo
// portion intact.
func osSuffixedRef(ref, osTag string) string {
	repo, tag := splitImage(ref)
	return fmt.Sprintf("%s:%s-%s", repo, tag, osTag)
}

// nodeOSTag reads a Kind node's /etc/os-release and returns "<ID><VERSION_ID>"
// (e.g. "debian12"), the same value NFD derives the operator's driver image
// suffix from.
func nodeOSTag(ctx context.Context, node string) (string, error) {
	res, err := runner.Run(ctx, "docker", "exec", node, "sh", "-c", ". /etc/os-release && printf '%s%s' \"${ID}\" \"${VERSION_ID}\"")
	if err != nil {
		return "", fmt.Errorf("read /etc/os-release on %s: %w", node, err)
	}
	tag := strings.TrimSpace(res.Stdout)
	if tag == "" {
		return "", fmt.Errorf("empty os tag from %s", node)
	}
	return tag, nil
}

// loadMockDriverImage tags the built mock-driver image with the node's OS
// suffix and kind-loads it, so the operator-rendered driver DaemonSet's
// image reference resolves on the node.
func loadMockDriverImage(ctx context.Context, h *harness.Harness, node string) {
	GinkgoHelper()
	osTag, err := nodeOSTag(ctx, node)
	Expect(err).NotTo(HaveOccurred(), "derive node OS tag")
	suffixed := osSuffixedRef(config.MockDriverImage(), osTag)
	By(fmt.Sprintf("tagging mock-driver image %s -> %s", config.MockDriverImage(), suffixed))
	_, err = runner.Run(ctx, "docker", "tag", config.MockDriverImage(), suffixed)
	Expect(err).NotTo(HaveOccurred(), "docker tag mock-driver os-suffixed")
	Expect(h.Cluster.LoadImage(ctx, suffixed)).To(Succeed(), "kind load mock-driver image")
}

// installNvmlMockManagedDriver installs nvml-mock with the driver symlink
// DISABLED, handing /run/nvidia/driver ownership to the operator-managed
// driver DaemonSet. Uses the profile's own count on both the CDI side and
// (via the values overlay) the driver.env side.
func installNvmlMockManagedDriver(ctx context.Context, h *harness.Harness, prof string, count int) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	By("helm upgrade --install nvml-mock (managed-driver mode, driverSymlink disabled)")
	Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set: map[string]string{
			"gpu.profile":                       prof,
			"gpu.count":                         fmt.Sprintf("%d", count),
			"image.repository":                  repo,
			"image.tag":                         tag,
			"gpuOperator.driverSymlink.enabled": "false",
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	})).To(Succeed(), "install nvml-mock managed-driver mode")
}

// gpuOperatorManagedDriverRelease builds the pinned GPU Operator release for
// the managed-driver lane: baseline + driver delta, pinned to the operator
// version whose contract is vendored under tests/e2e/contract/.
func gpuOperatorManagedDriverRelease(valuesFiles []string) helm.Release {
	return helm.Release{
		Name:            gpuOperatorRelease,
		Chart:           gpuOperatorChart,
		Namespace:       gpuOperatorNamespace,
		CreateNamespace: true,
		Version:         config.GPUOperatorVersion(),
		ValuesFiles:     valuesFiles,
		Wait:            true,
		Timeout:         10 * time.Minute,
	}
}

// writeOperatorValuesFiles writes the embedded baseline + delta overlays to
// temp files (helm -f needs paths) and returns the ordered path list plus a
// cleanup. Order is load-bearing: later files win per key.
func writeOperatorValuesFiles(overlays ...[]byte) ([]string, func()) {
	GinkgoHelper()
	var paths []string
	var cleanups []func()
	cleanup := func() {
		for _, c := range cleanups {
			c()
		}
	}
	for i, overlay := range overlays {
		path, err := assets.WriteTemp(fmt.Sprintf("gpu-operator-values-%d-*.yaml", i), overlay)
		Expect(err).NotTo(HaveOccurred(), "write operator values overlay %d", i)
		paths = append(paths, path)
		p := path
		cleanups = append(cleanups, func() { _ = os.Remove(p) })
	}
	return paths, cleanup
}

// installGPUOperatorManagedDriver adds the NVIDIA repo and installs the pinned
// operator with the given ordered values overlays.
func installGPUOperatorManagedDriver(ctx SpecContext, h *harness.Harness, overlays ...[]byte) {
	GinkgoHelper()
	Expect(h.Helm.RepoAdd(ctx, "nvidia", "https://helm.ngc.nvidia.com/nvidia")).To(Succeed(), "add NVIDIA Helm repo")
	Expect(h.Helm.RepoUpdate(ctx)).To(Succeed(), "update Helm repos")
	paths, cleanup := writeOperatorValuesFiles(overlays...)
	DeferCleanup(cleanup)
	Expect(h.Helm.UpgradeInstall(ctx, gpuOperatorManagedDriverRelease(paths))).
		To(Succeed(), "install pinned GPU Operator (managed driver)")
}
