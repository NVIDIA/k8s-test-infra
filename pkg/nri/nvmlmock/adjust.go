// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvmlmock

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// warnf logs a non-fatal condition. It is a package var so tests can capture
// or silence the output.
var warnf = func(format string, args ...any) {
	log.Printf("nvml-mock-nri: "+format, args...)
}

const (
	defaultHostOverlayPath       = "/var/lib/nvml-mock"
	defaultContainerOverlayPath  = "/opt/nvml-mock"
	defaultDeviceHostPath        = "/var/lib/nvml-mock/driver/dev"
	defaultOptOutAnnotation      = "nvml-mock.nvidia.com/inject"
	defaultDeviceAnnotation      = "nvml-mock.nvidia.com/devices"
	defaultImexChannelAnnotation = "nvml-mock.nvidia.com/imex-channels"
	// defaultImexChannelRelPath is where the main DaemonSet's setup.sh mknods
	// the mock IMEX channel nodes, resolved relative to the host overlay path.
	// It must track the imex.mockChannels surface in the chart: setup.sh writes
	// them to $DRIVER_ROOT/dev/nvidia-caps-imex-channels.
	defaultImexChannelRelPath = "driver/dev/nvidia-caps-imex-channels"
	// imexChannelContainerDir is the fixed kernel location the channels must
	// appear at inside the container. Consumers (nvidia-imex, the DRA driver's
	// compute-domain plugin) hard-code this path.
	imexChannelContainerDir = "/dev/nvidia-caps-imex-channels"
	// defaultTopologyRelPath is where setup.sh stages the cluster-level
	// ComputeDomain topology document inside the overlay tree. It is
	// resolved relative to the host overlay path (for the existence
	// check) and the container overlay path (for the injected
	// MOCK_TOPOLOGY_CONFIG env).
	defaultTopologyRelPath = "topology/topology.yaml"
)

var defaultShims = []string{
	"driver/usr/local/lib/libibmockumad.so.1",
	"driver/usr/local/lib/libibmockverbs.so.1",
	"driver/usr/local/lib/libibmocksys.so.1",
	"driver/usr/local/lib/libpcimocksys.so.1",
}

// Config controls how the mock driver tree is injected into containers.
type Config struct {
	HostOverlayPath      string
	ContainerOverlayPath string
	DeviceHostPath       string
	OptOutAnnotation     string
	DeviceAnnotation     string
	// ImexChannelAnnotation is the pod annotation key whose value "true" opts a
	// container into mock /dev/nvidia-caps-imex-channels/* injection. It is a
	// separate opt-in from DeviceAnnotation because an IMEX channel is a fabric
	// capability, not a GPU: a ComputeDomain workload may want channels without
	// the whole mock GPU device tree, and vice versa.
	ImexChannelAnnotation string
	// ImexChannelHostPath is the host directory holding the mock channel nodes
	// (channel0..N-1). The main nvml-mock DaemonSet stages them when
	// imex.mockChannels.enabled is set; this plugin only consumes them.
	ImexChannelHostPath string
	ExcludedNamespaces  []string
	Shims               []string

	// NodeName is the Kubernetes node this plugin runs on. When set (and a
	// topology document is staged in the overlay) it is injected as the
	// default NODE_NAME so the mock NVML engine's ComputeDomain topology
	// overlay resolves the container's per-node clique / cluster UUID. Empty
	// disables topology injection (the historical node-wide behavior).
	NodeName string
	// TopologyHostPath is where the plugin checks whether a topology
	// document has been staged (by setup.sh) into the overlay tree. Empty
	// defaults to <HostOverlayPath>/topology/topology.yaml.
	TopologyHostPath string
	// TopologyContainerPath is the in-container path injected as
	// MOCK_TOPOLOGY_CONFIG. Empty defaults to
	// <ContainerOverlayPath>/topology/topology.yaml.
	TopologyContainerPath string
}

// Container is the subset of container and pod state needed to decide whether
// and how to inject the nvml-mock overlay.
type Container struct {
	Namespace      string
	PodAnnotations map[string]string
	Env            []string
	Mounts         []Mount

	// Devices and CDIDevices are what the container already carries when the
	// runtime asks the plugin to adjust it. The kubelet applies the device
	// plugin's Allocate response before this point, so a non-empty NVIDIA entry
	// here means the device plugin already served this container. See MEP-0002.
	Devices    []Device
	CDIDevices []string
}

