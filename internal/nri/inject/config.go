// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"path/filepath"
	"strings"
)

const (
	// imexChannelRelPath is where the node agent's imex simulator mknods the
	// mock IMEX channel nodes, resolved relative to the host overlay path. It
	// must track that simulator and the imex.mockChannels chart surface.
	imexChannelRelPath = "driver/dev/nvidia-caps-imex-channels"
	// imexChannelContainerDir is the fixed kernel location the channels must
	// appear at inside the container. Consumers (nvidia-imex, the DRA driver's
	// compute-domain plugin) hard-code this path.
	imexChannelContainerDir = "/dev/nvidia-caps-imex-channels"
	// topologyRelPath is where the node agent's nvlink simulator stages the
	// cluster-level ComputeDomain topology document inside the overlay tree. It
	// resolves against the host overlay path for the existence check and against
	// the container overlay path for the injected MOCK_TOPOLOGY_CONFIG.
	topologyRelPath = "topology/topology.yaml"
	// configRelPath holds the mock NVML config and, beside it, the overrides
	// file runtime state is injected into. It is named once because the mount
	// and the injected MOCK_NVML_CONFIG have to agree: the overrides the
	// container resets are resolved relative to that config.
	configRelPath = "driver/config"
)

// defaultShims are preloaded in the order listed, so a symbol defined by more
// than one resolves to the first.
var defaultShims = []string{
	"driver/usr/local/lib/libibmockumad.so.1",
	"driver/usr/local/lib/libibmockverbs.so.1",
	"driver/usr/local/lib/libibmocksys.so.1",
	"driver/usr/local/lib/libpcisysfs.so.1",
}

// Config controls how the mock driver tree is injected into containers,
// grouped by the step each field feeds.
type Config struct {
	// Overlay — the mock driver tree and where it appears in a container.
	HostOverlayPath      string
	ContainerOverlayPath string
	Shims                []string

	// Scope — which containers are left exactly as authored.
	ExcludedNamespaces []string
	OptOutAnnotation   string

	// GPUs — the annotation-gated opt-in and the mechanism that delivers it.
	// A missing CDI spec degrades to raw injection rather than failing
	// container creation.
	DeviceAnnotation    string
	DeviceHostPath      string
	DeviceInjectionMode DeviceInjectionMode
	CDIDeviceName       string
	CDISpecHostPath     string

	// IMEX channels — a separate opt-in from the GPU one, because a channel is
	// a fabric capability rather than a GPU: a ComputeDomain workload may want
	// channels without the mock device tree, and vice versa. The node agent
	// stages the nodes when imex.mockChannels.enabled is set; this package only
	// consumes them.
	ImexChannelAnnotation string
	ImexChannelHostPath   string

	// ComputeDomain topology — NodeName gives the mock NVML engine's per-node
	// overlay its lookup key, so every mock GPU reports the node's clique and
	// cluster UUID. Empty disables topology injection.
	NodeName              string
	TopologyHostPath      string
	TopologyContainerPath string
}

// DefaultConfig returns the overlay contract described by the NRI design.
func DefaultConfig() Config {
	const hostOverlay = "/var/lib/nvml-mock"

	return Config{
		HostOverlayPath:      hostOverlay,
		ContainerOverlayPath: "/opt/nvml-mock",
		Shims:                append([]string(nil), defaultShims...),

		ExcludedNamespaces: []string{"kube-system"},
		OptOutAnnotation:   "nvml-mock.nvidia.com/inject",

		DeviceAnnotation:    "nvml-mock.nvidia.com/devices",
		DeviceHostPath:      filepath.Join(hostOverlay, "driver/dev"),
		DeviceInjectionMode: DeviceInjectionModeRaw,
		// The vendor is deliberately NOT nvidia.com: that namespace belongs to
		// the device plugin and the container toolkit. Keeping ours distinct is
		// what makes MEP-0002's "exactly one component emits CDI device
		// references for a container" invariant observable rather than merely
		// asserted, and it keeps alreadyHasGPUDevices' nvidia.com/ test
		// meaningful. The "all" device aggregates every mock GPU, which is what
		// the annotation has always meant.
		CDIDeviceName: "nvml-mock.nvidia.com/gpu=all",
		// Written by the node agent's cdi simulator. containerd resolves an
		// unknown CDI device by failing container creation outright, so the
		// plugin checks this exists before committing to a reference it cannot
		// honour.
		CDISpecHostPath: "/var/run/cdi/nvml-mock-nri.yaml",

		ImexChannelAnnotation: "nvml-mock.nvidia.com/imex-channels",
		ImexChannelHostPath:   filepath.Join(hostOverlay, imexChannelRelPath),
	}
}

func withDefaults(cfg Config) Config {
	defaults := DefaultConfig()

	cfg.HostOverlayPath = orDefault(cfg.HostOverlayPath, defaults.HostOverlayPath)
	cfg.ContainerOverlayPath = orDefault(cfg.ContainerOverlayPath, defaults.ContainerOverlayPath)
	cfg.OptOutAnnotation = orDefault(cfg.OptOutAnnotation, defaults.OptOutAnnotation)
	cfg.DeviceAnnotation = orDefault(cfg.DeviceAnnotation, defaults.DeviceAnnotation)
	cfg.DeviceHostPath = orDefault(cfg.DeviceHostPath, defaults.DeviceHostPath)
	cfg.DeviceInjectionMode = orDefault(cfg.DeviceInjectionMode, defaults.DeviceInjectionMode)
	cfg.CDIDeviceName = orDefault(cfg.CDIDeviceName, defaults.CDIDeviceName)
	cfg.CDISpecHostPath = orDefault(cfg.CDISpecHostPath, defaults.CDISpecHostPath)
	cfg.ImexChannelAnnotation = orDefault(cfg.ImexChannelAnnotation, defaults.ImexChannelAnnotation)

	// Derived from the overlay roots, so they resolve against whatever those
	// ended up being rather than against the packaged defaults.
	cfg.ImexChannelHostPath = orDefault(cfg.ImexChannelHostPath,
		filepath.Join(cfg.HostOverlayPath, imexChannelRelPath))
	cfg.TopologyHostPath = orDefault(cfg.TopologyHostPath,
		filepath.Join(cfg.HostOverlayPath, topologyRelPath))
	cfg.TopologyContainerPath = orDefault(cfg.TopologyContainerPath,
		filepath.Join(cfg.ContainerOverlayPath, topologyRelPath))

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
