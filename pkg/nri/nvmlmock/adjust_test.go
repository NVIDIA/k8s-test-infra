// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvmlmock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdjustPlainContainerAddsOverlayAndEnvironment(t *testing.T) {
	cfg := DefaultConfig()
	container := Container{
		Namespace: "default",
		Env: []string{
			"PATH=/usr/local/bin:/usr/bin",
			"LD_LIBRARY_PATH=/app/lib",
			"LD_PRELOAD=/app/libexisting.so",
			"MOCK_IB=off",
		},
	}

	adjustment, ok, err := Adjust(cfg, container)
	require.NoError(t, err)
	require.True(t, ok)

	require.Contains(t, adjustment.Mounts, Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin:/usr/local/bin:/usr/bin")
	require.Contains(t, adjustment.Env, "LD_LIBRARY_PATH=/opt/nvml-mock/driver/usr/lib64:/app/lib")
	require.Contains(t, adjustment.Env, "LD_PRELOAD=/app/libexisting.so:/opt/nvml-mock/driver/usr/local/lib/libibmockumad.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmocksys.so.1:/opt/nvml-mock/driver/usr/local/lib/libpcimocksys.so.1")
	require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
	require.Contains(t, adjustment.Env, "MOCK_IB_ROOT=/opt/nvml-mock/ib")
	require.Contains(t, adjustment.Env, "MOCK_IB_PING_SOCKET=/opt/nvml-mock/run/mock-ib.sock")
	require.Contains(t, adjustment.Env, "MOCK_PCI_ROOT=/opt/nvml-mock")
	// MOCK_IB=off is authored by the container and left unchanged (setDefaultEnv
	// is a no-op), so the plugin must NOT re-emit it — emitting untouched vars
	// would claim NRI ownership and conflict with other plugins.
	requireNoEnvKey(t, adjustment.Env, "MOCK_IB")
}

func TestAdjustEmitsOnlyAddedOrChangedEnv(t *testing.T) {
	container := Container{
		Namespace: "default",
		Env: []string{
			"FOO=bar",       // untouched -> must not be emitted
			"MOCK_IB=off",   // authored default -> unchanged -> not emitted
			"PATH=/usr/bin", // prepended -> changed -> emitted
		},
	}

	adjustment, ok, err := Adjust(DefaultConfig(), container)
	require.NoError(t, err)
	require.True(t, ok)

	requireNoEnvKey(t, adjustment.Env, "FOO")
	requireNoEnvKey(t, adjustment.Env, "MOCK_IB")
	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin:/usr/bin")
}

func TestAdjustInjectsTopologyEnvWhenStaged(t *testing.T) {
	overlayHost := t.TempDir()
	topoDir := filepath.Join(overlayHost, "topology")
	require.NoError(t, os.MkdirAll(topoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topoDir, "topology.yaml"), []byte("version: 1\n"), 0o644))

	cfg := DefaultConfig()
	cfg.HostOverlayPath = overlayHost
	cfg.NodeName = "kind-worker3"

	adjustment, ok, err := Adjust(cfg, Container{Namespace: "default"})
	require.NoError(t, err)
	require.True(t, ok)

	require.Contains(t, adjustment.Env, "NODE_NAME=kind-worker3")
	require.Contains(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG=/opt/nvml-mock/topology/topology.yaml")
}