// Adjustment is the mount/env/device delta that a runtime plugin applies.
type Adjustment struct {
	Mounts  []Mount
	Env     []string
	Devices []Device
}

// Mount describes a bind mount in a runtime-neutral form.
type Mount struct {
	Source      string
	Destination string
	Type        string
	Options     []string
}

// Device describes a host device node made visible in the container.
type Device struct {
	HostPath string
	Path     string
}

// DefaultConfig returns the overlay contract described by the NRI design.
func DefaultConfig() Config {
	return Config{
		HostOverlayPath:       defaultHostOverlayPath,
		ContainerOverlayPath:  defaultContainerOverlayPath,
		DeviceHostPath:        defaultDeviceHostPath,
		OptOutAnnotation:      defaultOptOutAnnotation,
		DeviceAnnotation:      defaultDeviceAnnotation,
		ImexChannelAnnotation: defaultImexChannelAnnotation,
		ImexChannelHostPath:   filepath.Join(defaultHostOverlayPath, defaultImexChannelRelPath),
		ExcludedNamespaces:    []string{"kube-system"},
		Shims:                 append([]string(nil), defaultShims...),
	}
}

// Adjust returns the container adjustment for a container, or ok=false when the
// pod/container should be left exactly as authored.
func Adjust(cfg Config, container Container) (Adjustment, bool, error) {
	cfg = withDefaults(cfg)
	if shouldSkip(cfg, container) {
		return Adjustment{}, false, nil
	}

	adjustment := Adjustment{
		Mounts: []Mount{
			{
				Source:      cfg.HostOverlayPath,
				Destination: cfg.ContainerOverlayPath,
				Type:        "bind",
				Options:     []string{"rbind", "ro", "nosuid", "nodev"},
			},
		},
		Env: buildEnv(cfg, container.Env, topologyInjectable(cfg)),
	}

	if strings.EqualFold(container.PodAnnotations[cfg.DeviceAnnotation], "true") {
		switch {
		case alreadyHasGPUDevices(container):
			// MEP-0002: the device plugin allocated a specific GPU and the kubelet
			// already applied it. Adding the whole device tree on top would widen
			// the container past its allocation, and would defeat the mock
			// engine's visibility filter, which derives the visible GPU set from
			// which /dev/nvidiaN nodes are present.
			warnf("device injection requested but the device plugin already served this container; leaving its allocation intact")
		default:
			// Fail open: the device tree is staged by the main nvml-mock DaemonSet,
			// and nothing orders this plugin's DaemonSet after it. If the tree is
			// missing (fresh node) or unreadable, degrade to overlay-only injection
			// rather than failing container creation for the whole pod.
			devices, err := discoverDevices(cfg.DeviceHostPath)
			if err != nil {
				warnf("device injection requested but device tree at %s is unavailable (%v); injecting overlay only", cfg.DeviceHostPath, err)
			} else {
				adjustment.Devices = devices
			}
		}
	}

	// IMEX channels are a separate opt-in and deliberately outside the MEP-0002
	// suppression above. That rule exists because the device plugin already
	// delivered the GPUs the scheduler allocated, so re-injecting the GPU tree
	// would widen the container past its allocation. The device plugin has no
	// concept of an IMEX channel and never delivers one, so there is no
	// allocation to widen — suppressing channels here would instead deny a
	// ComputeDomain workload the fabric it explicitly asked for.
	if strings.EqualFold(container.PodAnnotations[cfg.ImexChannelAnnotation], "true") {
		// Fail open like the device path: channels are staged by the main
		// DaemonSet's setup.sh (imex.mockChannels.enabled), and nothing orders
		// this plugin's DaemonSet after it. They are also off by default, so an
		// annotation on a node without them must not block the pod.
		channels, err := discoverImexChannels(cfg.ImexChannelHostPath)
		if err != nil {
			warnf("imex channel injection requested but the channel tree at %s is unavailable (%v); "+
				"injecting without channels (is imex.mockChannels.enabled set?)", cfg.ImexChannelHostPath, err)
		} else {
			adjustment.Devices = append(adjustment.Devices, channels...)
		}
	}

	return adjustment, true, nil
}

