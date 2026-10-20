// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	kmod "github.com/NVIDIA/k8s-test-infra/shims/nvidia-kmod"
)

// stageKernelModules writes the part of the node agent's tree the plugin checks
// for and returns the overlay root it lives under.
func stageKernelModules(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	conf := filepath.Join(root, kmod.StagedDir, kmod.ModprobeConfFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(conf), 0o755))
	require.NoError(t, os.WriteFile(conf, []byte(kmod.ModprobeConf(kmod.ContainerDir)), 0o600))
	return root
}

// driverInstaller is the container the GPU Operator runs its installer in, as
// NRI presents it.
func driverInstaller() Container {
	return Container{
		Name:      "nvidia-driver-ctr",
		Namespace: "gpu-operator",
		// As the driver image sets it.
		Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		PodLabels: map[string]string{
			"app":                         "nvidia-driver-daemonset",
			"app.kubernetes.io/component": "nvidia-driver",
		},
	}
}

func TestAdjustAdaptsTheDriverInstaller(t *testing.T) {
	t.Parallel()

	root := stageKernelModules(t)
	cfg := Config{
		HostOverlayPath:   root,
		DeviceHostPath:    stageDeviceNodes(t, "nvidia0", "nvidia1", "nvidiactl", "nvidia-uvm"),
		GPUOperatorDriver: true,
	}

	adjustment, ok := Adjust(cfg, driverInstaller())
	require.True(t, ok)

	// The simulated modules for its own modprobe, then the mock NVML in its
	// root. None of the overlay or the shims the GPU selections get: the
	// installer has to see the real kernel state, which libmockfs would hide.
	require.Equal(t, []Mount{
		{
			Source:      filepath.Join(root, kmod.StagedDir),
			Destination: kmod.ContainerDir,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
		},
		{
			Source:      filepath.Join(root, kmod.StagedDir, kmod.ModprobeConfFile),
			Destination: kmod.ContainerModprobeConf,
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		},
		{
			Source:      filepath.Join(root, "driver/usr/lib64/libnvidia-ml.so.1"),
			Destination: "/usr/lib64/libnvidia-ml.so.1",
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		},
		{
			Source:      filepath.Join(root, kmod.StagedDir, kmod.LdSoConfFile),
			Destination: "/etc/ld.so.conf.d/000-mokka-nvml.conf",
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		},
		{
			Source:      filepath.Join(root, "driver/config"),
			Destination: "/config",
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "rw", "nosuid", "nodev"},
		},
	}, adjustment.Mounts)
	// The linker directive, not the environment, serves the mock: it reaches
	// every process that resolves the library through the container's root,
	// where a variable would reach the container's own alone. PATH changes
	// only for the persistenced stand-in.
	require.Equal(t, []string{
		"PATH=/opt/mokka/nvidia-kmod/stand-ins:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}, adjustment.Env)
	require.Len(t, adjustment.Devices, 4, "every staged node, so the engine filters no GPU out")
}

// Mokka's CDI device would also bind its own nvidia-smi over the one the
// installer writes, so the installer gets raw nodes whatever the mode.
func TestAdjustGivesTheDriverInstallerRawNodesInCDIMode(t *testing.T) {
	t.Parallel()

	cfg := Config{
		HostOverlayPath:     stageKernelModules(t),
		DeviceHostPath:      stageDeviceNodes(t, "nvidia0", "nvidiactl"),
		DeviceInjectionMode: DeviceInjectionModeCDI,
		CDISpecHostPath:     stageCDISpec(t),
		GPUOperatorDriver:   true,
	}

	adjustment, ok := Adjust(cfg, driverInstaller())
	require.True(t, ok)
	require.Empty(t, adjustment.CDIDevices)
	require.Len(t, adjustment.Devices, 2)
}

func TestAdjustLeavesTheDriverInstallerAlone(t *testing.T) {
	t.Parallel()

	root := stageKernelModules(t)

	tests := map[string]struct {
		cfg       Config
		container func() Container
	}{
		"gate disabled": {
			cfg:       Config{HostOverlayPath: root},
			container: driverInstaller,
		},
		// k8s-driver-manager only unloads modules, which needs nothing from us.
		"init container": {
			cfg: Config{HostOverlayPath: root, GPUOperatorDriver: true},
			container: func() Container {
				c := driverInstaller()
				c.Name = "k8s-driver-manager"
				return c
			},
		},
		"same container name outside a driver pod": {
			cfg: Config{HostOverlayPath: root, GPUOperatorDriver: true},
			container: func() Container {
				c := driverInstaller()
				c.PodLabels = map[string]string{"app": "something-else"}
				return c
			},
		},
		"opted out": {
			cfg: Config{HostOverlayPath: root, GPUOperatorDriver: true},
			container: func() Container {
				c := driverInstaller()
				c.PodAnnotations = map[string]string{DefaultConfig().OptOutAnnotation: "false"}
				return c
			},
		},
		// A restarted container already carries the mounts; adding them twice
		// would fail its creation.
		"already adjusted": {
			cfg: Config{HostOverlayPath: root, GPUOperatorDriver: true},
			container: func() Container {
				c := driverInstaller()
				c.Mounts = []Mount{{Destination: kmod.ContainerModprobeConf}}
				return c
			},
		},
		// Mounting a missing source would fail the container's creation.
		"nothing staged": {
			cfg:       Config{HostOverlayPath: t.TempDir(), GPUOperatorDriver: true},
			container: driverInstaller,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adjustment, ok := Adjust(tt.cfg, tt.container())
			require.False(t, ok)
			require.Empty(t, adjustment.Mounts)
		})
	}
}
