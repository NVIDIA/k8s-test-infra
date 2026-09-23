// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package source provides StateSource implementations for the Mokka Node Agent.
package source

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
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

// settleDelay coalesces the burst a single update produces. Kubernetes renames
// a ConfigMap through a scratch directory and two symlinks, and an editor saves
// by renaming a temporary file, so one change arrives as several events.
const settleDelay = 100 * time.Millisecond

// atomicWritePrefix names the scratch entries Kubernetes renames a ConfigMap
// through: ..data, ..data_tmp and the timestamped directories.
const atomicWritePrefix = ".."

// FileSource watches the profile and the cluster topology document and emits a
// State update when either changes. It watches the directories holding them:
// both a ConfigMap swap and an editor's save replace the file's inode, so a
// watch on the file would hold the one that was just replaced.
type FileSource struct {
	configPath     string
	topologyPath   string
	resyncInterval time.Duration
	log            *zap.Logger
}

// NewFileSource returns a FileSource that watches configPath and topologyPath.
// resyncInterval re-reads both documents regardless of events, covering a host
// where inotify reports nothing — a profile on NFS, or a watch limit reached.
// Zero leaves reloads entirely to events.
func NewFileSource(configPath, topologyPath string, resyncInterval time.Duration, log *zap.Logger) *FileSource {
	return &FileSource{
		configPath:     configPath,
		topologyPath:   topologyPath,
		resyncInterval: resyncInterval,
		log:            log,
	}
}

// Watch sends the current State immediately and then pushes updates whenever
// either watched document changes. The channel is closed when ctx is done.
func (f *FileSource) Watch(ctx context.Context) <-chan agent.Update {
	ch := make(chan agent.Update, 1)
	go f.run(ctx, ch)
	return ch
}

// Close is a no-op for FileSource; run owns the watcher, and ctx bounds run.
func (f *FileSource) Close() error { return nil }

func (f *FileSource) run(ctx context.Context, ch chan<- agent.Update) {
	defer close(ch)

	var (
		events chan fsnotify.Event
		errs   chan error
	)
	w, err := fsnotify.NewWatcher()
	if err != nil {
		// A host can be out of inotify instances, and the agent is still
		// useful there: the resync becomes the only trigger.
		f.log.Error("cannot watch config directories; relying on the resync", zap.Error(err))
	} else {
		defer func() { _ = w.Close() }()
		events, errs = w.Events, w.Errors // a nil channel parks in select
	}

	// Watch before the first read, so an edit landing during startup is queued
	// by the watcher rather than lost between the two.
	names := f.rewatch(w)

	var lastHash [32]byte
	f.reload(ctx, ch, &lastHash) // emit immediately on subscribe

	settle := time.NewTimer(settleDelay)
	settle.Stop()
	defer settle.Stop()

	var resync <-chan time.Time
	if f.resyncInterval > 0 {
		t := time.NewTicker(f.resyncInterval)
		defer t.Stop()
		resync = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case e := <-events:
			if names.carries(e) {
				settle.Reset(settleDelay)
			}
		case err := <-errs:
			// Usually a queue overflow, which means events were dropped: the
			// change behind them would otherwise go unseen until the resync.
			f.log.Warn("config watch error", zap.Error(err))
			settle.Reset(settleDelay)
		case <-settle.C:
			names = f.rewatch(w)
			f.reload(ctx, ch, &lastHash)
		case <-resync:
			names = f.rewatch(w)
			f.reload(ctx, ch, &lastHash)
		}
	}
}

// watched is the set of file names whose events are worth a re-read.
type watched map[string]bool

// carries reports whether an event names one of the documents, or one of the
// scratch entries Kubernetes renames a ConfigMap through. A config directory
// shared with other processes, as on a host, churns with everything else.
func (w watched) carries(e fsnotify.Event) bool {
	name := filepath.Base(e.Name)

	return w[name] || strings.HasPrefix(name, atomicWritePrefix)
}

