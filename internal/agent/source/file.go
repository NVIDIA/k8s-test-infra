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
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// envIntOrDefault returns the integer value of the env var key, or def when
// the var is unset or not a valid integer.
func envIntOrDefault(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

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
	state.ConfigRaw = data
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

	dv := cfg.System.DriverVersion
	if dv == "" {
		// TODO(https://github.com/NVIDIA/k8s-test-infra/issues/717): bootstrap
		// shim — remove once Profile/Runtime split lands and driverVersion is
		// always embedded in the profile.
		dv = os.Getenv("DRIVER_VERSION")
	}
	state := &agent.State{
		Node: agent.NodeMeta{
			NodeName: os.Getenv("NODE_NAME"),
			Hostname: os.Getenv("HOSTNAME"),
			HostRoot: "/host",
		},
		Software: agent.SoftwareVersions{
			DriverVersion: dv,
			NVMLVersion:   cfg.System.NVMLVersion,
			CUDAVersion:   cfg.System.CUDAVersion,
		},
	}

	numDevices := resolveDeviceCount(cfg)
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
		state.Devices = append(state.Devices, buildDeviceSpec(i, defaults, cfg.Devices))
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
	// Set by the chart only where it also creates the marker directory and
	// starts the daemon, so it is the one signal that both exist on this node.
	state.Fabric.ManagerStateDir = strings.TrimSpace(os.Getenv(engine.EnvFabricStateDir))

	// IMEX mock surface: opt-in via IMEX_MOCK_CHANNELS=true, same shim
	// pattern as GPU_COUNT until MEP-0001 embeds this in the profile.
	if os.Getenv("IMEX_MOCK_CHANNELS") == "true" {
		state.IMEX = agent.IMEXState{
			Enabled:      true,
			IMEXMajor:    envIntOrDefault("IMEX_CHANNEL_MAJOR", 235),
			CapsMajor:    envIntOrDefault("IMEX_CAPS_MAJOR", 236),
			ChannelCount: envIntOrDefault("IMEX_CHANNEL_COUNT", 2048),
		}
	}

	return state, nil
}

// resolveDeviceCount returns the active GPU count from the profile, applying
// system.num_devices and then the GPU_COUNT env var as successive overrides.
// TODO(https://github.com/NVIDIA/k8s-test-infra/issues/717): GPU_COUNT is a
// bootstrap shim — runtime overrides should flow through a dedicated Runtime
// state populated by MEP-0001 CP and nvml-mock-ctl, not via env vars here.
func resolveDeviceCount(cfg engine.YAMLConfig) int {
	n := len(cfg.Devices)
	if n == 0 {
		n = 8
	}
	if cfg.System.NumDevices > 0 {
		n = cfg.System.NumDevices
	}
	if v, err := strconv.Atoi(os.Getenv("GPU_COUNT")); err == nil && v > 0 && v < n {
		n = v
	}
	return n
}

func buildDeviceSpec(i int, defaults engine.DeviceConfig, devices []engine.DeviceOverride) agent.DeviceSpec {
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
		spec.PCISubsystemID = defaults.PCI.SubsystemID
		spec.PCIBusID = defaults.PCI.BusID
	}
	for _, ov := range devices {
		if ov.Index != i {
			continue
		}
		applyDeviceOverride(&spec, ov)
	}
	return spec
}

func applyDeviceOverride(spec *agent.DeviceSpec, ov engine.DeviceOverride) {
	if ov.UUID != "" {
		spec.UUID = ov.UUID
	}
	if ov.Serial != "" {
		spec.Serial = ov.Serial
	}
	if ov.MinorNumber != 0 {
		spec.MinorNumber = ov.MinorNumber
	}
	// Each PCI field overrides independently, matching how the mock NVML engine
	// merges the same block (engine/config.go): a device that sets only bus_id
	// keeps the profile's identity words.
	if ov.PCI != nil {
		if ov.PCI.BusID != "" {
			spec.PCIBusID = ov.PCI.BusID
		}
		if ov.PCI.DeviceID != 0 {
			spec.PCIDeviceID = ov.PCI.DeviceID
		}
		if ov.PCI.SubsystemID != 0 {
			spec.PCISubsystemID = ov.PCI.SubsystemID
		}
	}
}
