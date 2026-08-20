//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/pod"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	nriWorkloadNS     = "default"
	nriAgentDaemonSet = "gpu-agent"
	nriAgentSelector  = "app=gpu-agent"
	nriNRIDaemonSet   = "nvml-mock-nri"
	nriPluginSelector = "app.kubernetes.io/name=nvml-mock-nri"
	// Device-plugin composition (#440, MEP-0002). Not a smaller image: the
	// overlay stages nvidia-smi without its glibc dependencies, so the image has
	// to supply them. busybox:1.36-glibc resolves ld-linux but ships no
	// libdl.so.2 or librt.so.1, and nvidia-smi exits 127 there.
	nriWorkloadImage = "debian:bookworm-slim"
	// nriMinimalImage is the negative control for overlay self-containment
	// (#438): glibc, so the tools' ELF interpreter resolves, but shipping no
	// InfiniBand stack and no shell. Running the tools here proves they load
	// their libraries from the overlay rather than from the image.
	//
	// Distroless and not Alpine, deliberately. These tools are glibc binaries
	// with PT_INTERP=/lib/ld-linux-*.so.*, an absolute path that RPATH cannot
	// redirect; on musl they fail to exec at all ("no such file or directory")
	// no matter what the overlay stages. Alpine would need glibc itself
	// staged, which is a different problem from this one.
	nriMinimalImage = "gcr.io/distroless/base-debian12:latest"
	// nriOverlayBinDir is where the NRI plugin mounts the staged tools. The
	// pod names them by absolute path because a shell-less image has no PATH
	// lookup to fall back on.
	nriOverlayBinDir = "/opt/nvml-mock/driver/usr/bin"

	// nriDomainName / nriDomainUUID identify the single ComputeDomain the
	// generated topology overlay declares. The UUID is arbitrary but must match
	// what check-fabric reports inside the injected pods.
	nriDomainName = "node-wide-domain"
	nriDomainUUID = "00000000-0000-0000-0000-0000000000cd"

	// Mock IMEX channel injection (#437). imex.mockChannels defaults to 2048
	// channels to match the DRA driver's getImexChannelCount(); the count is
	// irrelevant to the injection path and every channel is a real mknod on
	// every node, so the suite runs a small one.
	nriImexChannelCount = 8
	nriImexChannelDir   = "/dev/nvidia-caps-imex-channels"
	nriImexAnnotation   = "nvml-mock.nvidia.com/imex-channels"
	// nriImexChannelMajor must equal imex.mockChannels.channelMajor, which is
	// also the major the rendered proc-devices advertises to the DRA driver.
	nriImexChannelMajor = 235

	// nriDeviceAnnotation is the per-pod opt-in for device-node injection.
	nriDeviceAnnotation = "nvml-mock.nvidia.com/devices"
)

