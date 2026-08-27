// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	defaultHostOverlayPath       = "/var/lib/nvml-mock"
	defaultContainerOverlayPath  = "/opt/nvml-mock"
	defaultDeviceHostPath        = "/var/lib/nvml-mock/driver/dev"
	defaultOptOutAnnotation      = "nvml-mock.nvidia.com/inject"
	defaultDeviceAnnotation      = "nvml-mock.nvidia.com/devices"
	defaultImexChannelAnnotation = "nvml-mock.nvidia.com/imex-channels"
	// defaultImexChannelRelPath is where the node agent's imex simulator mknods
	// the mock IMEX channel nodes, resolved relative to the host overlay path.
	// It must track that simulator and the imex.mockChannels chart surface.
	defaultImexChannelRelPath = "driver/dev/nvidia-caps-imex-channels"
	// imexChannelContainerDir is the fixed kernel location the channels must
	// appear at inside the container. Consumers (nvidia-imex, the DRA driver's
	// compute-domain plugin) hard-code this path.
	imexChannelContainerDir = "/dev/nvidia-caps-imex-channels"
	// defaultTopologyRelPath is where the node agent's nvlink simulator stages
	// the cluster-level ComputeDomain topology document inside the overlay
	// tree. It is resolved relative to the host overlay path (for the existence
	// check) and the container overlay path (for the injected
	// MOCK_TOPOLOGY_CONFIG env).
	defaultTopologyRelPath = "topology/topology.yaml"

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
	// defaultCDISpecHostPath is where the cdi simulator writes the spec backing
	// defaultCDIDeviceName. containerd resolves an unknown CDI device by
	// failing container creation outright, so the plugin checks this exists
	// before it commits to a reference it cannot honour.
	defaultCDISpecHostPath = "/var/run/cdi/nvml-mock-nri.yaml"
)

// DeviceInjectionMode selects the mechanism the device opt-in uses to deliver
// mock GPUs. It changes the mechanism only: whether a container is served at
// all is decided before this is consulted, so neither mode can inject into a
// container the device plugin already served (MEP-0002).
type DeviceInjectionMode string

const (
	// DeviceInjectionModeRaw stages the mock /dev/nvidiaN nodes directly in the
	// adjustment. It is the default: MEP-0002 requires the raw path to stay
	// reachable, and it is the only mode that works on a runtime whose CDI
	// support is off or absent.
	DeviceInjectionModeRaw DeviceInjectionMode = "raw"
	// DeviceInjectionModeCDI hands the runtime a CDI device reference and lets
	// it resolve the device nodes from the spec the cdi simulator writes.
	// containerd 2.x enables CDI by default (enable_cdi = true, spec dirs
	// /etc/cdi and /var/run/cdi), so this needs no container toolkit on the node.
	DeviceInjectionModeCDI DeviceInjectionMode = "cdi"
)

// ParseDeviceInjectionMode rejects an unknown mode rather than coercing it. A
// typo that silently resolved to raw would look exactly like a working CDI
// deployment, and the difference is only visible in the OCI spec of an
// already-running pod.
func ParseDeviceInjectionMode(s string) (DeviceInjectionMode, error) {
	switch mode := DeviceInjectionMode(strings.ToLower(strings.TrimSpace(s))); mode {
	case DeviceInjectionModeRaw, DeviceInjectionModeCDI:
		return mode, nil
	case "":
		return DeviceInjectionModeRaw, nil
	default:
		return "", fmt.Errorf("invalid device injection mode %q: expected %q or %q",
			s, DeviceInjectionModeRaw, DeviceInjectionModeCDI)
	}
}

var defaultShims = []string{
	"driver/usr/local/lib/libibmockumad.so.1",
	"driver/usr/local/lib/libibmockverbs.so.1",
	"driver/usr/local/lib/libibmocksys.so.1",
	"driver/usr/local/lib/libpcisysfs.so.1",
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
	// (channel0..N-1). The node agent stages them when imex.mockChannels.enabled
	// is set; this plugin only consumes them.
	ImexChannelHostPath string
	ExcludedNamespaces  []string
	Shims               []string

	DeviceInjectionMode DeviceInjectionMode
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
	// TopologyHostPath is where the plugin checks whether the nvlink simulator
	// has staged a topology document into the overlay tree. Empty defaults to
	// <HostOverlayPath>/topology/topology.yaml.
	TopologyHostPath string
	// TopologyContainerPath is the in-container path injected as
	// MOCK_TOPOLOGY_CONFIG. Empty defaults to
	// <ContainerOverlayPath>/topology/topology.yaml.
	TopologyContainerPath string
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

func withDefaults(cfg Config) Config {
	defaults := DefaultConfig()

	cfg.HostOverlayPath = orDefault(cfg.HostOverlayPath, defaults.HostOverlayPath)
	cfg.ContainerOverlayPath = orDefault(cfg.ContainerOverlayPath, defaults.ContainerOverlayPath)
	cfg.DeviceHostPath = orDefault(cfg.DeviceHostPath, defaults.DeviceHostPath)
	cfg.OptOutAnnotation = orDefault(cfg.OptOutAnnotation, defaults.OptOutAnnotation)
	cfg.DeviceAnnotation = orDefault(cfg.DeviceAnnotation, defaults.DeviceAnnotation)
	cfg.ImexChannelAnnotation = orDefault(cfg.ImexChannelAnnotation, defaults.ImexChannelAnnotation)
	cfg.DeviceInjectionMode = orDefault(cfg.DeviceInjectionMode, defaults.DeviceInjectionMode)
	cfg.CDIDeviceName = orDefault(cfg.CDIDeviceName, defaults.CDIDeviceName)
	cfg.CDISpecHostPath = orDefault(cfg.CDISpecHostPath, defaults.CDISpecHostPath)

	// These three are derived from the overlay roots, so they resolve against
	// whatever those ended up being rather than against the packaged defaults.
	cfg.ImexChannelHostPath = orDefault(cfg.ImexChannelHostPath,
		filepath.Join(cfg.HostOverlayPath, defaultImexChannelRelPath))
	cfg.TopologyHostPath = orDefault(cfg.TopologyHostPath,
		filepath.Join(cfg.HostOverlayPath, defaultTopologyRelPath))
	cfg.TopologyContainerPath = orDefault(cfg.TopologyContainerPath,
		filepath.Join(cfg.ContainerOverlayPath, defaultTopologyRelPath))

	// An explicitly empty namespace list means "exclude nothing", so unlike the
	// shims it is not restored from the defaults.
	cfg.ExcludedNamespaces = compact(cfg.ExcludedNamespaces)
	cfg.Shims = compact(cfg.Shims)
	if len(cfg.Shims) == 0 {
		cfg.Shims = defaults.Shims
	}

	return cfg
}

func orDefault[T ~string](value, fallback T) T {
	if value == "" {
		return fallback
	}
	return value
}

// compact trims each item and drops the empties a comma-separated flag leaves
// behind, so `--flag=a,,b ` and `--flag=` mean what they look like.
func compact(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
