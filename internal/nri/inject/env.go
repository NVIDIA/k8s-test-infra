// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"strings"
)

// setEnvironment points the container's loader and the mock libraries at the
// overlay.
//
// Only keys the plugin adds or changes are emitted. Emitting an untouched key
// would have the NRI runtime mark it plugin-owned, turning any other plugin
// that edits the same key into a hard per-key conflict that fails container
// creation.
func setEnvironment(cfg Config, container Container, adjustment *Adjustment) {
	env := make(map[string]string, len(container.Env)+8)
	order := make([]string, 0, len(container.Env)+8)
	original := make(map[string]string, len(container.Env))

	for _, item := range container.Env {
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

	// ComputeDomain topology overlay: point the mock NVML engine at the staged
	// topology document and tell it which node this container runs on, so every
	// mock GPU reports the node's clique / cluster UUID
	// (nvmlDeviceGetGpuFabricInfo).
	if topologyInjectable(cfg) {
		setDefaultEnv(env, &order, "NODE_NAME", cfg.NodeName)
		setDefaultEnv(env, &order, "MOCK_TOPOLOGY_CONFIG", cfg.TopologyContainerPath)
	}

	for _, key := range order {
		if previous, existed := original[key]; existed && previous == env[key] {
			continue
		}
		adjustment.Env = append(adjustment.Env, key+"="+env[key])
	}
}

// topologyInjectable requires a known node name (so the engine's per-node
// overlay has a lookup key) and a topology document staged in the overlay (so
// the injected MOCK_TOPOLOGY_CONFIG resolves to a real file inside the
// container). The stat runs per container so the plugin tolerates the node
// agent staging the file after the plugin starts.
func topologyInjectable(cfg Config) bool {
	if cfg.NodeName == "" || cfg.TopologyHostPath == "" || cfg.TopologyContainerPath == "" {
		return false
	}
	_, err := os.Stat(cfg.TopologyHostPath)
	return err == nil
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

// setDefaultEnv leaves any value the workload authored in place.
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
