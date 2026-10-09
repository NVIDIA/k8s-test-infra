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

			It("lays out the mock driver files for DRA", Label("mockfiles"), func(ctx SpecContext) {
				assertions.DRAMockFiles(ctx, h.Kube, pod, p.ExpectedGPUs())
			})

			It("reports the profile GPUs via nvidia-smi", Label("nvidia-smi"), func(ctx SpecContext) {
				nvidiasmi.Inventory(ctx, h.Kube, pod, p)
			})

			It("exposes the NVLink topology (gated on fabricmanager)", Label("nvlink"), func(ctx SpecContext) {
				assertions.FabricManagerGate(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", pod, config.ReadyTimeout(), config.PollInterval())
				assertions.NVLink(ctx, h.Kube, pod, p)
			})

			It("publishes DRA ResourceSlices for the profile GPUs", Label("dra-resourceslices"), func(ctx SpecContext) {
				assertions.WaitResourceSlicePerNode(ctx, h.Kube, p.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
			})

			It("schedules a pod with a DRA ResourceClaim", Label("dra-scheduling"), func(ctx SpecContext) {
				scheduleDRAResourceClaimPod(ctx, h)
				waitDRATestPodRunning(ctx, h, draTestPodName)
			})

			// The claim's container holds a CDI device the DRA driver named
			// k8s.gpu.nvidia.com/claim=<id>, which is what the NRI plugin
			// accepts as a DRA allocation. Injection must then keep the
			// container to exactly the claimed GPU, as on real hardware.
			It("gives each ResourceClaim pod exactly its claimed GPU through NRI", Label("dra-nri"), func(ctx SpecContext) {
				requireNRIPlugin(ctx, h, "tilt up -- --dra")
				ensureDRATestPodRunning(ctx, h)
				collectPodOnFailure(h, "dra", kube.PodRef{Namespace: draTestNamespace, Pod: draTestPodName})
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
	return append([]byte(`apiVersion: resource.k8s.io/v1beta1
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
`), draClaimPodManifest(draTestPodName, "")...)
}

// draClaimPodManifest renders a pod with its own claim from the gpu-claim
// template, pinned to node when one is given. The pin is node affinity, not
// nodeName: nodeName bypasses the scheduler, which is what allocates the claim.
func draClaimPodManifest(name, node string) []byte {
	affinity := ""
	if node != "" {
		affinity = fmt.Sprintf(`
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchFields:
              - key: metadata.name
                operator: In
                values: [%q]`, node)
	}
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  restartPolicy: Never
  # With the TERM trap below, deleting the pod takes a second instead of
  # waiting out the 30s default grace: sleep as PID 1 installs no handler, so
  # the kernel discards the signal (see framework/pod).
  terminationGracePeriodSeconds: 1%s
  containers:
    - name: app
      # glibc, so the injected nvidia-smi runs when the NRI plugin is enabled.
      image: debian:bookworm-slim
      command: ["/bin/sh", "-c"]
      args: ["trap 'exit 0' TERM; sleep 300 & wait"]
      resources:
        claims:
          - name: gpu
  resourceClaims:
    - name: gpu
      resourceClaimTemplateName: gpu-claim
`, name, draTestNamespace, affinity))
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
	waitDRATestPodRunning(ctx, h, draTestPodName)
}

// runSecondDRAClaimPod starts a second pod with its own claim from the same
// template on the first pod's node and waits for it to run. The first pod keeps
// its claim, so the scheduler must allocate this one a different GPU.
func runSecondDRAClaimPod(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	node, err := h.Kube.PodNode(ctx, draTestNamespace, draTestPodName)
	Expect(err).NotTo(HaveOccurred(), "read the node of %s", draTestPodName)
	manifest := draClaimPodManifest(draSecondPodName, node)
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete previous second DRA claim pod")
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply second DRA claim pod")
	DeferCleanup(func(ctx SpecContext) {
		Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete second DRA claim pod")
	})
	// Before the wait, so a pod that never runs still leaves its claim behind.
	collectPodOnFailure(h, "dra", kube.PodRef{Namespace: draTestNamespace, Pod: draSecondPodName})
	waitDRATestPodRunning(ctx, h, draSecondPodName)
}

// collectPodOnFailure saves a failed spec's evidence about a pod: its
// description, what NRI injected into it, and the ResourceClaims in its
// namespace. Register it after the pod's own cleanup. DeferCleanup runs last
// in, first out, so this runs while the pod and any claim generated for it
// still exist; the suite-level collectors run after the pod is deleted.
func collectPodOnFailure(h *harness.Harness, diagSub string, ref kube.PodRef) {
	DeferCleanup(func(ctx SpecContext) {
		if !CurrentSpecReport().Failed() {
			return
		}
		c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, diagSub)
		c.Kubectl(ctx, ref.Pod+"-describe.txt", "describe", "pod", "-n", ref.Namespace, ref.Pod)
		c.Kubectl(ctx, ref.Pod+"-injection.txt", "exec", "-n", ref.Namespace, ref.Pod, "--",
			"sh", "-c", "env | sort; ls -la /opt/nvml-mock /dev/nvidia* 2>&1")
		c.Kubectl(ctx, ref.Pod+"-resourceclaims.yaml", "get", "resourceclaims", "-n", ref.Namespace, "-o", "yaml")
	})
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

func waitDRATestPodRunning(ctx context.Context, h *harness.Harness, name string) {
	GinkgoHelper()
	deadline := time.Now().Add(config.ReadyTimeout())
	var lastPhase string
	var lastErr error
	for {
		lastPhase, lastErr = h.Kube.PodPhase(ctx, draTestNamespace, name)
		if lastErr == nil && lastPhase == "Running" {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			Fail(fmt.Sprintf("context canceled waiting for DRA test pod %s: %v", name, ctx.Err()))
		case <-time.After(config.PollInterval()):
		}
	}

	describe, _ := h.Kube.DescribePod(ctx, draTestNamespace, name)
	if assertions.DRAEmptyDeviceEdits(ctx, h.Kube, draTestNamespace, name) {
		Fail("nvml-mock DRA dev-node layout regression: pod events contain 'empty device edits'\n" + describe)
	}
	Fail(fmt.Sprintf("DRA test pod %s did not reach Running (last phase=%q, err=%v)\n%s", name, lastPhase, lastErr, describe))
}
