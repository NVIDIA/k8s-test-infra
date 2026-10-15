// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/nri/pkg/api"
	"github.com/stretchr/testify/require"
)

func guestPlugin(t *testing.T, src bundleSource) (*Plugin, string) {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Inject.HostOverlayPath = src.OverlayRoot
	cfg.GuestProgram = src.GuestProgram
	cfg.Inject.GuestHostPath = t.TempDir()
	plugin := NewPlugin(cfg)
	plugin.guestOwner = selfOwner()

	return plugin, cfg.Inject.GuestHostPath
}

func launcherPod() *api.PodSandbox {
	return &api.PodSandbox{
		Name:        "virt-launcher-gpu-vm-abcde",
		Namespace:   "default",
		Annotations: map[string]string{"kubevirt.io/domain": "gpu-vm"},
	}
}

const bundleVolume = "/var/run/kubevirt-private/vmi-disks/nvml-mock/"

// computeContainer is the launcher's compute container as containerd hands it
// to NRI once the device plugin's CDI device is resolved, with the hostPath of
// the VM's bundle volume mounted.
func computeContainer(id string) *api.Container {
	return &api.Container{
		Id:   id,
		Name: "compute",
		Mounts: []*api.Mount{{
			Source: "/run/nvml-mock-guest", Destination: bundleVolume, Type: "bind", Options: []string{"rbind", "rw"},
		}},
		Linux: &api.LinuxContainer{
			Devices: []*api.LinuxDevice{{Path: "/dev/nvidia1", Type: "c", Major: 195, Minor: 1}},
			Resources: &api.LinuxResources{Devices: []*api.LinuxDeviceCgroup{{
				Allow: true, Type: "c", Major: api.Int64(195), Minor: api.Int64(1), Access: "rwm",
			}}},
		},
		CDIDevices: []*api.CDIDevice{{Name: "k8s.device-plugin.nvidia.com/gpu=GPU-b300b300-0000-0000-0000-000000000001"}},
	}
}

func TestCreateContainerMountsAWrittenGuestBundle(t *testing.T) {
	t.Parallel()

	plugin, guests := guestPlugin(t, stageSource(t))
	adjustment, _, err := plugin.CreateContainer(t.Context(), launcherPod(), computeContainer("c0ffee"))
	require.NoError(t, err)
	require.NotNil(t, adjustment)

	mounts := adjustment.GetMounts()
	require.Len(t, mounts, 1)
	require.Equal(t, filepath.Join(guests, "c0ffee"), mounts[0].GetSource())
	require.Equal(t, bundleVolume, mounts[0].GetDestination())
	require.Contains(t, mounts[0].GetOptions(), "ro")
	require.Empty(t, adjustment.GetEnv())
	require.Contains(t, readBundle(t, filepath.Join(guests, "c0ffee", BundleFile)), "dev/nvidia1")
}

// A mount whose source is missing fails creation of the container itself.
// Leaving the launcher unmodified lets QEMU report the missing disk instead.
func TestCreateContainerLeavesTheLauncherAloneWhenTheBundleCannotBeWritten(t *testing.T) {
	t.Parallel()

	plugin, _ := guestPlugin(t, bundleSource{OverlayRoot: t.TempDir(), GuestProgram: stageSource(t).GuestProgram})
	adjustment, _, err := plugin.CreateContainer(t.Context(), launcherPod(), computeContainer("c0ffee"))
	require.NoError(t, err)
	require.Nil(t, adjustment)
}

func TestRemoveContainerDeletesItsBundle(t *testing.T) {
	t.Parallel()

	plugin, guests := guestPlugin(t, stageSource(t))
	_, _, err := plugin.CreateContainer(t.Context(), launcherPod(), computeContainer("c0ffee"))
	require.NoError(t, err)

	require.NoError(t, plugin.RemoveContainer(t.Context(), launcherPod(), computeContainer("c0ffee")))
	require.NoDirExists(t, filepath.Join(guests, "c0ffee"))
}

func TestSynchronizeSweepsBundlesOfVanishedContainers(t *testing.T) {
	t.Parallel()

	plugin, guests := guestPlugin(t, stageSource(t))
	gone, live := containerID('a'), containerID('b')
	for _, id := range []string{gone, live} {
		_, _, err := plugin.CreateContainer(t.Context(), launcherPod(), computeContainer(id))
		require.NoError(t, err)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(guests, "foreign"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(guests, "foreign.sock"), nil, 0o644))

	updates, err := plugin.Synchronize(t.Context(), nil, []*api.Container{{Id: live}})
	require.NoError(t, err)
	require.Empty(t, updates)
	require.NoDirExists(t, filepath.Join(guests, gone))
	require.DirExists(t, filepath.Join(guests, live))
	require.DirExists(t, filepath.Join(guests, "foreign"))
	require.FileExists(t, filepath.Join(guests, "foreign.sock"))
}

func TestConfigureSubscribesToContainerRemoval(t *testing.T) {
	t.Parallel()

	mask, err := NewPlugin(DefaultConfig()).Configure(t.Context(), "", "containerd", "v2.2.0")
	require.NoError(t, err)
	require.True(t, mask.IsSet(api.Event_CREATE_CONTAINER))
	require.True(t, mask.IsSet(api.Event_REMOVE_CONTAINER))
}
