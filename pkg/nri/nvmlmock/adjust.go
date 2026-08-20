// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvmlmock is the NRI plugin that injects the nvml-mock LD_PRELOAD shim
// into GPU-requesting containers via container adjustments at creation time.
package nvmlmock

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
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

	// pciDevicesContainerPath and sysDevicesContainerPath are the kernel
	// paths the tree must appear at inside the container. Unlike the
	// LD_PRELOAD-based redirection (MOCK_PCI_ROOT), these cannot be
	// relocated: Go consumers such as GPU Feature Discovery and the DRA
	// driver hard-code them and read them with direct syscalls, which no
	// libc shim can intercept.
	pciDevicesContainerPath = "/sys/bus/pci/devices"
	sysDevicesContainerPath = "/sys/devices"

	// DeviceInjectionModeRaw stages the mock /dev/nvidiaN nodes directly in the
	// adjustment. It is the default: MEP-0002 requires the raw path to stay
	// reachable, and it is the only mode that works on a runtime whose CDI
	// support is off or absent.
	DeviceInjectionModeRaw = "raw"
	// DeviceInjectionModeCDI hands the runtime a CDI device reference and lets
	// it resolve the device nodes from the spec setup.sh stages. containerd
	// 2.x enables CDI by default (enable_cdi = true, spec dirs /etc/cdi and
	// /var/run/cdi), so this needs no container toolkit on the node.
	DeviceInjectionModeCDI = "cdi"
	// defaultCDIDeviceName is the fully-qualified CDI device the cdi mode
	// injects on the annotation path. The "all" device aggregates every mock
	// GPU, which is what the annotation has always meant (MEP-0002 goal 2).
	//
	// The vendor is deliberately NOT nvidia.com: that namespace belongs to the
	// device plugin and the container toolkit. Keeping ours distinct is what
	// makes MEP-0002's "exactly one component emits CDI device references for a
	// container" invariant observable rather than merely asserted, and it keeps
	// alreadyHasGPUDevices' nvidia.com/ test meaningful.
	defaultCDIDeviceName = "nvml-mock.nvidia.com/gpu=all"
	// defaultCDISpecHostPath is where setup.sh stages the spec that backs
	// defaultCDIDeviceName. containerd resolves an unknown CDI device by
	// failing container creation outright, so the plugin checks this exists
	// before it commits to a reference it cannot honour.
	defaultCDISpecHostPath = "/var/run/cdi/nvml-mock-nri.yaml"
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

	// DeviceInjectionMode selects how the annotation-gated device opt-in
	// delivers mock GPUs: DeviceInjectionModeRaw (default) or
	// DeviceInjectionModeCDI. It changes the mechanism only. Whether a
	// container is served at all is decided before this is consulted, so
	// neither mode can inject into a container the device plugin already
	// served (MEP-0002).
	DeviceInjectionMode string
	// CDIDeviceName is the CDI device injected in DeviceInjectionModeCDI.
	CDIDeviceName string
	// CDISpecHostPath is the staged CDI spec checked before a CDI reference is
	// emitted. A missing spec degrades to raw injection rather than failing
	// container creation.
	CDISpecHostPath string

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
	// CDIDevices are fully-qualified CDI device references the runtime resolves
	// itself. Devices and CDIDevices are alternatives for the GPU tree, never
	// both: emitting the same GPUs twice would widen the container and defeat
	// the mock engine's detectVisibleDevices filter.
	CDIDevices []string
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
		DeviceInjectionMode:   DeviceInjectionModeRaw,
		CDIDeviceName:         defaultCDIDeviceName,
		CDISpecHostPath:       defaultCDISpecHostPath,
	}
}