func TestAdjustSkipsTopologyEnvWhenNotConfigured(t *testing.T) {
	t.Run("no node name", func(t *testing.T) {
		overlayHost := t.TempDir()
		topoDir := filepath.Join(overlayHost, "topology")
		require.NoError(t, os.MkdirAll(topoDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(topoDir, "topology.yaml"), []byte("version: 1\n"), 0o644))

		cfg := DefaultConfig()
		cfg.HostOverlayPath = overlayHost

		adjustment, ok, err := Adjust(cfg, Container{Namespace: "default"})
		require.NoError(t, err)
		require.True(t, ok)
		requireNoEnvKey(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG")
		requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
	})

	t.Run("no staged topology document", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.HostOverlayPath = t.TempDir()
		cfg.NodeName = "kind-worker3"

		adjustment, ok, err := Adjust(cfg, Container{Namespace: "default"})
		require.NoError(t, err)
		require.True(t, ok)
		requireNoEnvKey(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG")
		requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
	})
}

func TestAdjustDoesNotOverrideAuthoredNodeName(t *testing.T) {
	overlayHost := t.TempDir()
	topoDir := filepath.Join(overlayHost, "topology")
	require.NoError(t, os.MkdirAll(topoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topoDir, "topology.yaml"), []byte("version: 1\n"), 0o644))

	cfg := DefaultConfig()
	cfg.HostOverlayPath = overlayHost
	cfg.NodeName = "kind-worker3"

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		Env:       []string{"NODE_NAME=authored-node"},
	})
	require.NoError(t, err)
	require.True(t, ok)

	// The container authored NODE_NAME, so the plugin must neither override it
	// nor re-emit it (which would claim ownership); the authored value simply
	// stays on the container. It must NOT be replaced with the plugin's node.
	require.NotContains(t, adjustment.Env, "NODE_NAME=kind-worker3")
	requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
	require.Contains(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG=/opt/nvml-mock/topology/topology.yaml")
}

func requireNoEnvKey(t *testing.T, env []string, key string) {
	t.Helper()
	for _, item := range env {
		if name, _, ok := strings.Cut(item, "="); ok && name == key {
			t.Fatalf("expected env not to contain key %q, got %q", key, item)
		}
	}
}

func TestAdjustDeviceOptInFailsOpenWhenTreeMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DeviceHostPath = filepath.Join(t.TempDir(), "does-not-exist")

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices": "true",
		},
	})
	// Degrade to overlay-only injection: no error, no devices, but the overlay
	// mount is still applied so the pod isn't blocked from starting.
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Contains(t, adjustment.Mounts, Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
}

// TestAdjustSuppressesDeviceInjectionWhenDevicePluginServedContainer pins the
// composition rule from MEP-0002. The device plugin delivers exactly the GPU the
// scheduler allocated; blanket-injecting every /dev/nvidiaN on top of that makes
// the mock engine's detectVisibleDevices filter see a full set, return nil, and
// expose every GPU to a pod that was allocated one.
func TestAdjustSuppressesDeviceInjectionWhenDevicePluginServedContainer(t *testing.T) {
	deviceRoot := t.TempDir()
	for _, name := range []string{"nvidia0", "nvidia1", "nvidiactl"} {
		require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, name), []byte{}, 0o644))
	}

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot

	tests := map[string]struct {
		container       Container
		wantSuppression bool
	}{
		"device plugin already supplied a gpu device node": {
			container: Container{
				Devices: []Device{{HostPath: "/var/lib/nvml-mock/driver/dev/nvidia0", Path: "/dev/nvidia0"}},
			},
			wantSuppression: true,
		},
		"device plugin already supplied an nvidia cdi device": {
			container:       Container{CDIDevices: []string{"nvidia.com/gpu=0"}},
			wantSuppression: true,
		},
		// Discrimination: a container carrying an unrelated device must still get
		// the full opt-in injection, otherwise the guard is a constant.
		"unrelated device node does not suppress": {
			container: Container{
				Devices: []Device{{HostPath: "/dev/fuse", Path: "/dev/fuse"}},
			},
			wantSuppression: false,
		},
		"unrelated cdi vendor does not suppress": {
			container:       Container{CDIDevices: []string{"example.com/widget=0"}},
			wantSuppression: false,
		},
		"no devices at all keeps the historical opt-in behaviour": {
			container:       Container{},
			wantSuppression: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			container := test.container
			container.Namespace = "default"
			container.PodAnnotations = map[string]string{"nvml-mock.nvidia.com/devices": "true"}

			adjustment, ok, err := Adjust(cfg, container)
			require.NoError(t, err)
			require.True(t, ok, "the container must still be adjusted; only the device list is suppressed")

			if test.wantSuppression {
				require.Empty(t, adjustment.Devices,
					"device plugin already served this container, so the plugin must not add device nodes")
			} else {
				require.ElementsMatch(t, []Device{
					{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
					{HostPath: filepath.Join(deviceRoot, "nvidia1"), Path: "/dev/nvidia1"},
					{HostPath: filepath.Join(deviceRoot, "nvidiactl"), Path: "/dev/nvidiactl"},
				}, adjustment.Devices)
			}

			// Suppression is scoped to devices: the overlay and env still arrive,
			// because the device plugin delivers neither.
			require.Contains(t, adjustment.Mounts, Mount{
				Source:      "/var/lib/nvml-mock",
				Destination: "/opt/nvml-mock",
				Type:        "bind",
				Options:     []string{"rbind", "ro", "nosuid", "nodev"},
			})
			require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
		})
	}
}

