// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// stageDeviceNodes writes named entries into a fresh device root.
func stageDeviceNodes(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte{}, 0o644))
	}
	return root
}

// stageCDISpec writes a spec at the path the CDI mode checks before committing
// to a device reference.
func stageCDISpec(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nvml-mock-nri.yaml")
	require.NoError(t, os.WriteFile(path, []byte("cdiVersion: \"0.6.0\"\n"), 0o644))
	return path
}

func deviceOptIn() Container {
	return Container{
		Namespace:      "default",
		PodAnnotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
	}
}

func TestAdjustDeviceOptInAddsNvidiaDeviceEntries(t *testing.T) {
	t.Parallel()

	deviceRoot := stageDeviceNodes(t, "nvidia0", "nvidia1", "nvidiactl", "nvidia-uvm", "not-nvidia")

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot

	adjustment, ok := Adjust(cfg, deviceOptIn())
	require.True(t, ok)

	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
		{HostPath: filepath.Join(deviceRoot, "nvidia1"), Path: "/dev/nvidia1"},
		{HostPath: filepath.Join(deviceRoot, "nvidiactl"), Path: "/dev/nvidiactl"},
		{HostPath: filepath.Join(deviceRoot, "nvidia-uvm"), Path: "/dev/nvidia-uvm"},
	}, adjustment.Devices)
}

func TestAdjustDeviceOptInFailsOpenWhenTreeMissing(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.DeviceHostPath = filepath.Join(t.TempDir(), "does-not-exist")

	// Degrade to overlay-only injection: no devices, but the overlay mount is
	// still applied so the pod isn't blocked from starting.
	adjustment, ok := Adjust(cfg, deviceOptIn())
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Contains(t, adjustment.Mounts, overlayMount())
}

// TestAdjustSuppressesDeviceInjectionWhenDevicePluginServedContainer pins the
// composition rule from MEP-0002. The device plugin delivers exactly the GPU the
// scheduler allocated; blanket-injecting every /dev/nvidiaN on top of that makes
// the mock engine's detectVisibleDevices filter see a full set, return nil, and
// expose every GPU to a pod that was allocated one.
func TestAdjustSuppressesDeviceInjectionWhenDevicePluginServedContainer(t *testing.T) {
	t.Parallel()

	deviceRoot := stageDeviceNodes(t, "nvidia0", "nvidia1", "nvidiactl")

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
			t.Parallel()

			container := test.container
			container.Namespace = "default"
			container.PodAnnotations = map[string]string{"nvml-mock.nvidia.com/devices": "true"}

			adjustment, ok := Adjust(cfg, container)
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
			require.Contains(t, adjustment.Mounts, overlayMount())
			require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
		})
	}
}

// TestAdjustDeviceOptInWarnsWhenTreeIsEmpty covers the one device-injection
// outcome that used to be silent. A device root that does NOT exist makes
// os.ReadDir fail, and the "device tree is unavailable" warning already fires
// for that. A device root that exists and holds no nvidia* entries returns
// ([], nil) instead, so the container started with the overlay mounted, no
// device nodes, and nothing said so at any log level — the mock engine then
// derives an empty visible-GPU set and the pod reports zero GPUs as if that
// were the configured state.
//
// The empty and prefix-mismatch arms assert the warning; the populated arm is
// the control that keeps the assertion honest, since a warning emitted
// unconditionally would satisfy the first two on its own.
func TestAdjustDeviceOptInWarnsWhenTreeIsEmpty(t *testing.T) {
	tests := map[string]struct {
		entries     []string
		wantDevices int
		wantWarning bool
	}{
		"empty device root":        {entries: nil, wantDevices: 0, wantWarning: true},
		"no entry matches nvidia*": {entries: []string{"kfd", "dri"}, wantDevices: 0, wantWarning: true},
		"populated device root":    {entries: []string{"nvidia0", "nvidiactl"}, wantDevices: 2, wantWarning: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			deviceRoot := stageDeviceNodes(t, test.entries...)
			warnings := captureWarnings(t)

			cfg := DefaultConfig()
			cfg.DeviceHostPath = deviceRoot

			// Fail open either way: the overlay mount still lands and the pod
			// is never blocked from starting. Only the diagnostic changes.
			adjustment, ok := Adjust(cfg, deviceOptIn())
			require.True(t, ok)
			require.Len(t, adjustment.Devices, test.wantDevices)
			require.Contains(t, adjustment.Mounts, overlayMount())

			captured := warnings.captured()
			if !test.wantWarning {
				require.Empty(t, captured)
				return
			}
			require.Len(t, captured, 1)
			require.Equal(t,
				"device injection requested but the device tree holds no device nodes; "+
					"injecting overlay only (has the node agent staged this node?)",
				captured[0].Message)
			require.Equal(t, deviceRoot, captured[0].Attrs["path"])
		})
	}
}

// TestAdjustCDIModeEmitsCDIReferenceInsteadOfRawNodes pins the #436 behaviour:
// in CDI mode the annotation-gated path hands the runtime a CDI device
// reference and stops staging raw device nodes itself. Both halves matter — a
// version that emitted the reference AND the raw nodes would double-serve the
// container and defeat the mock engine's detectVisibleDevices filter, which is
// MEP-0002's most important constraint because it fails green.
func TestAdjustCDIModeEmitsCDIReferenceInsteadOfRawNodes(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.DeviceHostPath = stageDeviceNodes(t, "nvidia0", "nvidia1")
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = stageCDISpec(t)

	adjustment, ok := Adjust(cfg, deviceOptIn())
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
	t.Parallel()

	deviceRoot := stageDeviceNodes(t, "nvidia0", "nvidia1")

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = filepath.Join(t.TempDir(), "absent.yaml")

	adjustment, ok := Adjust(cfg, deviceOptIn())
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
	t.Parallel()

	deviceRoot := stageDeviceNodes(t, "nvidia0")

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.CDISpecHostPath = stageCDISpec(t)

	require.Equal(t, DeviceInjectionModeRaw, DefaultConfig().DeviceInjectionMode,
		"raw stays the default per MEP-0002; CDI is the opt-in path")

	adjustment, ok := Adjust(cfg, deviceOptIn())
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
	t.Parallel()

	cfg := DefaultConfig()
	cfg.DeviceHostPath = stageDeviceNodes(t, "nvidia0")
	cfg.DeviceInjectionMode = DeviceInjectionModeCDI
	cfg.CDISpecHostPath = stageCDISpec(t)

	tests := map[string]Container{
		"raw device node from the device plugin": {
			Devices: []Device{{HostPath: "/var/lib/nvml-mock/driver/dev/nvidia0", Path: "/dev/nvidia0"}},
		},
		"cdi device from the device plugin": {CDIDevices: []string{"nvidia.com/gpu=0"}},
	}

	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			container.Namespace = "default"
			container.PodAnnotations = map[string]string{"nvml-mock.nvidia.com/devices": "true"}

			adjustment, ok := Adjust(cfg, container)
			require.True(t, ok)

			require.Empty(t, adjustment.CDIDevices, "suppression must hold in CDI mode too")
			require.Empty(t, adjustment.Devices)
			require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
		})
	}
}
