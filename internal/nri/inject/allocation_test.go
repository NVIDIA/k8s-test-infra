// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPUAllocationEvidence(t *testing.T) {
	t.Parallel()
	device := func(path string, major, minor int64) RuntimeDevice {
		return RuntimeDevice{Path: path, Type: "c", Major: major, Minor: minor}
	}
	grant := func(allow bool, major, minor *int64) DeviceRule {
		return DeviceRule{Allow: allow, Type: "c", Major: major, Minor: minor, Access: "rwm"}
	}
	tests := map[string]struct {
		container Container
		allocated bool
	}{
		"nothing":             {},
		"gpu zero exact":      {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0", 195, 0)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(195), int64Ptr(0))}}, true},
		"gpu seventeen exact": {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia17", 195, 17)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(195), int64Ptr(17))}}, true},
		"wrong minor":         {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0", 195, 0)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(195), int64Ptr(1))}}, false},
		"wrong major":         {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0", 195, 0)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(1), int64Ptr(0))}}, false},
		"deny only":           {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0", 195, 0)}, DeviceRules: []DeviceRule{grant(false, int64Ptr(195), int64Ptr(0))}}, false},
		"mknod only":          {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0", 195, 0)}, DeviceRules: []DeviceRule{{Allow: true, Type: "c", Major: int64Ptr(195), Minor: int64Ptr(0), Access: "m"}}}, false},
		"privileged wildcard": {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0", 195, 0)}, DeviceRules: []DeviceRule{grant(true, nil, nil)}}, false},
		"numbered node alone": {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0", 195, 0)}}, false},
		"control":             {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidiactl", 195, 255)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(195), int64Ptr(255))}}, false},
		"uvm":                 {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia-uvm", 511, 0)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(511), int64Ptr(0))}}, false},
		"uvm tools":           {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia-uvm-tools", 511, 1)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(511), int64Ptr(1))}}, false},
		"suffix":              {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia0extra", 195, 0)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(195), int64Ptr(0))}}, false},
		"negative":            {Container{IncomingDevices: []RuntimeDevice{device("/dev/nvidia-1", 195, 0)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(195), int64Ptr(0))}}, false},
		"unrelated node":      {Container{IncomingDevices: []RuntimeDevice{device("/dev/fuse", 10, 229)}, DeviceRules: []DeviceRule{grant(true, int64Ptr(10), int64Ptr(229))}}, false},
		// The names the device plugin's cdi-cri strategy hands the runtime:
		// its CDI vendor is fixed, the id follows --device-id-strategy.
		"device plugin cdi uuid":          {Container{CDIDevices: []string{"k8s.device-plugin.nvidia.com/gpu=GPU-c4d588f9-c5b0-9747-89b1-eda3f955765e"}}, true},
		"device plugin cdi index":         {Container{CDIDevices: []string{"k8s.device-plugin.nvidia.com/gpu=0"}}, true},
		"device plugin gdrcopy and mofed": {Container{CDIDevices: []string{"k8s.device-plugin.nvidia.com/gdrcopy=all", "k8s.device-plugin.nvidia.com/mofed=all"}}, false},
		"toolkit cdi kind":                {Container{CDIDevices: []string{"nvidia.com/gpu=0"}}, false},
		"dra claim cdi":                   {Container{CDIDevices: []string{"k8s.gpu.nvidia.com/claim=claim-uid-gpu-0"}}, true},
		"unrelated vendor cdi":            {Container{CDIDevices: []string{"example.com/gpu=0"}}, false},
		"unrelated nvidia class":          {Container{CDIDevices: []string{"nvidia.com/mig=0"}}, false},
		"unrelated nvidia dra class":      {Container{CDIDevices: []string{"k8s.gpu.nvidia.com/widget=0"}}, false},
		"mokka cdi is not an allocation":  {Container{CDIDevices: []string{"nvml-mock.nvidia.com/gpu=all"}}, false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.allocated, hasGPUAllocation(test.container))
		})
	}
}

func TestAllocationAwareAdjustments(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.DeviceHostPath = stageDeviceNodes(t, "nvidia0", "nvidia1", "nvidia2", "nvidiactl")
	cfg.CDISpecHostPath = stageCDISpec(t)

	alloc := func(index int64) Container {
		return Container{
			Namespace:       "default",
			IncomingDevices: []RuntimeDevice{{Path: "/dev/nvidia" + strconv.FormatInt(index, 10), Type: "c", Major: 195, Minor: index}},
			DeviceRules:     []DeviceRule{{Allow: true, Type: "c", Major: int64Ptr(195), Minor: int64Ptr(index), Access: "rwm"}},
		}
	}
	withManagement := func(container Container) Container {
		container.PodAnnotations = map[string]string{cfg.DeviceAnnotation: "true"}
		return container
	}
	assertAllocated := func(t *testing.T, container Container) {
		t.Helper()
		adjustment, ok := requireAdjust(t, cfg, container)
		require.True(t, ok)
		require.Contains(t, adjustment.Mounts, overlayMount())
		require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
		require.Empty(t, adjustment.Devices, "incoming allocation must not be widened")
		require.Empty(t, adjustment.CDIDevices, "Mokka must not emit gpu=all alongside an allocation")
	}

	t.Run("one raw allocated GPU", func(t *testing.T) { t.Parallel(); assertAllocated(t, alloc(2)) })
	t.Run("two raw allocated GPUs", func(t *testing.T) {
		t.Parallel()
		container := alloc(2)
		container.IncomingDevices = append(container.IncomingDevices, alloc(0).IncomingDevices...)
		container.DeviceRules = append(container.DeviceRules, alloc(0).DeviceRules...)
		assertAllocated(t, container)
	})
	t.Run("raw allocation wins over management", func(t *testing.T) { t.Parallel(); assertAllocated(t, withManagement(alloc(2))) })
	for name, reference := range map[string]string{
		"device plugin CDI": "k8s.device-plugin.nvidia.com/gpu=GPU-c4d588f9-c5b0-9747-89b1-eda3f955765e",
		"DRA CDI":           "k8s.gpu.nvidia.com/claim=claim-uid-gpu-0",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, management := range []bool{false, true} {
				container := Container{Namespace: "default", CDIDevices: []string{reference}}
				if management {
					container = withManagement(container)
				}
				assertAllocated(t, container)
				require.Equal(t, []string{reference}, container.CDIDevices)
			}
		})
	}
	t.Run("privileged inheritance does not select GPU tier", func(t *testing.T) {
		t.Parallel()
		container := alloc(0)
		container.DeviceRules = []DeviceRule{{Allow: true, Type: "a", Access: "rwm"}}
		adjustment, ok := requireAdjust(t, cfg, container)
		require.False(t, ok)
		require.Empty(t, adjustment)
	})
	t.Run("management CDI emits only all-GPU reference", func(t *testing.T) {
		t.Parallel()
		local := cfg
		local.DeviceInjectionMode = DeviceInjectionModeCDI
		adjustment, ok := requireAdjust(t, local, deviceOptIn())
		require.True(t, ok)
		require.Equal(t, []string{cfg.CDIDeviceName}, adjustment.CDIDevices)
		require.Empty(t, adjustment.Devices)
	})
}
