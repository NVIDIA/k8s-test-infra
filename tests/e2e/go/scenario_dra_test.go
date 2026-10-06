//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	draNamespace     = "nvidia"
	draTestNamespace = "default"
	draTestPodName   = "gpu-test-pod"
	draSecondPodName = "gpu-test-pod-2"
)

var _ = Describe("nvml-mock DRA", Label("dra"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "dra")
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

				// Setup, not a spec. Both DRA specs below read state that only
				// the driver publishes: one reads its ResourceSlices, the other
				// schedules against its DeviceClass. Installing from inside the
				// first of them made the second depend on that spec being
				// selected, so --focus on the scheduling spec alone left the pod
				// Pending until the wait timed out (#565).
				//
				// Unlike the GPU Operator case (#561), no second readiness
				// barrier moves with this wait: waitDRAPodsReady covers the
				// driver's own pods, and the scheduling spec's own wait for
				// Running absorbs the gap until the kubelet plugin publishes.
				waitDRAPodsReady(ctx, h)
			})

			It("lays out the mock driver files for DRA", func(ctx SpecContext) {
				assertions.DRAMockFiles(ctx, h.Kube, pod, p.ExpectedGPUs())
			})

			It("reports the profile GPUs via nvidia-smi", func(ctx SpecContext) {
				nvidiasmi.Inventory(ctx, h.Kube, pod, p)
			})

			It("exposes the NVLink topology (gated on fabricmanager)", func(ctx SpecContext) {
				assertions.FabricManagerGate(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", pod, config.ReadyTimeout(), config.PollInterval())
				assertions.NVLink(ctx, h.Kube, pod, p)
			})

			It("publishes DRA ResourceSlices for the profile GPUs", func(ctx SpecContext) {
				assertions.WaitResourceSlicePerNode(ctx, h.Kube, p.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
			})

			It("schedules a pod with a DRA ResourceClaim", func(ctx SpecContext) {
				scheduleDRAResourceClaimPod(ctx, h)
				waitDRATestPodRunning(ctx, h)
			})

			// The claim's container holds a CDI device the DRA driver named
			// k8s.gpu.nvidia.com/claim=<id>, which is what the NRI plugin
			// accepts as a DRA allocation. Injection must then keep the
			// container to exactly the claimed GPU, as on real hardware.
			It("gives each ResourceClaim pod exactly its claimed GPU through NRI", Label("dra-nri"), func(ctx SpecContext) {
				requireNRIPlugin(ctx, h, "tilt up -- --dra --nri")
				ensureDRATestPodRunning(ctx, h)
				pods := []string{draTestPodName}
				// A second claim on the same node is allocated a different GPU,
				// so a container shown the first GPU whatever its allocation
				// cannot pass. Same node, because every mock node serves the
				// same GPU UUIDs.
				if p.ExpectedGPUs() > 1 {
					runSecondDRAClaimPod(ctx, h)
					pods = append(pods, draSecondPodName)
				}

				claimed := map[string]string{}
				for _, name := range pods {
					ref := kube.PodRef{Namespace: draTestNamespace, Pod: name}
					res, err := h.Kube.ExecSh(ctx, ref, `test -d /opt/nvml-mock && test -n "${MOCK_NVML_CONFIG:-}"`)
					Expect(err).NotTo(HaveOccurred(),
						"%s was not injected by NRI: no /opt/nvml-mock overlay or MOCK_NVML_CONFIG\n%s", name, res.Combined())

					allocated, err := h.Kube.DRAAllocatedGPUUUIDs(ctx, ref)
					Expect(err).NotTo(HaveOccurred(), "resolve the GPU the scheduler allocated to %s", name)
					snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, ref)
					Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x in %s", name)
					Expect(snap.UUIDs()).To(ConsistOf(allocated),
						"nvidia-smi in %s must list exactly the claimed GPU", name)
					for _, uuid := range allocated {
						Expect(claimed).NotTo(HaveKey(uuid), "%s and %s were allocated the same GPU", claimed[uuid], name)
						claimed[uuid] = name
					}
				}
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
	c.Kubectl(ctx, "dra-kubelet-plugin-describe.txt", "describe", "pod", "-n", draNamespace, "-l", "dra-driver-nvidia-gpu-component=kubelet-plugin")
	c.Kubectl(ctx, "dra-driver-logs.txt", "logs", "-n", draNamespace, "-l", "app.kubernetes.io/name=dra-driver-nvidia-gpu", "--tail=100")
	c.Kubectl(ctx, "resourceslices.yaml", "get", "resourceslices", "-o", "yaml")
	c.Kubectl(ctx, "gpu-test-pod-describe.txt", "describe", "pod", "-n", draTestNamespace, draTestPodName)
	c.Kubectl(ctx, "resourceclaims.yaml", "get", "resourceclaims", "-A", "-o", "yaml")
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
      # glibc, so the injected nvidia-smi runs when the NRI plugin is enabled.
      image: debian:bookworm-slim
      command: ["sleep", "300"]
      resources:
        claims:
          - name: gpu
  resourceClaims:
    - name: gpu
      resourceClaimTemplateName: gpu-claim
`)
}

// ensureDRATestPodRunning reuses the pod the scheduling spec created, and
// creates it when that spec was not selected.
func ensureDRATestPodRunning(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	if phase, err := h.Kube.PodPhase(ctx, draTestNamespace, draTestPodName); err == nil && phase == "Running" {
		return
	}
	Expect(h.Kube.Delete(ctx, draResourceClaimManifest())).To(Succeed(), "delete previous DRA ResourceClaim test objects")
	Expect(h.Kube.Apply(ctx, draResourceClaimManifest())).To(Succeed(), "apply DRA ResourceClaim test pod")
	waitDRATestPodRunning(ctx, h)
}

// runSecondDRAClaimPod starts a second pod with its own claim from the same
// template on the first pod's node and waits for it to run. The first pod keeps
// its claim, so the scheduler must allocate this one a different GPU.
func runSecondDRAClaimPod(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	node, err := h.Kube.PodNode(ctx, draTestNamespace, draTestPodName)
	Expect(err).NotTo(HaveOccurred(), "read the node of %s", draTestPodName)
	manifest := []byte(fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  restartPolicy: Never
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchFields:
              - key: metadata.name
                operator: In
                values: ["%s"]
  containers:
    - name: app
      image: debian:bookworm-slim
      command: ["sleep", "300"]
      resources:
        claims:
          - name: gpu
  resourceClaims:
    - name: gpu
      resourceClaimTemplateName: gpu-claim
`, draSecondPodName, draTestNamespace, node))
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete previous second DRA claim pod")
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply second DRA claim pod")
	DeferCleanup(func(ctx SpecContext) {
		Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete second DRA claim pod")
	})
	Eventually(func() (string, error) {
		return h.Kube.PodPhase(ctx, draTestNamespace, draSecondPodName)
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		Should(Equal("Running"), "second DRA claim pod did not reach Running")
}

// requireNRIPlugin skips an NRI-only spec on a cluster without the plugin. With
// E2E_EXPECT_NRI set it fails instead, so a job meant to run NRI cannot pass on
// a skip.
func requireNRIPlugin(ctx context.Context, h *harness.Harness, hint string) {
	GinkgoHelper()
	if nriPluginEnabled(ctx, h) {
		return
	}
	if config.ExpectNRI() {
		Fail("E2E_EXPECT_NRI is set, but the nvml-mock DaemonSet runs no nvml-mock-nri container")
	}
	Skip("the nvml-mock NRI plugin is not enabled on this cluster (" + hint + ")")
}

// nriPluginEnabled reports whether the nvml-mock release runs the NRI plugin,
// so a suite that is also run without NRI can skip its NRI-only specs.
func nriPluginEnabled(ctx context.Context, h *harness.Harness) bool {
	GinkgoHelper()
	out, err := h.Kube.KubectlCombined(ctx, "get", "daemonset", "-n", nvmlMockNamespace, "-o",
		`jsonpath={.items[*].spec.template.spec.containers[?(@.name=="nvml-mock-nri")].name}`)
	Expect(err).NotTo(HaveOccurred(), "read nvml-mock DaemonSet containers: %s", out)
	return strings.Contains(out, "nvml-mock-nri")
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