// Go port of docs/demo/node-wide-injection/run.sh. A dedicated Kind cluster
// with containerd NRI enabled is created once; the nvml-mock chart is installed
// per selected GPU profile with `nri.enabled=true` (plus a per-node
// ComputeDomain overlay for fabric-attached profiles). The scenario then proves
// that an ordinary `gpu-agent` DaemonSet — no `nvidia.com/gpu` request, no
// hostPath/mock volumes, no `MOCK_*` env — sees the full mock GPU stack purely
// through NRI ambient injection, and that each node carries its assigned
// ComputeDomain clique / cluster UUID.
var _ = Describe("nvml-mock node-wide NRI injection", Label("nri"), Ordered, func() {
	var (
		h          *harness.Harness
		workers    []cluster.Node
		topoValues string
	)
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "nri")
		var err error
		workers, err = h.Cluster.Workers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(workers)).To(BeNumerically(">=", 2),
			"node-wide NRI scenario needs >= 2 Kind workers for the two-clique overlay, found %d", len(workers))
		topoValues = writeNRITopologyValues(workers)
		DeferCleanup(func() { _ = os.Remove(topoValues) })
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p             profile.Profile
				computeDomain bool
			)

			BeforeAll(func(ctx SpecContext) {
				p = loadProfile(name)
				// Only profiles that declare a device_defaults.fabric block
				// (h100/gb200/gb300) expose ComputeDomain identity; NVSwitch-only
				// profiles like a100 report "fabric NOT SUPPORTED".
				computeDomain = p.HasFabric()
				installNRIChart(ctx, h, p, topoValues, computeDomain)
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
				deployNRIAgent(ctx, h)
			})

			// gpu-agent readiness alone already proves injection (its `set -eu`
			// self-test fails otherwise); this asserts the pod spec stayed plain.
			It("keeps the workload pod plain (no nvidia.com/gpu request)", Label("nri-inject"), func(ctx SpecContext) {
				assertAgentHasNoGPURequest(ctx, h)
			})

			It("reports the profile GPUs via nvidia-smi in the injected pod", Label("nvidia-smi"), func(ctx SpecContext) {
				assertAgentSeesGPUs(ctx, h, p.ExpectedGPUs())
			})

			// #438. The IB CLI tools are dynamically linked and setup.sh
			// relocates them into the overlay, which until this change carried
			// none of their libraries. Nothing in CI ran them from an injected
			// pod, so the breakage was invisible.
			//
			// It was also wider than "minimal images only". Reverting the
			// library staging and running ibstat from an injected
			// debian:bookworm-slim pod reproduces the identical
			// "libibmad.so.5: cannot open shared object file" — neither
			// debian:bookworm-slim nor ubuntu:22.04 ships libibmad/libibumad/
			// libibverbs. A minimal image is simply the honest place to assert
			// it: nothing there can mask a missing library.
			It("runs the IB CLI tools from an image that ships no IB libraries", Label("nri-ib-minimal"), func(ctx SpecContext) {
				if !p.IBEnabled() {
					Skip("profile " + name + " ships infiniband.enabled=false; it exposes no HCAs to enumerate")
				}
				for _, tc := range []struct {
					name, tool     string
					wantEnumerated bool
				}{
					// ibstat links libibmad/libibumad directly and reads the
					// mock sysfs through the LD_PRELOAD shim, so it enumerates.
					{"nri-ib-ibstat", "ibstat", true},
					// ibv_devinfo covers the transitive dependency: it names
					// libnl nowhere and reaches it through libibverbs, so it is
					// the tool that catches a staging tree which resolves only
					// direct dependencies.
					//
					// It is NOT asserted to enumerate. In an NRI-injected pod
					// it reports "0 HCAs found" — and it does so identically on
					// debian:bookworm-slim, which was measured on the same
					// cluster. That gap predates this change and belongs to the
					// verbs path (libibverbs discards a device it cannot match
					// to a provider driver), not to library staging. Asserting
					// enumeration here would be asserting a bug fix that this
					// change does not make.
					{"nri-ib-ibv-devinfo", "ibv_devinfo", false},
				} {
					phase, logs := runIBToolInMinimalImage(ctx, h, tc.name, tc.tool, "-l")

					// The failure mode this issue is about, asserted by name so
					// a regression reports the linkage error itself rather than
					// a bare "substring not found".
					Expect(logs).NotTo(ContainSubstring("error while loading shared libraries"),
						"%s could not resolve its shared libraries from the overlay in %s:\n%s",
						tc.tool, nriMinimalImage, logs)
					Expect(phase).To(Equal("Succeeded"),
						"%s exited non-zero in %s:\n%s", tc.tool, nriMinimalImage, logs)

					if tc.wantEnumerated {
						// Assert the mock HCA by name, not a zero exit. ibstat
						// exits 0 while printing nothing when it finds no
						// devices, so an exit-code assertion would hold even
						// with the mock absent. The device name is what proves
						// the binary both loaded its libraries and got an
						// answer back.
						Expect(logs).To(ContainSubstring("mlx5_0"),
							"%s did not enumerate the mock HCA from %s\nphase=%s\noutput:\n%s",
							tc.tool, nriMinimalImage, phase, logs)
					}
				}
			})

			It("carries per-node ComputeDomain fabric identity through NRI", Label("compute-domain"), func(ctx SpecContext) {
				if !computeDomain {
					Skip("profile " + name + " declares no device_defaults.fabric block; ComputeDomain identity is unsupported")
				}
				assertNodeCliqueIdentities(ctx, h, workers)
			})
		})
	}

	// Composition with the device plugin (#440, MEP-0002). Everything above
	// covers pods that request no GPU resources. This covers the other half: a
	// pod that goes through the scheduler must see exactly what it was
	// allocated, not every GPU on the node.
	//
	// This is the only place the upstream device plugin and the NRI plugin run
	// on the same nodes. Before MEP-0002 the two never met in CI, and the
	// allocation was inert: NVIDIA_VISIBLE_DEVICES was set and nothing read it.
	Context("when the device plugin also serves the node", Label("nri-device-plugin"), Ordered, func() {
		var (
			p       profile.Profile
			gpuNode string
		)

		BeforeAll(func(ctx SpecContext) {
			Expect(selectedProfiles).NotTo(BeEmpty())
			p = loadProfile(selectedProfiles[0])
			// The allocation watcher (#506) rides in this context because it is
			// the only one where the device plugin actually allocates GPUs.
			installNRIChart(ctx, h, p, topoValues, p.HasFabric(), map[string]string{
				"allocationWatcher.enabled": "true",
				// Poll faster than the 2s default so the e2e is not dominated
				// by waiting; the engine's own override TTL still applies.
				"allocationWatcher.interval": "500ms",
			})
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
			deployDevicePluginOnWorkers(ctx, h, workers, p.ExpectedGPUs())
			gpuNode = workers[0].Name
		})

		It("gives a resource-requested pod exactly its allocated GPU", Label("nri-dp-isolation"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h, nriRequestPodManifest("nri-dp-single", gpuNode, 1), "nri-dp-single")

			allocated := allocatedGPUUUID(ctx, h, pod)
			visible := visibleGPUUUIDs(ctx, h, pod)

			Expect(visible).To(HaveLen(1),
				"pod requested 1 %s but nvidia-smi reports %d GPUs; the delivery path ignored the allocation",
				kube.GPUResourceName, len(visible))
			Expect(visible[0]).To(Equal(allocated),
				"pod was allocated %s but sees %s", allocated, visible[0])
		})

		// Two pods on ONE node must not see each other's GPU.
		//
		// This spec covers the --pass-device-specs half of MEP-0002, NOT the
		// suppression rule in adjust.go: these pods carry no device annotation, so
		// the NRI plugin never injects device nodes for them and there is nothing
		// to suppress. Mutation-checked — it stays green with the suppression
		// reverted. "keeps an annotated pod's allocation intact" below is the
		// spec that covers suppression.
		It("keeps two pods on one node isolated from each other", Label("nri-dp-isolation"), func(ctx SpecContext) {
			if p.ExpectedGPUs() < 2 {
				Skip("profile " + p.Name + " advertises fewer than 2 GPUs; isolation needs at least two")
			}
			first := applyNRIWorkload(ctx, h, nriRequestPodManifest("nri-dp-a", gpuNode, 1), "nri-dp-a")
			second := applyNRIWorkload(ctx, h, nriRequestPodManifest("nri-dp-b", gpuNode, 1), "nri-dp-b")

			firstVisible := visibleGPUUUIDs(ctx, h, first)
			secondVisible := visibleGPUUUIDs(ctx, h, second)

			Expect(firstVisible).To(HaveLen(1), "nri-dp-a should see exactly its allocated GPU")
			Expect(secondVisible).To(HaveLen(1), "nri-dp-b should see exactly its allocated GPU")
			Expect(firstVisible[0]).NotTo(Equal(secondVisible[0]),
				"two pods on %s were each allocated one GPU but both see %s; the allocation is not isolating",
				gpuNode, firstVisible[0])
			Expect(firstVisible[0]).To(Equal(allocatedGPUUUID(ctx, h, first)))
			Expect(secondVisible[0]).To(Equal(allocatedGPUUUID(ctx, h, second)))
		})

		// The spec that covers the suppression rule, and the only one that does.
		// A pod carrying BOTH a resource request and the device opt-in is the
		// collision MEP-0002 exists to resolve: the device plugin hands it one
		// device node, and the plugin's opt-in would stage the whole tree on top,
		// pushing the engine's visibility filter back to "all present" and
		// exposing every GPU to a pod allocated one.
		//
		// Mutation-checked: reverting alreadyHasGPUDevices turns this red with
		// nvidia-smi reporting every GPU instead of one.
		It("keeps an annotated pod's allocation intact", Label("nri-dp-suppression"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h,
				nriRequestPodManifest("nri-dp-annotated-request", gpuNode, 1,
					map[string]string{nriDeviceAnnotation: "true"}),
				"nri-dp-annotated-request")

			allocated := allocatedGPUUUID(ctx, h, pod)
			visible := visibleGPUUUIDs(ctx, h, pod)

			Expect(visible).To(HaveLen(1),
				"pod requested 1 %s and opted into device nodes; it must still see only its allocation, got %d GPUs",
				kube.GPUResourceName, len(visible))
			Expect(visible[0]).To(Equal(allocated),
				"pod was allocated %s but sees %s", allocated, visible[0])
		})

		// #506 item 1. The acceptance criterion is explicitly BOTH directions:
		// the number must move when a workload is scheduled AND come back when
		// it is removed. An increase-only assertion passes trivially against a
		// monotonic counter that never returns, so the release half is the
		// half that carries the weight.
		//
		// The observer is a SEPARATE pod that holds no GPU claim of its own and
		// sees every GPU on the node. That matters: reading memory from inside
		// the claiming pod would prove only that the pod sees itself, whereas
		// the surface #506 is about is a node-level one (dcgm-exporter's
		// FB_USED). The observer also survives the workload's deletion, which
		// is what makes the return observable at all.
		It("moves memory.used_bytes when a pod claims a GPU and returns it on delete",
			Label("nri-alloc-memory"), func(ctx SpecContext) {
				// Pinned to the SAME node as the claimant. The override file is a
				// per-node hostPath, so an observer on another node reads a node
				// that never saw the claim — which is exactly how this spec passed
				// locally and failed in CI before the pin.
				observer := applyNRIWorkload(ctx, h,
					nriAnnotatedPodManifest("nri-alloc-observer", gpuNode), "nri-alloc-observer")

				allGPUs := visibleGPUUUIDs(ctx, h, observer)
				Expect(allGPUs).To(HaveLen(p.ExpectedGPUs()), "the observer must see the whole node")

				claimantManifest := nriRequestPodManifest("nri-alloc-claimant", gpuNode, 1)
				workload := applyNRIWorkload(ctx, h, claimantManifest, "nri-alloc-claimant")
				claimed := allocatedGPUUUID(ctx, h, workload)

				idx := indexOfGPUUUID(allGPUs, claimed)
				Expect(idx).To(BeNumerically(">=", 0),
					"allocated GPU %s is not among the observer's %d GPUs", claimed, len(allGPUs))

				// Poll: the watcher writes at its interval and the engine
				// re-reads the override file at its own TTL, so the change is
				// eventually-consistent by design, not instant.
				Eventually(func(ctx SpecContext) int {
					return smiGPUMemoryUsedMiB(ctx, h, observer, idx)
				}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
					Should(BeNumerically(">", 0),
						"a pod holding %s on GPU %d did not move the used framebuffer; the allocation "+
							"watcher is not reaching the engine", kube.GPUResourceName, idx)

				// Every OTHER GPU must stay idle. Without this, a watcher that
				// reported node-wide totals on every device would pass the
				// assertion above.
				for other := range allGPUs {
					if other == idx {
						continue
					}
					Expect(smiGPUMemoryUsedMiB(ctx, h, observer, other)).To(Equal(0),
						"GPU %d holds no claim but reports memory in use; the watcher is not "+
							"attributing per device", other)
				}

				By("deleting the claimant and asserting the number returns")
				Expect(h.Kube.Delete(ctx, claimantManifest)).To(Succeed(), "delete %s", workload.Pod)

				Eventually(func(ctx SpecContext) int {
					return smiGPUMemoryUsedMiB(ctx, h, observer, idx)
				}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
					Should(Equal(0),
						"GPU %d still reports memory in use after its pod was deleted; the reading "+
							"is monotonic, not allocation-driven", idx)
			})

		It("still gives an annotated pod with no request every GPU", Label("nri-dp-optin"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h, nriAnnotatedPodManifest("nri-dp-annotated"), "nri-dp-annotated")
			// The opt-in path is unchanged: no resource request means the device
			// plugin never served this container, so the plugin still stages the
			// whole tree and every GPU stays visible.
			Expect(visibleGPUUUIDs(ctx, h, pod)).To(HaveLen(p.ExpectedGPUs()),
				"annotated pod with no %s request should keep seeing all %d GPUs",
				kube.GPUResourceName, p.ExpectedGPUs())

			// Mirror of the nri-cdi specs (#436). This release runs the shipped
			// default, so the devices must have come from the raw path. Without
			// this the CDI marker assertion would pass trivially if something set
			// NVML_MOCK_DEVICE_SOURCE unconditionally.
			Expect(nriDeviceSource(ctx, h, pod)).To(BeEmpty(),
				"default deviceInjectionMode is raw, so the CDI spec must not have been applied")
		})

		It("handles a plain unannotated pod with no GPU request", Label("nri-dp-plain"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h, nriPlainPodManifest("nri-dp-plain"), "nri-dp-plain")

			// Verify ambient overlay injection is present.
			res, err := h.Kube.ExecSh(ctx, pod, "test -d /opt/nvml-mock/driver")
			Expect(err).NotTo(HaveOccurred(), "check overlay mount in nri-dp-plain: %s", res.Combined())

			// Verify GPU visibility reported via nvidia-smi -L.
			visible := visibleGPUUUIDs(ctx, h, pod)
			Expect(visible).To(HaveLen(p.ExpectedGPUs()),
				"plain unannotated pod on DP node receives ambient overlay and reports all %d profile GPUs",
				p.ExpectedGPUs())
		})

		It("leaves the scheduler gate intact once the node is saturated", Label("nri-dp-scheduling"), func(ctx SpecContext) {
			// Ask for one more GPU than any node advertises. The pod must stay
			// Pending on the SCHEDULER's verdict: a mock that over-advertises, or
			// a plugin that hands out devices without accounting, would let it in.
			//
			// Deliberately unpinned. Setting nodeName bypasses scheduling entirely,
			// so kubelet admits the pod and then rejects it with
			// UnexpectedAdmissionError — which proves kubelet's device manager
			// works, not that the extended resource gates scheduling.
			name := "nri-dp-oversubscribed"
			pod := nriRequestPodManifest(name, "", p.ExpectedGPUs()+1)
			Expect(h.Kube.Apply(ctx, pod)).To(Succeed(), "apply %s", name)
			DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, pod) })

			Consistently(func() (string, error) {
				return h.Kube.PodPhase(ctx, nriWorkloadNS, name)
			}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(config.PollInterval()).
				Should(Equal("Pending"),
					"a pod requesting %d %s on a %d-GPU node must not schedule",
					p.ExpectedGPUs()+1, kube.GPUResourceName, p.ExpectedGPUs())

			// Pending alone is weak — an unschedulable pod and one stuck pulling an
			// image look identical by phase. Pin the reason to the GPU resource.
			out, err := h.Kube.KubectlCombined(ctx, "get", "events", "-n", nriWorkloadNS,
				"--field-selector", "involvedObject.name="+name)
			Expect(err).NotTo(HaveOccurred(), "read events for %s", name)
			Expect(out).To(ContainSubstring("Insufficient "+kube.GPUResourceName),
				"%s should be unschedulable on %s specifically, got events:\n%s",
				name, kube.GPUResourceName, strings.TrimSpace(out))
		})
	})

	// CDI device injection (#436). The plugin can deliver the annotation-gated
	// device tree either by staging raw device nodes itself or by handing the
	// runtime a CDI device reference. containerd 2.x enables CDI by default
	// (enable_cdi = true, spec dirs /etc/cdi and /var/run/cdi), so this works on
	// the stock kindest/node this scenario already runs on — no container
	// toolkit is involved.
	//
	// The two modes are identical in outcome by design, which is exactly what
	// makes this hard to test honestly: a cdi deployment that silently fell back
	// to raw would still show every GPU. NVML_MOCK_DEVICE_SOURCE is the
	// discriminator. It comes from the CDI spec's containerEdits, and the raw
	// path injects device nodes only — it has no mechanism to set an environment
	// variable — so the variable is present if and only if the runtime resolved
	// the spec.
	Context("when device injection is switched to CDI", Label("nri-cdi"), Ordered, func() {
		var p profile.Profile

		BeforeAll(func(ctx SpecContext) {
			Expect(selectedProfiles).NotTo(BeEmpty())
			p = loadProfile(selectedProfiles[0])
			installNRICDIChart(ctx, h, p)
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
		})

		AfterAll(func(ctx SpecContext) {
			// Put the release back on the default mechanism so any spec ordered
			// after this Context sees the shipped configuration.
			installNRIChart(ctx, h, p, topoValues, p.HasFabric())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
		})

		It("delivers every GPU through the CDI spec", Label("nri-cdi-inject"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h, nriAnnotatedPodManifest("nri-cdi-annotated"), "nri-cdi-annotated")

			Expect(nriDeviceSource(ctx, h, pod)).To(Equal("cdi"),
				"NVML_MOCK_DEVICE_SOURCE comes from the CDI spec's containerEdits; "+
					"an empty value means the runtime never resolved the spec and the plugin "+
					"quietly fell back to raw device nodes")

			// MEP-0002 goal 2 is a contract, not an implementation detail: the
			// annotation with no resource request keeps meaning "every GPU".
			Expect(visibleGPUUUIDs(ctx, h, pod)).To(HaveLen(p.ExpectedGPUs()),
				"annotated pod with no %s request must still see all %d GPUs through CDI",
				kube.GPUResourceName, p.ExpectedGPUs())
		})

		It("still suppresses injection for a pod the device plugin served", Label("nri-cdi-suppression"), func(ctx SpecContext) {
			// MEP-0002 forbids #436 from bypassing the suppression rule. The rule is
			// about who already served the container, not which mechanism serves it,
			// so switching to CDI must not reopen it. Without the device plugin on
			// this cluster the closest available proxy is a pod that carries the
			// annotation and no request, so assert the negative directly: a pod that
			// opted OUT gets neither the CDI marker nor the devices.
			pod := applyNRIWorkload(ctx, h, nriPlainPodManifest("nri-cdi-plain"), "nri-cdi-plain")

			Expect(nriDeviceSource(ctx, h, pod)).To(BeEmpty(),
				"a pod that did not opt in must not have the CDI spec applied")
		})
	})

	// Mock IMEX channel injection (#437). The NRI plugin already delivers the
	// mock GPU tree; ComputeDomain workloads additionally need the IMEX channel
	// nodes, which no mock node has because there is no NVIDIA kernel module to
	// create them.
	//
	// This rides the cluster the specs above already built. An earlier revision
	// of this work stood up a second five-node Kind cluster for the IMEX specs
	// and exhausted the runner's disk during `kind load docker-image`, which
	// surfaced as an unrelated-looking failure on whichever profile happened to
	// be running when the disk filled.
	Context("when a pod opts into mock IMEX channels", Label("nri-imex"), Ordered, func() {
		var gpuNode string

		BeforeAll(func(ctx SpecContext) {
			Expect(selectedProfiles).NotTo(BeEmpty())
			p := loadProfile(selectedProfiles[0])
			installNRIChart(ctx, h, p, topoValues, p.HasFabric())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
			gpuNode = workers[0].Name
		})

		It("delivers every staged channel into an annotated pod", Label("imex-channels"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h,
				nriImexPodManifest("nri-imex-annotated", gpuNode, true),
				"nri-imex-annotated")

			Expect(imexChannelNames(ctx, h, pod)).To(HaveLen(nriImexChannelCount),
				"annotated pod should see all %d channels staged by imex.mockChannels", nriImexChannelCount)
		})

		// The opt-in has to be a real gate. Without this, a plugin that injected
		// channels unconditionally would pass the spec above.
		It("keeps channels out of a pod that did not ask for them", Label("imex-channels"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h,
				nriImexPodManifest("nri-imex-plain", gpuNode, false),
				"nri-imex-plain")

			res, err := h.Kube.ExecSh(ctx, pod, `ls `+nriImexChannelDir+` 2>&1 || true`)
			Expect(err).NotTo(HaveOccurred(), "probe %s in nri-imex-plain", nriImexChannelDir)
			Expect(res.Combined()).To(ContainSubstring("No such file or directory"),
				"an unannotated pod must not receive %s, got:\n%s", nriImexChannelDir, res.Combined())
		})

		// The invariant that keeps the mock self-consistent, and the reason this
		// feature consumes imex.mockChannels instead of provisioning its own
		// nodes. The DRA driver's compute-domain plugin reads the channel major
		// out of the rendered proc-devices; the injected nodes must carry that
		// same major. Two provisioning paths with different majors would leave
		// the directory split across majors while proc-devices advertised one,
		// and nothing else in the suite would notice.
		It("injects channels whose major matches the advertised proc-devices major", Label("imex-channels"), func(ctx SpecContext) {
			pod := applyNRIWorkload(ctx, h,
				nriImexPodManifest("nri-imex-major", gpuNode, true),
				"nri-imex-major")

			advertised := advertisedImexMajor(ctx, h, gpuNode)
			Expect(advertised).To(Equal(nriImexChannelMajor),
				"rendered proc-devices should advertise the chart's channelMajor")

			for _, name := range imexChannelNames(ctx, h, pod) {
				res, err := h.Kube.ExecSh(ctx, pod, `stat -c %t `+nriImexChannelDir+`/`+name)
				Expect(err).NotTo(HaveOccurred(), "stat %s: %s", name, res.Combined())
				major, parseErr := strconv.ParseInt(strings.TrimSpace(res.Combined()), 16, 32)
				Expect(parseErr).NotTo(HaveOccurred(), "parse major of %s from %q", name, res.Combined())
				Expect(int(major)).To(Equal(advertised),
					"%s carries major %d but proc-devices advertises %d; the node has more than one channel provisioner",
					name, major, advertised)
			}
		})
	})

	// Failure-mode hardening (#434). Everything above asserts that injection
	// works. This asserts what happens when it stops working mid-run.
	//
	// The specs above cannot catch that on their own, and not by oversight:
	// injection is baked into the OCI spec at container-creation time, so pods
	// created while the plugin was healthy keep their injection no matter what
	// happens to the plugin afterwards. A suite that creates its pods early and
	// asserts later passes cleanly against a node that lost injection halfway
	// through. Only pods created *after* the failure come up unmocked.
	//
	// SIGSTOP is the honest reproduction of a wedged plugin: the process stays
	// alive, the ttRPC connection stays open, and the handler never answers.
	// `pgrep nvml-mock-nri` and any check that only proves the socket is bound
	// both report healthy throughout.
	Context("when the plugin wedges mid-run", Label("nri-failure"), Ordered, func() {
		var (
			victim     cluster.Node
			pluginPod  kube.PodRef
			restartsAt int
		)

		BeforeAll(func(ctx SpecContext) {
			Expect(selectedProfiles).NotTo(BeEmpty())
			p := loadProfile(selectedProfiles[0])
			installNRIChart(ctx, h, p, topoValues, p.HasFabric())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
			deployNRIAgent(ctx, h)

			victim = workers[0]
			pluginPod = nriPluginPodOnNode(ctx, h, victim.Name)
			var err error
			restartsAt, err = nriRestartCount(ctx, h, pluginPod)
			Expect(err).NotTo(HaveOccurred(), "read baseline restart count")

			By("SIGSTOP the nvml-mock-nri process on " + victim.Name)
			wedgeNRIPlugin(ctx, victim.Container)
		})

		// The readiness probe is the detectable half of the fail-open posture:
		// without it the DaemonSet keeps reporting its full desired count while
		// the node silently injects nothing.
		It("reports the wedged node as NotReady", Label("nri-failure-detect"), func(ctx SpecContext) {
			Eventually(func() (bool, error) {
				return h.Kube.DaemonSetReady(ctx, nvmlMockNamespace, nriNRIDaemonSet)
			}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(time.Second).
				Should(BeFalse(), "daemonset %s/%s stayed Ready while the plugin on %s was wedged",
					nvmlMockNamespace, nriNRIDaemonSet, victim.Name)
		})

		// The liveness probe is the recovery half. A wedged plugin cannot exit
		// on its own -- unlike a dropped connection, which makes stub.Run
		// return and the process exit -- so only a probe-driven restart clears
		// it. Asserting the kubelet's own reason pins that the restart came
		// from the liveness probe and not from something incidental.
		It("restarts the wedged plugin", Label("nri-failure-recover"), func(ctx SpecContext) {
			Eventually(func() (int, error) {
				return nriRestartCount(ctx, h, pluginPod)
			}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
				Should(BeNumerically(">", restartsAt),
					"plugin container on %s never restarted; a wedged plugin cannot recover on its own", victim.Name)

			described, err := h.Kube.DescribePod(ctx, pluginPod.Namespace, pluginPod.Pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(described).To(ContainSubstring("Liveness probe failed"),
				"restart was not attributed to the liveness probe:\n%s", described)
		})

		// The acceptance criterion: a pod created after the failure must be
		// correctly injected once the plugin recovers. This is the assertion
		// the positive specs above structurally cannot make.
		It("injects into a workload created after the wedge", Label("nri-failure-inject"), func(ctx SpecContext) {
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())

			By("recreating gpu-agent so its containers are created after the wedge")
			deployNRIAgent(ctx, h)

			p := loadProfile(selectedProfiles[0])
			pod := nriAgentPodOnNode(ctx, h, victim.Name)
			Expect(visibleGPUCount(ctx, h, pod)).To(Equal(p.ExpectedGPUs()),
				"a pod created after the wedge must still see %d injected GPUs", p.ExpectedGPUs())
		})
	})
})

