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
				node = migTargetNode(ctx, h)
			})

			// The mock's own surface, before any consumer. Asserting through the
			// real nvidia-smi binary rather than by re-reading the config is the
			// point: it is the same NVML MIG enumeration the device plugin walks,
			// so a failure here localises the problem to the mock instead of the
			// plugin wiring.
			It("enumerates the declared partitions through nvidia-smi", Label("mig-nvml"), func(ctx SpecContext) {
				total := p.ExpectedGPUs() * partitions

				By("nvidia-smi -q -x reports MIG on with the declared partition count")
				snap := migSnapshotOnNode(ctx, h, node)
				count, err := snap.MIGPartitionCount()
				Expect(err).NotTo(HaveOccurred(), "read the MIG partitions nvidia-smi -q -x reports")
				Expect(count).To(Equal(total),
					"a %d-GPU node declaring %d partitions per GPU should report %d MIG devices",
					p.ExpectedGPUs(), partitions, total)

				for gpu := range p.ExpectedGPUs() {
					g := migGPU(snap, gpu)
					// The mode is a fact the text listings cannot state: an
					// unpartitioned board and one whose partitions failed to
					// enumerate both list nothing.
					Expect(g.MIGEnabled()).To(BeTrue(),
						"GPU %d reports MIG mode %q (pending %q), not Enabled",
						gpu, g.MIGMode(), g.MIGModePending())
					Expect(migPartitionsOfGPU(snap, gpu)).To(HaveLen(partitions),
						"GPU %d declares %d partitions but nvidia-smi -q -x reports a different number", gpu, partitions)
				}

				// A board that offers the profile and one that has been carved
				// into it are told apart by the size of what came out, which
				// only the document reports per partition.
				By("each partition is sized as a slice of its own board")
				for gpu := range p.ExpectedGPUs() {
					assertMIGPartitionSizing(migGPU(snap, gpu), gpu, migPartitionsOfGPU(snap, gpu))
				}

				// Profile name and MIG UUID appear nowhere in `nvidia-smi -q -x`,
				// so both readings can only come from the text listing.
				By("nvidia-smi -L names every partition with the declared profile and a unique MIG UUID")
				devices := migDevicesOnNode(ctx, h, node)
				Expect(devices).To(HaveLen(total),
					"nvidia-smi -L should list the %d partitions nvidia-smi -q -x counts; the two NVML paths disagree",
					total)
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

			// `nvidia-smi mig` is the surface operators and nvidia-mig-parted
			// drive, and it reaches NVML through different entry points than
			// `nvidia-smi -L`: a mock whose -L enumeration is perfect can still
			// fail every one of these listings. Asserting nothing but a clean
			// exit is worth its own spec because it localises that break in one
			// line, before the assertions below have to explain themselves.
			It("answers every nvidia-smi mig listing without error", Label("mig-nvml"), func(ctx SpecContext) {
				listings := map[string]string{}
				for _, flag := range []string{"-lgi", "-lgip", "-lci", "-lcip"} {
					listings[flag] = migListingOnNode(ctx, h, node, flag)
				}

				// The compute-instance listings are read by row count alone.
				// Their columns restate the GPU-instance table, and the counts
				// below are the only facts about them a migStrategy=single
				// consumer depends on: the partition is unusable without a
				// compute instance, whatever the rest of the row says.
				total := p.ExpectedGPUs() * partitions

				// -lci enumerates the compute instances that exist, and
				// migStrategy=single gives every partition exactly one.
				lci, err := nvidiasmi.CountMigTableRows(listings["-lci"], nvidiasmi.MigComputeInstances)
				Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lci:\n%s", listings["-lci"])
				Expect(lci).To(Equal(total),
					"nvidia-smi mig -lci should list one compute instance per partition on a %d-GPU node with %d partitions each:\n%s",
					p.ExpectedGPUs(), partitions, listings["-lci"])

				// -lcip is a catalogue, not an inventory: it lists the compute
				// instance profiles each partition offers, and how many that is
				// varies by board. The portable claim is that no partition is
				// missing from it.
				lcip, err := nvidiasmi.CountMigTableRows(listings["-lcip"], nvidiasmi.MigComputeInstanceProfiles)
				Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lcip:\n%s", listings["-lcip"])
				Expect(lcip).To(BeNumerically(">=", total),
					"nvidia-smi mig -lcip should offer at least one compute instance profile for each of the %d partitions:\n%s",
					total, listings["-lcip"])
			})

			// Two NVML paths describing one board must not disagree. `-lgi` is
			// what a partitioning tool reads back to confirm its work, so a
			// board that enumerates correctly under `-q -x` and wrongly here is
			// a board those tools cannot drive.
			It("lists the same partitions through nvidia-smi mig -lgi as nvidia-smi -q -x reports", Label("mig-nvml"), func(ctx SpecContext) {
				instances := migGPUInstancesOnNode(ctx, h, node)
				snap := migSnapshotOnNode(ctx, h, node)

				Expect(instances).To(HaveLen(p.ExpectedGPUs()*partitions),
					"a %d-GPU node declaring %d partitions per GPU should list %d GPU instances",
					p.ExpectedGPUs(), partitions, p.ExpectedGPUs()*partitions)

				for gpu := range p.ExpectedGPUs() {
					onGPU := nvidiasmi.MigInstancesOfGPU(instances, gpu)
					reported := migPartitionsOfGPU(snap, gpu)
					Expect(onGPU).To(HaveLen(partitions),
						"GPU %d declares %d partitions but nvidia-smi mig -lgi lists %d",
						gpu, partitions, len(onGPU))
					Expect(onGPU).To(HaveLen(len(reported)),
						"nvidia-smi mig -lgi and nvidia-smi -q -x disagree about GPU %d: %d instances against %d mig_device elements",
						gpu, len(onGPU), len(reported))

					ids := map[int]bool{}
					placements := map[string]bool{}
					for _, i := range onGPU {
						Expect(i.Profile).To(Equal(p.MIGDeviceProfile()),
							"GPU instance %d on GPU %d carries profile %q, not the declared %q",
							i.InstanceID, gpu, i.Profile, p.MIGDeviceProfile())
						// The capability table under /dev/nvidia-caps is keyed
						// by instance ID, so a duplicate would collapse two
						// partitions onto one cap node.
						Expect(ids).NotTo(HaveKey(i.InstanceID),
							"GPU %d reports instance ID %d twice", gpu, i.InstanceID)
						ids[i.InstanceID] = true
						// Overlapping placements would mean two partitions
						// claiming the same slice of the board.
						Expect(placements).NotTo(HaveKey(i.Placement),
							"GPU %d reports placement %s twice", gpu, i.Placement)
						placements[i.Placement] = true
					}

					// Equal counts are not the same partitions. The instance IDs
					// are what the two paths must agree on, because they are what
					// a partitioning tool and the capability table below both
					// address a partition by.
					for _, part := range reported {
						Expect(ids).To(HaveKey(part.GPUInstanceID),
							"nvidia-smi -q -x reports instance %d on GPU %d, which nvidia-smi mig -lgi does not list",
							part.GPUInstanceID, gpu)
					}
				}
			})

			// The occupancy signal. A board that merely offers the profile and
			// one that has been carved into it look identical by profile name;
			// Free/Total is what tells them apart, and it is what a
			// scheduler-facing consumer reads to decide the board is spoken for.
			It("reports the declared profile as consumed in nvidia-smi mig -lgip", Label("mig-nvml"), func(ctx SpecContext) {
				listing := migListingOnNode(ctx, h, node, "-lgip")
				profiles, err := nvidiasmi.ListMigProfileCapacity(listing)
				Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lgip:\n%s", listing)

				for gpu := range p.ExpectedGPUs() {
					capacity, ok := nvidiasmi.MigCapacityFor(profiles, gpu, p.MIGDeviceProfile())
					Expect(ok).To(BeTrue(),
						"nvidia-smi mig -lgip offers no %s profile on GPU %d:\n%s",
						p.MIGDeviceProfile(), gpu, listing)
					// Asserting the taken count rather than a bare zero-free
					// keeps the claim true of a profile that carves only part
					// of its board; on the profiles this scenario runs, which
					// fill it, the two say the same thing.
					Expect(capacity.Total-capacity.Free).To(Equal(partitions),
						"GPU %d is carved into %d %s partitions, so -lgip should show that many taken, got %d free of %d",
						gpu, partitions, p.MIGDeviceProfile(), capacity.Free, capacity.Total)
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

				// Named against the instance IDs nvidia-smi reports, not merely
				// against the GPUs that have partitions. The document is the
				// only nvidia-smi surface that carries those IDs alongside the
				// partition count, and pinning them is what catches the drift
				// this scenario exists for: an agent keying the table by IDs the
				// engine did not assign stages a table of the right size whose
				// every entry guards the wrong partition.
				snap := migSnapshotOnNode(ctx, h, node)
				for gpu := range p.ExpectedGPUs() {
					reported := migPartitionsOfGPU(snap, gpu)
					Expect(reported).NotTo(BeEmpty(),
						"GPU %d reports no partitions, so this spec would assert nothing about it", gpu)

					for _, part := range reported {
						// One line per GPU instance and one per compute instance.
						// The device plugin looks up both and refuses the
						// partition if either is missing.
						Expect(table).To(MatchRegexp(`(?m)^gpu%d/gi%d/access \d+$`, gpu, part.GPUInstanceID),
							"no GPU-instance capability staged for GPU %d instance %d, which nvidia-smi reports as a partition:\n%s",
							gpu, part.GPUInstanceID, table)
						Expect(table).To(MatchRegexp(`(?m)^gpu%d/gi%d/ci%d/access \d+$`,
							gpu, part.GPUInstanceID, part.ComputeInstanceID),
							"no compute-instance capability staged for GPU %d instance %d/%d:\n%s",
							gpu, part.GPUInstanceID, part.ComputeInstanceID, table)
					}
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

				// The document states the mode outright, where a text listing
				// could only show the absence of partition lines — which a
				// board whose partitions failed to enumerate produces too.
				snap := migSnapshotOnNode(ctx, h, node)
				for gpu := range p.ExpectedGPUs() {
					g := migGPU(snap, gpu)
					Expect(g.MIGMode()).To(Equal("Disabled"),
						"GPU %d should report MIG off, got current_mig %q", gpu, g.MIGMode())
					// A GPU reporting one mode with the other pending is
					// mid-switch, which is neither state the device plugin can
					// be held to.
					Expect(g.MIGModePending()).To(Equal("Disabled"),
						"GPU %d has a MIG mode switch outstanding: current %q, pending %q",
						gpu, g.MIGMode(), g.MIGModePending())
					Expect(migPartitionsOfGPU(snap, gpu)).To(BeEmpty(),
						"MIG is off on GPU %d, so nvidia-smi -q -x must report no mig_device elements", gpu)
				}

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
			// right thing. A pod that schedules onto a MIG resource must be
			// given one real partition's identity and the cap nodes guarding
			// it — which is what fails if the agent's instance IDs disagree
			// with the engine's.
			//
			// The claim is about what the pod was handed, not what its own NVML
			// reports: on this path the in-container library runs on its
			// compiled-in defaults, because the container toolkit drops the env
			// that would point it at this node's profile (#747), so it describes
			// a stock mock GPU no matter how the node is partitioned. The
			// allocation is therefore asserted where the runtime honours it.
			It("gives a scheduled pod exactly one MIG partition", Label("mig-allocation"), func(ctx SpecContext) {
				deployMIGDevicePlugin(ctx, h, node, p.ExpectedGPUs()*partitions)

				// The `nvidia-smi -L` listing, not the -q -x document the other
				// specs read: what the plugin hands out is a MIG UUID, and the
				// document carries neither that nor the profile name to check
				// it against.
				onNode := migDevicesOnNode(ctx, h, node)
				workload := applyMIGWorkload(ctx, h, "mig-single", node)

				res, err := h.Kube.ExecSh(ctx, workload, `printf %s "${NVIDIA_VISIBLE_DEVICES:-}"`)
				Expect(err).NotTo(HaveOccurred(), "read NVIDIA_VISIBLE_DEVICES: %s", res.Combined())
				allocated := strings.TrimSpace(res.Combined())

				// A partition's own identity, not its parent's: a plugin handing
				// out whole-GPU UUIDs would have filled this in just as well.
				Expect(allocated).To(HavePrefix("MIG-"),
					"a migStrategy=single allocation must name a partition, got %q", allocated)
				// And a partition this node actually has. Pinning it to the
				// nvidia-smi listing is what ties the allocation back to the
				// engine's own enumeration rather than to a well-formed string.
				Expect(migProfileOf(onNode, allocated)).To(Equal(p.MIGDeviceProfile()),
					"allocated %q should be one of the %d partitions nvidia-smi reports on %s",
					allocated, len(onNode), node)

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

			// The runtime knob. Asserted through nvidia-smi on the node rather
			// than through the config, so what is being checked is that a real
			// consumer's NVML enumeration follows the override file — the whole
			// point of repartitioning without a restart.
			//
			// Allocation is deliberately not asserted: the capability surface
			// under /dev/nvidia-caps is staged once at startup, while a
			// repartition draws fresh instance IDs, so the device plugin
			// cannot hand out anything a runtime repartition produced.
			//
			// Kept last in this Ordered context: the override is a per-node
			// file that outlives the spec, so a failure between the write and
			// the restore below would leave every spec above it asserting
			// against a layout the chart never installed.
			It("follows a runtime repartition through nvml-mock-ctl", Label("mig-runtime"), func(ctx SpecContext) {
				// Every command is pinned to the node the assertions read.
				// Overrides are staged per node, so an unpinned write can land
				// on the mock pod of a different node than the one nvidia-smi
				// is questioned on, which both fails the spec and leaves a live
				// override behind on a node no later reset visits.
				DeferCleanup(func(ctx SpecContext) {
					resetRuntimeOverridesOnNode(ctx, h, node)
				})

				// Repartitioning down to one partition only asserts anything
				// on a profile that declares more than one: at one, GPU 0
				// already reports the count the override asks for and the
				// restore below is satisfied by the state never changing.
				Expect(partitions).To(BeNumerically(">", 1),
					"profile %s declares %d partition(s) per GPU, which cannot distinguish a repartition from an inert override",
					p.Name, partitions)

				By("re-lay-out GPU 0 as a single " + p.MIGDeviceProfile() + " partition")
				nvmlMockCtlOnNode(ctx, h, node, "mig", "--gpu", "0", "enable",
					"--profile", p.MIGDeviceProfile(), "--count", "1")

				Eventually(func() int {
					return len(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 0))
				}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
					Should(Equal(1), "GPU 0 should report the single partition the CLI asked for")

				// A repartition that reaches `nvidia-smi -q -x` but not
				// `mig -lgi` is the inconsistency two NVML paths over one
				// layout can produce, and -lgi is the one a partitioning tool
				// reads back. No Eventually: the document above has already
				// waited out the config TTL, so the layout this reads is settled.
				Expect(nvidiasmi.MigInstancesOfGPU(migGPUInstancesOnNode(ctx, h, node), 0)).To(HaveLen(1),
					"nvidia-smi mig -lgi should report the single partition nvidia-smi -q -x already shows on GPU 0")

				// The occupancy numbers have to come from the instances that
				// exist, not from the layout the chart declared: a board this
				// spec has just carved down to one partition is the only state
				// that tells the two apart. The spec above reads them on a
				// board where they agree.
				capacityListing := migListingOnNode(ctx, h, node, "-lgip")
				capacities, err := nvidiasmi.ListMigProfileCapacity(capacityListing)
				Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lgip:\n%s", capacityListing)
				capacity, ok := nvidiasmi.MigCapacityFor(capacities, 0, p.MIGDeviceProfile())
				Expect(ok).To(BeTrue(),
					"nvidia-smi mig -lgip offers no %s profile on GPU 0:\n%s", p.MIGDeviceProfile(), capacityListing)
				Expect(capacity.Total-capacity.Free).To(Equal(1),
					"GPU 0 now holds one %s partition, so -lgip should show one taken, got %d free of %d",
					p.MIGDeviceProfile(), capacity.Free, capacity.Total)

				Expect(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 1)).To(HaveLen(partitions),
					"a repartition of GPU 0 must not disturb its neighbours")

				By("clear the override and let the profile's declared layout come back")
				resetRuntimeOverridesOnNode(ctx, h, node)

				Eventually(func() int {
					return len(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 0))
				}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
					Should(Equal(partitions), "removing the override should restore the declared layout")
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

// migTargetNode picks the node every spec in this scenario asserts about. It
// has to be a node the device plugin runs on, which is the sgpu-labelled fleet:
// both device-plugin manifests select on that label, and the specs below read
// the partition count it advertises.
//
// Deriving it from the mock instead is what made this scenario flaky. The chart
// installs nvml-mock with an empty nodeSelector and a blanket toleration, so a
// mock pod lands on the control plane too, and the first-pod-by-name tiebreak
// picked that pod roughly one profile in three. Every spec that only questions
// the mock passes on such a node, because the mock is there; the specs that read
// nvidia.com/gpu cannot, because the device plugin is not.
func migTargetNode(ctx context.Context, h *harness.Harness) string {
	GinkgoHelper()
	nodes, err := h.Cluster.Nodes(ctx)
	Expect(err).NotTo(HaveOccurred(), "list cluster nodes")

	var targets []string
	for _, n := range nodes {
		v, ok, err := h.Kube.NodeLabel(ctx, n.Name, sgpuNodeLabel)
		Expect(err).NotTo(HaveOccurred(), "read label %s on node %s", sgpuNodeLabel, n.Name)
		if ok && v == sgpuNodeLabelValue {
			targets = append(targets, n.Name)
		}
	}
	// Nodes come back sorted by name, so this is the same node on every run and
	// across both profiles sharing one cluster.
	Expect(targets).NotTo(BeEmpty(),
		"no node carries %s=%s, the label the device-plugin manifests select on",
		sgpuNodeLabel, sgpuNodeLabelValue)
	return targets[0]
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

// migSnapshotOnNode reads an `nvidia-smi -q -x` document from inside the
// nvml-mock pod on node, which sees the whole board. This is where the MIG mode
// and the partition counts come from; the `nvidia-smi -L` listing below answers
// the two readings the document does not carry.
//
// The exec and the decode are asserted here, which is safe inside an
// Eventually: both only fail when nvidia-smi itself is broken, and that is not
// something a spec waits for.
func migSnapshotOnNode(ctx context.Context, h *harness.Harness, node string) nvidiasmi.Snapshot {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, nvmlPodOnNode(ctx, h, node))
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x on node %s", node)
	return snap
}

// migDevicesOnNode lists the partitions `nvidia-smi -L` reports from inside the
// nvml-mock pod on node, which sees the whole board.
//
// Only the specs needing a partition's profile name or its MIG UUID read this
// listing: `nvidia-smi -q -x` prints neither anywhere in its <mig_devices>
// block, so this is the only surface either appears on.
func migDevicesOnNode(ctx context.Context, h *harness.Harness, node string) []nvidiasmi.MigDevice {
	GinkgoHelper()
	return migDevicesInPod(ctx, h, nvmlPodOnNode(ctx, h, node))
}

// migListingOnNode runs one `nvidia-smi mig` listing in the nvml-mock pod on
// node and returns its output. These commands exit non-zero when NVML refuses
// a query and print no table at all, so the error check is both the
// exit-status assertion and what keeps a parser from being handed an error
// message to find no partitions in.
func migListingOnNode(ctx context.Context, h *harness.Harness, node, flag string) string {
	GinkgoHelper()
	target := nvmlPodOnNode(ctx, h, node)
	By("nvidia-smi mig " + flag)
	res, err := h.Kube.Exec(ctx, target, "nvidia-smi", "mig", flag)
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi mig %s in %s exited non-zero: %s",
		flag, target.Pod, res.Combined())
	return res.Combined()
}

// migGPUInstancesOnNode lists the GPU instances `nvidia-smi mig -lgi` reports
// from inside the nvml-mock pod on node, which sees the whole board.
func migGPUInstancesOnNode(ctx context.Context, h *harness.Harness, node string) []nvidiasmi.MigGPUInstance {
	GinkgoHelper()
	listing := migListingOnNode(ctx, h, node, "-lgi")
	instances, err := nvidiasmi.ListMigGPUInstances(listing)
	Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lgi:\n%s", listing)
	return instances
}

// migDevicesInPod lists the partitions nvidia-smi reports inside a pod.
func migDevicesInPod(ctx context.Context, h *harness.Harness, target kube.PodRef) []nvidiasmi.MigDevice {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, target, "nvidia-smi", "-L")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L in %s: %s", target.Pod, res.Combined())
	return nvidiasmi.ListMigDevices(res.Combined())
}

// migProfileOf reports the profile of the partition carrying uuid, or "" when
// no partition on the node has it. Returning "" rather than failing lets a
// caller assert the expected profile and the UUID's membership in one step.
func migProfileOf(devices []nvidiasmi.MigDevice, uuid string) string {
	for _, d := range devices {
		if d.UUID == uuid {
			return d.Profile
		}
	}
	return ""
}

// assertMIGPartitionSizing asserts each partition is reported as a slice of the
// board it was carved from, rather than as a handle that answers with its
// parent's figures.
//
// This is the claim no text listing can make: `nvidia-smi -L` and
// `nvidia-smi mig -lgi` name a partition's profile, and a mock whose MIG device
// handles fell through to the parent device would satisfy both while reporting
// every "1g" partition as the whole board — which is what a consumer sizing a
// workload to its partition reads and gets wrong.
//
// The claim is deliberately a proper-fraction one rather than the profile
// name's own figure: a slice's framebuffer is not the round number the name
// carries. A 1g.5gb A100 partition holds 4864 MiB, because the name rounds the
// raw allocation up to an eighth of the board.
func assertMIGPartitionSizing(gpu nvidiasmi.GPU, index int, partitions []nvidiasmi.MIGPartition) {
	GinkgoHelper()

	boardMiB, ok := gpu.MemoryTotalMiB()
	Expect(ok).To(BeTrue(), "GPU %d reports no framebuffer total to size its partitions against", index)

	slicedMiB := 0
	for _, part := range partitions {
		Expect(part.MemoryTotalMiB).To(SatisfyAll(
			BeNumerically(">", 0), BeNumerically("<", boardMiB)),
			"GPU %d instance %d reports %d MiB, which is not a slice of the board's %d MiB",
			index, part.GPUInstanceID, part.MemoryTotalMiB, boardMiB)
		// A partition with no SMs can run nothing, and the count comes from the
		// MIG attributes block rather than from the memory getters above, so it
		// is the one reading that says that block was answered at all.
		Expect(part.MultiprocessorCount).To(BeNumerically(">", 0),
			"GPU %d instance %d reports no multiprocessors", index, part.GPUInstanceID)
		slicedMiB += part.MemoryTotalMiB
	}

	Expect(slicedMiB).To(BeNumerically("<=", boardMiB),
		"GPU %d hands out %d MiB across %d partitions, more than the %d MiB board holds",
		index, slicedMiB, len(partitions), boardMiB)
}

// migGPU narrows a document to one GPU's readings, in nvidia-smi's own GPU
// order — the same index `-i N` takes.
func migGPU(snap nvidiasmi.Snapshot, gpu int) nvidiasmi.GPU {
	GinkgoHelper()
	g, err := snap.GPU(gpu)
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -q -x should describe GPU %d", gpu)
	return g
}

// migPartitionsOfGPU narrows a document's <mig_devices> blocks to one GPU, so a
// per-device repartition can be asserted without the other GPUs' partitions
// counting towards it.
func migPartitionsOfGPU(snap nvidiasmi.Snapshot, gpu int) []nvidiasmi.MIGPartition {
	GinkgoHelper()
	partitions, err := migGPU(snap, gpu).MIGPartitions()
	Expect(err).NotTo(HaveOccurred(),
		"read the MIG partitions nvidia-smi -q -x reports for GPU %d", gpu)
	return partitions
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
