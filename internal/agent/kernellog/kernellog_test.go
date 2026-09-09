// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kernellog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// node builds a host whose kernel log is a regular file, so a test can read
// back what a driver would have printed.
func node(t *testing.T, devices ...agent.DeviceSpec) (*host.Host, *agent.State, string) {
	t.Helper()

	h := host.New(t.TempDir())
	require.NoError(t, os.MkdirAll(h.RootPath("driver/config"), 0o755))

	kmsg := filepath.Join(t.TempDir(), "kmsg")
	require.NoError(t, os.WriteFile(kmsg, nil, 0o600))

	return h, &agent.State{Devices: devices}, kmsg
}

func gpus(busIDs ...string) []agent.DeviceSpec {
	specs := make([]agent.DeviceSpec, 0, len(busIDs))
	for i, busID := range busIDs {
		specs = append(specs, agent.DeviceSpec{Index: i, PCIBusID: busID})
	}

	return specs
}

// inject writes what `nvml-mock-ctl fail` writes, since the override file is
// the only thing that passes between the two.
func inject(t *testing.T, h *host.Host, target mockctl.Target, mode string, xid uint64) {
	t.Helper()

	path := h.RootPath("driver/config/overrides.yaml")
	doc, err := mockctl.Load(path)
	require.NoError(t, err)
	require.NoError(t, doc.Fail(target, mode, 0, xid))
	require.NoError(t, mockctl.WriteAtomic(path, doc))
}

func kernelLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}

func staged(t *testing.T, h *host.Host, state *agent.State, kmsg string) *Simulator {
	t.Helper()

	s := New(Options{Path: kmsg})
	require.NoError(t, s.Stage(t.Context(), h, state))
	require.True(t, s.Ready())

	return s
}

// The announcement follows the injection the CLI wrote, and follows it once:
// the watcher re-reads the file on every tick, but a driver prints an Xid when
// the fault happens, not for as long as the GPU stays broken.
func TestSimulator_AnnouncesEachInjectionOnce(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0", "0000:2B:00.0")...)
	s := staged(t, h, state, kmsg)

	s.Poll(t.Context())
	require.Empty(t, kernelLog(t, kmsg), "a healthy node prints nothing")

	inject(t, h, mockctl.Target{Index: 1}, engine.FailureModeLost, 79)
	s.Poll(t.Context())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:2b:00): 79\n", kernelLog(t, kmsg))

	s.Poll(t.Context())
	s.Poll(t.Context())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:2b:00): 79\n", kernelLog(t, kmsg),
		"a standing failure is not a new Xid")
}

// Recovery retracts nothing — kernel logs never do — but it does end the fault,
// so the next injection is a new Xid even when the code repeats.
func TestSimulator_AnnouncesAgainAfterRecovery(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeHealthy, 0)
	s.Poll(t.Context())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\n", kernelLog(t, kmsg),
		"recovery prints nothing of its own")

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\nkernel: NVRM: Xid (PCI:0000:1a:00): 79\n",
		kernelLog(t, kmsg))
}

// The counterpart to the test above, and the limit of watching state instead
// of events: a clear and a re-arm of the same code between two reads leave a
// document identical to the one already announced, so the second injection
// prints nothing. Pinned because it is inherent rather than an oversight — the
// document that comes back is byte-for-byte the one before it, at any poll
// rate — and because it costs a reader of the log nothing: recovery prints no
// line either, so one line and two both say "Xid 79 here, never retracted".
func TestSimulator_CollapsesAClearAndRearmBetweenPolls(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeHealthy, 0)
	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\n", kernelLog(t, kmsg))
}

// `--gpu all` faults every device, so every device raises the Xid — and only
// the devices this node serves, since the agent announces from the state it
// staged rather than from whatever the profile lists.
func TestSimulator_AnnouncesEveryTargetedDevice(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0", "0000:2B:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{All: true}, engine.FailureModeECCUncorrectable, 48)
	s.Poll(t.Context())

	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 48\nkernel: NVRM: Xid (PCI:0000:2b:00): 48\n",
		kernelLog(t, kmsg))
}

// The allocation watcher rewrites the same file every few seconds. Only a
// change in what a device raises is an event; anything else in the document is
// not this simulator's business.
func TestSimulator_IgnoresUnrelatedOverrides(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	path := h.RootPath("driver/config/overrides.yaml")
	doc, err := mockctl.Load(path)
	require.NoError(t, err)
	doc.SetFields(mockctl.Target{Index: 0}, map[string]any{"memory": map[string]any{"used_bytes": 42}})
	require.NoError(t, mockctl.WriteAtomic(path, doc))

	s.Poll(t.Context())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\n", kernelLog(t, kmsg))
}

