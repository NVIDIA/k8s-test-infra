//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/pod"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	migWorkloadNS = "default"
	// migWorkloadImage needs a shell and glibc: the specs exec into it and run
	// the staged nvidia-smi, which is a dynamically linked glibc binary.
	migWorkloadImage = "debian:bookworm-slim"

	// migCapDevDir is where a container receives the cap nodes guarding its
	// partition. The device plugin names them by this absolute path and roots
	// the host side at --nvidia-driver-root, which is where the node-agent
	// staged them.
	migCapDevDir = "/dev/nvidia-caps"

	// migMinorsPath is the capability table inside the nvml-mock pod, which
	// mounts the staging area at /host. This is the source the device plugin
	// self-mounts over its own /proc/driver.
	migMinorsPath = "/host/var/lib/nvml-mock/driver/proc/driver/nvidia-caps/mig-minors"
	// migStagedCapDevDir is the staged cap-node directory, same mount.
	migStagedCapDevDir = "/host/var/lib/nvml-mock/driver/dev/nvidia-caps"
)

// MIG scenario (#241). The acceptance criterion this exists for is the last one
// on that issue: the upstream device plugin, running migStrategy=single against
// the mock, publishes one schedulable resource per MIG partition.
//
// That is a end-to-end claim over three pieces that are individually tested
// elsewhere — the engine's MIG lifecycle, the C ABI that exposes it, and the
// node-agent's /dev/nvidia-caps staging — and it is the only place they are
// asserted to agree with each other. The instance IDs are the seam: the engine
// assigns them from placement allocation and the agent keys the capability table
// by them, so a drift between the two yields a plugin that starts, advertises
// the right count, and hands a pod the cap nodes of a different partition.
var _ = Describe("nvml-mock MIG", Label("mig"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "mig")
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p          profile.Profile
				node       string
				partitions int
			)

			BeforeAll(func(ctx SpecContext) {
				p = loadProfile(name)
				if !p.MIGCapable() {
					Skip("profile " + name + " declares no MIG partitioning; it is not a MIG-capable board")
				}
				Expect(p.MIGDeviceProfile()).NotTo(BeEmpty(),
					"profile %s declares a mixed MIG layout, which migStrategy=single rejects", name)
				partitions = p.MIGPartitionsPerGPU()

				installMIGChart(ctx, h, p)
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace,
					"nvml-mock", config.ReadyTimeout(), config.PollInterval())
				node = podNode(ctx, h, firstNvmlPod(ctx, h))
			})

			// The mock's own surface, before any consumer. Asserting through the
			// real nvidia-smi binary rather than by re-reading the config is the
			// point: it is the same NVML MIG enumeration the device plugin walks,
			// so a failure here localises the problem to the mock instead of the
			// plugin wiring.
			It("enumerates the declared partitions through nvidia-smi", Label("mig-nvml"), func(ctx SpecContext) {
				devices := migDevicesOnNode(ctx, h, node)

				Expect(devices).To(HaveLen(p.ExpectedGPUs()*partitions),
					"a %d-GPU node declaring %d partitions per GPU should list %d MIG devices",
					p.ExpectedGPUs(), partitions, p.ExpectedGPUs()*partitions)
				Expect(nvidiasmi.MigProfiles(devices)).To(Equal([]string{p.MIGDeviceProfile()}),
					"migStrategy=single requires every partition to carry one profile")

				uuids := map[string]bool{}
				for _, d := range devices {
					Expect(d.UUID).To(HavePrefix("MIG-"),
						"MIG device %d on GPU %d does not carry a MIG UUID", d.Index, d.GPU)
					Expect(uuids).NotTo(HaveKey(d.UUID),
						"MIG UUID %s is reported twice; consumers tell partitions apart by it", d.UUID)
					uuids[d.UUID] = true
				}
			})

			// The node-agent half. The plugin reads this table to map a partition
			// to its cap device, so it has to name every partition the mock
			// enumerates — checked here against nvidia-smi rather than against
			// the config, so the two derivations are compared to each other.
			It("stages a capability table covering every partition", Label("mig-caps"), func(ctx SpecContext) {
				mockPod := nvmlPodOnNode(ctx, h, node)

				res, err := h.Kube.ExecSh(ctx, mockPod, "cat "+migMinorsPath)
				Expect(err).NotTo(HaveOccurred(), "read %s: %s", migMinorsPath, res.Combined())
				table := res.Combined()

				for _, d := range migDevicesOnNode(ctx, h, node) {
					// One line per GPU instance and one per compute instance.
					// The device plugin looks up both and refuses the partition
					// if either is missing.
					Expect(table).To(MatchRegexp(`(?m)^gpu%d/gi\d+/access \d+$`, d.GPU),
						"no GPU-instance capability staged for GPU %d:\n%s", d.GPU, table)
					Expect(table).To(MatchRegexp(`(?m)^gpu%d/gi\d+/ci\d+/access \d+$`, d.GPU),
						"no compute-instance capability staged for GPU %d:\n%s", d.GPU, table)
				}

				// Every minor in the table must have a node on disk, or a pod
				// allocated that partition fails admission rather than starting.
				for _, minor := range migMinorsIn(table) {
					probe := fmt.Sprintf("test -c %s/nvidia-cap%s", migStagedCapDevDir, minor)
					res, err := h.Kube.ExecSh(ctx, mockPod, probe)
					Expect(err).NotTo(HaveOccurred(),
						"capability table names minor %s but %s/nvidia-cap%s is not a character device: %s",
						minor, migStagedCapDevDir, minor, res.Combined())
				}
			})

			// The acceptance criterion.
			It("publishes one nvidia.com/gpu per MIG partition", Label("mig-device-plugin"), func(ctx SpecContext) {
				deployMIGDevicePlugin(ctx, h, node, p.ExpectedGPUs()*partitions)
			})

			// The negative control for the spec above, and the reason it is worth
			// running: `nvidia.com/gpu: 56` on an 8-GPU node is only evidence of
			// MIG if the same cluster reports 8 with MIG off. Without this, a
			// plugin that ignored --mig-strategy and a mock that over-advertised
			// would be indistinguishable from success.
			It("falls back to whole GPUs when MIG is switched off", Label("mig-device-plugin"), func(ctx SpecContext) {
				installMIGChart(ctx, h, p, false)
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace,
					"nvml-mock", config.ReadyTimeout(), config.PollInterval())
				DeferCleanup(func(ctx SpecContext) {
					installMIGChart(ctx, h, p)
					assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace,
						"nvml-mock", config.ReadyTimeout(), config.PollInterval())
				})

				Expect(migDevicesOnNode(ctx, h, node)).To(BeEmpty(),
					"MIG is off, so nvidia-smi must list no partitions")

				// The plain manifest, because the MIG one waits for a capability
				// table that an unpartitioned node correctly does not stage.
				Expect(h.Kube.Apply(ctx, assets.DevicePluginManifest)).To(Succeed(), "apply plain device plugin")
				Expect(h.Kube.DeletePodsByLabel(ctx, devicePluginNamespace, devicePluginSelector)).
					To(Succeed(), "restart device plugin pods")
				assertions.WaitDaemonSetReady(ctx, h.Kube, devicePluginNamespace, devicePluginName,
					config.ReadyTimeout(), config.PollInterval())
				assertions.WaitAllocatableGPU(ctx, h.Kube, node, p.ExpectedGPUs(),
					config.ReadyTimeout(), config.PollInterval())
			})

			// Advertising the right count is not the same as handing out the
			// right thing. A pod that schedules onto a MIG resource must receive
			// its partition's cap nodes and see exactly one MIG device — which is
			// what fails if the agent's instance IDs disagree with the engine's.
			It("gives a scheduled pod exactly one MIG partition", Label("mig-allocation"), func(ctx SpecContext) {
				deployMIGDevicePlugin(ctx, h, node, p.ExpectedGPUs()*partitions)

				workload := applyMIGWorkload(ctx, h, "mig-single", node)

				devices := migDevicesInPod(ctx, h, workload)
				Expect(devices).To(HaveLen(1),
					"a pod requesting 1 %s under migStrategy=single must see one partition",
					kube.GPUResourceName)
				Expect(devices[0].Profile).To(Equal(p.MIGDeviceProfile()))

				// The allocation is the MIG device's own UUID, not its parent's.
				// A plugin handed whole-GPU identities would still satisfy the
				// count assertion above.
				res, err := h.Kube.ExecSh(ctx, workload, `printf %s "${NVIDIA_VISIBLE_DEVICES:-}"`)
				Expect(err).NotTo(HaveOccurred(), "read NVIDIA_VISIBLE_DEVICES: %s", res.Combined())
				Expect(strings.TrimSpace(res.Combined())).To(Equal(devices[0].UUID),
					"the allocated identity should be the partition the pod sees")

				// Two cap nodes: one for the GPU instance, one for the compute
				// instance. Their presence is what a MIG-aware runtime requires
				// to open the partition.
				res, err = h.Kube.ExecSh(ctx, workload, "ls "+migCapDevDir)
				Expect(err).NotTo(HaveOccurred(), "list %s: %s", migCapDevDir, res.Combined())
				Expect(strings.Fields(res.Combined())).To(HaveLen(2),
					"a MIG pod should receive its GPU- and compute-instance cap nodes, got:\n%s",
					res.Combined())
			})

			// The scheduler gate, on the MIG resource count rather than the GPU
			// count. A mock that reported partitions without the plugin
			// accounting for them would let this pod in.
			It("stops scheduling once every partition is claimed", Label("mig-allocation"), func(ctx SpecContext) {
				total := p.ExpectedGPUs() * partitions
				deployMIGDevicePlugin(ctx, h, node, total)

				name := "mig-oversubscribed"
				// Unpinned on purpose: a nodeName bypasses scheduling, so
				// kubelet would admit and then reject the pod, which proves the
				// device manager works rather than the resource gating.
				manifest := migPodManifest(name, "", total+1)
				Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply %s", name)
				DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, manifest) })

				Consistently(func() (string, error) {
					return h.Kube.PodPhase(ctx, migWorkloadNS, name)
				}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(config.PollInterval()).
					Should(Equal("Pending"),
						"a pod requesting %d %s on a %d-partition node must not schedule",
						total+1, kube.GPUResourceName, total)

				// Pending alone is weak: an unschedulable pod and one stuck
				// pulling an image look identical by phase. Pin the reason to
				// the GPU resource.
				out, err := h.Kube.KubectlCombined(ctx, "get", "events", "-n", migWorkloadNS,
					"--field-selector", "involvedObject.name="+name)
				Expect(err).NotTo(HaveOccurred(), "read events for %s", name)
				Expect(out).To(ContainSubstring("Insufficient "+kube.GPUResourceName),
					"%s should be unschedulable on %s specifically, got events:\n%s",
					name, kube.GPUResourceName, strings.TrimSpace(out))
			})
		})
	}
})

