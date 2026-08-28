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
func setEnvironment(cfg Config, container Container, adjustment *Adjustment) {
	env := newEnvSet(container.Env)

	env.prepend("PATH", filepath.Join(cfg.ContainerOverlayPath, "driver/usr/bin"))
	env.prepend("LD_LIBRARY_PATH", filepath.Join(cfg.ContainerOverlayPath, "driver/usr/lib64"))
	env.appendList("LD_PRELOAD", shimPaths(cfg))
	env.setDefault("MOCK_NVML_CONFIG", filepath.Join(cfg.ContainerOverlayPath, "driver/config/config.yaml"))
	env.setDefault("MOCK_IB", "full")
	env.setDefault("MOCK_IB_ROOT", filepath.Join(cfg.ContainerOverlayPath, "ib"))
	env.setDefault("MOCK_IB_PING_SOCKET", filepath.Join(cfg.ContainerOverlayPath, "run/mock-ib.sock"))
	env.setDefault("MOCK_PCI_ROOT", cfg.ContainerOverlayPath)

	// ComputeDomain topology overlay: point the mock NVML engine at the staged
	// topology document and tell it which node this container runs on, so every
	// mock GPU reports the node's clique / cluster UUID
	// (nvmlDeviceGetGpuFabricInfo).
	if topologyInjectable(cfg) {
		env.setDefault("NODE_NAME", cfg.NodeName)
		env.setDefault("MOCK_TOPOLOGY_CONFIG", cfg.TopologyContainerPath)
	}

	adjustment.Env = append(adjustment.Env, env.changed()...)
}

// envSet is the container's environment plus the edits this package makes to
// it, remembering the authored values so changed can report only the keys that
// actually moved.
type envSet struct {
	values   map[string]string
	authored map[string]string
	order    []string
}

func newEnvSet(existing []string) *envSet {
	env := &envSet{
		values:   make(map[string]string, len(existing)+8),
		authored: make(map[string]string, len(existing)),
		order:    make([]string, 0, len(existing)+8),
	}

	for _, item := range existing {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, seen := env.values[key]; !seen {
			env.order = append(env.order, key)
		}
		env.values[key] = value
		env.authored[key] = value
	}
	return env
}

// setDefault leaves any value the workload authored in place.
func (e *envSet) setDefault(key, value string) {
	if _, ok := e.values[key]; ok {
		return
	}
	e.order = append(e.order, key)
	e.values[key] = value
}

// prepend puts value ahead of an authored one, so the overlay's copy of a
// binary or library wins the search.
func (e *envSet) prepend(key, value string) {
	if current, ok := e.values[key]; ok && current != "" {
		e.values[key] = value + ":" + current
		return
	}
	e.setDefault(key, value)
}

// appendList puts values behind an authored one, so a workload's own
// LD_PRELOAD keeps priority over the shims.
func (e *envSet) appendList(key string, values []string) {
	value := strings.Join(values, ":")
	if current, ok := e.values[key]; ok && current != "" {
		e.values[key] = current + ":" + value
		return
	}
	e.setDefault(key, value)
}

// changed returns only the keys this package added or modified, in insertion
// order. Emitting an untouched key would have the NRI runtime mark it
// plugin-owned, turning any other plugin that edits the same key into a hard
// per-key conflict that fails container creation.
func (e *envSet) changed() []string {
	result := make([]string, 0, len(e.order))
	for _, key := range e.order {
		if authored, existed := e.authored[key]; existed && authored == e.values[key] {
			continue
		}
		result = append(result, key+"="+e.values[key])
	}
	return result
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

// shimPaths resolves a relative shim against the container's overlay mount and
// passes an absolute one through untouched.
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
