// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/kubelet"
	"go.uber.org/zap"
)

const name = "numa"

const helperSource = "/usr/local/bin/mokka-kubelet-helper"

// HostRunner applies or restores NUMA setup in the node's mount namespace.
type HostRunner interface {
	Run(context.Context, HostAction, bool) error
}

// Options controls the node filesystem and optional kubelet configuration.
type Options struct {
	Enabled              bool
	ConfigureKubelet     bool
	Policy               string
	CPUManagerPolicy     string
	ReservedCPUs         string
	MemoryManagerPolicy  string
	SystemReservedMemory string
	SystemReservedCPU    string
	// Runner defaults to the privileged pod runner.
	Runner HostRunner
}

// Simulator reapplies the node NUMA view after boot and restores it on disable.
type Simulator struct {
	host         *host.Host
	opts         Options
	ready        atomic.Bool
	desired      string
	runner       HostRunner
	helperSource string
}

// New constructs a simulator without changing the node.
func New(h *host.Host, opts Options) (*Simulator, error) {
	opts = opts.withDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if opts.Runner == nil {
		opts.Runner = NewPodRunner(h)
	}
	sim := &Simulator{
		host:         h,
		opts:         opts,
		helperSource: helperSource,
		runner:       opts.Runner,
	}
	return sim, nil
}

// Name identifies this simulator in health reports.
func (s *Simulator) Name() string { return name }

// Ready reports whether the node setup has completed.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage validates and renders the topology without changing the node mount.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))
	if !s.opts.Enabled {
		return nil
	}
	if err := s.rejectLegacySetup(); err != nil {
		return err
	}
	copyState := *state
	plan, err := Resolve(s.host, &copyState)
	if err != nil {
		return err
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	s.desired = s.configurationKey(data)
	applied, err := s.matchApplied()
	if err != nil {
		return err
	}
	if !applied {
		if err := Render(s.host, plan); err != nil {
			return err
		}
	}
	if err := s.stageKubeletConfig(plan); err != nil {
		return err
	}
	return s.stageFiles(data)
}

func (s *Simulator) stageFiles(data []byte) error {
	if err := s.stageHelper(); err != nil {
		return err
	}
	for name, body := range map[string][]byte{"desired": []byte(s.desired + "\n"), "plan.json": append(data, '\n')} {
		if err := fsutil.Write(s.host.RootPath("numa", name), body, 0700); err != nil {
			return err
		}
	}
	return nil
}

func (s *Simulator) matchApplied() (bool, error) {
	previous, err := readApplied(s.host)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if len(previous) > 0 && strings.TrimSpace(string(previous)) != s.desired {
		return false, errors.New("NUMA topology or policy changed on an active node; disable NUMA simulation to restore the node before applying the new layout")
	}
	return len(previous) > 0, nil
}

// Apply installs or restores the node setup from the privileged agent pod.
func (s *Simulator) Apply(ctx context.Context, _ *agent.State) error {
	if err := s.rejectLegacySetup(); err != nil {
		return err
	}
	_, err := readApplied(s.host)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !s.opts.Enabled && os.IsNotExist(err) {
		registered, err := kubelet.BootResetRegistered(s.host)
		if err != nil {
			return err
		}
		if !registered {
			s.ready.Store(true)
			return nil
		}
	}
	action := ActionApply
	if !s.opts.Enabled {
		action = ActionRestore
	}
	err = s.runHost(ctx, action)
	if err != nil {
		return fmt.Errorf("%s node NUMA filesystem: %w", action, err)
	}
	s.ready.Store(true)
	zap.L().Info("node NUMA simulation reconciled", zap.String("simulator", name), zap.String("action", string(action)), zap.String("policy", s.opts.Policy))
	return nil
}

func (s *Simulator) runHost(ctx context.Context, action HostAction) error {
	return s.runner.Run(ctx, action, s.opts.ConfigureKubelet)
}

func (s *Simulator) stageHelper() error {
	return fsutil.Copy(s.helperSource, s.host.RootPath("kubelet/mokka-kubelet-helper"), 0700)
}

// Revoke keeps node state across pod teardown; explicit disable restores it.
func (s *Simulator) Revoke(context.Context) error { return nil }

// Discard retains staging files and the boot helper across pod replacement.
func (s *Simulator) Discard(context.Context) error { return nil }

var _ agent.Simulator = (*Simulator)(nil)
var _ agent.Applier = (*Simulator)(nil)

func (opts Options) withDefaults() Options {
	if opts.Policy == "" {
		opts.Policy = "single-numa-node"
	}
	if opts.CPUManagerPolicy == "" {
		opts.CPUManagerPolicy = "none"
	}
	if opts.MemoryManagerPolicy == "" {
		opts.MemoryManagerPolicy = "none"
	}
	if opts.SystemReservedMemory == "" {
		opts.SystemReservedMemory = "256Mi"
	}
	return opts
}

func (opts Options) validate() error {
	if !slices.Contains([]string{"none", "best-effort", "restricted", "single-numa-node"}, opts.Policy) {
		return fmt.Errorf("unsupported NUMA topology policy %q", opts.Policy)
	}
	if !slices.Contains([]string{"none", "static"}, opts.CPUManagerPolicy) {
		return fmt.Errorf("unsupported CPU Manager policy %q", opts.CPUManagerPolicy)
	}
	if !slices.Contains([]string{"none", "static"}, opts.MemoryManagerPolicy) {
		return fmt.Errorf("unsupported Memory Manager policy %q", opts.MemoryManagerPolicy)
	}
	return opts.validateManagers()
}

func (opts Options) validateManagers() error {
	if opts.SystemReservedCPU != "" && opts.ReservedCPUs != "" {
		return errors.New("systemReservedCPU and reservedCPUs are mutually exclusive")
	}
	if !opts.Enabled {
		return nil
	}
	if !opts.ConfigureKubelet && (opts.CPUManagerPolicy == "static" || opts.MemoryManagerPolicy == "static") {
		return errors.New("static resource managers require configureKubelet")
	}
	if opts.MemoryManagerPolicy == "static" && opts.CPUManagerPolicy != "static" {
		return errors.New("static Memory Manager requires static CPU Manager")
	}
	return nil
}

func (s *Simulator) configurationKey(topology []byte) string {
	key := fmt.Sprintf("%x:%t:%s:%s:%s:%s:%s", sha256.Sum256(topology), s.opts.ConfigureKubelet, s.opts.Policy, s.opts.CPUManagerPolicy, s.opts.ReservedCPUs, kubeletMemoryPolicy(s.opts.MemoryManagerPolicy), s.opts.SystemReservedMemory)
	if s.opts.SystemReservedCPU != "" {
		key += ":system-cpu=" + s.opts.SystemReservedCPU
	}
	return key
}

func (s *Simulator) rejectLegacySetup() error {
	for _, path := range []string{"numa/.applied", "numa/applied"} {
		if _, err := os.Stat(s.host.RootPath(path)); err == nil {
			return errors.New("restore NUMA with the previous Mokka version before upgrading, or use a fresh node")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
