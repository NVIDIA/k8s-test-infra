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
	// driverComponentLabel marks the GPU Operator's driver pods.
	driverComponentLabel = "app.kubernetes.io/component"
	driverComponent      = "nvidia-driver"
	sysModulePath        = "/sys/module"
	deleteModuleSyscall  = "delete_module"
	visibleDevicesEnv    = "NVIDIA_VISIBLE_DEVICES"
)

// driverManager matches the driver pod's init container, k8s-driver-manager,
// while the simulated modules are enabled.
func driverManager(cfg Config, container Container) bool {
	return cfg.DriverModulesHostPath != "" && driverPod(container, driverManagerContainerName)
}

// driverContainer matches the driver pod's driver container while the
// simulated modules are enabled.
func driverContainer(cfg Config, container Container) bool {
	return cfg.DriverModulesHostPath != "" && driverPod(container, driverContainerName)
}

func driverPod(container Container, name string) bool {
	return container.Name == name && container.PodLabels[driverComponentLabel] == driverComponent
}

// adjustDriverContainer binds the simulated modules writable over /sys/module,
// where a driver stand-in loads them.
func adjustDriverContainer(cfg Config, container Container) (Adjustment, bool) {
	zap.L().Debug("adjusting the driver container", zap.String("namespace", container.Namespace))
	return Adjustment{Mounts: []Mount{modulesMount(cfg, "rw")}}, true
}

// adjustDriverManager binds the simulated modules over /sys/module and hands
// delete_module to the plugin's listener, so the stock driver manager unloads
// them. Both or neither: without the listener the kernel would answer ENOENT,
// and runc fails a container whose listener does not answer.
func adjustDriverManager(cfg Config, container Container) (Adjustment, bool) {
	if container.HasSeccompPolicy {
		// The notification policy would replace it with allow-all.
		zap.L().Warn("driver manager has a seccomp policy; leaving it unmodified",
			zap.String("namespace", container.Namespace))
		return Adjustment{}, false
	}
	if !listening(cfg.ModuleUnloadListenerPath) {
		zap.L().Warn("module unload listener is not serving; the driver manager sees the node's own kernel modules",
			zap.String("namespace", container.Namespace), zap.String("listener", cfg.ModuleUnloadListenerPath))
		return Adjustment{}, false
	}

	zap.L().Debug("adjusting the driver manager", zap.String("namespace", container.Namespace))
	return Adjustment{
		// Read-only: the listener unloads on the node's side.
		Mounts: []Mount{modulesMount(cfg, "ro")},
		SeccompNotify: &SeccompNotify{
			ListenerPath: cfg.ModuleUnloadListenerPath,
			Metadata:     container.Namespace + "/" + container.Name,
			Syscalls:     []string{deleteModuleSyscall},
		},
	}, true
}

// wantsDriverModules matches a container that asks the NVIDIA runtime for GPUs
// (NVIDIA_VISIBLE_DEVICES, its last value, neither empty, void nor none) while
// the simulated modules are enabled. The GPU Operator's operands gate on
// /sys/module/nvidia/initstate; the runtime would give them Mokka's static
// tree through CDI only if the node's /proc/modules listed nvidia when it
// created them, which the driver stand-in binds a moment after the driver
// manager lets the operands back. Bound here, the live tree is there however
// the race goes, and the gate follows the stand-in's loads and unloads.
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

// adjustDriverModules binds the simulated modules read-only over /sys/module,
// unless the tree is missing: the mount would fail container creation.
func adjustDriverModules(cfg Config, container Container, adjustment *Adjustment) {
	if _, err := os.Stat(cfg.DriverModulesHostPath); err != nil {
		zap.L().Warn("simulated modules unavailable; leaving /sys/module as authored",
			zap.String("namespace", container.Namespace), zap.String("container", container.Name), zap.Error(err))
		return
	}
	adjustment.Mounts = append(adjustment.Mounts, modulesMount(cfg, "ro"))
}

func hasMountAt(container Container, destination string) bool {
	for _, mount := range container.Mounts {
		if mount.Destination == destination {
			return true
		}
	}
	return false
}

// modulesMount binds the simulated modules over /sys/module; rprivate as the
// overlay binds.
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

// listenerProbeTimeout bounds the probe on the container-creation path.
const listenerProbeTimeout = 100 * time.Millisecond