// The engine deep-merges the shared bucket into the per-device one, so `fail
// --gpu all --xid 79` followed by `fail --gpu 0 --mode lost` leaves device 0
// raising 79 like the rest. Read as shadowing, the device an operator had just
// singled out was the one device on the node that got no kernel line.
func TestSimulator_AnnouncesADeviceSingledOutAfterAll(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0", "0000:2B:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{All: true}, engine.FailureModeLost, 79)
	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 0)
	s.Poll(t.Context())

	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\nkernel: NVRM: Xid (PCI:0000:2b:00): 79\n",
		kernelLog(t, kmsg))
}

// bus_id is hand-authored, and this sink is node-wide and unnamespaced: an
// address is only ever written after passing the same check the rendered PCI
// tree applies, so nobody holding edit on the profile can put lines of their
// own where a health agent reads them.
func TestSimulator_RefusesAnAddressThatIsNotOne(t *testing.T) {
	t.Parallel()

	forged := "0000:1a:00\nkernel: NVRM: Xid (PCI:0000:ff:00): 48"
	h, state, kmsg := node(t, gpus(forged, "0000:2B:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{All: true}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:2b:00): 79\n", kernelLog(t, kmsg),
		"the forged device announces nothing; its neighbour is unaffected")
}

// A write that failed announced nothing, so the Xid is still owed: the next
// tick has to carry it rather than treat it as already reported, which would
// lose it until the fault was cleared and injected again.
func TestSimulator_RetriesAFailedWrite(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0")...)
	s := staged(t, h, state, kmsg)

	// Stands in for a transient failure: the write cannot land while the path
	// is a directory, and can once it is the kernel log again.
	unwritable := filepath.Join(t.TempDir(), "kmsg-dir")
	require.NoError(t, os.Mkdir(unwritable, 0o755))
	s.path = unwritable

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	s.path = kmsg
	s.Poll(t.Context())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\n", kernelLog(t, kmsg))

	s.Poll(t.Context())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\n", kernelLog(t, kmsg),
		"once written it is announced, and stays announced")
}

// Restaging rebuilds the device list, and an index that now names a different
// GPU must not inherit what the old one announced — its first Xid would read as
// a fault already reported.
func TestSimulator_ForgetsRenumberedDevices(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	// A profile edit puts another GPU at index 0, with the fault still standing.
	require.NoError(t, s.Stage(t.Context(), h, &agent.State{Devices: gpus("0000:3C:00.0")}))
	s.Poll(t.Context())

	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\nkernel: NVRM: Xid (PCI:0000:3c:00): 79\n",
		kernelLog(t, kmsg), "the new device announces its own fault")
}

// A profile that declares no pci.bus_id still serves an address through NVML,
// and the kernel line has to name the device the mock names.
func TestSimulator_NamesDevicesTheProfileLeavesAddressless(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	require.Equal(t, xidRecord(engine.BaseDevicePCIBusID(0), 79)+"\n", kernelLog(t, kmsg))
}

// A cluster that will not grant the node agent a writable /dev/kmsg says so
// with an empty path. The NVML injection stands on its own, so that has to be
// quiet rather than an error on every tick.
func TestSimulator_WithoutAKernelLogIsInert(t *testing.T) {
	t.Parallel()

	h, state, _ := node(t, gpus("0000:1A:00.0")...)
	s := New(Options{Path: ""})
	require.NoError(t, s.Stage(t.Context(), h, state))
	require.True(t, s.Ready())

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, s.Run(ctx))
}

// The daemon has to reach Poll on its own: everything above drives it by hand.
func TestSimulator_RunPolls(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0")...)
	s := New(Options{Path: kmsg, Interval: time.Millisecond})
	require.NoError(t, s.Stage(t.Context(), h, state))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	require.Eventually(t, func() bool {
		return kernelLog(t, kmsg) != ""
	}, 5*time.Second, 5*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

// Shutdown withdraws nothing: an Xid already in the kernel log is history, and
// the next agent must be free to announce the same fault again.
func TestSimulator_DiscardIsQuiet(t *testing.T) {
	t.Parallel()

	h, state, kmsg := node(t, gpus("0000:1A:00.0")...)
	s := staged(t, h, state, kmsg)

	inject(t, h, mockctl.Target{Index: 0}, engine.FailureModeLost, 79)
	s.Poll(t.Context())

	require.NoError(t, s.Discard(t.Context(), h))
	require.False(t, s.Ready())
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79\n", kernelLog(t, kmsg))
}
