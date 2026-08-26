// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package infiniband implements the InfiniBand HCA simulator: a fake
// /sys/class/infiniband tree, the LD_PRELOAD shims and IB CLI tools that read
// it, and the mock-ib daemon serving UMAD and verbs over a Unix socket.
package infiniband

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/daemon"
)

const (
	name = "infiniband"
	// socketName matches the chart's MOCK_IB_PING_SOCKET basename, which the
	// LD_PRELOAD shims in workload pods dial.
	socketName = "mock-ib.sock"
)

// Mode selects how much of the IB stack is simulated.
type Mode string

const (
	// ModeOff mocks nothing. The shims stay LD_PRELOAD'd and no-op.
	ModeOff Mode = "off"
	// ModeSysfs renders the sysfs tree only: ibstat and ibstatus work, and any
	// real host IB stays masked. No daemon, so no UMAD or verbs traffic.
	ModeSysfs Mode = "sysfs"
	// ModeFull adds the mock-ib daemon backing UMAD, verbs and ibping.
	ModeFull Mode = "full"
)

// ParseMode validates a MOCK_IB tier, case-insensitively. An unknown value is
// an error rather than a silent fallback: a typo must not disable IB quietly.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case ModeOff, ModeSysfs, ModeFull:
		return m, nil
	case "":
		return ModeOff, nil
	default:
		return "", fmt.Errorf("invalid IB mode %q: expected off, sysfs or full", s)
	}
}

// Options configures the simulator.
type Options struct {
	Mode Mode
	// SocketPath overrides the derived socket location. Production leaves it
	// empty: the path must land on the host mount, which only Stage knows.
	SocketPath string
	TCPPort    int
	Fabric     bool
}

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Daemon    = (*Simulator)(nil)
)

// Simulator fakes the InfiniBand HCAs (Host Channel Adapters) absent on
// CPU-only nodes. Consumers reach it through the LD_PRELOAD shims: sysfs reads
// via libibmocksys, verbs and UMAD via libibmockverbs and the daemon.
type Simulator struct {
	opts Options

	ready   atomic.Bool
	serving atomic.Bool

	// Captured by Stage because Run is called without a Host. Both must resolve
	// under the host mount so workloads and the agent address the same files.
	ibRootPath atomic.Pointer[string]
	socketPath atomic.Pointer[string]

	// lastStaged is the shape the tree holds; dirty says it changed since the
	// daemon was built. Stage does the comparison because it runs before Reload
	// in one reconcile — comparing in Reload would always see its own refresh.
	lastStaged atomic.Pointer[agent.NetworkShape]
	dirty      atomic.Bool

	// restart carries reload requests to the Run loop. Buffered depth 1: a
	// pending signal already covers any newer shape.
	restart chan struct{}
}

// New returns an infiniband Simulator.
func New(opts Options) *Simulator {
	return &Simulator{opts: opts, restart: make(chan struct{}, 1)}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether Stage succeeded and, where a daemon is expected, it is
// serving.
func (s *Simulator) Ready() bool {
	if !s.ready.Load() {
		return false
	}
	return !s.daemonExpected() || s.serving.Load()
}

// daemonExpected reports whether this node should be running mock-ib. Full mode
// alone is not enough: the chart may select it on a profile that declares no
// InfiniBand, and readiness must not then wait on a daemon that never starts.
func (s *Simulator) daemonExpected() bool {
	if s.opts.Mode != ModeFull {
		return false
	}
	staged := s.lastStaged.Load()
	return staged != nil && staged.IBEnabled
}

// Stage renders the IB sysfs tree and installs the tools and shims that read it.
// It is a no-op (but marks ready) when the mode is off or the profile declares
// no InfiniBand.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	// The root exists even when nothing is rendered into it: MOCK_IB_ROOT is
	// LD_PRELOAD'd into every process regardless of tier, and an empty tree is
	// what masks any real host InfiniBand.
	root := ibRoot(h)
	if err := h.MkdirAll(root, 0o755); err != nil {
		return err
	}
	s.ibRootPath.Store(&root)

	socket := s.opts.SocketPath
	if socket == "" {
		socket = h.RootPath("run", socketName)
	}
	s.socketPath.Store(&socket)

	net := state.NodeShape.Network
	if s.opts.Mode == ModeOff || !net.IBEnabled {
		s.ready.Store(true)
		return nil
	}
	s.recordShape(net)

	if err := stageSysfs(h, state); err != nil {
		return fmt.Errorf("render ib sysfs: %w", err)
	}
	for _, stage := range []func(*host.Host) error{
		stageIBTools, stageIBShims, stageVerbsConfig, stageCheckFabric,
	} {
		if err := stage(h); err != nil {
			return err
		}
	}

	s.ready.Store(true)
	return nil
}

