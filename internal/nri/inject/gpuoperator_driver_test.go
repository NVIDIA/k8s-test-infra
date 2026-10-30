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

// stageKernelModules writes the part of the node agent's tree the workload
// checks for and returns the overlay root it lives under.
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

func TestGPUOperatorDriverMatchesTheInstallerAlone(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		container func() Container
		want      bool
	}{
		"installer": {container: driverInstaller, want: true},
		// k8s-driver-manager only unloads modules, which needs nothing from us.
		"init container": {
			container: func() Container {
				c := driverInstaller()
				c.Name = "k8s-driver-manager"
				return c
			},
		},
		"same container name outside a driver pod": {
			container: func() Container {
				c := driverInstaller()
				c.PodLabels = map[string]string{"app": "something-else"}
				return c
			},
		},
		// Routing takes the first workload that claims a container, so no two
		// may claim the same one.
		"toolkit installer": {container: toolkitInstaller},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, GPUOperatorDriver{}.Matches(tt.container()))
		})
	}
}

func TestGPUOperatorDriverAdaptsTheInstaller(t *testing.T) {
	t.Parallel()

	root := stageKernelModules(t)
	devices := stageDeviceNodes(t, "nvidia0", "nvidia1", "nvidiactl", "nvidia-uvm")
	driver := GPUOperatorDriver{HostOverlayPath: root, DeviceHostPath: devices}

	adjustment, ok := driver.Adjust(driverInstaller())
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
	// Raw nodes, never Mokka's CDI device, which would also bind its own
	// nvidia-smi over the one the installer writes.
	require.Len(t, adjustment.Devices, 4, "every staged node, so the engine filters no GPU out")
	require.Empty(t, adjustment.CDIDevices)
}

func TestGPUOperatorDriverLeavesTheInstallerAlone(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		root      string
		container func() Container
	}{
		// A restarted container already carries the mounts; adding them twice
		// would fail its creation.
		"already adjusted": {
			root: stageKernelModules(t),
			container: func() Container {
				c := driverInstaller()
				c.Mounts = []Mount{{Destination: kmod.ContainerModprobeConf}}
				return c
			},
		},
		// Mounting a missing source would fail the container's creation.
		"nothing staged": {root: t.TempDir(), container: driverInstaller},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			driver := GPUOperatorDriver{HostOverlayPath: tt.root, DeviceHostPath: t.TempDir()}
			adjustment, ok := driver.Adjust(tt.container())
			require.False(t, ok)
			require.Empty(t, adjustment.Mounts)
		})
	}
}
