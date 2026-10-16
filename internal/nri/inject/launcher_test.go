// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// bundleVolume is where KubeVirt mounts the hostPath directory of the VM's
// `hostDisk: {path: /run/nvml-mock-guest/bundle.tar}` volume named nvml-mock.
const bundleVolume = "/var/run/kubevirt-private/vmi-disks/nvml-mock/"

// launcher is the compute container of a KubeVirt virt-launcher pod holding
// the device plugin's allocation of the given minors and declaring the bundle
// volume, as containerd hands it to NRI once CDI is resolved.
func launcher(minors ...int64) Container {
	container := Container{
		ID:             "c0ffee",
		Namespace:      "default",
		PodAnnotations: map[string]string{"kubevirt.io/domain": "gpu-vm"},
		CDIDevices:     []string{"k8s.device-plugin.nvidia.com/gpu=GPU-c4d588f9-c5b0-9747-89b1-eda3f955765e"},
		Mounts: []Mount{
			{Source: "/var/lib/kubelet/pods/p/volumes/kubernetes.io~empty-dir/private", Destination: "/var/run/kubevirt-private"},
			{Source: "/run/nvml-mock-guest", Destination: bundleVolume, Type: "bind", Options: []string{"rbind", "rw"}},
		},
	}
	for _, minor := range minors {
		container.IncomingDevices = append(container.IncomingDevices, RuntimeDevice{
			Path: "/dev/nvidia" + strconv.FormatInt(minor, 10), Type: "c", Major: 195, Minor: minor,
		})
		container.DeviceRules = append(container.DeviceRules, DeviceRule{
			Allow: true, Type: "c", Major: new(int64(195)), Minor: new(minor), Access: "rwm",
		})
	}

	return container
}

func bundleMount(source, destination string) Mount {
	return Mount{
		Source:      source,
		Destination: destination,
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
	}
}

// libvirt and QEMU run in the launcher's compute container. The overlay's
// LD_PRELOAD shims would load into both, so the container gets nothing but its
// own bundle, in place of the empty placeholder its hostDisk volume names.
func TestLauncherGetsOnlyAGuestBundle(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), launcher(2))
	require.True(t, ok)
	require.Equal(t, Adjustment{Guest: &GuestBundle{
		Dir:    "/run/nvml-mock-guests/c0ffee",
		Minors: []int{2},
		Mounts: []Mount{bundleMount("/run/nvml-mock-guests/c0ffee", bundleVolume)},
	}}, adjustment)
}

// A VM may attach the bundle more than once; each volume is its own mount.
func TestLauncherSwapsEveryPlaceholderMount(t *testing.T) {
	t.Parallel()

	container := launcher(0)
	container.Mounts = append(container.Mounts, Mount{Source: "/run/nvml-mock-guest/", Destination: "/var/run/kubevirt-private/vmi-disks/again/"})

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.True(t, ok)
	require.Equal(t, []Mount{
		bundleMount("/run/nvml-mock-guests/c0ffee", bundleVolume),
		bundleMount("/run/nvml-mock-guests/c0ffee", "/var/run/kubevirt-private/vmi-disks/again/"),
	}, adjustment.Guest.Mounts)
}

func TestLauncherMinorsAreSortedAndUnique(t *testing.T) {
	t.Parallel()

	container := launcher(3, 1)
	container.IncomingDevices = append(container.IncomingDevices, container.IncomingDevices[0])

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.True(t, ok)
	require.Equal(t, []int{1, 3}, adjustment.Guest.Minors)
}

// The other opt-ins would bring the overlay's environment back into QEMU.
func TestLauncherIgnoresTheOtherOptIns(t *testing.T) {
	t.Parallel()

	container := launcher(0)
	container.PodAnnotations["nvml-mock.nvidia.com/devices"] = "true"
	container.PodAnnotations["nvml-mock.nvidia.com/infiniband"] = "true"
	container.PodAnnotations["nvml-mock.nvidia.com/imex-channels"] = "true"

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.True(t, ok)
	require.NotNil(t, adjustment.Guest)
	require.Empty(t, adjustment.Mounts)
	require.Empty(t, adjustment.Env)
	require.Empty(t, adjustment.Devices)
	require.Empty(t, adjustment.CDIDevices)
}

