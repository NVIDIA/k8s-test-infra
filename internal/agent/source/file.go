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
	ibconfig "github.com/NVIDIA/k8s-test-infra/internal/ib/config"
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

// FileSource watches the profile and the cluster topology document and emits a
// State update when either changes. It polls the containing directories so
// ConfigMap atomic ..data symlink swaps are detected correctly — watching a file
// itself would pin the replaced inode.
type FileSource struct {
	configPath   string
	topologyPath string
	pollInterval time.Duration
	log          *slog.Logger
}

// NewFileSource returns a FileSource that watches configPath and topologyPath.
func NewFileSource(configPath, topologyPath string, log *slog.Logger) *FileSource {
	return &FileSource{
		configPath:   configPath,
		topologyPath: topologyPath,
		pollInterval: defaultPollInterval,
		log:          log,
	}
}

// Watch sends the current State immediately and then pushes updates whenever
// either watched document changes. The channel is closed when ctx is done.
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

	topology, err := readTopology(f.topologyPath)
	if err != nil {
		f.send(ctx, ch, agent.Update{Err: err, At: time.Now()})
		return
	}

	h := inputsHash(data, topology)
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
	state.TopologyRaw = topology
	f.log.Info("state updated from config", "config", f.configPath)
	f.send(ctx, ch, agent.Update{State: state, At: time.Now()})
}

func (f *FileSource) send(ctx context.Context, ch chan<- agent.Update, u agent.Update) {
	select {
	case ch <- u:
	case <-ctx.Done():
	}
}

// readTopology returns the cluster topology document, or nil where the node has
// none — an unset path or an unmounted ConfigMap. Other read failures surface as
// errors, because a nil document retracts the one already staged on this node.
func readTopology(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read topology: %w", err)
	}

	return data, nil
}

// inputsHash digests both documents so an edit to either is a change. Hashing
// the fixed-width sums keeps a byte from shifting across the boundary.
func inputsHash(config, topology []byte) [32]byte {
	c, t := sha256.Sum256(config), sha256.Sum256(topology)

	return sha256.Sum256(append(c[:], t[:]...))
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

	driverParams, err := compileDriverParams(data)
	if err != nil {
		return nil, err
	}
	state.DriverParams = driverParams

	numDevices := resolveDeviceCount(cfg)
	state.NodeShape.NumGPUs = numDevices

	state.NodeShape.Topology = compileTopology(cfg)

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

	network, err := compileNetwork(data, numDevices)
	if err != nil {
		return nil, err
	}
	state.NodeShape.Network = network

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

// compileTopology resolves the root-complex layout. PCIe-only profiles omit the
// block entirely, leaving a zero topology.
func compileTopology(cfg engine.YAMLConfig) agent.PCIeTopology {
	if cfg.PCIeTopology == nil {
		return agent.PCIeTopology{}
	}

	topology := agent.PCIeTopology{
		CoresPerNUMA:  cfg.PCIeTopology.CoresPerNUMA,
		RootComplexes: make([]agent.RootComplex, 0, len(cfg.PCIeTopology.RootComplexes)),
	}

	for _, rc := range cfg.PCIeTopology.RootComplexes {
		topology.RootComplexes = append(topology.RootComplexes, agent.RootComplex{
			ID:          rc.ID,
			NUMANode:    rc.NUMANode,
			DeviceBDFs:  rc.Devices,
			CPUAffinity: rc.CPUAffinity,
		})
	}

	return topology
}

// driverProfile is the slice of the profile YAML carrying nvidia kernel-module
// parameters. It is parsed separately from engine.YAMLConfig because the NVML
// engine never reads these: they exist for the consumers that go straight to
// /proc/driver/nvidia/params, nvidia-modprobe above all.
type driverProfile struct {
	Driver *driverBlock `json:"driver"`
}

// driverBlock uses pointers throughout so that a silent profile is
// distinguishable from one asking for uid 0 or for device-file management off.
type driverBlock struct {
	DeviceFileUID *int `json:"device_file_uid"`
	DeviceFileGID *int `json:"device_file_gid"`
	// DeviceFileMode takes an octal literal (0660), which YAML resolves the
	// way a leading zero implies, or the decimal form the driver itself
	// reports (432). Plain 660 is neither and means mode 01224.
	DeviceFileMode    *int  `json:"device_file_mode"`
	ModifyDeviceFiles *bool `json:"modify_device_files"`
}

// compileDriverParams resolves the device-node parameters, falling back to what
// the real nvidia module reports when the profile says nothing. Defaults land
// here rather than at write time so State reaches every simulator fully
// resolved and gpudriver never re-derives them.
func compileDriverParams(data []byte) (agent.DriverParams, error) {
	params := agent.DriverParams{DeviceFileMode: 0o666, ModifyDeviceFiles: true}

	var prof driverProfile
	if err := yaml.Unmarshal(data, &prof); err != nil {
		return params, fmt.Errorf("parse driver params: %w", err)
	}

	if prof.Driver == nil {
		return params, nil
	}

	if prof.Driver.DeviceFileUID != nil {
		params.DeviceFileUID = *prof.Driver.DeviceFileUID
	}

	if prof.Driver.DeviceFileGID != nil {
		params.DeviceFileGID = *prof.Driver.DeviceFileGID
	}

	if prof.Driver.DeviceFileMode != nil {
		params.DeviceFileMode = *prof.Driver.DeviceFileMode
	}

	if prof.Driver.ModifyDeviceFiles != nil {
		params.ModifyDeviceFiles = *prof.Driver.ModifyDeviceFiles
	}

	return params, nil
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
		Index: i,
		// The driver hands out one minor per device in order. Resolving it here
		// keeps /dev/nvidiaN, NVML and the procfs GPU directories in agreement
		// on profiles that list devices without spelling out minor_number.
		MinorNumber:  i,
		Name:         defaults.Name,
		Architecture: defaults.Architecture,
		Serial:       defaults.Serial,
		VBIOSVersion: defaults.VBIOSVersion,
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
	if ov.VBIOSVersion != "" {
		spec.VBIOSVersion = ov.VBIOSVersion
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

// compileNetwork resolves the profile's infiniband block into a NetworkShape.
// It parses the same bytes a second time through mockib's own partial schema,
// which already models this block and carries the default ladder the renderer
// expects; engine.YAMLConfig deliberately does not model it because the NVML
// engine never reads it.
func compileNetwork(data []byte, numGPUs int) (agent.NetworkShape, error) {
	var prof ibconfig.Profile
	if err := yaml.Unmarshal(data, &prof); err != nil {
		return agent.NetworkShape{}, fmt.Errorf("parse infiniband: %w", err)
	}
	if !prof.Infiniband.Enabled {
		return agent.NetworkShape{}, nil
	}

	// Resolve defaults here rather than leaving them to render time, so State
	// fully describes the node and Reload can compare two shapes for equality.
	ib := prof.Infiniband.Defaults()

	hcaCount := ib.HCACountOverride
	if hcaCount <= 0 {
		hcaCount = numGPUs * ib.HCAsPerGPU
	}

	return agent.NetworkShape{
		IBEnabled:        true,
		HCACount:         hcaCount,
		HCAType:          ib.HCAType,
		FWVersion:        ib.FWVersion,
		HWRev:            ib.HWRev,
		BoardID:          ib.BoardID,
		NodeDescTemplate: ib.NodeDescTemplate,
		LinkLayer:        ib.LinkLayer,
		RateGbps:         ib.RateGbps,
		PortState:        ib.PortState,
		PhysState:        ib.PhysState,
		GUIDPrefix:       ib.GUIDPrefix,
	}, nil
}
