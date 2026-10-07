// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// surfaces is what a container observably received, reduced to the parts the
// GPU, InfiniBand and IMEX selections each control.
type surfaces struct {
	overlay bool
	// config reports whether MOCK_NVML_CONFIG was injected, which is what
	// turns the mock GPUs on.
	config bool
	// visible is the injected MOCK_NVML_VISIBLE_DEVICES value, empty when none
	// was injected. It is kept apart from config because "none" hides every
	// mock GPU, and a container that gets both must show both.
	visible string
	// ib is the injected MOCK_IB value, empty when none was injected.
	ib       string
	gpus     int
	channels int
}

func observe(adjustment Adjustment) surfaces {
	env := make(map[string]string, len(adjustment.Env))
	for _, item := range adjustment.Env {
		key, value, _ := strings.Cut(item, "=")
		env[key] = value
	}
	got := surfaces{
		overlay: slices.ContainsFunc(adjustment.Mounts, func(m Mount) bool {
			return m.Destination == overlayMount().Destination
		}),
		config:  env["MOCK_NVML_CONFIG"] != "",
		visible: env["MOCK_NVML_VISIBLE_DEVICES"],
		ib:      env["MOCK_IB"],
	}
	for _, device := range adjustment.Devices {
		if filepath.Dir(device.Path) == imexChannelContainerDir {
			got.channels++
		} else {
			got.gpus++
		}
	}
	return got
}

// stageHCA renders the smallest IB tree the plugin recognises as staged HCAs
// and returns the overlay root it lives under.
func stageHCA(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ibRelPath, "sys/class/infiniband/mlx5_0"), 0o755))
	return root
}

// Each selection adds only its own surface, whatever the others chose. GPU and
// InfiniBand share the overlay, so each switches the other's surface off
// explicitly; IMEX needs nothing but its channel nodes.
func TestAdjustComposesIndependentSelections(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HostOverlayPath = stageHCA(t)
	cfg.DeviceHostPath = stageDeviceNodes(t, "nvidia0", "nvidia1")
	cfg.ImexChannelHostPath = stageDeviceNodes(t, "channel0")

	tests := []struct {
		gpu, ib, imex bool
		want          surfaces
	}{
		{want: surfaces{}},
		{gpu: true, want: surfaces{overlay: true, config: true, ib: "off", gpus: 2}},
		{ib: true, want: surfaces{overlay: true, visible: "none", ib: "full"}},
		{imex: true, want: surfaces{channels: 1}},
		{gpu: true, ib: true, want: surfaces{overlay: true, config: true, ib: "full", gpus: 2}},
		{gpu: true, imex: true, want: surfaces{overlay: true, config: true, ib: "off", gpus: 2, channels: 1}},
		{ib: true, imex: true, want: surfaces{overlay: true, visible: "none", ib: "full", channels: 1}},
		{gpu: true, ib: true, imex: true, want: surfaces{overlay: true, config: true, ib: "full", gpus: 2, channels: 1}},
	}
	for _, test := range tests {
		annotations := map[string]string{}
		var name []string
		for annotation, selected := range map[string]bool{
			cfg.DeviceAnnotation:      test.gpu,
			cfg.InfiniBandAnnotation:  test.ib,
			cfg.ImexChannelAnnotation: test.imex,
		} {
			if selected {
				annotations[annotation] = "true"
				name = append(name, filepath.Base(annotation))
			}
		}
		slices.Sort(name)
		if len(name) == 0 {
			name = []string{"none"}
		}
		t.Run("selected="+strings.Join(name, "+"), func(t *testing.T) {
			t.Parallel()

			adjustment, ok := Adjust(cfg, Container{Namespace: "default", PodAnnotations: annotations})
			require.Equal(t, test.gpu || test.ib || test.imex, ok)
			require.Equal(t, test.want, observe(adjustment))
			if !test.gpu && !test.ib {
				require.Empty(t, adjustment.Mounts, "IMEX alone needs no overlay")
				require.Empty(t, adjustment.Env, "IMEX alone needs no environment")
			}
		})
	}
}

