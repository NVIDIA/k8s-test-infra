//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// Running nvidia-smi in a pod and asserting through Gomega. Every helper is a
// Ginkgo helper so failures point at the calling spec line. ExecQuiet keeps the
// ~90 KB document out of the Ginkgo log; only the decoded problems are
// attached to a failure.

// Inventory asserts nvidia-smi runs in the pod at all, and that the `-q -x`
// document describes the profile's full device name and exactly ExpectedGPUs
// devices with no processes on them.
func Inventory(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	// The bare invocation renders the human table, a code path -q -x never
	// exercises. Only its exit status is checked; everything below reads the
	// machine-readable document instead.
	ginkgo.By("nvidia-smi default output")
	res, err := k.Exec(ctx, pod, "nvidia-smi")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "nvidia-smi exited with error: %s", res.Combined())

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x describes %d GPUs named %q", p.ExpectedGPUs(), p.DisplayName))
	out := query(ctx, k, pod)

	problems := InventoryProblems(out, p.DisplayName, p.ExpectedGPUs())
	gomega.Expect(problems).To(gomega.BeEmpty(), "nvidia-smi inventory wrong:\n%s",
		strings.Join(problems, "\n"))

	ginkgo.By("nvidia-smi -q -x reports no phantom processes")
	problems = PhantomProcessProblems(out)
	gomega.Expect(problems).To(gomega.BeEmpty(), "phantom processes:\n%s",
		strings.Join(problems, "\n"))
}

// JpgOfaUtilization asserts nvidia-smi -q -x reports the given JPEG and OFA
// percentages on every GPU. Both elements read N/A until the NVML getters
// existed, so the configured values were silently dropped. See issue #637.
func JpgOfaUtilization(ctx context.Context, k *kube.Client, pod kube.PodRef, wantJPEG, wantOFA int) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x reports jpeg_util %d %% / ofa_util %d %%", wantJPEG, wantOFA))
	problems := JpgOfaUtilizationProblems(query(ctx, k, pod), wantJPEG, wantOFA)
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"JPEG/OFA utilization wrong:\n%s", strings.Join(problems, "\n"))
}

// PCIeIdentity asserts nvidia-smi -q -x reports per-GPU PCIe identity values a
// real GPU could produce: the link, device and host maxima all at the profile's
// configured generation rather than N/A or Gen0, and a non-zero board ID that is
// unique across the node. See issue #638.
func PCIeIdentity(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x PCIe identity on %s (Gen%d, %d GPUs)",
		p.Name, p.MaxPCIeLinkGen(), p.ExpectedGPUs()))
	problems := PCIeIdentityProblems(query(ctx, k, pod), p.ExpectedGPUs(), p.MaxPCIeLinkGen())
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"PCIe identity wrong for profile %s:\n%s", p.Name, strings.Join(problems, "\n"))
}

// VirtualizationMode asserts nvidia-smi -q -x reports the bare-metal
// virtualization mode on every GPU. The element read N/A while
// nvmlDeviceGetVirtualizationMode was a generated stub, which claims the driver
// cannot tell whether the GPU is virtualized. See issue #640.
func VirtualizationMode(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.GinkgoHelper()

	ginkgo.By("nvidia-smi -q -x reports virtualization_mode None")
	problems := VirtualizationModeProblems(query(ctx, k, pod))
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"virtualization mode wrong:\n%s", strings.Join(problems, "\n"))
}

// ProcessMonitorAndTopology asserts the two nvidia-smi subcommands that reach
// the mock through the reverse-engineered internal export table still behave.
// They are checked together because they share that path and are unusually
// sensitive to what neighbouring NVML calls return: implementing
// nvmlDeviceGetVirtualizationMode during the investigation behind PR #630 moved
// pmon onto a different branch and segfaulted it. See issue #640.
func ProcessMonitorAndTopology(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.GinkgoHelper()

	// pmon's own failure is expected, so the error is deliberately not
	// asserted on — only the exit code, which is what tells a graceful refusal
	// from a crash.
	ginkgo.By("nvidia-smi pmon -c 1 does not crash")
	res, _ := k.Exec(ctx, pod, "nvidia-smi", "pmon", "-c", "1")
	problems := ProcessMonitorProblems(res.ExitCode, res.Combined())
	gomega.Expect(problems).To(gomega.BeEmpty(), strings.Join(problems, "\n"))

	ginkgo.By("nvidia-smi topo -m succeeds")
	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "topo", "-m")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"nvidia-smi topo -m exited %d: %s", res.ExitCode, res.Combined())
}

