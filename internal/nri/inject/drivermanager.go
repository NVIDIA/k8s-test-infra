// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"net"
	"time"

	"go.uber.org/zap"
)

const (
	driverManagerContainerName = "k8s-driver-manager"
	// driverComponentLabel marks the GPU Operator's driver pods, whichever
	// object (ClusterPolicy or NVIDIADriver) the operator rendered them from.
	driverComponentLabel = "app.kubernetes.io/component"
	driverComponent      = "nvidia-driver"
	// sysModulePath is where the driver manager looks for loaded modules.
	sysModulePath = "/sys/module"
	// deleteModuleSyscall is how the driver manager unloads them.
	deleteModuleSyscall = "delete_module"
)

// driverManager identifies the GPU Operator driver pod's init container, which
// pauses the GPU operands and unloads the loaded driver before each install,
// while the simulated modules are enabled.
func driverManager(cfg Config, container Container) bool {
	return cfg.DriverModulesHostPath != "" && driverPod(container, driverManagerContainerName)
}

func driverPod(container Container, name string) bool {
	return container.Name == name && container.PodLabels[driverComponentLabel] == driverComponent
}

// adjustDriverManager shows the driver manager the node's simulated kernel
// modules and answers its delete_module calls from them, so that the stock
// image does on a mock node what it does around a real driver change. Its
// container is privileged, so /sys/module is otherwise the node kernel's,
// with no nvidia module in it, and the driver manager would skip everything.
//
// The two go together or not at all: a driver manager that sees the modules
// but reaches the kernel to unload them fails with ENOENT, which on a real
// node means a driver it cannot unload. It is left unmodified, as on a node
// without the simulation, when the feature is off or the listener is down;
// runc would fail the container on a listener socket it cannot connect to.
func adjustDriverManager(cfg Config, container Container) (Adjustment, bool) {
	if container.HasSeccompPolicy {
		// The notification policy replaces the container's whole one, which
		// would widen a confined container to every syscall. The GPU
		// Operator's driver manager is privileged, which runs it without one.
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
		// Read-only: the listener makes the unloads on the node's side of
		// this bind, and nothing in the container writes /sys/module.
		Mounts: []Mount{{
			Source:      cfg.DriverModulesHostPath,
			Destination: sysModulePath,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev", "noexec"},
		}},
		SeccompNotify: &SeccompNotify{
			ListenerPath: cfg.ModuleUnloadListenerPath,
			Metadata:     container.Namespace + "/" + container.Name,
			Syscalls:     []string{deleteModuleSyscall},
		},
	}, true
}

// listening reports whether something accepts on the socket, as runc's connect
// must. The socket file alone outlives a plugin that crashed, and is briefly
// absent while a starting plugin replaces it.
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

// listenerProbeTimeout bounds the probe on the container-creation path; the
// listener runs in this process, so it answers at once or not at all.
const listenerProbeTimeout = 100 * time.Millisecond