func TestAdjustSkipsOptOutExcludedNamespaceAndExistingMount(t *testing.T) {
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
		"existing overlay mount": {
			Namespace: "default",
			Mounts: []Mount{
				{Destination: "/opt/nvml-mock"},
			},
		},
	}

	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			adjustment, ok, err := Adjust(cfg, container)
			require.NoError(t, err)
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}
}

func TestAdjustPrependsDefaultsWhenEnvIsUnset(t *testing.T) {
	adjustment, ok, err := Adjust(DefaultConfig(), Container{Namespace: "default"})
	require.NoError(t, err)
	require.True(t, ok)

	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin")
	require.Contains(t, adjustment.Env, "LD_LIBRARY_PATH=/opt/nvml-mock/driver/usr/lib64")
	require.Contains(t, adjustment.Env, "LD_PRELOAD=/opt/nvml-mock/driver/usr/local/lib/libibmockumad.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmocksys.so.1:/opt/nvml-mock/driver/usr/local/lib/libpcimocksys.so.1")
	require.Contains(t, adjustment.Env, "MOCK_IB=full")
}

func TestAdjustDeviceOptInAddsNvidiaDeviceEntries(t *testing.T) {
	deviceRoot := t.TempDir()
	for _, name := range []string{"nvidia0", "nvidia1", "nvidiactl", "nvidia-uvm", "not-nvidia"} {
		require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, name), []byte{}, 0o644))
	}

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices": "true",
		},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
		{HostPath: filepath.Join(deviceRoot, "nvidia1"), Path: "/dev/nvidia1"},
		{HostPath: filepath.Join(deviceRoot, "nvidiactl"), Path: "/dev/nvidiactl"},
		{HostPath: filepath.Join(deviceRoot, "nvidia-uvm"), Path: "/dev/nvidia-uvm"},
	}, adjustment.Devices)
}

// TestDiscoverDevicesSkipsChannelDirectory pins a defect that predates IMEX
// channel injection. imex.mockChannels (#541) mknods the channel nodes into a
// nvidia-caps-imex-channels DIRECTORY inside the same device root the plugin
// scans, and that directory name matches the "nvidia" prefix filter. Injecting
// a directory as a LinuxDevice is not a device node, so the runtime cannot
// create it in the container.
func TestDiscoverDevicesSkipsChannelDirectory(t *testing.T) {
	deviceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, "nvidia0"), []byte{}, 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(deviceRoot, "nvidia-caps-imex-channels"), 0o755))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.Equal(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
	}, adjustment.Devices)
}

