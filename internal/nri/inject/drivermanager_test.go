// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// driverManagerConfig enables the simulated modules, with the unload listener
// serving when listening is true.
func driverManagerConfig(t *testing.T, listening bool) Config {
	t.Helper()

	// t.TempDir's paths can exceed the length limit of a socket path.
	dir, err := os.MkdirTemp("", "dm")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })

	cfg := DefaultConfig()
	cfg.HostOverlayPath = t.TempDir()
	cfg.DriverModulesHostPath = filepath.Join(dir, "modules")
	cfg.ModuleUnloadListenerPath = filepath.Join(dir, "unload.sock")
	if listening {
		listener, err := net.Listen("unix", cfg.ModuleUnloadListenerPath)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, listener.Close()) })
	}
	return withDefaults(cfg)
}

func driverManagerContainer() Container {
	return Container{
		Name:      driverManagerContainerName,
		Namespace: "gpu-operator",
		PodLabels: map[string]string{"app": "nvidia-driver-daemonset", driverComponentLabel: driverComponent},
	}
}

func TestAdjustShowsTheDriverManagerTheSimulatedModules(t *testing.T) {
	t.Parallel()

	cfg := driverManagerConfig(t, true)

	adjustment, ok := Adjust(cfg, driverManagerContainer())

	require.True(t, ok)
	require.Equal(t, []Mount{{
		Source:      cfg.DriverModulesHostPath,
		Destination: "/sys/module",
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev", "noexec"},
	}}, adjustment.Mounts)
	require.Equal(t, &SeccompNotify{
		ListenerPath: cfg.ModuleUnloadListenerPath,
		Metadata:     "gpu-operator/k8s-driver-manager",
		Syscalls:     []string{"delete_module"},
	}, adjustment.SeccompNotify)
	require.Empty(t, adjustment.Env, "the driver manager gets no mock GPUs")
	require.Empty(t, adjustment.Devices)
}

// Seeing loaded modules it cannot unload, the driver manager would fail as on
// a node whose driver is stuck; runc would fail a container whose listener it
// cannot reach. Without the listener, the container is left as authored.
func TestAdjustLeavesTheDriverManagerUnmodifiedWithoutTheListener(t *testing.T) {
	t.Parallel()

	_, ok := Adjust(driverManagerConfig(t, false), driverManagerContainer())

	require.False(t, ok)
}

func TestAdjustLeavesTheDriverManagerUnmodifiedWhenDisabled(t *testing.T) {
	t.Parallel()

	cfg := driverManagerConfig(t, true)
	cfg.DriverModulesHostPath = ""

	_, ok := Adjust(cfg, driverManagerContainer())

	require.False(t, ok)
}

// Disabled, the driver pod's containers are ordinary ones again: gated, and
// given the generic overlay when they opt in.
func TestAdjustTreatsTheDriverPodAsAnyOtherWhenDisabled(t *testing.T) {
	t.Parallel()

	cfg := driverManagerConfig(t, true)
	cfg.DriverModulesHostPath = ""
	container := driverManagerContainer()
	container.PodAnnotations = map[string]string{cfg.DeviceAnnotation: "true"}

	adjustment, ok := Adjust(cfg, container)

	require.True(t, ok)
	require.NotEmpty(t, adjustment.Env, "the generic overlay applies")
	require.Nil(t, adjustment.SeccompNotify)
	require.True(t, Gated(cfg, container))
}

// The name alone is not enough: other pods may run a container called
// k8s-driver-manager, such as the vGPU manager's.
func TestAdjustRecognizesOnlyTheDriverPodsDriverManager(t *testing.T) {
	t.Parallel()

	cfg := driverManagerConfig(t, true)
	container := driverManagerContainer()
	container.PodLabels = map[string]string{driverComponentLabel: "nvidia-vgpu-manager"}

	_, ok := Adjust(cfg, container)

	require.False(t, ok)
	require.True(t, Gated(cfg, container))
	require.False(t, Gated(cfg, driverManagerContainer()))
}

// The stand-in for the driver loads its modules by writing them where the
// driver manager and the startup probe read them.
func TestAdjustGivesTheDriverContainerTheSimulatedModulesWritable(t *testing.T) {
	t.Parallel()

	cfg := driverManagerConfig(t, false)
	container := driverManagerContainer()
	container.Name = driverContainerName

	adjustment, ok := Adjust(cfg, container)

	require.True(t, ok)
	require.Equal(t, []Mount{{
		Source:      cfg.DriverModulesHostPath,
		Destination: "/sys/module",
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", "rw", "nosuid", "nodev", "noexec"},
	}}, adjustment.Mounts)
	require.Nil(t, adjustment.SeccompNotify, "the driver container unloads nothing")
	require.Empty(t, adjustment.Env, "the driver container gets no mock GPUs")
	require.False(t, Gated(cfg, container))

	cfg.DriverModulesHostPath = ""
	_, ok = Adjust(cfg, container)
	require.False(t, ok)
}