// installMIGChart installs the release with the profile's declared MIG
// partitioning switched on. Pass enabled=false for the negative control, which
// leaves the same profile's layout inert.
func installMIGChart(ctx context.Context, h *harness.Harness, p profile.Profile, enabled ...bool) {
	GinkgoHelper()
	on := true
	if len(enabled) > 0 {
		on = enabled[0]
	}
	repo, tag := splitImage(config.Image())
	rel := helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set: map[string]string{
			"gpu.count":        strconv.Itoa(p.ExpectedGPUs()),
			"gpu.profile":      p.Name,
			"image.repository": repo,
			"image.tag":        tag,
			"gpu.mig.enabled":  strconv.FormatBool(on),
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	}
	By(fmt.Sprintf("helm upgrade --install nvml-mock with gpu.mig.enabled=%t (profile=%s)", on, p.Name))
	Expect(h.Helm.UpgradeInstall(ctx, rel)).To(Succeed(),
		"helm upgrade --install nvml-mock with gpu.mig.enabled=%t (profile=%s)", on, p.Name)
}

// deployMIGDevicePlugin applies the migStrategy=single manifest and waits for
// the node to advertise want partitions. Pods are deleted first so the plugin
// re-reads the capability table: it builds its device map once at startup, so a
// plugin that outlived a repartitioning would keep serving the old layout.
func deployMIGDevicePlugin(ctx context.Context, h *harness.Harness, node string, want int) {
	GinkgoHelper()
	By("deploying the device plugin with --mig-strategy=single")
	Expect(h.Kube.Apply(ctx, assets.DevicePluginMIGManifest)).To(Succeed(), "apply MIG device plugin manifest")
	Expect(h.Kube.DeletePodsByLabel(ctx, devicePluginNamespace, devicePluginSelector)).
		To(Succeed(), "restart device plugin pods")
	assertions.WaitDaemonSetReady(ctx, h.Kube, devicePluginNamespace, devicePluginName,
		config.ReadyTimeout(), config.PollInterval())
	assertions.WaitAllocatableGPU(ctx, h.Kube, node, want, config.ReadyTimeout(), config.PollInterval())
}