// GpuReset asserts `nvidia-smi --gpu-reset` resets the GPUs it is pointed at and
// reports it the way real hardware does. Reset runs entirely through the internal
// export table, where the dispatcher's catch-all used to fault writing a zero
// count through an argument that carries no count on the reset completion slot —
// every invocation died with a bare exit 139 and no output.
//
// Both spellings are covered because `-r` is the one a runbook or a remediation
// controller is likely to carry, and both scopes are covered because a bare
// --gpu-reset walks every GPU while -i names one.
func GpuReset(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	for _, tc := range []struct {
		args     []string
		wantGPUs int
	}{
		{[]string{"nvidia-smi", "--gpu-reset"}, p.ExpectedGPUs()},
		{[]string{"nvidia-smi", "-r", "-i", "0"}, 1},
	} {
		ginkgo.By(fmt.Sprintf("%s resets %d GPU(s)", strings.Join(tc.args, " "), tc.wantGPUs))
		res, _ := k.Exec(ctx, pod, tc.args...)
		problems := GpuResetProblems(res.ExitCode, res.Combined(), tc.wantGPUs)
		gomega.Expect(problems).To(gomega.BeEmpty(), strings.Join(problems, "\n"))
	}
}

// TemperatureThresholds asserts nvidia-smi -q -x uses the
// architecture-correct threshold presentation for the profile: absolute
// elements on pre-Ada, *_tlimit_threshold elements on Ada and later. See issue
// #635.
func TemperatureThresholds(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x temperature thresholds on %s (arch=%s, tlimit=%v)",
		p.Name, p.Architecture(), p.ReportsTLimitTemp()))
	problems := TemperatureProblems(query(ctx, k, pod), p.ReportsTLimitTemp(),
		p.ShutdownThresholdC(), p.SlowdownThresholdC(), p.MaxOperatingC())
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"temperature threshold presentation wrong for profile %s:\n%s",
		p.Name, strings.Join(problems, "\n"))
}

// C2CMode asserts nvidia-smi -q -x reports the C2C state the profile declares:
// Enabled on a Grace board, N/A on every other one. The expectation is derived
// from the profile rather than passed in, so the same spec covers both
// directions as the CI matrix moves across profiles. See issue #639.
func C2CMode(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x c2c_mode on %s (c2c_enabled=%v)", p.Name, p.C2CEnabled()))
	problems := C2CModeProblems(query(ctx, k, pod), p.C2CEnabled())
	gomega.Expect(problems).To(gomega.BeEmpty(), "GPU C2C Mode wrong for profile %s:\n%s",
		p.Name, strings.Join(problems, "\n"))
}

// PlatformIdentity asserts nvidia-smi -q -x reports the platform identity the
// profile declares: on a rack-scale profile the configured chassis, slot, tray,
// host and peer type with a module id that is distinct per GPU, and N/A across
// the whole block on every other profile. Both directions come from the profile,
// so one spec covers them as the CI matrix moves across profiles. See issue #642.
func PlatformIdentity(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	identity, declared := p.PlatformIdentity()
	var want *PlatformExpectation
	if declared {
		want = &PlatformExpectation{
			ChassisSerialNumber: identity.ChassisSerialNumber,
			SlotNumber:          identity.SlotNumber,
			TrayIndex:           identity.TrayIndex,
			HostID:              identity.HostID,
			PeerType:            identity.PeerType,
			ModuleIDs:           identity.ModuleIDs,
		}
	}

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x platformInfo on %s (declares a location=%v)", p.Name, declared))
	problems := PlatformIdentityProblems(query(ctx, k, pod), want)
	gomega.Expect(problems).To(gomega.BeEmpty(), "platform identity wrong for profile %s:\n%s",
		p.Name, strings.Join(problems, "\n"))
}

// FabricHealth asserts nvidia-smi -q -x reports a healthy fabric on every GPU.
// Every element of the block read N/A while the mock reported no health
// summary, which says the driver answered nothing rather than that the fabric
// is well. See issue #677.
func FabricHealth(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.GinkgoHelper()

	ginkgo.By("nvidia-smi -q -x reports a healthy fabric health block")
	problems := FabricHealthProblems(query(ctx, k, pod), HealthyFabricBlock())
	gomega.Expect(problems).To(gomega.BeEmpty(), "fabric health wrong:\n%s",
		strings.Join(problems, "\n"))
}

// ThrottleCounters asserts nvidia-smi -q -x reports five zeroed clocks-event
// counters on every GPU. Every counter read N/A while the field ids behind them
// went unanswered, which says the driver could not report whether the GPU had
// ever been throttled — the opposite of the "never throttled" a healthy profile
// means. See issue #678.
func ThrottleCounters(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.GinkgoHelper()

	ginkgo.By("nvidia-smi -q -x reports 0 us for every clocks-event counter")
	problems := ThrottleCounterProblems(query(ctx, k, pod), UnthrottledCounters())
	gomega.Expect(problems).To(gomega.BeEmpty(), "clocks event reason counters wrong:\n%s",
		strings.Join(problems, "\n"))
}