func withDefaults(cfg Config) Config {
	defaults := DefaultConfig()
	if cfg.HostOverlayPath == "" {
		cfg.HostOverlayPath = defaults.HostOverlayPath
	}
	if cfg.ContainerOverlayPath == "" {
		cfg.ContainerOverlayPath = defaults.ContainerOverlayPath
	}
	if cfg.DeviceHostPath == "" {
		cfg.DeviceHostPath = defaults.DeviceHostPath
	}
	if cfg.OptOutAnnotation == "" {
		cfg.OptOutAnnotation = defaults.OptOutAnnotation
	}
	if cfg.DeviceAnnotation == "" {
		cfg.DeviceAnnotation = defaults.DeviceAnnotation
	}
	if cfg.ImexChannelAnnotation == "" {
		cfg.ImexChannelAnnotation = defaults.ImexChannelAnnotation
	}
	if cfg.ImexChannelHostPath == "" {
		cfg.ImexChannelHostPath = filepath.Join(cfg.HostOverlayPath, defaultImexChannelRelPath)
	}
	if len(cfg.Shims) == 0 {
		cfg.Shims = defaults.Shims
	}
	if cfg.TopologyHostPath == "" {
		cfg.TopologyHostPath = filepath.Join(cfg.HostOverlayPath, defaultTopologyRelPath)
	}
	if cfg.TopologyContainerPath == "" {
		cfg.TopologyContainerPath = filepath.Join(cfg.ContainerOverlayPath, defaultTopologyRelPath)
	}
	return cfg
}

// topologyInjectable reports whether this container should get the
// ComputeDomain topology environment. It requires a known node name (so the
// engine's per-node overlay has a lookup key) and a topology document staged
// in the overlay (so the injected MOCK_TOPOLOGY_CONFIG resolves to a real
// file inside the container). The stat runs per container so the plugin
// tolerates the daemon staging the file after the plugin starts.
func topologyInjectable(cfg Config) bool {
	if cfg.NodeName == "" || cfg.TopologyHostPath == "" || cfg.TopologyContainerPath == "" {
		return false
	}
	_, err := os.Stat(cfg.TopologyHostPath)
	return err == nil
}

func shouldSkip(cfg Config, container Container) bool {
	if strings.EqualFold(container.PodAnnotations[cfg.OptOutAnnotation], "false") {
		return true
	}
	for _, namespace := range cfg.ExcludedNamespaces {
		if container.Namespace == namespace {
			return true
		}
	}
	for _, mount := range container.Mounts {
		if mount.Destination == cfg.ContainerOverlayPath {
			return true
		}
	}
	return false
}

func buildEnv(cfg Config, existing []string, injectTopology bool) []string {
	env := make(map[string]string, len(existing)+8)
	order := make([]string, 0, len(existing)+8)
	// original records the container's authored env so we only emit the keys
	// the plugin actually adds or changes. Emitting untouched keys would have
	// the NRI runtime mark them plugin-owned, turning any other plugin that
	// edits the same key into a hard per-key conflict that fails container
	// creation.
	original := make(map[string]string, len(existing))
	for _, item := range existing {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, seen := env[key]; !seen {
			order = append(order, key)
		}
		env[key] = value
		original[key] = value
	}

	prependEnv(env, &order, "PATH", filepath.Join(cfg.ContainerOverlayPath, "driver/usr/bin"))
	prependEnv(env, &order, "LD_LIBRARY_PATH", filepath.Join(cfg.ContainerOverlayPath, "driver/usr/lib64"))
	appendEnv(env, &order, "LD_PRELOAD", shimPaths(cfg))
	setDefaultEnv(env, &order, "MOCK_NVML_CONFIG", filepath.Join(cfg.ContainerOverlayPath, "driver/config/config.yaml"))
	setDefaultEnv(env, &order, "MOCK_IB", "full")
	setDefaultEnv(env, &order, "MOCK_IB_ROOT", filepath.Join(cfg.ContainerOverlayPath, "ib"))
	setDefaultEnv(env, &order, "MOCK_IB_PING_SOCKET", filepath.Join(cfg.ContainerOverlayPath, "run/mock-ib.sock"))
	setDefaultEnv(env, &order, "MOCK_PCI_ROOT", cfg.ContainerOverlayPath)

	// ComputeDomain topology overlay: point the mock NVML engine at the
	// staged cluster-level topology document and tell it which node this
	// container runs on, so every mock GPU reports the node's clique /
	// cluster UUID (nvmlDeviceGetGpuFabricInfo). setDefaultEnv leaves any
	// value the workload authored in place.
	if injectTopology {
		setDefaultEnv(env, &order, "NODE_NAME", cfg.NodeName)
		setDefaultEnv(env, &order, "MOCK_TOPOLOGY_CONFIG", cfg.TopologyContainerPath)
	}

	result := make([]string, 0, len(order))
	for _, key := range order {
		if prev, existed := original[key]; existed && prev == env[key] {
			continue
		}
		result = append(result, key+"="+env[key])
	}
	return result
}

