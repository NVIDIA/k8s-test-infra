// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/nri/pkg/api"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// linuxDevice stats a real path, so these exercise the syscall rather than a
// mock. The error paths are the interesting ones: both mean the plugin is about
// to hand containerd a device it cannot open.
func TestLinuxDeviceRejectsNonDevicePaths(t *testing.T) {
	t.Parallel()

	t.Run("nonexistent path returns the wrapped stat error", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "nvidia0")

		device, err := linuxDevice(inject.Device{HostPath: missing, Path: "/dev/nvidia0"})

		// Catches a swallowed stat error: without the check, a vanished device
		// node yields a LinuxDevice with major/minor decoded from a zeroed
		// Stat_t, so containerd is told /dev/nvidia0 is device 0:0.
		require.Error(t, err)
		require.Nil(t, device)
		require.ErrorContains(t, err, "stat device "+missing)
	})

	t.Run("regular file is rejected as not a character device", func(t *testing.T) {
		t.Parallel()
		regular := filepath.Join(t.TempDir(), "nvidia0")
		require.NoError(t, os.WriteFile(regular, []byte("not a device"), 0o644))

		device, err := linuxDevice(inject.Device{HostPath: regular, Path: "/dev/nvidia0"})

		// Catches a missing S_IFCHR check. A regular file stats fine, so
		// without it the plugin emits type "c" for a path that is not a
		// character device and container creation fails inside the runtime.
		require.Error(t, err)
		require.Nil(t, device)
		require.ErrorContains(t, err, regular+" is not a character device")
	})

	t.Run("directory is rejected as not a character device", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		device, err := linuxDevice(inject.Device{HostPath: dir, Path: "/dev/nvidia0"})

		require.Error(t, err)
		require.Nil(t, device)
		require.ErrorContains(t, err, dir+" is not a character device")
	})
}

// The device number assertions live in devnum_oracle_test.go: /dev/null's
// major and minor are only 1 and 3 under the Linux dev_t encoding. Everything
// asserted here is platform independent.
func TestLinuxDeviceMapsCharacterDeviceMetadata(t *testing.T) {
	t.Parallel()

	device, err := linuxDevice(inject.Device{HostPath: "/dev/null", Path: "/dev/nvidia0"})
	require.NoError(t, err)
	require.NotNil(t, device)

	// Path must be the container path, not the host path. Catches a swap that
	// would mount the host's staging path into the container namespace.
	require.Equal(t, "/dev/nvidia0", device.Path)
	require.Equal(t, "c", device.Type)

	// FileMode must carry only the permission bits. Catches passing the raw
	// st_mode, whose S_IFCHR bits would land in the mode field.
	require.NotNil(t, device.FileMode)
	require.Equal(t, uint32(0o666), device.FileMode.GetValue()&uint32(os.ModePerm))
	require.Zero(t, device.FileMode.GetValue()&^uint32(os.ModePerm))
}

func TestContainerFromNRI(t *testing.T) {
	t.Parallel()

	t.Run("copies pod and container fields", func(t *testing.T) {
		t.Parallel()
		pod := &api.PodSandbox{
			Namespace:   "gpu-tests",
			Annotations: map[string]string{"nvml-mock.nvidia.com/inject": "true"},
		}
		container := &api.Container{
			Env: []string{"PATH=/usr/bin", "MOCK_IB=off"},
			Mounts: []*api.Mount{{
				Source:      "/var/lib/nvml-mock",
				Destination: "/opt/nvml-mock",
				Type:        "bind",
				Options:     []string{"rbind", "ro"},
			}},
		}

		result := containerFromNRI(pod, container)

		require.Equal(t, "gpu-tests", result.Namespace)
		require.Equal(t, map[string]string{"nvml-mock.nvidia.com/inject": "true"}, result.PodAnnotations)
		require.Equal(t, []string{"PATH=/usr/bin", "MOCK_IB=off"}, result.Env)
		require.Equal(t, []inject.Mount{{
			Source:      "/var/lib/nvml-mock",
			Destination: "/opt/nvml-mock",
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		}}, result.Mounts)
	})

	// MEP-0002: the suppression rule in inject.Adjust is inert unless fromNRI
	// actually carries the incoming device state across. Verified against a real
	// containerd 2.2.0 CreateContainer payload, which populates Linux.Devices with
	// the device plugin's allocation and CDIDevices for the cdi-* strategies.
	t.Run("copies the devices the runtime already applied", func(t *testing.T) {
		t.Parallel()
		container := &api.Container{
			Linux: &api.LinuxContainer{
				Devices: []*api.LinuxDevice{
					{Path: "/dev/nvidia0", Type: "c", Major: 195, Minor: 0},
					{Path: "/dev/fuse", Type: "c", Major: 10, Minor: 229},
				},
			},
			CDIDevices: []*api.CDIDevice{{Name: "nvidia.com/gpu=0"}},
		}

		result := containerFromNRI(nil, container)

		require.Equal(t, []inject.Device{
			{Path: "/dev/nvidia0"},
			{Path: "/dev/fuse"},
		}, result.Devices)
		require.Equal(t, []string{"nvidia.com/gpu=0"}, result.CDIDevices)
	})

	t.Run("tolerates a nil Linux block", func(t *testing.T) {
		t.Parallel()
		// NRI leaves Linux nil for non-Linux or minimal containers; a nil deref
		// here takes the plugin down and stops injection node-wide.
		require.NotPanics(t, func() {
			result := containerFromNRI(nil, &api.Container{Env: []string{"PATH=/usr/bin"}})
			require.Empty(t, result.Devices)
			require.Empty(t, result.CDIDevices)
		})
	})

	t.Run("does not alias the runtime's slices", func(t *testing.T) {
		t.Parallel()
		container := &api.Container{
			Env:    []string{"PATH=/usr/bin"},
			Mounts: []*api.Mount{{Source: "/src", Options: []string{"rbind"}}},
		}

		result := containerFromNRI(nil, container)
		result.Env[0] = "PATH=/clobbered"
		result.Mounts[0].Options[0] = "rw"

		// Catches dropping the defensive copies. NRI owns these slices; writing
		// through them corrupts the runtime's own view of the container.
		require.Equal(t, []string{"PATH=/usr/bin"}, container.Env)
		require.Equal(t, []string{"rbind"}, container.Mounts[0].Options)
	})

	t.Run("tolerates nil pod and nil container", func(t *testing.T) {
		t.Parallel()
		// Catches a nil dereference. NRI may deliver either as nil, and a panic
		// in CreateContainer takes the plugin down and stops all injection.
		require.NotPanics(t, func() {
			result := containerFromNRI(nil, nil)
			require.Empty(t, result.Namespace)
			require.Empty(t, result.Env)
			require.Empty(t, result.Mounts)
		})

		podOnly := containerFromNRI(&api.PodSandbox{Namespace: "default"}, nil)
		require.Equal(t, "default", podOnly.Namespace)
		require.Empty(t, podOnly.Env)
	})
}