// ConfComputeMemory asserts nvidia-smi -q -x reports 0 MiB across the Conf
// Compute protected memory block on every GPU. The whole block read N/A while
// the two NVML getters behind it were generated stubs, which says the driver
// could not report whether any memory is protected — where every real board,
// CC-capable or not, answers none. See issue #711.
func ConfComputeMemory(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.GinkgoHelper()

	ginkgo.By("nvidia-smi -q -x reports 0 MiB of Conf Compute protected memory")
	problems := ConfComputeMemoryProblems(query(ctx, k, pod))
	gomega.Expect(problems).To(gomega.BeEmpty(), "Conf Compute protected memory wrong:\n%s",
		strings.Join(problems, "\n"))
}

// MaxCustomerBoostClock asserts nvidia-smi -q -x reports the profile's
// clocks.graphics_max as the OEM boost ceiling on every GPU, and that the Max
// Clocks row beside it agrees. The row read N/A while both NVML getters behind
// it were generated stubs, which says the driver could not report an OEM
// ceiling where every real board reports one. See issue #712.
func MaxCustomerBoostClock(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	ginkgo.By(fmt.Sprintf("nvidia-smi -q -x max_customer_boost_clocks on %s (graphics_max=%d MHz)",
		p.Name, p.GraphicsMaxClockMHz()))
	problems := MaxCustomerBoostClockProblems(query(ctx, k, pod), p.GraphicsMaxClockMHz())
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"Max Customer Boost Clocks wrong for profile %s:\n%s", p.Name, strings.Join(problems, "\n"))
}

// PowerProfiles asserts `nvidia-smi power-profiles` behaves the way the profile
// declares: on a Blackwell board on a 570-or-newer driver it lists exactly the
// configured profiles on every GPU and reports nothing requested or enforced;
// on every other profile it is declined. Both directions come from the profile,
// so one spec covers them as the CI matrix moves across profiles.
//
// The whole subcommand answered "Workload Power Profiles feature is not
// supported on this device" while both getters behind it were generated stubs,
// so a consumer could not discover a single profile the board offers.
func PowerProfiles(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	declared, _ := p.WorkloadPowerProfiles()
	wantIDs := make([]int, 0, len(declared))
	for _, wp := range declared {
		wantIDs = append(wantIDs, wp.ID)
	}

	if !p.SupportsWorkloadPowerProfiles() {
		ginkgo.By(fmt.Sprintf("nvidia-smi power-profiles -l is declined on %s (declares %d profiles, driver %d.x)",
			p.Name, len(wantIDs), p.DriverMajor()))
		res, _ := k.Exec(ctx, pod, "nvidia-smi", "power-profiles", "-l")
		problems := PowerProfileUnsupportedProblems(res.Combined(), res.ExitCode)
		gomega.Expect(problems).To(gomega.BeEmpty(),
			"power-profiles should be declined on profile %s:\n%s", p.Name, strings.Join(problems, "\n"))
		return
	}

	ginkgo.By(fmt.Sprintf("nvidia-smi power-profiles -l lists %v on each of %d GPU(s) on %s",
		wantIDs, p.ExpectedGPUs(), p.Name))
	res, _ := k.Exec(ctx, pod, "nvidia-smi", "power-profiles", "-l")
	problems := PowerProfileListProblems(res.Combined(), res.ExitCode, p.ExpectedGPUs(), wantIDs)
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"power profile list wrong for profile %s:\n%s", p.Name, strings.Join(problems, "\n"))

	// A profile that pre-requests something reports that instead of nothing,
	// and the round trip below would start from it rather than from empty.
	// The shipped profiles all leave it empty, matching every capture.
	if len(p.RequestedWorkloadPowerProfiles()) > 0 {
		return
	}
	ginkgo.By("nvidia-smi power-profiles -gr / -ge report nothing requested or enforced")
	requested, _ := k.Exec(ctx, pod, "nvidia-smi", "power-profiles", "-gr")
	enforced, _ := k.Exec(ctx, pod, "nvidia-smi", "power-profiles", "-ge")
	problems = PowerProfileCurrentProblems(
		requested.Combined(), requested.ExitCode, enforced.Combined(), enforced.ExitCode)
	gomega.Expect(problems).To(gomega.BeEmpty(),
		"requested/enforced power profiles wrong for profile %s:\n%s", p.Name, strings.Join(problems, "\n"))

	powerProfileWrites(ctx, k, pod, p)
}