// nriPluginPodOnNode returns the nvml-mock-nri pod scheduled on node.
func nriPluginPodOnNode(ctx context.Context, h *harness.Harness, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		pods, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, nriPluginSelector)
		if err != nil {
			return "", err
		}
		for _, pod := range pods {
			podNode, err := h.Kube.PodNode(ctx, nvmlMockNamespace, pod)
			if err != nil {
				return "", err
			}
			if podNode == node {
				name = pod
				return name, nil
			}
		}
		return "", nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running nvml-mock-nri pod on node %s", node)
	return kube.PodRef{Namespace: nvmlMockNamespace, Pod: name}
}

func nriRestartCount(ctx context.Context, h *harness.Harness, pod kube.PodRef) (int, error) {
	out, err := h.Kube.KubectlCombined(ctx, "get", "pod", "-n", pod.Namespace, pod.Pod,
		"-o", "jsonpath={.status.containerStatuses[0].restartCount}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// wedgeNRIPlugin freezes the plugin process with SIGSTOP, reproducing a handler
// that never answers while the process and its runtime connection stay up.
//
// The signal is sent from the Kind node's PID namespace, not from inside the
// container, and that is load-bearing: the plugin is PID 1 of its container's
// PID namespace, and the kernel discards signals with a default action that are
// sent to a namespace init from within that same namespace. `kubectl exec ...
// kill -STOP 1` is silently a no-op. From the node -- an ancestor namespace --
// the signal is delivered.
func wedgeNRIPlugin(ctx context.Context, container string) {
	GinkgoHelper()
	pid := nriPluginHostPID(ctx, container)
	_, err := runner.Run(ctx, "docker", "exec", container, "kill", "-STOP", pid)
	Expect(err).NotTo(HaveOccurred(), "SIGSTOP nvml-mock-nri (pid %s) on %s", pid, container)

	// Confirm the process really is stopped rather than trusting kill's exit
	// code: a wedge that did not take would make every assertion below vacuous.
	Eventually(func() (string, error) {
		res, err := runner.RunQuiet(ctx, "docker", "exec", container, "cat", "/proc/"+pid+"/stat")
		if err != nil {
			return "", err
		}
		fields := strings.Fields(res.Stdout)
		if len(fields) < 3 {
			return "", fmt.Errorf("unexpected /proc/%s/stat: %q", pid, res.Stdout)
		}
		return fields[2], nil // state: T = stopped
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(time.Second).
		Should(Equal("T"), "nvml-mock-nri (pid %s) on %s did not enter the stopped state", pid, container)
}

// nriPluginHostPID finds the plugin process in the Kind node's PID namespace.
// It reads /proc/<pid>/exe rather than shelling out to pgrep: procps is not
// guaranteed in the node image, and matching on a command line would also match
// the matching process itself.
func nriPluginHostPID(ctx context.Context, container string) string {
	GinkgoHelper()
	const script = `for p in /proc/[0-9]*; do case "$(readlink "$p/exe" 2>/dev/null)" in */nvml-mock-nri) echo "${p##*/}";; esac; done`
	res, err := runner.Run(ctx, "docker", "exec", container, "sh", "-c", script)
	Expect(err).NotTo(HaveOccurred(), "locate nvml-mock-nri on %s: %s", container, res.Combined())

	pids := strings.Fields(res.Stdout)
	Expect(pids).NotTo(BeEmpty(), "no nvml-mock-nri process found on %s", container)
	return pids[0]
}

// installNRIChart (re)installs the nvml-mock release with the NRI plugin
// enabled. Fabric-attached profiles additionally get the generated
// ComputeDomain overlay via `-f` (a structured merge of topology.domains, never
// --set-file which would stuff the raw bytes in as a string literal).
func installNRIChart(ctx context.Context, h *harness.Harness, p profile.Profile, topoValues string,
	withComputeDomain bool, extraSet ...map[string]string,
) {
	GinkgoHelper()
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
			"nri.enabled":      "true",
			// Stage the mock IMEX channel nodes so the NRI channel opt-in has
			// something to deliver (#437). This is the ONLY mechanism that
			// creates them; the NRI plugin consumes what this stages.
			"imex.mockChannels.enabled":      "true",
			"imex.mockChannels.channelCount": strconv.Itoa(nriImexChannelCount),
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	}
	for _, set := range extraSet {
		for k, v := range set {
			rel.Set[k] = v
		}
	}
	if withComputeDomain {
		rel.ValuesFiles = []string{topoValues}
	}
	By("helm upgrade --install nvml-mock with NRI enabled (profile=" + p.Name + ")")
	Expect(h.Helm.UpgradeInstall(ctx, rel)).To(Succeed(), "helm upgrade --install nvml-mock with NRI (profile=%s)", p.Name)
}

// deployNRIAgent (re)creates the plain gpu-agent DaemonSet. It deletes any
// prior instance first so containers are created AFTER the nvml-mock daemon
// staged the overlay — NRI only injects at container-creation time — then waits
// for readiness, which fails unless every pod's ambient self-test passed.
func deployNRIAgent(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	Expect(h.Kube.Delete(ctx, assets.NRIGpuAgentManifest)).To(Succeed(), "delete previous gpu-agent DaemonSet")
	Expect(h.Kube.Apply(ctx, assets.NRIGpuAgentManifest)).To(Succeed(), "apply gpu-agent DaemonSet")
	assertions.WaitDaemonSetReady(ctx, h.Kube, nriWorkloadNS, nriAgentDaemonSet, config.ReadyTimeout(), config.PollInterval())
}

// assertAgentHasNoGPURequest mirrors run.sh's "gpu-agent has no nvidia.com/gpu
// resource request" guard: node-wide injection must not depend on the extended
// resource being requested.
func assertAgentHasNoGPURequest(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	out, err := h.Kube.KubectlCombined(ctx, "get", "daemonset", "-n", nriWorkloadNS, nriAgentDaemonSet,
		"-o", "jsonpath={.spec.template.spec.containers[0].resources}")
	Expect(err).NotTo(HaveOccurred(), "read gpu-agent container resources")
	Expect(out).NotTo(ContainSubstring(kube.GPUResourceName),
		"gpu-agent must not request %s; node-wide injection is ambient (resources=%s)", kube.GPUResourceName, out)
}

// assertAgentSeesGPUs reads `nvidia-smi -q -x` in a gpu-agent pod and asserts
// the NRI-injected overlay exposes exactly the profile's GPU count.
func assertAgentSeesGPUs(ctx context.Context, h *harness.Harness, expectedGPUs int) {
	GinkgoHelper()
	pod := firstNRIAgentPod(ctx, h)
	Expect(visibleGPUCount(ctx, h, pod)).To(Equal(expectedGPUs),
		"gpu-agent should see %d NRI-injected GPUs", expectedGPUs)
}

// assertNodeCliqueIdentities runs the staged `check-fabric` consumer inside the
// gpu-agent pod on every worker and asserts each node reports the clique /
// cluster UUID the topology overlay assigned to it — with no nvidia.com/gpu
// request and no MOCK_* env in the pod spec (identity comes only from NRI).
func assertNodeCliqueIdentities(ctx context.Context, h *harness.Harness, workers []cluster.Node) {
	GinkgoHelper()
	cliqueByNode := nriCliqueByNode(workers)
	for _, w := range workers {
		expected := cliqueByNode[w.Name]
		pod := nriAgentPodOnNode(ctx, h, w.Name)
		res, err := h.Kube.ExecSh(ctx, pod, "check-fabric 2>&1")
		Expect(err).NotTo(HaveOccurred(), "check-fabric on %s: %s", w.Name, res.Combined())
		out := res.Combined()
		Expect(out).To(ContainSubstring(fmt.Sprintf("cliqueId    : %d", expected)),
			"%s: expected cliqueId %d from check-fabric\n%s", w.Name, expected, strings.TrimSpace(out))
		Expect(strings.ToLower(out)).To(ContainSubstring(strings.ToLower("clusterUuid : "+nriDomainUUID)),
			"%s: expected clusterUuid %s from check-fabric\n%s", w.Name, nriDomainUUID, strings.TrimSpace(out))
	}
}

// deployDevicePluginOnWorkers reuses the validator scenario's device-plugin
// deployment and extends the capacity wait to every worker, so the composition
// specs can pin pods to a node knowing it advertises the full profile count.
func deployDevicePluginOnWorkers(ctx SpecContext, h *harness.Harness, workers []cluster.Node, expectedGPUs int) {
	GinkgoHelper()
	By("deploying the upstream device plugin alongside the NRI plugin")
	deployDevicePlugin(ctx, h, workers[0].Name, expectedGPUs)
	for _, w := range workers[1:] {
		assertions.WaitAllocatableGPU(ctx, h.Kube, w.Name, expectedGPUs, config.ReadyTimeout(), config.PollInterval())
	}
}

// applyNRIWorkload applies a manifest, waits for the pod to run, and registers
// cleanup so the next spec starts from a known allocation state.
func applyNRIWorkload(ctx context.Context, h *harness.Harness, manifest []byte, name string) kube.PodRef {
	GinkgoHelper()
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply workload %s", name)
	DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, manifest) }) //nolint:contextcheck // Ginkgo cleanup ctx is intentionally distinct from the outer spec ctx
	Eventually(func() (string, error) {
		return h.Kube.PodPhase(ctx, nriWorkloadNS, name)
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		Should(Equal("Running"), "workload %s never reached Running", name)
	return kube.PodRef{Namespace: nriWorkloadNS, Pod: name}
}

// nriWorkload is the shared shape of this scenario's workloads: a long-lived
// container on the standard image, in the workload namespace. Callers vary
// placement, annotations and the GPU request from there.
func nriWorkload(name string) pod.Spec {
	return pod.Spec{
		Name:      name,
		Namespace: nriWorkloadNS,
		Image:     nriWorkloadImage,
		// Kept alive so specs can exec into it. Traps SIGTERM so teardown does
		// not wait out the grace period: `sleep` as PID 1 installs no handler
		// and the kernel discards the signal. Backgrounding it leaves the shell
		// free to run the trap. `TERM`, not `SIGTERM`: dash is /bin/sh here and
		// rejects the prefixed name as a bad trap, installing nothing.
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{"trap 'exit 0' TERM; sleep 3600 & wait"},
	}
}

// nriAnyGPUNode places a pod on whichever node carries the mock's GPU label,
// unless the caller pinned one. Pinning matters whenever the pod observes
// node-local state: the runtime override file lives on a per-node hostPath, so
// an unpinned observer can be scheduled onto a different node from the workload
// it is meant to watch and will then read a node that never saw the allocation.
func nriAnyGPUNode(spec pod.Spec, node string) pod.Spec {
	if node != "" {
		spec.Node = node
		return spec
	}
	spec.NodeSelector = map[string]string{gpuPresentLabel: "true"}
	return spec
}

// nriRequestPodManifest renders a plain GPU-requesting pod: a resource request
// and nothing else. No hostPath, no MOCK_* env, no runtimeClassName, no
// annotation. This is the shape MEP-0002 exists to make work. An empty node
// leaves placement to the scheduler, which the oversubscription spec relies on.
func nriRequestPodManifest(name, node string, gpus int, annotations ...map[string]string) []byte {
	spec := nriWorkload(name)
	spec.Node = node
	spec.GPUs = gpus
	spec.Annotations = map[string]string{}
	for _, set := range annotations {
		for key, value := range set {
			spec.Annotations[key] = value
		}
	}
	return spec.Render()
}

// nriAnnotatedPodManifest renders a pod that opts into device nodes via the
// annotation and requests no GPU resources. Pass a node to pin it there.
func nriAnnotatedPodManifest(name string, node ...string) []byte {
	pinned := ""
	if len(node) > 0 {
		pinned = node[0]
	}
	spec := nriAnyGPUNode(nriWorkload(name), pinned)
	spec.Annotations = map[string]string{nriDeviceAnnotation: "true"}
	return spec.Render()
}

// nriPlainPodManifest renders a pod that opts into nothing: no GPU request and
// no device annotation. It still receives the overlay and the environment,
// which is the node-wide NRI contract.
func nriPlainPodManifest(name string) []byte {
	return nriAnyGPUNode(nriWorkload(name), "").Render()
}

// nriMinimalIBPodManifest renders a run-to-completion pod on a minimal image
// that invokes one IB tool from the overlay by absolute path. There is no
// `sleep` wrapper and no `sh -c`: the image has no shell, which is the whole
// point of using it. The label is what the spec selects on to read the pod's
// logs, since it has already exited by the time they are collected.
func nriMinimalIBPodManifest(name, tool string, args ...string) []byte {
	spec := nriAnyGPUNode(nriWorkload(name), "")
	spec.Image = nriMinimalImage
	// Replaces the keepalive shell wholesale; this image has none, so the trap
	// script would reach the tool as arguments.
	spec.Command = append([]string{nriOverlayBinDir + "/" + tool}, args...)
	spec.Args = nil
	spec.Labels = map[string]string{"app": name}
	return spec.Render()
}

// runIBToolInMinimalImage applies the pod, waits for it to terminate, and
// returns its final phase together with its logs. It waits for a terminal
// phase rather than Running because the pod is expected to run once and exit;
// both outcomes are returned so the caller asserts on the output instead of on
// the wait succeeding.
func runIBToolInMinimalImage(ctx context.Context, h *harness.Harness, name, tool string, args ...string) (string, string) {
	GinkgoHelper()
	manifest := nriMinimalIBPodManifest(name, tool, args...)
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply %s", name)
	DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, manifest) }) //nolint:contextcheck // Ginkgo cleanup ctx is intentionally distinct from the outer spec ctx

	var phase string
	Eventually(func() (string, error) {
		var err error
		phase, err = h.Kube.PodPhase(ctx, nriWorkloadNS, name)
		return phase, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		Should(BeElementOf("Succeeded", "Failed"),
			"%s never reached a terminal phase", name)

	logs, err := h.Kube.Logs(ctx, nriWorkloadNS, "app="+name, 100)
	Expect(err).NotTo(HaveOccurred(), "read logs from %s", name)
	return phase, logs
}

// nriDeviceSource reads NVML_MOCK_DEVICE_SOURCE from inside the pod. The
// variable is set by the CDI spec's containerEdits, so an empty string means
// the container was not served through CDI. `printenv` returns non-zero when
// the variable is unset, hence ExecSh with an echo that always succeeds.
func nriDeviceSource(ctx context.Context, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	res, err := h.Kube.ExecSh(ctx, pod, `echo -n "$NVML_MOCK_DEVICE_SOURCE"`)
	Expect(err).NotTo(HaveOccurred(), "read NVML_MOCK_DEVICE_SOURCE from %s", pod.Pod)
	return strings.TrimSpace(res.Stdout)
}

// installNRICDIChart reinstalls the release with the device opt-in switched to
// CDI. Everything else matches installNRIChart, so a difference observed
// between the two is attributable to the mechanism and nothing else.
func installNRICDIChart(ctx context.Context, h *harness.Harness, p profile.Profile) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	rel := helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set: map[string]string{
			"gpu.count":                 strconv.Itoa(p.ExpectedGPUs()),
			"gpu.profile":               p.Name,
			"image.repository":          repo,
			"image.tag":                 tag,
			"nri.enabled":               "true",
			"nri.deviceInjectionMode":   "cdi",
			"imex.mockChannels.enabled": "false",
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	}
	By("helm upgrade --install nvml-mock with NRI device injection mode=cdi (profile=" + p.Name + ")")
	Expect(h.Helm.UpgradeInstall(ctx, rel)).To(Succeed(), "helm upgrade --install nvml-mock with CDI injection (profile=%s)", p.Name)
}