func TestAdjustmentToNRI(t *testing.T) {
	t.Parallel()

	t.Run("maps mounts and env", func(t *testing.T) {
		t.Parallel()
		adjustment := inject.Adjustment{
			Mounts: []inject.Mount{{
				Source:      "/var/lib/nvml-mock",
				Destination: "/opt/nvml-mock",
				Type:        "bind",
				Options:     []string{"rbind", "ro"},
			}},
			Env: []string{"MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml"},
		}

		result := adjustmentToNRI(adjustment)

		require.Len(t, result.Mounts, 1)
		require.Equal(t, "/var/lib/nvml-mock", result.Mounts[0].Source)
		require.Equal(t, "/opt/nvml-mock", result.Mounts[0].Destination)
		require.Equal(t, "bind", result.Mounts[0].Type)
		require.Equal(t, []string{"rbind", "ro"}, result.Mounts[0].Options)

		require.Len(t, result.Env, 1)
		require.Equal(t, "MOCK_NVML_CONFIG", result.Env[0].Key)
		require.Equal(t, "/opt/nvml-mock/driver/config/config.yaml", result.Env[0].Value)
	})

	t.Run("splits env on the first separator only", func(t *testing.T) {
		t.Parallel()
		result := adjustmentToNRI(inject.Adjustment{
			Env: []string{"LD_PRELOAD=/a/lib.so:/b/lib.so", "PATH=/usr/bin=weird"},
		})

		// Catches splitting on every separator, which would truncate any value
		// containing "=" -- LD_PRELOAD chains and PATH entries are the risk.
		require.Len(t, result.Env, 2)
		require.Equal(t, "LD_PRELOAD", result.Env[0].Key)
		require.Equal(t, "/a/lib.so:/b/lib.so", result.Env[0].Value)
		require.Equal(t, "PATH", result.Env[1].Key)
		require.Equal(t, "/usr/bin=weird", result.Env[1].Value)
	})

	t.Run("drops env entries with no separator", func(t *testing.T) {
		t.Parallel()
		result := adjustmentToNRI(inject.Adjustment{
			Env: []string{"MALFORMED", "PATH=/usr/bin"},
		})

		// Catches emitting a KeyValue with an empty key, which claims NRI
		// ownership of a variable that does not exist and can collide with
		// another plugin's adjustment.
		require.Len(t, result.Env, 1)
		require.Equal(t, "PATH", result.Env[0].Key)
	})

	t.Run("fails open per device and keeps the usable ones", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "nvidia1")
		result := adjustmentToNRI(inject.Adjustment{
			Devices: []inject.Device{
				{HostPath: missing, Path: "/dev/nvidia1"},
				{HostPath: "/dev/null", Path: "/dev/nvidia0"},
			},
		})

		// Catches turning a per-device stat failure into a whole-container
		// error. One unstaged device node must not block container creation.
		require.NotNil(t, result.Linux)
		require.Len(t, result.Linux.Devices, 1)
		require.Equal(t, "/dev/nvidia0", result.Linux.Devices[0].Path)
	})

	t.Run("leaves Linux unset when no device is usable", func(t *testing.T) {
		t.Parallel()
		result := adjustmentToNRI(inject.Adjustment{
			Devices: []inject.Device{
				{HostPath: filepath.Join(t.TempDir(), "nvidia0"), Path: "/dev/nvidia0"},
			},
		})
		require.Empty(t, result.Env)
		require.Empty(t, result.Mounts)
		require.Nil(t, result.Linux)
	})
}
