// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// overlayMount is the driver tree delivered to an allocated or explicitly
// all-GPU container.
func overlayMount() Mount {
	return Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	}
}

// configMount is the writable window in the otherwise read-only overlay.
func configMount() Mount {
	return Mount{
		Source:      "/var/lib/nvml-mock/driver/config",
		Destination: "/opt/nvml-mock/driver/config",
		Type:        "bind",
		Options:     []string{"rbind", "rw", "nosuid", "nodev"},
	}
}

func TestAdjustLeavesAPlainContainerUntouched(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), Container{Namespace: "default"})
	require.False(t, ok)
	require.Empty(t, adjustment)
}

// `nvidia-smi --gpu-reset` clears the device's bucket from overrides.yaml, which
// sits beside the config.yaml MOCK_NVML_CONFIG points at. With the whole overlay
// read-only that write failed with EROFS on exactly the GPUs that had state to
// clear, so the config directory gets its own writable bind. It has to stay
// layered over the overlay: ordered first, the read-only rbind would cover it.
func TestAdjustMountsConfigDirWritableOverReadOnlyOverlay(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), deviceOptIn())
	require.True(t, ok)
	require.Contains(t, adjustment.Mounts, configMount())

	overlay, config := -1, -1
	for i, m := range adjustment.Mounts {
		switch m.Destination {
		case "/opt/nvml-mock":
			overlay = i
			require.Contains(t, m.Options, "ro", "the mock library and nvidia-smi stay immutable")
		case "/opt/nvml-mock/driver/config":
			config = i
		}
	}
	require.NotEqual(t, -1, overlay)
	require.NotEqual(t, -1, config)
	require.Less(t, overlay, config, "the writable config bind must be applied after the overlay it sits inside")
}

// An unannotated container gets the overlay and the environment but nothing
// else: both device paths are opt-in, so the default must stay empty.
func TestAdjustAllocatedGPUReceivesOverlayWithoutWideningGPUSet(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), Container{
		Namespace: "default",
		Devices:   []Device{{Path: "/dev/nvidia3"}},
	})
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Empty(t, adjustment.CDIDevices)
	require.NotEmpty(t, adjustment.Env)
	require.Contains(t, adjustment.Mounts, overlayMount())
}

func TestAdjustSkipsOptOutExcludedNamespaceAndExistingMount(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = []string{"kube-system", "nvml-mock"}

	tests := map[string]Container{
		"opt out annotation": {
			Namespace: "default",
			PodAnnotations: map[string]string{
				"nvml-mock.nvidia.com/inject": "false",
			},
		},
		"excluded namespace": {
			Namespace: "kube-system",
		},
		// A container already carrying the overlay has been through here
		// before; injecting twice would stack duplicate LD_PRELOAD entries.
		"existing overlay mount": {
			Namespace: "default",
			Mounts:    []Mount{{Destination: "/opt/nvml-mock"}},
		},
	}

	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adjustment, ok := Adjust(cfg, container)
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}
}

// An empty exclusion list means "exclude nothing", so it must not fall back to
// the packaged kube-system default.
func TestEmptyExclusionListExcludesNothing(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = nil

	_, ok := Adjust(cfg, Container{Namespace: "kube-system", Devices: []Device{{Path: "/dev/nvidia0"}}})
	require.True(t, ok)
}

func TestAdjustIMEXOnlyDoesNotExposeGPUOverlay(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = stageDeviceNodes(t, "channel0")

	adjustment, ok := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{cfg.ImexChannelAnnotation: "true"},
	})
	require.True(t, ok)
	require.Empty(t, adjustment.Mounts)
	require.Empty(t, adjustment.Env)
	require.Empty(t, adjustment.CDIDevices)
	require.Len(t, adjustment.Devices, 1)
}

func TestAdjustRecognizesAllocatedInfiniBandDeviceWithoutExposingGPUs(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), Container{
		Namespace: "default",
		Devices:   []Device{{Path: "/dev/infiniband/uverbs0"}},
	})
	require.True(t, ok)
	require.Contains(t, adjustment.Mounts, overlayMount())
	require.Contains(t, adjustment.Env, "MOCK_IB=full")
	require.Contains(t, adjustment.Env, "MOCK_NVML_VISIBLE_DEVICES=none")
	require.Empty(t, adjustment.Devices)
}

func TestAdjustDoesNotMistakeControlNodeForGPUAllocation(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), Container{
		Namespace: "default",
		Devices:   []Device{{Path: "/dev/nvidiactl"}},
	})
	require.False(t, ok)
	require.Empty(t, adjustment)
}
