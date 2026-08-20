// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package source provides StateSource implementations for the Mokka Node Agent.
package source

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

const defaultPollInterval = 5 * time.Second

// FileSource watches a YAML config file and emits State updates on change.
// It polls the config directory so ConfigMap atomic ..data symlink swaps
// are detected correctly — watching the file itself would pin the replaced inode.
type FileSource struct {
	configPath   string
	pollInterval time.Duration
	log          *slog.Logger
}

// NewFileSource returns a FileSource that watches configPath for changes.
func NewFileSource(configPath string, log *slog.Logger) *FileSource {
	return &FileSource{
		configPath:   configPath,
		pollInterval: defaultPollInterval,
		log:          log,
	}
}

// Watch sends the current State immediately and then pushes updates whenever
// the config file content changes. The channel is closed when ctx is done.
func (f *FileSource) Watch(ctx context.Context) <-chan agent.Update {
	ch := make(chan agent.Update, 1)
	go f.run(ctx, ch)
	return ch
}

// Close is a no-op for FileSource; it owns no external resources.
func (f *FileSource) Close() error { return nil }

func (f *FileSource) run(ctx context.Context, ch chan<- agent.Update) {
	defer close(ch)

	var lastHash [32]byte
	f.poll(ctx, ch, &lastHash) // emit immediately on subscribe

	t := time.NewTicker(f.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.poll(ctx, ch, &lastHash)
		}
	}
}

func (f *FileSource) poll(ctx context.Context, ch chan<- agent.Update, lastHash *[32]byte) {
	data, err := os.ReadFile(f.configPath)
	if err != nil {
		f.send(ctx, ch, agent.Update{Err: fmt.Errorf("read config: %w", err), At: time.Now()})
		return
	}

	h := sha256.Sum256(data)
	if h == *lastHash {
		return // content unchanged
	}
	*lastHash = h

	state, err := compileState(data)
	if err != nil {
		f.send(ctx, ch, agent.Update{Err: fmt.Errorf("%s: %w", f.configPath, err), At: time.Now()})
		return
	}
	f.log.Info("state updated from config", "config", f.configPath)
	f.send(ctx, ch, agent.Update{State: state, At: time.Now()})
}

func (f *FileSource) send(ctx context.Context, ch chan<- agent.Update, u agent.Update) {
	select {
	case ch <- u:
	case <-ctx.Done():
	}
}

// compileState parses raw YAML config bytes and builds the agent State.
// Runtime telemetry fields (utilization, power, temperature, clocks) are
// discarded — they belong to the runtime override file owned by nvml-mock-ctl.
func compileState(data []byte) (*agent.State, error) {
	var cfg engine.YAMLConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	state := &agent.State{
		Node: agent.NodeMeta{
			NodeName: os.Getenv("NODE_NAME"),
			Hostname: os.Getenv("HOSTNAME"),
			HostRoot: "/host",
		},
		Software: agent.SoftwareVersions{
			DriverVersion: cfg.System.DriverVersion,
			NVMLVersion:   cfg.System.NVMLVersion,
			CUDAVersion:   cfg.System.CUDAVersion,
		},
	}

	// Resolve device count: system.num_devices wins, then devices list, then default.
	numDevices := len(cfg.Devices)
	if numDevices == 0 {
		numDevices = 8
	}
	if cfg.System.NumDevices > 0 {
		numDevices = cfg.System.NumDevices
	}
	state.NodeShape.NumGPUs = numDevices

	// PCIe topology
	if cfg.PCIeTopology != nil {
		state.NodeShape.Topology.CoresPerNUMA = cfg.PCIeTopology.CoresPerNUMA
		for _, rc := range cfg.PCIeTopology.RootComplexes {
			state.NodeShape.Topology.RootComplexes = append(
				state.NodeShape.Topology.RootComplexes,
				agent.RootComplex{
					ID:          rc.ID,
					NUMANode:    rc.NUMANode,
					DeviceBDFs:  rc.Devices,
					CPUAffinity: rc.CPUAffinity,
				},
			)
		}
	}

	// Per-device specs: merge DeviceDefaults with per-device overrides.
	defaults := cfg.DeviceDefaults
	for i := range numDevices {
		spec := agent.DeviceSpec{
			Index:        i,
			Name:         defaults.Name,
			Architecture: defaults.Architecture,
			Serial:       defaults.Serial,
		}
		if defaults.ComputeCapability != nil {
			spec.ComputeCapMajor = defaults.ComputeCapability.Major
			spec.ComputeCapMinor = defaults.ComputeCapability.Minor
		}
		if defaults.Memory != nil {
			spec.MemoryTotalBytes = defaults.Memory.TotalBytes
		}
		if defaults.PCI != nil {
			spec.PCIDeviceID = defaults.PCI.DeviceID
			spec.PCIBusID = defaults.PCI.BusID
		}

		// Per-device overrides win over defaults.
		for _, ov := range cfg.Devices {
			if ov.Index != i {
				continue
			}
			if ov.UUID != "" {
				spec.UUID = ov.UUID
			}
			if ov.Serial != "" {
				spec.Serial = ov.Serial
			}
			if ov.MinorNumber != 0 {
				spec.MinorNumber = ov.MinorNumber
			}
			if ov.PCI != nil && ov.PCI.BusID != "" {
				spec.PCIBusID = ov.PCI.BusID
			}
		}

		state.Devices = append(state.Devices, spec)
	}

	// NVLink / fabric state
	if cfg.NVLink != nil {
		state.Fabric.Enabled = true
		state.Fabric.LinksPerGPU = cfg.NVLink.LinksPerGPU
		state.Fabric.BandwidthPerLinkMbps = cfg.NVLink.BandwidthPerLinkMbps
	}
	if defaults.Fabric != nil {
		state.Fabric.ClusterUUID = defaults.Fabric.ClusterUUID
		state.Fabric.CliqueID = defaults.Fabric.CliqueID
	}

	return state, nil
}
