// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"net"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	driverManagerContainerName = "k8s-driver-manager"
	driverContainerName        = "nvidia-driver-ctr"
	driverComponentLabel       = "app.kubernetes.io/component"
	driverComponent            = "nvidia-driver"
	sysModulePath              = "/sys/module"
	deleteModuleSyscall        = "delete_module"
	visibleDevicesEnv          = "NVIDIA_VISIBLE_DEVICES"
)

func driverManager(cfg Config, container Container) bool {
	return cfg.DriverModulesHostPath != "" && driverPod(container, driverManagerContainerName)
}

func driverContainer(cfg Config, container Container) bool {
	return cfg.DriverModulesHostPath != "" && driverPod(container, driverContainerName)
}

func driverPod(container Container, name string) bool {
	return container.Name == name && container.PodLabels[driverComponentLabel] == driverComponent
}

// adjustDriverContainer binds the simulated modules writable: the stand-in loads them.
func adjustDriverContainer(cfg Config, container Container) (Adjustment, bool) {
	if !modulesAvailable(cfg, container) {
		return Adjustment{}, false
	}
	zap.L().Debug("adjusting the driver container", zap.String("namespace", container.Namespace))
	return Adjustment{Mounts: []Mount{modulesMount(cfg, "rw")}}, true
}

// adjustDriverManager binds the simulated modules and hands delete_module to
// the plugin's listener. Both or neither: the kernel would answer ENOENT.
func adjustDriverManager(cfg Config, container Container) (Adjustment, bool) {
	if !listening(cfg.ModuleUnloadListenerPath) {
		zap.L().Warn("module unload listener is not serving; the driver manager sees the node's own kernel modules",
			zap.String("namespace", container.Namespace), zap.String("listener", cfg.ModuleUnloadListenerPath))
		return Adjustment{}, false
	}
	if !modulesAvailable(cfg, container) {
		return Adjustment{}, false
	}

	zap.L().Debug("adjusting the driver manager", zap.String("namespace", container.Namespace))
	return Adjustment{
		Mounts: []Mount{modulesMount(cfg, "ro")},
		SeccompNotify: &SeccompNotify{
			ListenerPath: cfg.ModuleUnloadListenerPath,
			Metadata:     container.Namespace + "/" + container.Name,
			Syscalls:     []string{deleteModuleSyscall},
		},
	}, true
}

// wantsDriverModules matches a container that asks the NVIDIA runtime for GPUs.
// CDI gives it Mokka's static tree only if /proc/modules listed nvidia at
// creation; the live tree lets the operands' initstate gate follow the driver.
func wantsDriverModules(cfg Config, container Container) bool {
	if cfg.DriverModulesHostPath == "" || hasMountAt(container, sysModulePath) {
		return false
	}
	value, set := "", false
	for _, kv := range container.Env {
		if v, ok := strings.CutPrefix(kv, visibleDevicesEnv+"="); ok {
			value, set = v, true
		}
	}
	return set && value != "" && value != "void" && value != "none"
}

func adjustDriverModules(cfg Config, container Container, adjustment *Adjustment) {
	if modulesAvailable(cfg, container) {
		adjustment.Mounts = append(adjustment.Mounts, modulesMount(cfg, "ro"))
	}
}

// modulesAvailable: a bind of a missing source fails container creation.
func modulesAvailable(cfg Config, container Container) bool {
	if _, err := os.Stat(cfg.DriverModulesHostPath); err != nil {
		zap.L().Warn("simulated modules unavailable; leaving /sys/module as authored",
			zap.String("namespace", container.Namespace), zap.String("container", container.Name), zap.Error(err))
		return false
	}
	return true
}

func hasMountAt(container Container, destination string) bool {
	for _, mount := range container.Mounts {
		if mount.Destination == destination {
			return true
		}
	}
	return false
}

func modulesMount(cfg Config, access string) Mount {
	return Mount{
		Source:      cfg.DriverModulesHostPath,
		Destination: sysModulePath,
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", access, "nosuid", "nodev", "noexec"},
	}
}

// listening dials the socket: its file alone outlives a crashed plugin.
func listening(path string) bool {
	conn, err := net.DialTimeout("unix", path, listenerProbeTimeout)
	if err != nil {
		return false
	}
	if err := conn.Close(); err != nil {
		zap.L().Debug("close listener probe", zap.Error(err))
	}
	return true
}

const listenerProbeTimeout = 100 * time.Millisecond
