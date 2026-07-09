//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	draClusterName   = "nvml-mock-dra"
	draNamespace     = "nvidia"
	draDriverRelease = "nvidia-dra-driver"
	draDriverChart   = "nvidia/nvidia-dra-driver-gpu"
	draTestNamespace = "default"
	draTestPodName   = "gpu-test-pod"
)

var _ = Describe("nvml-mock DRA", Label("dra"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, draClusterName, assets.KindDRAConfig, "dra")
		DeferCleanup(func(ctx SpecContext) {
			collectDRAOnFailure(ctx, h)
		})
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p   profile.Profile
				pod kube.PodRef
			)

			BeforeAll(func(ctx SpecContext) {
				p, pod, _ = setupStandaloneProfile(ctx, h, name)
			})

			It("lays out the mock driver files for DRA", func(ctx SpecContext) {
				assertions.DRAMockFiles(ctx, h.Kube, pod, p.ExpectedGPUs())
			})

			It("reports the profile GPUs via nvidia-smi", func(ctx SpecContext) {
				assertions.NvidiaSMI(ctx, h.Kube, pod, p)
			})

			It("exposes the NVLink topology (gated on fabricmanager)", func(ctx SpecContext) {
				assertions.FabricManagerGate(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", pod, config.ReadyTimeout(), config.PollInterval())
				assertions.NVLink(ctx, h.Kube, pod, p)
			})

			It("publishes DRA ResourceSlices for the profile GPUs", func(ctx SpecContext) {
				installDRADriver(ctx, h)
				assertions.WaitResourceSliceTotal(ctx, h.Kube, p.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
			})

			It("schedules a pod with a DRA ResourceClaim", func(ctx SpecContext) {
				scheduleDRAResourceClaimPod(ctx, h)
				waitDRATestPodRunning(ctx, h)
			})
		})
	}
})

func collectDRAOnFailure(ctx context.Context, h *harness.Harness) {
	if !CurrentSpecReport().Failed() || h == nil || h.Kube == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, "dra")
	c.Kubectl(ctx, "dra-pods.txt", "get", "pods", "-n", draNamespace, "-o", "wide")
	c.Kubectl(ctx, "dra-kubelet-plugin-describe.txt", "describe", "pod", "-n", draNamespace, "-l", "app.kubernetes.io/component=kubelet-plugin")
	c.Kubectl(ctx, "dra-driver-logs.txt", "logs", "-n", draNamespace, "-l", "app.kubernetes.io/name=nvidia-dra-driver-gpu", "--tail=100")
	c.Kubectl(ctx, "resourceslices.yaml", "get", "resourceslices", "-o", "yaml")
	c.Kubectl(ctx, "gpu-test-pod-describe.txt", "describe", "pod", "-n", draTestNamespace, draTestPodName)
	c.Kubectl(ctx, "resourceclaims.yaml", "get", "resourceclaims", "-A", "-o", "yaml")
}

func installDRADriver(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	Expect(h.Helm.RepoAdd(ctx, "nvidia", "https://helm.ngc.nvidia.com/nvidia")).To(Succeed(), "add NVIDIA Helm repo")
	Expect(h.Helm.RepoUpdate(ctx)).To(Succeed(), "update Helm repos")
	Expect(h.Helm.UpgradeInstall(ctx, draDriverHelmRelease())).To(Succeed(), "install NVIDIA DRA driver")
	waitDRAPodsReady(ctx, h)
}

func draDriverHelmRelease() helm.Release {
	return helm.Release{
		Name:            draDriverRelease,
		Chart:           draDriverChart,
		Namespace:       draNamespace,
		CreateNamespace: true,
		Set: map[string]string{
			"gpuResourcesEnabledOverride":      "true",
			"nvidiaDriverRoot":                 "/var/lib/nvml-mock/driver",
			"resources.computeDomains.enabled": "false",
		},
		Wait:    true,
		Timeout: 3 * time.Minute,
	}
}

func waitDRAPodsReady(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	_, err := h.Kube.KubectlCombined(
		ctx,
		"wait",
		"-n", draNamespace,
		"--for=condition=ready",
		"pod",
		"--all",
		"--timeout="+config.ReadyTimeout().String(),
	)
	Expect(err).NotTo(HaveOccurred(), "wait for DRA pods")
}

func scheduleDRAResourceClaimPod(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	manifest := draResourceClaimManifest()
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete previous DRA ResourceClaim test objects")
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply DRA ResourceClaim test pod")
}

func draResourceClaimManifest() []byte {
	return []byte(`apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaimTemplate
metadata:
  name: gpu-claim
spec:
  spec:
    devices:
      requests:
        - name: gpu
          deviceClassName: gpu.nvidia.com
---
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-pod
spec:
  restartPolicy: Never
  containers:
    - name: app
      image: busybox:1.36
      command: ["sleep", "300"]
      resources:
        claims:
          - name: gpu
  resourceClaims:
    - name: gpu
      resourceClaimTemplateName: gpu-claim
`)
}

func waitDRATestPodRunning(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	deadline := time.Now().Add(config.ReadyTimeout())
	var lastPhase string
	var lastErr error
	for {
		lastPhase, lastErr = h.Kube.PodPhase(ctx, draTestNamespace, draTestPodName)
		if lastErr == nil && lastPhase == "Running" {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			Fail(fmt.Sprintf("context canceled waiting for DRA test pod: %v", ctx.Err()))
		case <-time.After(config.PollInterval()):
		}
	}

	describe, _ := h.Kube.DescribePod(ctx, draTestNamespace, draTestPodName)
	if assertions.DRAEmptyDeviceEdits(ctx, h.Kube, draTestNamespace, draTestPodName) {
		Fail("nvml-mock DRA dev-node layout regression: pod events contain 'empty device edits'\n" + describe)
	}
	Fail(fmt.Sprintf("DRA test pod did not reach Running (last phase=%q, err=%v)\n%s", lastPhase, lastErr, describe))
}
