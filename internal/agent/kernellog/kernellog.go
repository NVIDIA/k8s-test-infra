// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package kernellog simulates the kernel side of an Xid: the printk a real
// driver emits when a GPU faults.
//
// It belongs to the agent rather than to nvml-mock-ctl because the kernel log
// is host state, like the character devices and the PCI sysfs tree the other
// simulators write, and because the fault is not the CLI's to announce — an
// injection reaches the mock through the runtime override document, whoever
// writes it. Watching that document instead of the CLI's own invocation is what
// lets the allocation watcher, a hand-edited file or a future control plane
// raise an Xid that the agents watching this node can see.
package kernellog

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

const name = "kernellog"

// defaultInterval matches the TTL of the engine's own override cache, so the
// kernel line and the NVML event follow an injection within the same second.
const defaultInterval = time.Second

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Daemon    = (*Simulator)(nil)
)

// Simulator announces newly injected Xids on the node's kernel log.
type Simulator struct {
	path     string // kernel log to write; empty announces nowhere
	interval time.Duration
	staged   atomic.Bool

	mu        sync.Mutex
	overrides string         // runtime override document to watch
	devices   []device       // in device order, so a fault on several prints in it
	announced map[int]uint64 // device index -> Xid already on the kernel log
	warned    map[int]uint64 // device index -> Xid we already failed to write
	lastErr   string         // deduplicates a persistent read failure
}

// device is what this simulator needs of a GPU: which bucket of the override
// document speaks for it, and what to call it in a kernel line.
type device struct {
	index int
	busID string
}

// Options configures the simulator.
type Options struct {
	// Path is the kernel log to write, /dev/kmsg on a node that grants one.
	// Empty disables the announcement, which is how a deployment that cannot
	// reach the kernel log says so: the NVML side of an injection stands on its
	// own, so this is a quiet degradation rather than an error.
	Path string
	// Interval is how often the override document is re-read. Zero picks a
	// default aligned with the engine's own polling.
	Interval time.Duration
}

// New returns a kernel log Simulator.
func New(opts Options) *Simulator {
	interval := opts.Interval
	if interval == 0 {
		interval = defaultInterval
	}

	return &Simulator{
		path:      opts.Path,
		interval:  interval,
		announced: map[int]uint64{},
		warned:    map[int]uint64{},
	}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the simulator knows which devices this node serves.
func (s *Simulator) Ready() bool { return s.staged.Load() }

// Stage records where the injections arrive and which address each device
// answers to, so a kernel line names the device the way NVML does. It writes
// nothing: the artifact this simulator produces is an event, not a file.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	zap.L().Info("staging simulator", zap.String("simulator", name))

	s.mu.Lock()
	defer s.mu.Unlock()

	// The document nvml-mock-ctl writes and the engine reads. Derived rather
	// than configured because both ends already agree on it: the DaemonSet
	// points MOCK_NVML_OVERRIDES here, and it is the CLI's default besides.
	s.overrides = h.RootPath("driver/config/overrides.yaml")

	previous := s.devices
	s.devices = make([]device, 0, len(state.Devices))
	for _, d := range state.Devices {
		// A profile is free to leave addressing to the mock, and such a device
		// still serves the address it inherits from the base mock. Naming it
		// any other way would leave the kernel line pointing at no GPU.
		busID := strings.ToLower(d.PCIBusID)
		if busID == "" {
			busID = engine.BaseDevicePCIBusID(d.Index)
		}

		// The boundary the rendered PCI tree already applies to bus_id, for a
		// sharper reason: the address goes into the node's kernel ring buffer,
		// which is unnamespaced and read by health agents, so an address
		// carrying a newline would let whoever can edit the profile put lines
		// of their own where a remediator reads them. Logged verbatim, which is
		// safe — zap escapes it — and is what names the offending value.
		if !agent.ValidBDF(busID) {
			zap.L().Warn("device has no usable PCI address; its Xids cannot be announced",
				zap.String("simulator", name), zap.Int("device", d.Index),
				zap.String("pci", d.PCIBusID))
			continue
		}
		s.devices = append(s.devices, device{index: d.Index, busID: busID})
	}

	// A profile edit can renumber devices, and an index that now names another
	// GPU must not inherit what the old one announced, or the new device's
	// first Xid is read as a fault already reported.
	s.forgetRenumbered(previous)

	s.staged.Store(true)
	// Says how many devices can be named, which is what a silent kernel log
	// comes down to when a profile's addresses do not survive the check above.
	zap.L().Info("simulator staged", zap.String("simulator", name),
		zap.Int("devices", len(s.devices)), zap.String("kernelLog", s.path))

	return nil
}