// TestAdjustImexChannelOptInAddsChannelDevices covers #437: an annotated pod
// gets the channel nodes staged by imex.mockChannels at their real kernel path.
func TestAdjustImexChannelOptInAddsChannelDevices(t *testing.T) {
	channelRoot := t.TempDir()
	for _, name := range []string{"channel0", "channel1", "channel2", "not-a-channel"} {
		require.NoError(t, os.WriteFile(filepath.Join(channelRoot, name), []byte{}, 0o644))
	}
	require.NoError(t, os.Mkdir(filepath.Join(channelRoot, "channel-subdir"), 0o755))

	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = channelRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/imex-channels": "true"},
	})
	require.NoError(t, err)
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
	channelRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(channelRoot, "channel0"), []byte{}, 0o644))

	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = channelRoot

	for name, annotations := range map[string]map[string]string{
		"no annotations at all": nil,
		"opt-in set to false":   {"nvml-mock.nvidia.com/imex-channels": "false"},
		"only the device opt-in": {
			"nvml-mock.nvidia.com/devices": "true",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := cfg
			cfg.DeviceHostPath = t.TempDir()
			adjustment, ok, err := Adjust(cfg, Container{Namespace: "default", PodAnnotations: annotations})
			require.NoError(t, err)
			require.True(t, ok)
			require.Empty(t, adjustment.Devices)
		})
	}
}

// TestAdjustImexChannelOptInFailsOpenWhenTreeMissing mirrors the device path:
// imex.mockChannels is off by default, so an annotation on a node that never
// staged channels must degrade to overlay-only rather than block the pod.
func TestAdjustImexChannelOptInFailsOpenWhenTreeMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = filepath.Join(t.TempDir(), "does-not-exist")

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/imex-channels": "true"},
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Contains(t, adjustment.Mounts, Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
}

// TestAdjustImexChannelsSurviveDevicePluginAllocation pins the composition rule
// against MEP-0002 (#548). The device plugin allocates GPUs; it has no concept
// of an IMEX channel and never delivers one. Suppressing channels because a GPU
// was allocated would deny a ComputeDomain workload the fabric it asked for, so
// the GPU suppression must NOT extend to channels.
func TestAdjustImexChannelsSurviveDevicePluginAllocation(t *testing.T) {
	deviceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, "nvidia0"), []byte{}, 0o644))
	channelRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(channelRoot, "channel0"), []byte{}, 0o644))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.ImexChannelHostPath = channelRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices":       "true",
			"nvml-mock.nvidia.com/imex-channels": "true",
		},
		// The kubelet already applied the device plugin's Allocate response.
		Devices: []Device{{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"}},
	})
	require.NoError(t, err)
	require.True(t, ok)

	// The GPU tree is suppressed (MEP-0002) but the channel stays.
	require.Equal(t, []Device{
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
	}, adjustment.Devices)
}

// TestAdjustCDIModeEmitsCDIReferenceInsteadOfRawNodes pins the #436 behaviour:
// in CDI mode the annotation-gated path hands the runtime a CDI device
// reference and stops staging raw device nodes itself. Both halves matter — a
// version that emitted the reference AND the raw nodes would double-serve the
// container and defeat the mock engine's detectVisibleDevices filter, which is
// MEP-0002's most important constraint because it fails green.
func TestAdjustCDIModeEmitsCDIReferenceInsteadOfRawNodes(t *testing.T) {
	deviceRoot := t.TempDir()
	for _, name := range []string{"nvidia0", "nvidia1"} {
		require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, name), []byte{}, 0o644))
	}
	specPath := filepath.Join(t.TempDir(), "nvml-mock-nri.yaml")
	require.NoError(t, os.WriteFile(specPath, []byte("cdiVersion: \"0.6.0\"\n"), 0o644))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = specPath

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.Equal(t, []string{"nvml-mock.nvidia.com/gpu=all"}, adjustment.CDIDevices)
	require.Empty(t, adjustment.Devices,
		"CDI mode must not also stage raw device nodes; two sources for one container is exactly what MEP-0002 forbids")
}

// TestAdjustCDIModeFallsBackToRawWhenSpecMissing keeps the raw path reachable.
// MEP-0002 requires the fallback to survive, and a missing spec is a hard
// container-creation failure in containerd rather than a degraded start, so the
// plugin checks before it commits to the CDI reference.
func TestAdjustCDIModeFallsBackToRawWhenSpecMissing(t *testing.T) {
	deviceRoot := t.TempDir()
	for _, name := range []string{"nvidia0", "nvidia1"} {
		require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, name), []byte{}, 0o644))
	}

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = filepath.Join(t.TempDir(), "absent.yaml")

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.Empty(t, adjustment.CDIDevices, "no staged spec means no CDI reference the runtime could resolve")
	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
		{HostPath: filepath.Join(deviceRoot, "nvidia1"), Path: "/dev/nvidia1"},
	}, adjustment.Devices)
}