// recordShape stores the staged shape and flags a change for Reload.
func (s *Simulator) recordShape(net agent.NetworkShape) {
	if prev := s.lastStaged.Load(); prev != nil && *prev != net {
		s.dirty.Store(true)
	}
	s.lastStaged.Store(&net)
}

// Discard removes the rendered tree and every file Stage copied into the
// overlay. It is a no-op when Stage never completed successfully.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	if !s.ready.Load() {
		return nil
	}
	return errors.Join(
		// The whole ib/ subtree: infiniband is its only writer.
		removeTree(ibRoot(h)),
		discardTools(h),
		discardShims(h),
		removeTree(h.RootPath("driver/etc/libibverbs.d")),
	)
}

// discardTools removes the binaries and libraries Stage copied in. Named files
// rather than directories: driver/usr/bin and driver/usr/lib64 are shared with
// gpudriver, whose nvidia-smi and libnvidia-ml must survive.
func discardTools(h *host.Host) error {
	errs := make([]error, 0, 3+len(fallbackTools))
	errs = append(errs,
		removeStaged(h, filepath.Join(toolBundleRoot, "bin"), "driver/usr/bin"),
		removeStaged(h, filepath.Join(toolBundleRoot, "lib64"), "driver/usr/lib64"),
		h.Remove(h.RootPath("driver/usr/bin/check-fabric")),
	)
	for _, tool := range fallbackTools {
		errs = append(errs, h.Remove(h.RootPath("driver/usr/bin", tool)))
	}
	return errors.Join(errs...)
}

func discardShims(h *host.Host) error {
	matches, _ := filepath.Glob(h.RootPath("driver/usr/local/lib/libibmock*.so*"))
	errs := make([]error, 0, len(matches))
	for _, p := range matches {
		errs = append(errs, h.Remove(p))
	}
	return errors.Join(errs...)
}

func removeTree(path string) error {
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// removeStaged deletes from dstRel every file that srcDir contributed.
func removeStaged(h *host.Host, srcDir, dstRel string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil // nothing was staged from here
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := h.Remove(h.RootPath(dstRel, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// serverRestartBackoff throttles the Run loop when the daemon cannot be built,
// so a malformed tree cannot spin it hot.
const serverRestartBackoff = 2 * time.Second

// Run serves UMAD and verbs until ctx is cancelled, rebuilding the daemon
// whenever Reload reports a changed IB shape. The agent calls this on the Stage
// barrier, so the tree it scans already exists.
func (s *Simulator) Run(ctx context.Context) error {
	if s.opts.Mode != ModeFull {
		return nil
	}

	for ctx.Err() == nil {
		// Park rather than return when the profile declares no IB: Run is
		// launched once, so returning would strand a later edit that turns it on.
		if !s.daemonExpected() {
			s.awaitRestart(ctx)
			continue
		}
		if err := s.serveOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("mock-ib daemon exited", "simulator", name, "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(serverRestartBackoff):
			}
		}
	}
	return nil
}

func (s *Simulator) awaitRestart(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-s.restart:
	}
}

// serveOnce runs one daemon generation, returning when ctx is cancelled or a
// restart is requested.
func (s *Simulator) serveOnce(ctx context.Context) error {
	root, socket := s.ibRootPath.Load(), s.socketPath.Load()
	if root == nil || socket == nil {
		return errors.New("ib root not staged")
	}

	genCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Rebuilding drops the peer registry, but peers re-register on
	// registerWithPeersLoop's 2s cadence, so the fabric re-converges.
	go func() {
		select {
		case <-genCtx.Done():
		case <-s.restart:
			cancel()
		}
	}()

	srv, err := daemon.NewServer(daemon.Config{
		SocketPath: *socket,
		IBRoot:     *root,
		TCPPort:    s.opts.TCPPort,
		Fabric:     s.opts.Fabric,
	}, slog.NewLogLogger(slog.Default().With("simulator", name).Handler(), slog.LevelInfo))
	if err != nil {
		return err
	}

	s.serving.Store(true)
	defer s.serving.Store(false)

	if err := srv.ListenAndServe(genCtx); err != nil && genCtx.Err() == nil {
		return err
	}
	return nil
}

// Reload rebuilds the daemon only when Stage saw the IB shape change, so an
// unrelated GPU-field edit never bounces the socket.
func (s *Simulator) Reload(_ context.Context, _ *agent.State) error {
	if s.opts.Mode != ModeFull || !s.dirty.Swap(false) {
		return nil
	}
	select {
	case s.restart <- struct{}{}:
	default: // a restart is already pending
	}
	return nil
}