// forgetRenumbered drops what was announced for any device whose address is not
// the one it had at the previous staging, including a device that is gone.
func (s *Simulator) forgetRenumbered(previous []device) {
	current := make(map[int]string, len(s.devices))
	for _, d := range s.devices {
		current[d.index] = d.busID
	}

	for _, was := range previous {
		if current[was.index] != was.busID {
			delete(s.announced, was.index)
			delete(s.warned, was.index)
		}
	}
}

// Run announces injections until the agent shuts down.
func (s *Simulator) Run(ctx context.Context) error {
	if s.path == "" {
		<-ctx.Done()
		return nil
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.Poll(ctx)
		}
	}
}

// Reload is a no-op: Stage already delivers every revision.
func (s *Simulator) Reload(_ context.Context, _ *agent.State) error { return nil }

// Discard stops the announcements. It retracts nothing — a kernel log never
// takes an Xid back — and forgets what it announced, so the next agent may
// report a fault that outlives this one.
func (s *Simulator) Discard(_ context.Context, _ *host.Host) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))
	s.staged.Store(false)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.announced = map[int]uint64{}
	s.warned = map[int]uint64{}

	return nil
}

// Poll announces every Xid that has appeared since the last call. It is the
// unit of work Run repeats, exported so a caller can drive it directly.
//
// Nothing here fails the agent: the fault has already been injected into NVML,
// and a node whose kernel log cannot be written is worse off but not broken.
func (s *Simulator) Poll(_ context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Not yet staged, or a deployment that told us it has no kernel log: in
	// neither case is there anything to say.
	if s.overrides == "" || s.path == "" {
		return
	}

	doc, err := mockctl.Load(s.overrides)
	if err != nil {
		// Once per distinct failure: a malformed document stays malformed
		// until someone rewrites it, and this runs every second.
		if msg := err.Error(); msg != s.lastErr {
			s.lastErr = msg
			zap.L().Warn("cannot read runtime overrides; Xids will not be announced",
				zap.String("simulator", name), zap.String("path", s.overrides), zap.Error(err))
		}
		return
	}
	s.lastErr = ""

	for _, d := range s.devices {
		code := doc.FailureXid(d.index)
		if code == 0 {
			// Recovered, or never faulted. Forgetting it here is what makes a
			// repeat of the same Xid a new event rather than a duplicate.
			delete(s.announced, d.index)
			delete(s.warned, d.index)
			continue
		}
		// A code this device is already known to raise is the same fault, not a
		// new one. This does swallow a clear and a re-arm of the same code that
		// both land between two reads: what comes back is byte-for-byte the
		// document already announced, so no reader of state can tell the two
		// apart, at any poll rate. Harmless for a reader of the log, since
		// recovery prints nothing either — one line and two both say the device
		// raised this Xid and never took it back.
		if s.announced[d.index] == code {
			continue
		}

		s.emit(d, code)
	}
}

func (s *Simulator) emit(d device, code uint64) {
	wrote, err := emitXid(s.path, d.busID, code)
	if wrote {
		s.announced[d.index] = code
		delete(s.warned, d.index)
		zap.L().Info("announced Xid on the kernel log",
			zap.String("simulator", name), zap.Int("device", d.index),
			zap.String("pci", d.busID), zap.Uint64("xid", code))

		return
	}

	// A write that did not happen is not an announcement, so it is left out of
	// announced and the next tick tries again: a transient failure costs a
	// second rather than the Xid, which would otherwise go unannounced until
	// the fault was cleared and injected afresh. The dedupe moves onto the
	// warning, which is what must not repeat every second.
	if s.warned[d.index] == code {
		return
	}
	s.warned[d.index] = code

	if err != nil {
		zap.L().Warn("could not announce Xid on the kernel log",
			zap.String("simulator", name), zap.String("path", s.path),
			zap.Int("device", d.index), zap.Uint64("xid", code), zap.Error(err))

		return
	}

	zap.L().Warn("no kernel log on this node; Xid announced nowhere",
		zap.String("simulator", name), zap.String("path", s.path),
		zap.Int("device", d.index), zap.Uint64("xid", code))
}