// nriImexPodManifest renders a pod pinned to one node that optionally opts into
// mock IMEX channel injection. It requests no GPU resources.
func nriImexPodManifest(name, node string, wantChannels bool) []byte {
	spec := nriWorkload(name)
	spec.Node = node
	spec.Annotations = map[string]string{}
	if wantChannels {
		spec.Annotations[nriImexAnnotation] = "true"
	}
	return spec.Render()
}

// imexChannelNames lists the channel nodes visible inside a pod.
func imexChannelNames(ctx context.Context, h *harness.Harness, pod kube.PodRef) []string {
	GinkgoHelper()
	res, err := h.Kube.ExecSh(ctx, pod, `ls `+nriImexChannelDir)
	Expect(err).NotTo(HaveOccurred(), "list %s in %s: %s", nriImexChannelDir, pod.Pod, res.Combined())
	var names []string
	for _, line := range strings.Split(res.Combined(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "channel") {
			names = append(names, line)
		}
	}
	return names
}

// advertisedImexMajor reads the channel major out of the proc-devices file that
// imex.mockChannels renders for the DRA driver, as staged on the node itself.
func advertisedImexMajor(ctx context.Context, h *harness.Harness, node string) int {
	GinkgoHelper()
	pod := nriMockPodOnNode(ctx, h, node)
	res, err := h.Kube.ExecSh(ctx, pod, `cat /host/var/lib/nvml-mock/imex/proc-devices`)
	Expect(err).NotTo(HaveOccurred(), "read rendered proc-devices on %s: %s", node, res.Combined())

	for _, line := range strings.Split(res.Combined(), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[1] == "nvidia-caps-imex-channels" {
			major, parseErr := strconv.Atoi(fields[0])
			Expect(parseErr).NotTo(HaveOccurred(), "parse major from %q", line)
			return major
		}
	}
	Fail("rendered proc-devices carries no nvidia-caps-imex-channels entry:\n" + res.Combined())
	return 0
}