// Adjust returns the container adjustment for a container, or ok=false when the
// pod/container should be left exactly as authored.
//
//nolint:cyclop // existing complexity; refactor deferred
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
	adjustment.Mounts = append(adjustment.Mounts, pciSysfsMounts(cfg)...)

	if strings.EqualFold(container.PodAnnotations[cfg.DeviceAnnotation], "true") {
		switch {
		case alreadyHasGPUDevices(container):
			// MEP-0002: the device plugin allocated a specific GPU and the kubelet
			// already applied it. Adding the whole device tree on top would widen
			// the container past its allocation, and would defeat the mock
			// engine's visibility filter, which derives the visible GPU set from
			// which /dev/nvidiaN nodes are present.
			warnf("device injection requested but the device plugin already served this container; leaving its allocation intact")
		case cfg.DeviceInjectionMode == DeviceInjectionModeCDI && cdiSpecStaged(cfg):
			// The runtime resolves the device nodes from the staged spec. Nothing
			// is added to adjustment.Devices: the CDI reference and the raw nodes
			// describe the same GPUs, and delivering both would widen the
			// container and defeat the engine's detectVisibleDevices filter.
			adjustment.CDIDevices = []string{cfg.CDIDeviceName}
		default:
			// Fail open: the device tree is staged by the main nvml-mock DaemonSet,
			// and nothing orders this plugin's DaemonSet after it. If the tree is
			// missing (fresh node) or unreadable, degrade to overlay-only injection
			// rather than failing container creation for the whole pod.
			//
			// This is also where CDI mode lands when its spec is not staged.
			// containerd fails container creation on an unresolvable CDI device,
			// so falling back to raw nodes keeps the pod starting.
			if cfg.DeviceInjectionMode == DeviceInjectionModeCDI {
				warnf("cdi device injection requested but no spec is staged at %s; falling back to raw device nodes", cfg.CDISpecHostPath)
			}
			devices, err := discoverDevices(cfg.DeviceHostPath)
			switch {
			case err != nil:
				warnf("device injection requested but device tree at %s is unavailable (%v); injecting overlay only", cfg.DeviceHostPath, err)
			case len(devices) == 0:
				// The directory is readable and holds nothing we recognise, so
				// os.ReadDir reports success and the case above never fires.
				// Injecting silently would hand the container an overlay with
				// no device nodes, and the engine derives its visible-GPU set
				// from which /dev/nvidiaN are present — so the pod reports zero
				// GPUs as though that were the configured state. Still fail
				// open, but say so.
				warnf("device injection requested but the device tree at %s holds no device nodes; "+
					"injecting overlay only (has the nvml-mock DaemonSet staged this node?)", cfg.DeviceHostPath)
			default:
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

//nolint:cyclop // existing complexity; refactor deferred
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
	if cfg.DeviceInjectionMode == "" {
		cfg.DeviceInjectionMode = defaults.DeviceInjectionMode
	}
	if cfg.CDIDeviceName == "" {
		cfg.CDIDeviceName = defaults.CDIDeviceName
	}
	if cfg.CDISpecHostPath == "" {
		cfg.CDISpecHostPath = defaults.CDISpecHostPath
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

// pciSysfsMounts maps the rendered PCI tree onto the kernel paths inside the
// container. It returns both mounts or neither: /sys/bus/pci/devices holds
// relative symlinks into ../../../devices/pciDDDD:BB, so without
// /sys/devices every entry dangles and reads fail with ENOENT — the same
// symptom as no mount at all, only harder to diagnose.
//
// Mounting /sys/devices necessarily hides the host's other device classes
// (CPU topology among them) from the container. That is the price of serving
// consumers that resolve GPUs through sysfs: the tree cannot be assembled
// per root complex instead, because a bind mount at a path sysfs does not
// already have (say /sys/devices/pci0000:80) needs a mountpoint the runtime
// cannot create on a read-only sysfs. It also shadows virtual/dmi/id, which
// is why the renderer mirrors the node's DMI attributes into the tree: kind's
// createContainer hook bind-mounts the node's product files there, and a
// missing target fails container creation.
//
// An unfinished tree is skipped rather than reported: it is staged by the
// main nvml-mock DaemonSet and nothing orders this plugin after it, and a
// mount the tree cannot honour fails container creation for the whole pod.
// Silence rather than a warning because this runs for every container on the
// node, staged or not.
//
// "Finished" is the renderer's marker, not the presence of the directories
// mounted here: those exist from the start of a render while the DMI
// attributes kind's hook needs are written at its end, so a tree caught
// mid-render would otherwise pass and then fail every container on the node.
func pciSysfsMounts(cfg Config) []Mount {
	if cfg.HostOverlayPath == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(cfg.HostOverlayPath, render.MarkerRelPath)); err != nil {
		return nil
	}
	// Paths come from the renderer that writes them: a guard statting a path
	// nothing renders would fail open, dropping the mounts silently.
	sysDevices := filepath.Join(cfg.HostOverlayPath, render.SysDevicesRelPath)
	pciDevices := filepath.Join(cfg.HostOverlayPath, render.PCIDevicesRelPath)
	for _, dir := range []string{sysDevices, pciDevices} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return nil
		}
	}

	// /sys/devices first: containerd orders mounts parent-before-child, but
	// emitting them in dependency order keeps the adjustment readable and
	// correct under any runtime that applies them verbatim.
	return []Mount{
		{
			Source:      sysDevices,
			Destination: sysDevicesContainerPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev"},
		},
		{
			Source:      pciDevices,
			Destination: pciDevicesContainerPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev"},
		},
	}
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

// cdiSpecStaged reports whether the CDI spec backing cfg.CDIDeviceName is
// present on the node. The stat runs per container, like topologyInjectable,
// so the plugin tolerates setup.sh staging the spec after the plugin starts.
func cdiSpecStaged(cfg Config) bool {
	if cfg.CDISpecHostPath == "" {
		return false
	}
	_, err := os.Stat(cfg.CDISpecHostPath)
	return err == nil
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
