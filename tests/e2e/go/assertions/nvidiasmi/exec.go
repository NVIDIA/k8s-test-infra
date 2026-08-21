//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"context"
	"fmt"
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