// powerProfileWrites drives `-sr` and `-cr`.
//
// Two constraints shape every command here.
//
// Every flag goes into a single nvidia-smi invocation, because the mock's
// requested set lives in the process that loaded libnvidia-ml.so: a second exec
// gets a fresh library and would read back the configured request, not the
// write. nvidia-smi evaluates `-sr` and `-cr` before `-ge`, which is what makes
// a read-after-write expressible at all — and why `-gr`, which it evaluates
// first, cannot be used to observe one.
//
// And each is scoped to one GPU with `-i 0`, because nvidia-smi 580.65.06
// applies a comma-separated profile list in full only to the first GPU it
// visits and passes just the first profile to the rest. That is the consumer's
// behaviour, not the mock's, so pinning it here would make these assertions
// fail on a driver that fixed it. `-l` already covers every GPU.
func powerProfileWrites(ctx context.Context, k *kube.Client, pod kube.PodRef, p profile.Profile) {
	ginkgo.GinkgoHelper()

	const oneGPU = 1

	if first, second, ok := p.IndependentWorkloadProfilePair(); ok {
		ginkgo.By(fmt.Sprintf(
			"nvidia-smi power-profiles -sr %d,%d -cr %d -ge leaves only %d enforced on %s",
			first, second, first, second, p.Name))
		res, _ := k.Exec(ctx, pod, "nvidia-smi", "power-profiles",
			"-sr", fmt.Sprintf("%d,%d", first, second), "-cr", strconv.Itoa(first), "-ge", "-i", "0")
		problems := PowerProfileRoundTripProblems(
			res.Combined(), res.ExitCode, oneGPU, []int{second})
		gomega.Expect(problems).To(gomega.BeEmpty(),
			"power profile set/clear round trip wrong for profile %s:\n%s",
			p.Name, strings.Join(problems, "\n"))
	}

	// Requesting profiles that exclude each other is what separates the
	// requested set from the enforced one: both are requested, only the
	// higher-priority one is enforced.
	if winner, loser, ok := p.ConflictingWorkloadProfilePair(); ok {
		ginkgo.By(fmt.Sprintf(
			"nvidia-smi power-profiles -sr %d,%d -ge enforces only the higher-priority %d on %s",
			winner, loser, winner, p.Name))
		res, _ := k.Exec(ctx, pod, "nvidia-smi", "power-profiles",
			"-sr", fmt.Sprintf("%d,%d", winner, loser), "-ge", "-i", "0")
		problems := PowerProfileArbitrationProblems(
			res.Combined(), res.ExitCode, oneGPU, winner, loser)
		gomega.Expect(problems).To(gomega.BeEmpty(),
			"power profile arbitration wrong for profile %s:\n%s", p.Name, strings.Join(problems, "\n"))
	}

	// nvidia-smi checks the id against the list the board advertised before
	// it calls NVML, so refusing this one also confirms the advertised list
	// reached it intact.
	if badID, ok := unadvertisedProfileID(p); ok {
		ginkgo.By(fmt.Sprintf("nvidia-smi power-profiles -sr %d is refused on %s", badID, p.Name))
		res, _ := k.Exec(ctx, pod, "nvidia-smi", "power-profiles", "-sr", strconv.Itoa(badID), "-i", "0")
		problems := PowerProfileSetRejectedProblems(res.Combined(), res.ExitCode, badID)
		gomega.Expect(problems).To(gomega.BeEmpty(),
			"power profile rejection wrong for profile %s:\n%s", p.Name, strings.Join(problems, "\n"))
	}
}

// unadvertisedProfileID picks a profile id the board does not advertise but
// nvidia-smi still recognises as a name, so the refusal comes from the board's
// list rather than from the id being unparseable.
func unadvertisedProfileID(p profile.Profile) (int, bool) {
	declared, _ := p.WorkloadPowerProfiles()
	advertised := make(map[int]bool, len(declared))
	for _, wp := range declared {
		advertised[wp.ID] = true
	}
	// NVML_POWER_PROFILE_* runs to 18 in the headers nvidia-smi 580 was
	// built against; staying inside that range keeps the refusal about the
	// board rather than about an out-of-range index.
	for id := range 19 {
		if !advertised[id] {
			return id, true
		}
	}
	return 0, false
}

// query execs `nvidia-smi -q -x` and asserts it succeeded, returning stdout.
func query(ctx context.Context, k *kube.Client, pod kube.PodRef) string {
	ginkgo.GinkgoHelper()

	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"nvidia-smi -q -x exited with error: %s", res.Combined())
	return res.Stdout
}

// SnapshotFromPod execs `nvidia-smi -q -x` in pod and decodes it. It returns an
// error rather than asserting, so pollers can retry; the combined output is
// folded into the error because a failed exec has no document to report.
func SnapshotFromPod(ctx context.Context, k *kube.Client, pod kube.PodRef) (Snapshot, error) {
	res, err := k.ExecQuiet(ctx, pod, "nvidia-smi", "-q", "-x")
	if err != nil {
		return Snapshot{}, fmt.Errorf("nvidia-smi -q -x: %w: %s", err, res.Combined())
	}
	return ParseSnapshot(res.Stdout)
}