func prependEnv(env map[string]string, order *[]string, key, value string) {
	if current, ok := env[key]; ok && current != "" {
		env[key] = value + ":" + current
		return
	}
	setDefaultEnv(env, order, key, value)
}

func appendEnv(env map[string]string, order *[]string, key string, values []string) {
	value := strings.Join(values, ":")
	if current, ok := env[key]; ok && current != "" {
		env[key] = current + ":" + value
		return
	}
	setDefaultEnv(env, order, key, value)
}

func setDefaultEnv(env map[string]string, order *[]string, key, value string) {
	if _, ok := env[key]; ok {
		return
	}
	*order = append(*order, key)
	env[key] = value
}

func shimPaths(cfg Config) []string {
	paths := make([]string, 0, len(cfg.Shims))
	for _, shim := range cfg.Shims {
		if filepath.IsAbs(shim) {
			paths = append(paths, shim)
			continue
		}
		paths = append(paths, filepath.Join(cfg.ContainerOverlayPath, shim))
	}
	return paths
}

// alreadyHasGPUDevices reports whether the container arrived carrying GPU
// devices that something else put there — in practice the NVIDIA device plugin,
// whose Allocate response the kubelet applies before the runtime asks this
// plugin to adjust anything. It recognises both delivery mechanisms the plugin
// supports: raw device nodes (--pass-device-specs) and CDI device references
// (--device-list-strategy=cdi-*).
func alreadyHasGPUDevices(container Container) bool {
	for _, device := range container.Devices {
		if strings.HasPrefix(device.Path, "/dev/nvidia") {
			return true
		}
	}
	for _, device := range container.CDIDevices {
		if strings.HasPrefix(device, "nvidia.com/") {
			return true
		}
	}
	return false
}

// discoverDevices lists the mock /dev/nvidia* nodes staged in the overlay.
func discoverDevices(deviceHostPath string) ([]Device, error) {
	return scanDeviceDir(deviceHostPath, "nvidia", "/dev")
}

// discoverImexChannels lists the mock IMEX channel nodes staged by
// imex.mockChannels, mapping them onto the fixed kernel path consumers expect.
func discoverImexChannels(channelHostPath string) ([]Device, error) {
	return scanDeviceDir(channelHostPath, "channel", imexChannelContainerDir)
}

// scanDeviceDir collects the device nodes directly under hostDir whose names
// carry prefix, mapping each onto containerDir inside the container.
//
// Directories are skipped: hostDir is a device root, not a tree to recurse, and
// the mock device root holds a nvidia-caps-imex-channels DIRECTORY that matches
// the "nvidia" prefix. Handing a directory to the runtime as a device node
// cannot work, so it must never enter the adjustment.
func scanDeviceDir(hostDir, prefix, containerDir string) ([]Device, error) {
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		return nil, err
	}

	devices := make([]Device, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		devices = append(devices, Device{
			HostPath: filepath.Join(hostDir, name),
			Path:     filepath.Join(containerDir, name),
		})
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Path < devices[j].Path
	})
	return devices, nil
}
