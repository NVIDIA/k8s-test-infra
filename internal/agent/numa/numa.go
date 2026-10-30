// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cri"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"go.uber.org/zap"
)

//go:embed host.sh
var hostScript []byte

const hostDir = "/var/lib/nvml-mock/numa"

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
}

// Simulator keeps a simulated NUMA view on the node until explicitly disabled.
type Simulator struct {
	host    *host.Host
	opts    Options
	ready   atomic.Bool
	desired string
	runHost func(context.Context, ...string) error
}

// New constructs a simulator without changing the node.
func New(h *host.Host, opts Options) (*Simulator, error) {
	opts = opts.withDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}
	sim := &Simulator{host: h, opts: opts}
	sim.runHost = func(ctx context.Context, args ...string) error {
		// systemctl uses PID 1's private socket, including on nodes without D-Bus.
		// A linked oneshot runs the mount in the node's mount namespace.
		const unit = "mokka-numa-setup.service"
		body := fmt.Sprintf("[Unit]\nDescription=Mokka GPU-only NUMA setup\n[Service]\nType=oneshot\nTimeoutStartSec=120\nExecStart=/bin/bash %s/host.sh %s\n", hostDir, strings.Join(args, " "))
		if err := fsutil.Write(h.RootPath("numa", unit), []byte(body), 0644); err != nil {
			return err
		}
		for _, command := range [][]string{{"link", "--force", hostDir + "/" + unit}, {"daemon-reload"}, {"start", unit}} {
			if err := cri.Systemctl(ctx, cri.Chroot{Root: h.Prefix}, command...); err != nil {
				return err
			}
		}
		return nil
	}
	return sim, nil
}

// Name identifies this simulator in health reports.
func (s *Simulator) Name() string { return "numa" }

// Ready reports whether the node setup has completed.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage validates and renders the topology without changing the node mount.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	if !s.opts.Enabled {
		return nil
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
	for name, body := range map[string][]byte{"host.sh": hostScript, "desired": []byte(s.desired + "\n"), "plan.json": append(data, '\n')} {
		if err := fsutil.Write(s.host.RootPath("numa", name), body, 0700); err != nil {
			return err
		}
	}
	return nil
}

func (s *Simulator) matchApplied() (bool, error) {
	previous, err := os.ReadFile(s.host.RootPath("numa/applied"))
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if len(previous) > 0 && strings.TrimSpace(string(previous)) != s.desired {
		return false, errors.New("NUMA topology or policy changed on an active node; disable NUMA simulation to restore the node before applying the new layout")
	}
	return len(previous) > 0, nil
}

// Apply installs or restores the persistent node setup.
func (s *Simulator) Apply(ctx context.Context, _ *agent.State) error {
	applied, err := os.ReadFile(s.host.RootPath("numa/applied"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !s.opts.Enabled && os.IsNotExist(err) {
		s.ready.Store(true)
		return nil
	}
	if s.opts.Enabled && strings.TrimSpace(string(applied)) == s.desired {
		s.ready.Store(true)
		return nil
	}
	action := "apply"
	if !s.opts.Enabled {
		action = "restore"
	}
	err = s.runHost(ctx, action, strconv.FormatBool(s.opts.ConfigureKubelet), s.opts.Policy)
	if err != nil {
		return fmt.Errorf("%s node NUMA filesystem: %w", action, err)
	}
	s.ready.Store(true)
	zap.L().Info("node NUMA simulation reconciled", zap.String("action", action), zap.String("policy", s.opts.Policy))
	return nil
}

// Revoke keeps node state across pod teardown; explicit disable restores it.
func (s *Simulator) Revoke(context.Context) error { return nil }

// Discard retains the files needed by the persistent node service.
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
		opts.MemoryManagerPolicy = "None"
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
	if !slices.Contains([]string{"None", "Static"}, opts.MemoryManagerPolicy) {
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
	if !opts.ConfigureKubelet && (opts.CPUManagerPolicy == "static" || opts.MemoryManagerPolicy == "Static") {
		return errors.New("static resource managers require configureKubelet")
	}
	if opts.MemoryManagerPolicy == "Static" && opts.CPUManagerPolicy != "static" {
		return errors.New("static Memory Manager requires static CPU Manager")
	}
	return nil
}

func (s *Simulator) configurationKey(topology []byte) string {
	key := fmt.Sprintf("%x:%t:%s:%s:%s:%s:%s", sha256.Sum256(topology), s.opts.ConfigureKubelet, s.opts.Policy, s.opts.CPUManagerPolicy, s.opts.ReservedCPUs, s.opts.MemoryManagerPolicy, s.opts.SystemReservedMemory)
	if s.opts.SystemReservedCPU != "" {
		key += ":system-cpu=" + s.opts.SystemReservedCPU
	}
	return key
}