// nriMockPodOnNode resolves the main nvml-mock DaemonSet pod on a given node.
func nriMockPodOnNode(ctx context.Context, h *harness.Harness, node string) kube.PodRef {
	GinkgoHelper()
	names, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, "app.kubernetes.io/name=nvml-mock")
	Expect(err).NotTo(HaveOccurred(), "list nvml-mock pods")
	for _, name := range names {
		on, nodeErr := h.Kube.PodNode(ctx, nvmlMockNamespace, name)
		Expect(nodeErr).NotTo(HaveOccurred(), "read node of %s", name)
		if on == node {
			return kube.PodRef{Namespace: nvmlMockNamespace, Pod: name}
		}
	}
	Fail("no running nvml-mock pod on node " + node)
	return kube.PodRef{}
}

// allocatedGPUUUID returns the UUID the device plugin allocated to the pod, as
// the kubelet wrote it into the container environment.
func allocatedGPUUUID(ctx context.Context, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	res, err := h.Kube.ExecSh(ctx, pod, `printf %s "${NVIDIA_VISIBLE_DEVICES:-}"`)
	Expect(err).NotTo(HaveOccurred(), "read NVIDIA_VISIBLE_DEVICES in %s: %s", pod.Pod, res.Combined())
	uuid := strings.TrimSpace(res.Combined())
	Expect(uuid).NotTo(BeEmpty(), "device plugin set no NVIDIA_VISIBLE_DEVICES on %s", pod.Pod)
	return uuid
}