// rewatch attaches the watcher to the directory holding each document and
// returns the names to react to. It runs before every reload: a save can
// retarget a symlink, and a directory that was missing — an unmounted topology
// ConfigMap — may have appeared since.
func (f *FileSource) rewatch(w *fsnotify.Watcher) watched {
	names := watched{}
	for _, path := range []string{f.configPath, f.topologyPath} {
		if path == "" {
			continue // --topology is unset where the cluster declares none
		}
		f.watchDir(w, path, names)

		// A profile is usually reached through a symlink, and edits land in the
		// directory it resolves into rather than the one the path names: ..data
		// inside a ConfigMap mount, /opt/profiles behind /etc on a host.
		if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
			f.watchDir(w, resolved, names)
		}
	}

	return names
}

func (f *FileSource) watchDir(w *fsnotify.Watcher, path string, names watched) {
	names[filepath.Base(path)] = true
	if w == nil {
		return
	}

	dir := filepath.Dir(path)
	if err := w.Add(dir); err != nil {
		f.log.Warn("cannot watch directory; the resync still reloads it",
			zap.String("dir", dir), zap.Error(err))
	}
}

// reload reads both documents and emits a State when their contents differ from
// the last emission. It is the only place a change is detected, whether an event
// or the resync brought us here.
func (f *FileSource) reload(ctx context.Context, ch chan<- agent.Update, lastHash *[32]byte) {
	data, err := os.ReadFile(f.configPath)
	if err != nil {
		f.send(ctx, ch, agent.Update{Err: fmt.Errorf("read config: %w", err), At: time.Now()})
		return
	}

	topology, err := readTopology(f.topologyPath, f.log)
	if err != nil {
		f.send(ctx, ch, agent.Update{Err: err, At: time.Now()})
		return
	}

	h := inputsHash(data, topology)
	if h == *lastHash {
		f.log.Debug("config and topology unchanged; skipping reconcile")
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
	f.log.Info("state updated from config", zap.String("config", f.configPath))
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
func readTopology(path string, log *zap.Logger) ([]byte, error) {
	if path == "" {
		log.Debug("no topology path configured; ComputeDomain topology disabled")
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		log.Debug("topology file not yet mounted", zap.String("path", path))
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

// parseProfile decodes a profile and checks what the agent acts on before the
// engine ever sees the file. The agent stages the character devices and writes
// both CDI specs, so it cannot wait for the engine to reject minors that
// collide. Only that check runs here: the rest of the engine's validation
// demands fields the agent deliberately tolerates, driver_version among them.
func parseProfile(data []byte) (engine.YAMLConfig, error) {
	var cfg engine.YAMLConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse yaml: %w", err)
	}
	return cfg, engine.ValidateMinorNumbers(&cfg)
}

// compileState parses raw YAML config bytes and builds the agent State.
// Runtime telemetry fields (utilization, power, temperature, clocks) are
// discarded — they belong to the runtime override file owned by nvml-mock-ctl.
func compileState(data []byte) (*agent.State, error) {
	cfg, err := parseProfile(data)
	if err != nil {
		return nil, err
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
	if v, err := strconv.Atoi(os.Getenv("GPU_COUNT")); err == nil && v > 0 {
		if v > n {
			// Silently capping would leave an operator who asked for more GPUs
			// than the profile declares wondering why nvidia-smi disagrees.
			zap.L().Warn("GPU_COUNT exceeds the profile device count; capping",
				zap.Int("requested", v), zap.Int("profile_devices", n))
		} else {
			n = v
		}
	}
	return n
}

func buildDeviceSpec(i int, defaults engine.DeviceConfig, devices []engine.DeviceOverride) agent.DeviceSpec {
	spec := agent.DeviceSpec{
		Index: i,
		// Absent an explicit minor_number, the driver is taken to have probed
		// in index order.
		MinorNumber:  i,
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
	if ov.MinorNumber != nil {
		spec.MinorNumber = *ov.MinorNumber
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