// A socket file left by a plugin that crashed outlives its listener; runc
// would fail the container connecting to it.
func TestAdjustLeavesTheDriverManagerUnmodifiedWithAStaleListenerSocket(t *testing.T) {
	t.Parallel()

	cfg := driverManagerConfig(t, false)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: cfg.ModuleUnloadListenerPath, Net: "unix"})
	require.NoError(t, err)
	listener.SetUnlinkOnClose(false)
	require.NoError(t, listener.Close())
	require.FileExists(t, cfg.ModuleUnloadListenerPath)

	_, ok := Adjust(cfg, driverManagerContainer())

	require.False(t, ok)
}

// The notification policy replaces the container's whole one: a confined
// driver manager keeps its own rather than being opened to every syscall.
func TestAdjustLeavesAConfinedDriverManagerUnmodified(t *testing.T) {
	t.Parallel()

	container := driverManagerContainer()
	container.HasSeccompPolicy = true

	_, ok := Adjust(driverManagerConfig(t, true), container)

	require.False(t, ok)
}

// operandContainer is a GPU Operator operand's gate: it asks the NVIDIA
// runtime for every GPU and waits for /sys/module/nvidia/initstate.
func operandContainer() Container {
	return Container{
		Name:      "toolkit-validation",
		Namespace: "gpu-operator",
		PodLabels: map[string]string{"app": "nvidia-device-plugin-daemonset"},
		Env:       []string{"PATH=/usr/bin", "NVIDIA_VISIBLE_DEVICES=all"},
	}
}

// withModulesTree creates the simulated modules' root, as the DaemonSet's
// hostPath does before the plugin runs.
func withModulesTree(t *testing.T, cfg Config) Config {
	t.Helper()
	require.NoError(t, os.MkdirAll(cfg.DriverModulesHostPath, 0o755))
	return cfg
}

// The runtime gives an operand Mokka's tree only if /proc/modules listed nvidia
// when it created the container; one created just before the driver stand-in
// loaded would wait on its gate forever. The plugin binds the live tree
// whatever the race, read-only, and adds nothing else.
func TestAdjustGivesAContainerAskingForGPUsTheSimulatedModules(t *testing.T) {
	t.Parallel()

	cfg := withModulesTree(t, driverManagerConfig(t, false))

	adjustment, ok := Adjust(cfg, operandContainer())

	require.True(t, ok)
	require.Equal(t, []Mount{{
		Source:      cfg.DriverModulesHostPath,
		Destination: "/sys/module",
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev", "noexec"},
	}}, adjustment.Mounts)
	require.Empty(t, adjustment.Env, "no mock GPUs without an allocation or the annotation")
	require.Empty(t, adjustment.Devices)
	require.Nil(t, adjustment.SeccompNotify)
}

// The runtime reads the variable's last value; void, none and empty ask for
// no GPU.
func TestAdjustLeavesContainersNotAskingForGPUsUnmodified(t *testing.T) {
	t.Parallel()

	cfg := withModulesTree(t, driverManagerConfig(t, false))
	for name, env := range map[string][]string{
		"unset":        {"PATH=/usr/bin"},
		"empty":        {"NVIDIA_VISIBLE_DEVICES="},
		"void":         {"NVIDIA_VISIBLE_DEVICES=void"},
		"none":         {"NVIDIA_VISIBLE_DEVICES=none"},
		"last is void": {"NVIDIA_VISIBLE_DEVICES=all", "NVIDIA_VISIBLE_DEVICES=void"},
	} {
		container := operandContainer()
		container.Env = env

		_, ok := Adjust(cfg, container)

		require.False(t, ok, name)
	}
}

func TestAdjustLeavesOperandsUnmodifiedWhenDisabled(t *testing.T) {
	t.Parallel()

	cfg := driverManagerConfig(t, false)
	cfg.DriverModulesHostPath = ""

	_, ok := Adjust(cfg, operandContainer())

	require.False(t, ok)
}

// A container that already has a /sys/module, such as one adjusted before,
// keeps it.
func TestAdjustKeepsAnExistingSysModuleMount(t *testing.T) {
	t.Parallel()

	cfg := withModulesTree(t, driverManagerConfig(t, false))
	container := operandContainer()
	container.Mounts = []Mount{{Source: "/elsewhere", Destination: "/sys/module", Type: "bind"}}

	_, ok := Adjust(cfg, container)

	require.False(t, ok)
}

// A missing tree would fail container creation; the container is left with
// the node's /sys/module instead.
func TestAdjustSkipsTheSimulatedModulesWhenTheTreeIsMissing(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(driverManagerConfig(t, false), operandContainer())

	require.True(t, ok)
	require.Empty(t, adjustment.Mounts)
}

// A GPU workload gets the mock GPUs and, asking the runtime for them too, the
// simulated modules beside the overlay.
func TestAdjustGivesAGPUWorkloadTheOverlayAndTheSimulatedModules(t *testing.T) {
	t.Parallel()

	cfg := withModulesTree(t, driverManagerConfig(t, false))
	container := operandContainer()
	container.PodAnnotations = map[string]string{cfg.DeviceAnnotation: "true"}

	adjustment, ok := Adjust(cfg, container)

	require.True(t, ok)
	require.NotEmpty(t, adjustment.Env, "the generic overlay applies")
	destinations := make([]string, 0, len(adjustment.Mounts))
	for _, m := range adjustment.Mounts {
		destinations = append(destinations, m.Destination)
	}
	require.Contains(t, destinations, cfg.ContainerOverlayPath)
	require.Contains(t, destinations, "/sys/module")
}