// visibleGPUUUIDs returns the UUIDs nvidia-smi reports inside the pod, in
// nvidia-smi's own GPU order — the order its --id indices refer to.
func visibleGPUUUIDs(ctx context.Context, h *harness.Harness, pod kube.PodRef) []string {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, pod)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x in %s", pod.Pod)
	return snap.UUIDs()
}

// visibleGPUCount reports how many GPUs nvidia-smi describes inside the pod.
func visibleGPUCount(ctx context.Context, h *harness.Harness, pod kube.PodRef) int {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, pod)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x in %s", pod.Pod)
	return snap.Count()
}

func firstNRIAgentPod(ctx context.Context, h *harness.Harness) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		pods, err := h.Kube.RunningPodNames(ctx, nriWorkloadNS, nriAgentSelector)
		if err != nil || len(pods) == 0 {
			return "", err
		}
		name = pods[0]
		return name, nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running gpu-agent pod found")
	return kube.PodRef{Namespace: nriWorkloadNS, Pod: name}
}

func nriAgentPodOnNode(ctx context.Context, h *harness.Harness, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		pods, err := h.Kube.RunningPodNames(ctx, nriWorkloadNS, nriAgentSelector)
		if err != nil {
			return "", err
		}
		for _, pod := range pods {
			podNode, err := h.Kube.PodNode(ctx, nriWorkloadNS, pod)
			if err != nil {
				return "", err
			}
			if podNode == node {
				name = pod
				return name, nil
			}
		}
		return "", nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running gpu-agent pod on node %s", node)
	return kube.PodRef{Namespace: nriWorkloadNS, Pod: name}
}