// TestAdjustRawModeEmitsNoCDIReference is the discriminating half of the pair
// above: the default mode must stay on raw nodes and never emit a CDI device,
// even when a spec is staged on the node.
func TestAdjustRawModeEmitsNoCDIReference(t *testing.T) {
	deviceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, "nvidia0"), []byte{}, 0o644))
	specPath := filepath.Join(t.TempDir(), "nvml-mock-nri.yaml")
	require.NoError(t, os.WriteFile(specPath, []byte("cdiVersion: \"0.6.0\"\n"), 0o644))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.CDISpecHostPath = specPath

	require.Equal(t, DeviceInjectionModeRaw, DefaultConfig().DeviceInjectionMode,
		"raw stays the default per MEP-0002; CDI is the opt-in path")

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.Empty(t, adjustment.CDIDevices)
	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
	}, adjustment.Devices)
}

// TestAdjustCDIModeStillSuppressesWhenDevicePluginServedContainer is the
// MEP-0002 "must not bypass the suppression rule" clause. The rule is about who
// already served the container, not about which mechanism serves it, so
// switching the mechanism to CDI must not reopen the hole.
func TestAdjustCDIModeStillSuppressesWhenDevicePluginServedContainer(t *testing.T) {
	deviceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, "nvidia0"), []byte{}, 0o644))
	specPath := filepath.Join(t.TempDir(), "nvml-mock-nri.yaml")
	require.NoError(t, os.WriteFile(specPath, []byte("cdiVersion: \"0.6.0\"\n"), 0o644))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = specPath

	tests := map[string]Container{
		"raw device node from the device plugin": {
			Devices: []Device{{HostPath: "/var/lib/nvml-mock/driver/dev/nvidia0", Path: "/dev/nvidia0"}},
		},
		"cdi device from the device plugin": {CDIDevices: []string{"nvidia.com/gpu=0"}},
	}

	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			container.Namespace = "default"
			container.PodAnnotations = map[string]string{"nvml-mock.nvidia.com/devices": "true"}

			adjustment, ok, err := Adjust(cfg, container)
			require.NoError(t, err)
			require.True(t, ok)

			require.Empty(t, adjustment.CDIDevices, "suppression must hold in CDI mode too")
			require.Empty(t, adjustment.Devices)
			require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
		})
	}
}

// TestAdjustCDIModeStillDeliversImexChannelsAsRawDevices pins the interaction
// between the two opt-ins. IMEX channels are deliberately outside the CDI spec:
// they are a fabric capability with their own annotation, staged by
// imex.mockChannels rather than by the GPU device tree. So a container that asks
// for both gets its GPUs through CDI and its channels as raw device nodes, in
// one adjustment. Losing the channels here would deny a ComputeDomain workload
// the fabric it explicitly requested.
func TestAdjustCDIModeStillDeliversImexChannelsAsRawDevices(t *testing.T) {
	deviceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, "nvidia0"), []byte{}, 0o644))
	channelRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(channelRoot, "channel0"), []byte{}, 0o644))
	specPath := filepath.Join(t.TempDir(), "nvml-mock-nri.yaml")
	require.NoError(t, os.WriteFile(specPath, []byte("cdiVersion: \"0.6.0\"\n"), 0o644))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.ImexChannelHostPath = channelRoot
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = specPath

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices":       "true",
			"nvml-mock.nvidia.com/imex-channels": "true",
		},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.Equal(t, []string{"nvml-mock.nvidia.com/gpu=all"}, adjustment.CDIDevices)
	require.Equal(t, []Device{
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
	}, adjustment.Devices,
		"channels ride the raw device list even in cdi mode; the GPU tree must not reappear alongside them")
}
