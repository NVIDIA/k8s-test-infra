// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"

	kmod "github.com/NVIDIA/k8s-test-infra/shims/nvidia-kmod"
)

// The GPU Operator gives its driver pods this component label, whether a
// ClusterPolicy or an NVIDIADriver created them, and runs the installer in this
// container. The pod's init container, k8s-driver-manager, needs nothing: it
// only unloads modules, which the kernel does for the simulated ones as for
// real ones.
const (
	componentLabel      = "app.kubernetes.io/component"
	driverComponent     = "nvidia-driver"
	driverInstallerName = "nvidia-driver-ctr"
)

// Where the installer's root serves the mock NVML. The operator's operands
// search a driver root's /usr/lib64 first, and the engine looks for its
// configuration in <root>/config beside the library it was loaded from.
const (
	driverNVMLPath   = kmod.ContainerNVMLDir + "/libnvidia-ml.so.1"
	driverConfigPath = "/config"
)

// GPUOperatorDriver is the GPU Operator's driver installer, whose image stays
// NVIDIA's own: it gets the simulated kernel modules for its `modprobe nvidia`,
// and the mock NVML in its root, which the container also exposes to every
// other operand at /run/nvidia/driver.
type GPUOperatorDriver struct {
	// HostOverlayPath is the node agent's tree.
	HostOverlayPath string
	// DeviceHostPath holds the node agent's device nodes.
	DeviceHostPath string
}

var _ Workload = GPUOperatorDriver{}

// Matches reports whether container is the driver installer.
func (GPUOperatorDriver) Matches(container Container) bool {
	return container.Name == driverInstallerName && container.PodLabels[componentLabel] == driverComponent
}

// Adjust mounts the staged module tree, its install directives and the mock
// NVML, and starts the persistenced stand-in in place of the daemon.
//
// Nothing is added when the node agent has not staged the module tree. A bind
// whose source is missing fails container creation, and the container is
// better off failing its real module load visibly, in its own log. Nor is
// anything added to a container that already carries the install directives:
// one that has been through here before would fail its creation on the second
// set of mounts.
func (d GPUOperatorDriver) Adjust(container Container) (Adjustment, bool) {
	if hasKernelModules(container) {
		return Adjustment{}, false
	}

	staged := filepath.Join(d.HostOverlayPath, kmod.StagedDir)
	conf := filepath.Join(staged, kmod.ModprobeConfFile)
	if _, err := os.Stat(conf); err != nil {
		zap.L().Warn("simulated kernel modules are not staged; the driver container keeps the real ones",
			zap.String("path", conf), zap.Error(err))
		return Adjustment{}, false
	}

	var adjustment Adjustment
	attachKernelModules(staged, conf, &adjustment)
	d.attachMockNVML(staged, &adjustment)
	attachPersistencedStandIn(container, &adjustment)

	return adjustment, true
}

// hasKernelModules reports whether the container already carries the install
// directives.
func hasKernelModules(container Container) bool {
	for _, mount := range container.Mounts {
		if mount.Destination == kmod.ContainerModprobeConf {
			return true
		}
	}
	return false
}

// attachPersistencedStandIn puts the staged tree's stand-ins first on the
// container's PATH, so that it starts the one for nvidia-persistenced. The
// real daemon needs the Resource Manager, which the simulated modules do not
// implement, and its failure aborts the userspace-only install the container
// runs over a driver it keeps loaded.
func attachPersistencedStandIn(container Container, adjustment *Adjustment) {
	env := newEnvSet(container.Env)
	env.prepend("PATH", kmod.ContainerStandInDir)
	adjustment.Env = append(adjustment.Env, env.changed()...)
}

// attachKernelModules mounts the staged module tree and its install directives,
// so that the container's own `modprobe nvidia` builds and loads the simulated
// modules instead of the real ones it compiled.
//
// Both binds are read-only and rprivate, for the reason mountOverlay gives: the
// container holds /run/nvidia as a Bidirectional volume, which makes its root
// rshared. The container then exposes its root at /run/nvidia/driver with an
// rbind, which carries these mounts along, so `chroot /run/nvidia/driver
// modprobe nvidia-peermem` finds the directives too.
func attachKernelModules(staged, conf string, adjustment *Adjustment) {
	adjustment.Mounts = append(adjustment.Mounts,
		Mount{
			Source:      staged,
			Destination: kmod.ContainerDir,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
		},
		Mount{
			Source:      conf,
			Destination: kmod.ContainerModprobeConf,
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		},
	)
}

// attachMockNVML puts the mock NVML where the installer's own userspace is
// looked up, without displacing any of it:
//
//   - The library is bound alone, at the first path the operator's operands
//     search a driver root for. A directory bind would hide the image's
//     /usr/lib64, which holds the dynamic loader on x86_64. Ubuntu images
//     install the real library under /usr/lib/<triplet>, so nothing the
//     installer writes lands on the bind. Its sweep for conflicting libraries
//     fails to delete it and carries on, as --silent answers.
//   - The linker directive has the ldconfig run the installer ends with put
//     the mock ahead of the library it installed. Everything that resolves
//     the library through the container's root then loads the mock: the
//     container's own processes, the startup probe's nvidia-smi among them,
//     and the validator's `chroot /run/nvidia/driver nvidia-smi`.
//   - The engine finds its configuration beside the root it was loaded from:
//     the container serves /config to its own processes, whose root is /, and
//     to every operand, whose root is /run/nvidia/driver. It is writable, as
//     the overlay's is, because the engine writes resets back through it.
//   - Every GPU node is added, as the real userspace creates them. With all of
//     them present the engine filters no GPU out.
func (d GPUOperatorDriver) attachMockNVML(staged string, adjustment *Adjustment) {
	adjustment.Mounts = append(adjustment.Mounts,
		Mount{
			Source:      filepath.Join(d.HostOverlayPath, mockNVMLRelPath),
			Destination: driverNVMLPath,
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		},
		Mount{
			Source:      filepath.Join(staged, kmod.LdSoConfFile),
			Destination: kmod.ContainerLdSoConf,
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		},
		Mount{
			Source:      filepath.Join(d.HostOverlayPath, configRelPath),
			Destination: driverConfigPath,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "rw", "nosuid", "nodev"},
		},
	)

	attachRawGPUNodes(d.DeviceHostPath, adjustment)
}
