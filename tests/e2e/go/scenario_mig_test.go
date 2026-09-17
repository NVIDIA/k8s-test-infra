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

// migLayout is a partitioning this suite installs. No chart profile declares
// one, so the suite states its own and asserts against the same values it
// passed to helm, which is what keeps the install and the expectations from
// drifting apart.
type migLayout struct {
	Profile string // MIG profile name, e.g. "1g.10gb"
	Count   int    // instances per GPU
}

// migLayouts fills each board with its smallest slice — the only shape
// migStrategy=single can publish under a single resource name. Every
// MIG-capable profile needs an entry, and a capable board missing one fails
// the suite instead of skipping: a silent skip is how these boards went
// untested before.
// A slice is named for the share of its own board it holds, so the names below
// follow each profile's declared memory rather than NVIDIA's published listing
// for that board: the b200 profile describes a 192GiB board and so offers
// 1g.24gb where NVIDIA publishes 1g.23gb for a 180GB one. Read a board's names
// off `nvidia-smi mig -lgip` rather than the MIG guide.
var migLayouts = map[string]migLayout{
	"a100":  {Profile: "1g.5gb", Count: 7},
	"h100":  {Profile: "1g.10gb", Count: 7},
	"b200":  {Profile: "1g.24gb", Count: 7},
	"gb200": {Profile: "1g.24gb", Count: 7},
	"gb300": {Profile: "1g.36gb", Count: 7},
}

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
				layout     migLayout
				node       string
				partitions int
			)

			BeforeAll(func(ctx SpecContext) {
				p = loadProfile(name)
				if !p.MIGCapable() {
					Skip("profile " + name + " is not a MIG-capable board")
				}
				var declared bool
				layout, declared = migLayouts[name]
				Expect(declared).To(BeTrue(),
					"profile %s is a MIG-capable board with no entry in migLayouts; "+
						"add one rather than leaving the board untested", name)
				partitions = layout.Count

				installMIGChart(ctx, h, p, layout)
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
				Expect(nvidiasmi.MigProfiles(devices)).To(Equal([]string{layout.Profile}),
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

			// The compute-instance half of `nvidia-smi mig`, which the two specs
			// below do not reach: they read -lgi and -lgip, and a mock can
			// answer both perfectly while failing every compute-instance query.
			//
			// Both listings are read by row count alone. Their columns restate
			// the GPU-instance table, and the counts here are the only facts
			// about them a migStrategy=single consumer depends on: a partition
			// is unusable without a compute instance, whatever the rest of the
			// row says.
			It("lists a compute instance for every partition", Label("mig-nvml"), func(ctx SpecContext) {
				total := p.ExpectedGPUs() * partitions

				// -lci enumerates the compute instances that exist, and
				// migStrategy=single gives every partition exactly one.
				lciListing := migListingOnNode(ctx, h, node, "-lci")
				lci, err := nvidiasmi.CountMigTableRows(lciListing, nvidiasmi.MigComputeInstances)
				Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lci:\n%s", lciListing)
				Expect(lci).To(Equal(total),
					"nvidia-smi mig -lci should list one compute instance per partition on a %d-GPU node with %d partitions each:\n%s",
					p.ExpectedGPUs(), partitions, lciListing)

				// -lcip is a catalogue, not an inventory: it lists the compute
				// instance profiles each partition offers, and how many that is
				// varies by board. The portable claim is that no partition is
				// missing from it.
				lcipListing := migListingOnNode(ctx, h, node, "-lcip")
				lcip, err := nvidiasmi.CountMigTableRows(lcipListing, nvidiasmi.MigComputeInstanceProfiles)
				Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lcip:\n%s", lcipListing)
				Expect(lcip).To(BeNumerically(">=", total),
					"nvidia-smi mig -lcip should offer at least one compute instance profile for each of the %d partitions:\n%s",
					total, lcipListing)
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
					Expect(onGPU).To(HaveLen(partitions),
						"GPU %d declares %d partitions but nvidia-smi mig -lgi lists %d",
						gpu, partitions, len(onGPU))

					ids := map[int]bool{}
					placements := map[string]bool{}
					for _, i := range onGPU {
						Expect(i.Profile).To(Equal(layout.Profile),
							"GPU instance %d on GPU %d carries profile %q, not the declared %q",
							i.InstanceID, gpu, i.Profile, layout.Profile)
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
					// address a partition by. Compared as sets, so an instance
					// one path invents is caught as well as one it drops.
					reported := map[int]bool{}
					for _, part := range migPartitionsOfGPU(snap, gpu) {
						reported[part.GPUInstanceID] = true
					}
					Expect(reported).To(Equal(ids),
						"nvidia-smi -q -x and nvidia-smi mig -lgi disagree about which instances GPU %d holds", gpu)
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
					capacity, ok := nvidiasmi.MigCapacityFor(profiles, gpu, layout.Profile)
					Expect(ok).To(BeTrue(),
						"nvidia-smi mig -lgip offers no %s profile on GPU %d:\n%s",
						layout.Profile, gpu, listing)
					// Asserting the taken count rather than a bare zero-free
					// keeps the claim true of a profile that carves only part
					// of its board; on the profiles this scenario runs, which
					// fill it, the two say the same thing.
					Expect(capacity.Total-capacity.Free).To(Equal(partitions),
						"GPU %d is carved into %d %s partitions, so -lgip should show that many taken, got %d free of %d",
						gpu, partitions, layout.Profile, capacity.Free, capacity.Total)
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

			// Deploying the plugin — applying the manifest, restarting its pods
			// and waiting for the node to re-advertise — is the most expensive
			// step in this scenario, and all three specs below need the same
			// deployment. It runs once here rather than per spec, which nothing
			// between them disturbs.
			//
			// A BeforeAll rather than a preceding spec, so that a label filter
			// selecting only one of these still gets the plugin.
			Context("with the device plugin in migStrategy=single", Ordered, func() {
				BeforeAll(func(ctx SpecContext) {
					deployMIGDevicePlugin(ctx, h, node, p.ExpectedGPUs()*partitions)
				})

				// The acceptance criterion. It re-reads the count the setup
				// above already waited for, rather than asserting nothing, so
				// the claim is named in the report: a failure attributed to a
				// BeforeAll says which container broke, not which claim.
				It("publishes one nvidia.com/gpu per MIG partition", Label("mig-device-plugin"), func(ctx SpecContext) {
					assertions.WaitAllocatableGPU(ctx, h.Kube, node, p.ExpectedGPUs()*partitions,
						config.ReadyTimeout(), config.PollInterval())
				})

				// Advertising the right count is not the same as handing out the
				// right thing. A pod that schedules onto a MIG resource must be
				// given one real partition's identity and the cap nodes guarding
				// it — which is what fails if the agent's instance IDs disagree
				// with the engine's.
				//
				// The claim is about what the pod was handed, not what its own
				// NVML reports: on this path the in-container library runs on
				// its compiled-in defaults, because the container toolkit drops
				// the env that would point it at this node's profile (#747), so
				// it describes a stock mock GPU no matter how the node is
				// partitioned. The allocation is therefore asserted where the
				// runtime honours it.
				It("gives a scheduled pod exactly one MIG partition", Label("mig-allocation"), func(ctx SpecContext) {
					// The `nvidia-smi -L` listing, not the -q -x document the
					// other specs read: what the plugin hands out is a MIG UUID,
					// and the document carries neither that nor the profile name
					// to check it against.
					onNode := migDevicesOnNode(ctx, h, node)
					workload := applyMIGWorkload(ctx, h, "mig-single", node)

					res, err := h.Kube.ExecSh(ctx, workload, `printf %s "${NVIDIA_VISIBLE_DEVICES:-}"`)
					Expect(err).NotTo(HaveOccurred(), "read NVIDIA_VISIBLE_DEVICES: %s", res.Combined())
					allocated := strings.TrimSpace(res.Combined())

					// A partition's own identity, not its parent's: a plugin
					// handing out whole-GPU UUIDs would have filled this in just
					// as well.
					Expect(allocated).To(HavePrefix("MIG-"),
						"a migStrategy=single allocation must name a partition, got %q", allocated)
					// And a partition this node actually has. Pinning it to the
					// nvidia-smi listing is what ties the allocation back to the
					// engine's own enumeration rather than to a well-formed string.
					Expect(migProfileOf(onNode, allocated)).To(Equal(layout.Profile),
						"allocated %q should be one of the %d partitions nvidia-smi reports on %s",
						allocated, len(onNode), node)

					// Two cap nodes: one for the GPU instance, one for the
					// compute instance. Their presence is what a MIG-aware
					// runtime requires to open the partition.
					res, err = h.Kube.ExecSh(ctx, workload, "ls "+migCapDevDir)
					Expect(err).NotTo(HaveOccurred(), "list %s: %s", migCapDevDir, res.Combined())
					Expect(strings.Fields(res.Combined())).To(HaveLen(2),
						"a MIG pod should receive its GPU- and compute-instance cap nodes, got:\n%s",
						res.Combined())
				})

				// The scheduler gate, on the MIG resource count rather than the
				// GPU count. A mock that reported partitions without the plugin
				// accounting for them would let this pod in.
				It("stops scheduling once every partition is claimed", Label("mig-allocation"), func(ctx SpecContext) {
					total := p.ExpectedGPUs() * partitions

					name := "mig-oversubscribed"
					// Unpinned on purpose: a nodeName bypasses scheduling, so
					// kubelet would admit and then reject the pod, which proves
					// the device manager works rather than the resource gating.
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
					// pulling an image look identical by phase. Pin the reason
					// to the GPU resource.
					out, err := h.Kube.KubectlCombined(ctx, "get", "events", "-n", migWorkloadNS,
						"--field-selector", "involvedObject.name="+name)
					Expect(err).NotTo(HaveOccurred(), "read events for %s", name)
					Expect(out).To(ContainSubstring("Insufficient "+kube.GPUResourceName),
						"%s should be unschedulable on %s specifically, got events:\n%s",
						name, kube.GPUResourceName, strings.TrimSpace(out))
				})
			})

			// The negative control for the context above, and the reason it is
			// worth running: `nvidia.com/gpu: 56` on an 8-GPU node is only
			// evidence of MIG if the same cluster reports 8 with MIG off.
			// Without this, a plugin that ignored --mig-strategy and a mock that
			// over-advertised would be indistinguishable from success.
			//
			// It runs after that context rather than before it because it
			// leaves the plain plugin manifest in place: ahead of those specs,
			// each of them would have to deploy the MIG plugin again.
			It("falls back to whole GPUs when MIG is switched off", Label("mig-device-plugin"), func(ctx SpecContext) {
				installMIGChart(ctx, h, p, layout, false)
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace,
					"nvml-mock", config.ReadyTimeout(), config.PollInterval())
				DeferCleanup(func(ctx SpecContext) {
					installMIGChart(ctx, h, p, layout)
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

			// The runtime knob, driven and read entirely through nvidia-smi.
			// The commands that carve the board and the enumeration that reads
			// it back are separate processes, so what is being checked is that
			// a partitioning outlives the process that performed it — the whole
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
			It("follows a runtime repartition through nvidia-smi mig", Label("mig-runtime"), func(ctx SpecContext) {
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

				By("re-lay-out GPU 0 as a single " + layout.Profile + " partition")
				// Three separate nvidia-smi processes, which is the point:
				// each one's engine dies with it, so the second command can
				// only see what the first recorded. Compute instances go
				// first because NVML refuses to destroy a GPU instance that
				// still holds one, and -C creates the compute instance
				// spanning the new GPU instance — without it the board would
				// carry a partition no MIG device is derived from.
				migMutateOnNode(ctx, h, node, "-dci", "-i", "0")
				migMutateOnNode(ctx, h, node, "-dgi", "-i", "0")
				migMutateOnNode(ctx, h, node, "-cgi", layout.Profile, "-C", "-i", "0")

				Eventually(func() int {
					return len(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 0))
				}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
					Should(Equal(1), "GPU 0 should report the single partition nvidia-smi carved")

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
				capacity, ok := nvidiasmi.MigCapacityFor(capacities, 0, layout.Profile)
				Expect(ok).To(BeTrue(),
					"nvidia-smi mig -lgip offers no %s profile on GPU 0:\n%s", layout.Profile, capacityListing)
				Expect(capacity.Total-capacity.Free).To(Equal(1),
					"GPU 0 now holds one %s partition, so -lgip should show one taken, got %d free of %d",
					layout.Profile, capacity.Free, capacity.Total)

				Expect(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 1)).To(HaveLen(partitions),
					"a repartition of GPU 0 must not disturb its neighbours")

				By("clear the override and let the installed layout come back")
				resetRuntimeOverridesOnNode(ctx, h, node)

				Eventually(func() int {
					return len(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 0))
				}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
					Should(Equal(partitions), "removing the override should restore the declared layout")
			})

			// The targeted, by-id half of the partitioning surface, which the
			// spec above does not reach: it repartitions with the bulk forms
			// (-cgi <profile>, -dgi -i <gpu>) that act on a whole board, where
			// an operator and nvidia-mig-parted name one instance at a time.
			//
			// This is one spec rather than several because the interesting
			// assertions are about what each step leaves behind for the next:
			// every command is its own process, so an id this reads out of one
			// listing is only valid in the next command if the mock recorded it.
			//
			// Also kept after the declarative specs, for the same reason as the
			// repartition above: the override it writes is a per-node file that
			// outlives the spec.
			It("creates, lists and deletes partitions by id through nvidia-smi", Label("mig-runtime"), func(ctx SpecContext) {
				DeferCleanup(func(ctx SpecContext) {
					resetRuntimeOverridesOnNode(ctx, h, node)
				})

				// Two partitions of the declared profile have to fit, since the
				// spec creates a pair and then deletes one of them to show the
				// delete was targeted rather than wholesale.
				Expect(partitions).To(BeNumerically(">=", 2),
					"profile %s fits %d partition(s) of %s per GPU; this spec needs two to tell a targeted delete from a bulk one",
					p.Name, partitions, layout.Profile)

				// The profile's id is read off -lgip rather than written in:
				// the ids are per-board, so no literal is portable across the
				// profiles this scenario runs. Reading it also asserts the id
				// and the name -lgip prints agree, which is what lets the two
				// spellings below name the same profile.
				capacityListing := migListingOnNode(ctx, h, node, "-lgip")
				capacities, err := nvidiasmi.ListMigProfileCapacity(capacityListing)
				Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lgip:\n%s", capacityListing)
				declared, ok := nvidiasmi.MigCapacityFor(capacities, 0, layout.Profile)
				Expect(ok).To(BeTrue(),
					"nvidia-smi mig -lgip offers no %s profile on GPU 0:\n%s", layout.Profile, capacityListing)

				By("nvidia-smi mig -dci -i 0 && -dgi -i 0: clear GPU 0 down to bare metal")
				migMutateOnNode(ctx, h, node, "-dci", "-i", "0")
				migMutateOnNode(ctx, h, node, "-dgi", "-i", "0")

				// The compute-instance listing is read by row count, which has
				// no per-GPU column to filter on, so the other GPUs' declared
				// partitions are in every number below. Counting from a
				// baseline taken with GPU 0 empty keeps the assertions about
				// what this spec did rather than about the profile's size.
				baseCIs := migComputeInstanceCountOnNode(ctx, h, node)
				Expect(nvidiasmi.MigInstancesOfGPU(migGPUInstancesOnNode(ctx, h, node), 0)).To(BeEmpty(),
					"GPU 0 should carry no GPU instances once -dgi has run")

				By("nvidia-smi mig -cgi " + strconv.Itoa(declared.ProfileID) + "," + layout.Profile +
					" -C -i 0: create two partitions, naming the profile by id and by name")
				// One command, both spellings: nvidia-smi resolves each element
				// of the list separately, so this covers the id form and the
				// name form against the same profile, and a mock that reported
				// an id no profile answers to would fail on the first element.
				migMutateOnNode(ctx, h, node,
					"-cgi", strconv.Itoa(declared.ProfileID)+","+layout.Profile, "-C", "-i", "0")

				created := nvidiasmi.MigInstancesOfGPU(migGPUInstancesOnNode(ctx, h, node), 0)
				Expect(created).To(HaveLen(2),
					"nvidia-smi mig -lgi should report the two partitions the -cgi list asked for")
				for _, instance := range created {
					Expect(instance.Profile).To(Equal(layout.Profile),
						"the partition created by id %d should carry the same profile as the one created by name",
						declared.ProfileID)
					Expect(instance.ProfileID).To(Equal(declared.ProfileID),
						"nvidia-smi mig -lgi should report the profile under the id -lgip published")
				}
				Expect(migComputeInstanceCountOnNode(ctx, h, node)).To(Equal(baseCIs+2),
					"-C should have given each new partition a compute instance")
				Expect(migDeviceCountOfGPU(ctx, h, node, 0)).To(Equal(2),
					"nvidia-smi -L should show a MIG device for each of GPU 0's two partitions")

				By("nvidia-smi mig -dci -gi <id> -ci 0 && -dgi -gi <id>: delete one partition, by id")
				// The first instance's id, read from the listing above: NVML
				// chooses these, so a spec that assumed 0 and 1 would be
				// asserting against the allocator rather than the delete.
				doomed, spared := created[0].InstanceID, created[1].InstanceID

				// The refusal first, which is what makes the order below a
				// contract rather than a convention. A mock that tore the
				// compute instance down for the caller would let a partitioning
				// tool with the order wrong pass here and fail on a real board,
				// and the correct-order steps that follow cannot detect that.
				By("nvidia-smi mig -dgi -gi <id>: refused while the partition still holds a compute instance")
				migMutateRefusedOnNode(ctx, h, node, "-dgi", "-gi", strconv.Itoa(doomed))
				Expect(nvidiasmi.MigInstancesOfGPU(migGPUInstancesOnNode(ctx, h, node), 0)).To(HaveLen(2),
					"a refused destroy must leave both partitions standing")
				Expect(migComputeInstanceCountOnNode(ctx, h, node)).To(Equal(baseCIs+2),
					"a refused destroy must not have taken the compute instance with it")

				// The compute instance goes first. Real NVML refuses to destroy
				// a GPU instance that still holds one, so this is the order a
				// partitioning tool has to use.
				migMutateOnNode(ctx, h, node, "-dci", "-gi", strconv.Itoa(doomed), "-ci", "0")
				migMutateOnNode(ctx, h, node, "-dgi", "-gi", strconv.Itoa(doomed))

				remaining := nvidiasmi.MigInstancesOfGPU(migGPUInstancesOnNode(ctx, h, node), 0)
				Expect(remaining).To(HaveLen(1), "only the partition named by -dgi should be gone")
				Expect(remaining[0].InstanceID).To(Equal(spared),
					"the surviving partition should be the one -dgi did not name")
				Expect(migComputeInstanceCountOnNode(ctx, h, node)).To(Equal(baseCIs+1),
					"the deleted partition's compute instance should have gone with it")

				By("nvidia-smi mig -cgi " + layout.Profile +
					" -i 0: a GPU instance with no compute instance, created without -C")
				migMutateOnNode(ctx, h, node, "-cgi", layout.Profile, "-i", "0")

				Expect(nvidiasmi.MigInstancesOfGPU(migGPUInstancesOnNode(ctx, h, node), 0)).To(HaveLen(2),
					"nvidia-smi mig -lgi should report the partition created without -C")
				Expect(migComputeInstanceCountOnNode(ctx, h, node)).To(Equal(baseCIs+1),
					"a partition created without -C holds no compute instance")
				// The distinction -C makes, and the reason a repartition that
				// omits it leaves a board no consumer can use: MIG devices are
				// derived from compute instances, so a GPU instance without one
				// is invisible to everything that allocates.
				Expect(migDeviceCountOfGPU(ctx, h, node, 0)).To(Equal(1),
					"nvidia-smi -L should omit the partition that has no compute instance")

				By("nvidia-smi mig -cci 0 -gi <id>: give that partition a compute instance")
				bare := bareGpuInstanceOnGPU(ctx, h, node, 0, spared)
				migMutateOnNode(ctx, h, node, "-cci", "0", "-gi", strconv.Itoa(bare))

				Expect(migComputeInstanceCountOnNode(ctx, h, node)).To(Equal(baseCIs+2),
					"-cci should have added the compute instance the partition was missing")
				Expect(migDeviceCountOfGPU(ctx, h, node, 0)).To(Equal(2),
					"nvidia-smi -L should now show both of GPU 0's partitions")

				By("nvidia-smi -i 0 -mig 0: disable MIG, dropping every partition with it")
				migSetModeOnNode(ctx, h, node, 0, false)

				Eventually(func() int {
					return len(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 0))
				}).WithContext(ctx).WithTimeout(runtimeTTLTimeout).WithPolling(runtimeTTLPoll).
					Should(BeZero(), "disabling MIG should leave GPU 0 carrying no partitions")
				Expect(migGPU(migSnapshotOnNode(ctx, h, node), 0).MIGEnabled()).To(BeFalse(),
					"nvidia-smi -q -x should report GPU 0's MIG mode as disabled")
				Expect(migPartitionsOfGPU(migSnapshotOnNode(ctx, h, node), 1)).To(HaveLen(partitions),
					"disabling MIG on GPU 0 must not disturb its neighbours")
			})
		})
	}
})

// installMIGChart installs the release with the profile's declared MIG
// partitioning switched on. Pass enabled=false for the negative control, which
// leaves the same profile's layout inert.
// installMIGChart installs the chart with layout as the partitioning. The
// layout is passed even when MIG is off, since the chart reads it only under
// gpu.mig.enabled and a uniform call keeps the two paths comparable.
func installMIGChart(ctx context.Context, h *harness.Harness, p profile.Profile, layout migLayout, enabled ...bool) {
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
			"gpu.count":                       strconv.Itoa(p.ExpectedGPUs()),
			"gpu.profile":                     p.Name,
			"image.repository":                repo,
			"image.tag":                       tag,
			"gpu.mig.enabled":                 strconv.FormatBool(on),
			"gpu.mig.gpuInstances[0].profile": layout.Profile,
			"gpu.mig.gpuInstances[0].count":   strconv.Itoa(layout.Count),
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
//
// ExecQuiet keeps the table out of the Ginkgo log. The pollers below re-read
// these listings every interval, so streaming them buried the mutations the
// specs are actually narrating. Nothing is lost: the output is still captured,
// and every assertion that can fail on it prints the listing it read.
func migListingOnNode(ctx context.Context, h *harness.Harness, node, flag string) string {
	GinkgoHelper()
	target := nvmlPodOnNode(ctx, h, node)
	By("nvidia-smi mig " + flag)
	res, err := h.Kube.ExecQuiet(ctx, target, "nvidia-smi", "mig", flag)
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi mig %s in %s exited non-zero: %s",
		flag, target.Pod, res.Combined())
	return res.Combined()
}

// migMutateOnNode runs one `nvidia-smi mig` mutation in the nvml-mock pod on
// node. Each call is its own process, and its own NVML engine: a mutation only
// reaches the next command in the sequence because the library recorded it on
// the way out.
func migMutateOnNode(ctx context.Context, h *harness.Harness, node string, args ...string) {
	GinkgoHelper()
	target := nvmlPodOnNode(ctx, h, node)
	By("nvidia-smi mig " + strings.Join(args, " "))
	res, err := h.Kube.Exec(ctx, target, append([]string{"nvidia-smi", "mig"}, args...)...)
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi mig %v in %s exited non-zero: %s",
		args, target.Pod, res.Combined())
}

// migMutateRefusedOnNode runs one `nvidia-smi mig` mutation that NVML is
// expected to reject, and returns its output.
//
// The non-zero exit is the assertion, so this cannot go through
// migMutateOnNode: there, a refusal is the failure. Reading the exit code
// rather than the error keeps the check on what nvidia-smi reported instead of
// on how the runner wrapped it.
func migMutateRefusedOnNode(ctx context.Context, h *harness.Harness, node string, args ...string) string {
	GinkgoHelper()
	target := nvmlPodOnNode(ctx, h, node)
	By("nvidia-smi mig " + strings.Join(args, " ") + " (expected to be refused)")
	res, _ := h.Kube.Exec(ctx, target, append([]string{"nvidia-smi", "mig"}, args...)...)
	Expect(res.ExitCode).NotTo(BeZero(),
		"nvidia-smi mig %v in %s should have been refused, got:\n%s", args, target.Pod, res.Combined())
	return res.Combined()
}

// migSetModeOnNode switches one GPU's MIG mode with `nvidia-smi -i <gpu> -mig
// <0|1>`, which is not a `mig` subcommand and so cannot go through
// migMutateOnNode.
//
// This is the command that turns MIG on and off on real hardware; the specs
// above reach the same setting through the chart, which cannot show that the
// NVML path a consumer drives has the same effect.
func migSetModeOnNode(ctx context.Context, h *harness.Harness, node string, gpu int, on bool) {
	GinkgoHelper()
	mode := "0"
	if on {
		mode = "1"
	}
	target := nvmlPodOnNode(ctx, h, node)
	args := []string{"nvidia-smi", "-i", strconv.Itoa(gpu), "-mig", mode}
	By(strings.Join(args, " "))
	res, err := h.Kube.Exec(ctx, target, args...)
	Expect(err).NotTo(HaveOccurred(), "%v in %s exited non-zero: %s",
		args, target.Pod, res.Combined())
}

// migComputeInstanceCountOnNode counts the rows of `nvidia-smi mig -lci` across
// the whole board. The listing carries no column an assertion could filter one
// GPU on, so callers compare against a baseline rather than an absolute.
func migComputeInstanceCountOnNode(ctx context.Context, h *harness.Harness, node string) int {
	GinkgoHelper()
	listing := migListingOnNode(ctx, h, node, "-lci")
	count, err := nvidiasmi.CountMigTableRows(listing, nvidiasmi.MigComputeInstances)
	Expect(err).NotTo(HaveOccurred(), "parse nvidia-smi mig -lci:\n%s", listing)
	return count
}

// migDeviceCountOfGPU counts the MIG devices `nvidia-smi -L` derives from one
// GPU's partitions, which is not the same as that GPU's GPU-instance count: a
// GPU instance holding no compute instance yields no MIG device.
func migDeviceCountOfGPU(ctx context.Context, h *harness.Harness, node string, gpu int) int {
	GinkgoHelper()
	var count int
	for _, device := range migDevicesOnNode(ctx, h, node) {
		if device.GPU == gpu {
			count++
		}
	}
	return count
}

// bareGpuInstanceOnGPU returns the id of the GPU instance on gpu that is not
// exclude, failing unless there is exactly one such instance.
//
// The caller knows which instance it left alone and wants the other one, whose
// id NVML chose; asserting the count here is what keeps a caller from picking
// an arbitrary instance when the board holds more than it expected.
func bareGpuInstanceOnGPU(ctx context.Context, h *harness.Harness, node string, gpu, exclude int) int {
	GinkgoHelper()
	var found []int
	for _, instance := range nvidiasmi.MigInstancesOfGPU(migGPUInstancesOnNode(ctx, h, node), gpu) {
		if instance.InstanceID != exclude {
			found = append(found, instance.InstanceID)
		}
	}
	Expect(found).To(HaveLen(1),
		"GPU %d should carry exactly one instance other than %d, got %v", gpu, exclude, found)
	return found[0]
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
//
// Quiet for the same reason as migListingOnNode: on a fully partitioned board
// this is one line per partition per call, and the counting helpers read it
// repeatedly.
func migDevicesInPod(ctx context.Context, h *harness.Harness, target kube.PodRef) []nvidiasmi.MigDevice {
	GinkgoHelper()
	res, err := h.Kube.ExecQuiet(ctx, target, "nvidia-smi", "-L")
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
