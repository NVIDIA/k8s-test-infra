// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAdjustImexChannelOptInAddsChannelDevices covers #437: an annotated pod
// gets the channel nodes staged by imex.mockChannels at their real kernel path.
func TestAdjustImexChannelOptInAddsChannelDevices(t *testing.T) {
	t.Parallel()

	channelRoot := stageDeviceNodes(t, "channel0", "channel1", "channel2", "not-a-channel")
	require.NoError(t, os.Mkdir(filepath.Join(channelRoot, "channel-subdir"), 0o755))

	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = channelRoot

	adjustment, ok := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/imex-channels": "true"},
	})
	require.True(t, ok)

	require.Equal(t, []Device{
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
		{HostPath: filepath.Join(channelRoot, "channel1"), Path: "/dev/nvidia-caps-imex-channels/channel1"},
		{HostPath: filepath.Join(channelRoot, "channel2"), Path: "/dev/nvidia-caps-imex-channels/channel2"},
	}, adjustment.Devices)
}

// TestAdjustWithoutImexAnnotationInjectsNoChannels proves the opt-in gate is a
// real gate: a fully staged channel tree must stay out of an unannotated pod.
func TestAdjustWithoutImexAnnotationInjectsNoChannels(t *testing.T) {
	t.Parallel()

	channelRoot := stageDeviceNodes(t, "channel0")

	tests := map[string]map[string]string{
		"no annotations at all":  nil,
		"opt-in set to false":    {"nvml-mock.nvidia.com/imex-channels": "false"},
		"only the device opt-in": {"nvml-mock.nvidia.com/devices": "true"},
	}

	for name, annotations := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.ImexChannelHostPath = channelRoot
			cfg.DeviceHostPath = t.TempDir()

			adjustment, ok := Adjust(cfg, Container{Namespace: "default", PodAnnotations: annotations})
			require.True(t, ok)
			require.Empty(t, adjustment.Devices)
		})
	}
}

// TestAdjustImexChannelOptInFailsOpenWhenTreeMissing mirrors the device path:
// imex.mockChannels is off by default, so an annotation on a node that never
// staged channels must degrade to overlay-only rather than block the pod.
func TestAdjustImexChannelOptInFailsOpenWhenTreeMissing(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = filepath.Join(t.TempDir(), "does-not-exist")

	adjustment, ok := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/imex-channels": "true"},
	})
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Contains(t, adjustment.Mounts, overlayMount())
}

// TestAdjustImexChannelsSurviveDevicePluginAllocation pins the composition rule
// against MEP-0002 (#548). The device plugin allocates GPUs; it has no concept
// of an IMEX channel and never delivers one. Suppressing channels because a GPU
// was allocated would deny a ComputeDomain workload the fabric it asked for, so
// the GPU suppression must NOT extend to channels.
func TestAdjustImexChannelsSurviveDevicePluginAllocation(t *testing.T) {
	t.Parallel()

	deviceRoot := stageDeviceNodes(t, "nvidia0")
	channelRoot := stageDeviceNodes(t, "channel0")

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.ImexChannelHostPath = channelRoot

	adjustment, ok := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices":       "true",
			"nvml-mock.nvidia.com/imex-channels": "true",
		},
		// The kubelet already applied the device plugin's Allocate response.
		Devices: []Device{{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"}},
	})
	require.True(t, ok)

	// The GPU tree is suppressed (MEP-0002) but the channel stays.
	require.Equal(t, []Device{
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
	}, adjustment.Devices)
}

// TestAdjustCDIModeStillDeliversImexChannelsAsRawDevices pins the interaction
// between the two opt-ins. IMEX channels are deliberately outside the CDI spec:
// they are a fabric capability with their own annotation, staged by
// imex.mockChannels rather than by the GPU device tree. So a container that asks
// for both gets its GPUs through CDI and its channels as raw device nodes, in
// one adjustment. Losing the channels here would deny a ComputeDomain workload
// the fabric it explicitly requested.
func TestAdjustCDIModeStillDeliversImexChannelsAsRawDevices(t *testing.T) {
	t.Parallel()

	channelRoot := stageDeviceNodes(t, "channel0")

	cfg := DefaultConfig()
	cfg.DeviceHostPath = stageDeviceNodes(t, "nvidia0")
	cfg.ImexChannelHostPath = channelRoot
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = stageCDISpec(t)

	adjustment, ok := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices":       "true",
			"nvml-mock.nvidia.com/imex-channels": "true",
		},
	})
	require.True(t, ok)

	require.Equal(t, []string{"nvml-mock.nvidia.com/gpu=all"}, adjustment.CDIDevices)
	require.Equal(t, []Device{
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
	}, adjustment.Devices,
		"channels ride the raw device list even in cdi mode; the GPU tree must not reappear alongside them")
}