// migDevicesOnNode lists the partitions nvidia-smi reports from inside the
// nvml-mock pod on node, which sees the whole board.
func migDevicesOnNode(ctx context.Context, h *harness.Harness, node string) []nvidiasmi.MigDevice {
	GinkgoHelper()
	return migDevicesInPod(ctx, h, nvmlPodOnNode(ctx, h, node))
}

// migDevicesInPod lists the partitions nvidia-smi reports inside a pod.
func migDevicesInPod(ctx context.Context, h *harness.Harness, target kube.PodRef) []nvidiasmi.MigDevice {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, target, "nvidia-smi", "-L")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L in %s: %s", target.Pod, res.Combined())
	return nvidiasmi.ListMigDevices(res.Combined())
}

// migMinorsIn returns the cap minors named by a mig-minors table. Every line is
// "<capability> <minor>", including the node-wide config and monitor entries,
// which must have device nodes too.
func migMinorsIn(table string) []string {
	var minors []string
	for line := range strings.Lines(table) {
		if fields := strings.Fields(line); len(fields) == 2 {
			minors = append(minors, fields[1])
		}
	}
	return minors
}

// migPodManifest renders a pod requesting gpus MIG partitions. An empty node
// leaves placement to the scheduler, which the oversubscription spec needs.
func migPodManifest(name, node string, gpus int) []byte {
	return pod.Spec{
		Name:      name,
		Namespace: migWorkloadNS,
		Image:     migWorkloadImage,
		Node:      node,
		GPUs:      gpus,
		// Kept alive so specs can exec in. Traps SIGTERM so teardown does not
		// wait out the grace period; `TERM` unprefixed because dash is /bin/sh.
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{"trap 'exit 0' TERM; sleep 3600 & wait"},
	}.Render()
}

// applyMIGWorkload applies a one-partition pod pinned to node and waits for it
// to run.
func applyMIGWorkload(ctx context.Context, h *harness.Harness, name, node string) kube.PodRef {
	GinkgoHelper()
	manifest := migPodManifest(name, node, 1)
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply workload %s", name)
	DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, manifest) }) //nolint:contextcheck // Ginkgo cleanup ctx is intentionally distinct from the outer spec ctx
	assertions.WaitPodPhase(ctx, h.Kube, migWorkloadNS, name, "Running",
		config.ReadyTimeout(), config.PollInterval())
	return kube.PodRef{Namespace: migWorkloadNS, Pod: name}
}