func writeNRITopologyValues(workers []cluster.Node) string {
	GinkgoHelper()
	path, err := assets.WriteTemp("nri-topology-*.yaml", nriTopologyValues(workers))
	Expect(err).NotTo(HaveOccurred(), "write NRI topology values")
	return path
}

// nriTopologyValues renders a two-clique ComputeDomain overlay values fragment
// from the discovered worker names (first half -> clique 0, second half ->
// clique 1), keeping the overlay cluster-name agnostic.
func nriTopologyValues(workers []cluster.Node) []byte {
	mid := (len(workers) + 1) / 2
	var b strings.Builder
	b.WriteString("# Generated by the node-wide NRI injection e2e scenario.\n")
	b.WriteString("topology:\n")
	b.WriteString("  enabled: true\n")
	b.WriteString("  domains:\n")
	b.WriteString("    - name: " + nriDomainName + "\n")
	b.WriteString("      uuid: \"" + nriDomainUUID + "\"\n")
	b.WriteString("      cliques:\n")
	for cliqueID, group := range [][]cluster.Node{workers[:mid], workers[mid:]} {
		b.WriteString(fmt.Sprintf("        - id: %d\n", cliqueID))
		b.WriteString("          nodes:\n")
		for _, n := range group {
			b.WriteString("            - " + n.Name + "\n")
		}
	}
	return []byte(b.String())
}

func nriCliqueByNode(workers []cluster.Node) map[string]int {
	mid := (len(workers) + 1) / 2
	m := make(map[string]int, len(workers))
	for _, n := range workers[:mid] {
		m[n.Name] = 0
	}
	for _, n := range workers[mid:] {
		m[n.Name] = 1
	}
	return m
}

// indexOfGPUUUID returns the position of uuid in the observer's GPU list, or -1.
// nvidia-smi indexes by position in that list, which is what the per-GPU
// readings take.
func indexOfGPUUUID(uuids []string, uuid string) int {
	for i, u := range uuids {
		if u == uuid {
			return i
		}
	}
	return -1
}