func TestLauncherHonoursConfiguredGuestPaths(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.GuestHostPath = "/srv/guests"
	cfg.GuestPlaceholderPath = "/srv/placeholder"
	container := launcher(0)
	container.Mounts[1].Source = "/srv/placeholder"

	adjustment, ok := Adjust(cfg, container)
	require.True(t, ok)
	require.Equal(t, "/srv/guests/c0ffee", adjustment.Guest.Dir)
	require.Equal(t, []Mount{bundleMount("/srv/guests/c0ffee", bundleVolume)}, adjustment.Guest.Mounts)
}

// A VM that requests a GPU without the bundle volume is still a valid VM; its
// guest just has no mock driver to install.
func TestLauncherWithoutTheBundleVolumeIsLeftAlone(t *testing.T) {
	t.Parallel()

	container := launcher(0)
	container.Mounts = container.Mounts[:1]

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.False(t, ok)
	require.Empty(t, adjustment)

	reason, skipped := Skip(DefaultConfig(), container)
	require.True(t, skipped)
	require.Equal(t, "KubeVirt launcher declares no guest bundle volume", reason)
}

func TestValidContainerID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		require.False(t, ValidContainerID(id), "%q", id)
	}
	require.True(t, ValidContainerID(strings.Repeat("c0", 32)))
}

func TestLauncherIsLeftAloneWhen(t *testing.T) {
	t.Parallel()

	tests := map[string]func(Container) Container{
		// The console logger shares the pod but holds no GPU.
		"it holds no allocation": func(Container) Container {
			return Container{
				ID:             "c0ffee",
				Namespace:      "default",
				PodAnnotations: map[string]string{"kubevirt.io/domain": "gpu-vm"},
			}
		},
		"it opted out": func(c Container) Container {
			c.PodAnnotations["nvml-mock.nvidia.com/inject"] = "false"

			return c
		},
		"its bundle is already mounted": func(c Container) Container {
			c.Mounts[1].Source = "/run/nvml-mock-guests/c0ffee"

			return c
		},
		// Without a minor the bundle would carry no /dev entry, and the guest
		// would see every GPU on the node.
		"no allocated device carries its own grant": func(c Container) Container {
			c.DeviceRules = []DeviceRule{{Allow: true, Type: "a", Access: "rwm"}}

			return c
		},
		"its ID is not a path component": func(c Container) Container {
			c.ID = "../escape"

			return c
		},
		"it has no ID": func(c Container) Container {
			c.ID = ""

			return c
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adjustment, ok := Adjust(DefaultConfig(), mutate(launcher(0)))
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}
}

// QEMU without its allocation, and the hook and console sidecars, share the
// launcher pod; an opt-in there would put the shims beside the hypervisor.
func TestLauncherPodOptInsDoNotReachUnallocatedContainers(t *testing.T) {
	t.Parallel()

	for _, annotation := range []string{
		"nvml-mock.nvidia.com/devices",
		"nvml-mock.nvidia.com/infiniband",
		"nvml-mock.nvidia.com/imex-channels",
	} {
		t.Run(annotation, func(t *testing.T) {
			t.Parallel()

			container := Container{
				ID:        "c0ffee",
				Namespace: "default",
				PodAnnotations: map[string]string{
					"kubevirt.io/domain": "gpu-vm",
					annotation:           "true",
				},
			}

			adjustment, ok := Adjust(DefaultConfig(), container)
			require.False(t, ok)
			require.Empty(t, adjustment)

			reason, skipped := Skip(DefaultConfig(), container)
			require.True(t, skipped)
			require.Equal(t, "KubeVirt launcher pod; only the allocated container is adjusted", reason)
		})
	}
}

func TestAllocatedPodsOutsideKubeVirtKeepTheOverlay(t *testing.T) {
	t.Parallel()

	container := launcher(0)
	delete(container.PodAnnotations, "kubevirt.io/domain")

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.True(t, ok)
	require.Nil(t, adjustment.Guest)
	require.Contains(t, adjustment.Mounts, overlayMount())
	require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
}

func TestGPUMinor(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]int{"/dev/nvidia0": 0, "/dev/nvidia17": 17, "/dev/nvidia1048575": 1048575} {
		got, ok := gpuMinor(path)
		require.True(t, ok, path)
		require.Equal(t, want, got, path)
	}
	for _, path := range []string{
		"/dev/nvidiactl", "/dev/nvidia-uvm", "/dev/nvidia", "/dev/nvidia01",
		"/dev/nvidia+1", "/dev/nvidia-1", "/dev/nvidia0x", "/dev/fuse",
		"/dev/nvidia1048576", "/dev/nvidia4294967295",
	} {
		_, ok := gpuMinor(path)
		require.False(t, ok, path)
	}
}