// A privileged container inherits every host device under a wildcard rule. None
// of that is a request, so it selects nothing. An exact rule for an IB device is
// not taken as an RDMA allocation either: no allocation signal has been
// validated against a real RDMA device plugin or DRA driver yet.
func TestAdjustDoesNotSelectFromInheritedDevices(t *testing.T) {
	t.Parallel()

	device := func(path string, major, minor int64) RuntimeDevice {
		return RuntimeDevice{Path: path, Type: "c", Major: major, Minor: minor}
	}
	inherited := []RuntimeDevice{
		device("/dev/nvidia0", 195, 0),
		device("/dev/nvidiactl", 195, 255),
		device("/dev/infiniband/uverbs0", 231, 192),
		device("/dev/infiniband/umad0", 231, 0),
		device("/dev/infiniband/rdma_cm", 10, 58),
	}
	wildcard := []DeviceRule{{Allow: true, Type: "a", Access: "rwm"}}

	tests := map[string]Container{
		"inherited GPU and IB nodes": {IncomingDevices: inherited},
		"inherited nodes with a wildcard rule": {
			IncomingDevices: inherited,
			DeviceRules:     wildcard,
		},
		"IB node with an exact rule": {
			IncomingDevices: []RuntimeDevice{device("/dev/infiniband/uverbs0", 231, 192)},
			DeviceRules:     []DeviceRule{{Allow: true, Type: "c", Major: new(int64(231)), Minor: new(int64(192)), Access: "rwm"}},
		},
	}
	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			container.Namespace = "default"
			adjustment, ok := Adjust(DefaultConfig(), container)
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}

	// The positive control: the same inheritance with the devices annotation is
	// selected for GPUs only. Inherited IB nodes must neither switch the fabric
	// on nor hide the GPUs behind MOCK_NVML_VISIBLE_DEVICES=none, which is how
	// privileged pods on RDMA hosts once lost every GPU.
	t.Run("inherited nodes with the devices annotation", func(t *testing.T) {
		t.Parallel()

		cfg := DefaultConfig()
		cfg.HostOverlayPath = stageHCA(t)
		cfg.DeviceHostPath = stageDeviceNodes(t, "nvidia0", "nvidia1")
		adjustment, ok := Adjust(cfg, Container{
			Namespace:       "default",
			PodAnnotations:  map[string]string{cfg.DeviceAnnotation: "true"},
			IncomingDevices: inherited,
			DeviceRules:     wildcard,
		})
		require.True(t, ok)
		require.Equal(t, surfaces{overlay: true, config: true, ib: "off", gpus: 2}, observe(adjustment))
	})
}

// The InfiniBand opt-in adds the fabric without widening a GPU allocation.
func TestAdjustKeepsAnAllocationWhenInfiniBandIsSelected(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HostOverlayPath = stageHCA(t)
	cfg.DeviceHostPath = stageDeviceNodes(t, "nvidia0", "nvidia1")

	adjustment, ok := Adjust(cfg, Container{
		Namespace:       "default",
		PodAnnotations:  map[string]string{cfg.InfiniBandAnnotation: "true"},
		IncomingDevices: []RuntimeDevice{{Path: "/dev/nvidia1", Type: "c", Major: 195, Minor: 1}},
		DeviceRules:     []DeviceRule{{Allow: true, Type: "c", Major: new(int64(195)), Minor: new(int64(1)), Access: "rwm"}},
	})
	require.True(t, ok)
	require.Equal(t, surfaces{overlay: true, config: true, ib: "full"}, observe(adjustment),
		"the allocated GPU stays the only one; the engine filters on it")
	require.Empty(t, adjustment.CDIDevices)
}

// Mokka's values for a selected surface are defaults the workload may override.
// The switches that keep an unselected surface off are not: the annotations
// decide what a container gets, not an environment baked into its image.
func TestAdjustEnvironmentOverridePolicy(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HostOverlayPath = stageHCA(t)
	gpu := map[string]string{cfg.DeviceAnnotation: "true"}
	ib := map[string]string{cfg.InfiniBandAnnotation: "true"}

	tests := map[string]struct {
		annotations map[string]string
		env         string
		want        string
	}{
		"GPU only turns an authored fabric off":    {gpu, "MOCK_IB=full", "MOCK_IB=off"},
		"IB only hides GPUs an image made visible": {ib, "MOCK_NVML_VISIBLE_DEVICES=all", "MOCK_NVML_VISIBLE_DEVICES=none"},
		"a selected fabric keeps an authored tier": {ib, "MOCK_IB=sysfs", ""},
		"a selected fabric keeps an authored root": {ib, "MOCK_IB_ROOT=/custom/ib", ""},
		"selected GPUs keep an authored config":    {gpu, "MOCK_NVML_CONFIG=/custom/config.yaml", ""},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adjustment, ok := Adjust(cfg, Container{
				Namespace:      "default",
				PodAnnotations: test.annotations,
				Env:            []string{test.env},
			})
			require.True(t, ok)
			key, _, _ := strings.Cut(test.env, "=")
			if test.want == "" {
				requireNoEnvKey(t, adjustment.Env, key)
				return
			}
			require.Contains(t, adjustment.Env, test.want)
		})
	}
}

// A node whose profile simulates no InfiniBand stages an empty IB tree. The
// opt-in still injects, so the pod starts and its tools find no HCA, but the
// plugin says why.
func TestAdjustWarnsWhenInfiniBandIsSelectedWithoutStagedHCAs(t *testing.T) {
	warnings := captureWarnings(t)

	cfg := DefaultConfig()
	cfg.HostOverlayPath = t.TempDir()

	adjustment, ok := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{cfg.InfiniBandAnnotation: "true"},
	})
	require.True(t, ok)
	require.Equal(t, surfaces{overlay: true, visible: "none", ib: "full"}, observe(adjustment))
	require.Len(t, warnings.captured(), 1)
	require.Contains(t, warnings.captured()[0].Message, "no HCA is staged")
}
