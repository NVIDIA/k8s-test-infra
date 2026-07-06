// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvmlmock

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultHostOverlayPath      = "/var/lib/nvml-mock"
	defaultContainerOverlayPath = "/opt/nvml-mock"
	defaultDeviceHostPath       = "/var/lib/nvml-mock/driver/dev"
	defaultOptOutAnnotation     = "nvml-mock.nvidia.com/inject"
	defaultDeviceAnnotation     = "nvml-mock.nvidia.com/devices"
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
	ExcludedNamespaces   []string
	Shims                []string
}

// Container is the subset of container and pod state needed to decide whether
// and how to inject the nvml-mock overlay.
type Container struct {
	Namespace      string
	PodAnnotations map[string]string
	Env            []string
	Mounts         []Mount
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
		HostOverlayPath:      defaultHostOverlayPath,
		ContainerOverlayPath: defaultContainerOverlayPath,
		DeviceHostPath:       defaultDeviceHostPath,
		OptOutAnnotation:     defaultOptOutAnnotation,
		DeviceAnnotation:     defaultDeviceAnnotation,
		ExcludedNamespaces:   []string{"kube-system"},
		Shims:                append([]string(nil), defaultShims...),
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
		Env: buildEnv(cfg, container.Env),
	}

	if strings.EqualFold(container.PodAnnotations[cfg.DeviceAnnotation], "true") {
		devices, err := discoverDevices(cfg.DeviceHostPath)
		if err != nil {
			return Adjustment{}, false, err
		}
		adjustment.Devices = devices
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
	if len(cfg.Shims) == 0 {
		cfg.Shims = defaults.Shims
	}
	return cfg
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

func buildEnv(cfg Config, existing []string) []string {
	env := make(map[string]string, len(existing)+8)
	order := make([]string, 0, len(existing)+8)
	for _, item := range existing {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, seen := env[key]; !seen {
			order = append(order, key)
		}
		env[key] = value
	}

	prependEnv(env, &order, "PATH", filepath.Join(cfg.ContainerOverlayPath, "driver/usr/bin"))
	prependEnv(env, &order, "LD_LIBRARY_PATH", filepath.Join(cfg.ContainerOverlayPath, "driver/usr/lib64"))
	appendEnv(env, &order, "LD_PRELOAD", shimPaths(cfg))
	setDefaultEnv(env, &order, "MOCK_NVML_CONFIG", filepath.Join(cfg.ContainerOverlayPath, "driver/config/config.yaml"))
	setDefaultEnv(env, &order, "MOCK_IB", "full")
	setDefaultEnv(env, &order, "MOCK_IB_ROOT", filepath.Join(cfg.ContainerOverlayPath, "ib"))
	setDefaultEnv(env, &order, "MOCK_IB_PING_SOCKET", filepath.Join(cfg.ContainerOverlayPath, "run/mock-ib.sock"))
	setDefaultEnv(env, &order, "MOCK_PCI_ROOT", cfg.ContainerOverlayPath)

	result := make([]string, 0, len(order))
	for _, key := range order {
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

func discoverDevices(deviceHostPath string) ([]Device, error) {
	entries, err := os.ReadDir(deviceHostPath)
	if err != nil {
		return nil, err
	}

	devices := make([]Device, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "nvidia") {
			continue
		}
		devices = append(devices, Device{
			HostPath: filepath.Join(deviceHostPath, name),
			Path:     filepath.Join("/dev", name),
		})
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Path < devices[j].Path
	})
	return devices, nil
}
