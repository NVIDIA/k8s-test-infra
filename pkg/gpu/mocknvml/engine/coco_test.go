// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// TestGetConfComputeMemSizeInfo_ZeroOnEveryDevice pins the reading real
// hardware gives on every board, CC-capable or not: an answer of zero rather
// than the N/A a generated stub produced.
func TestGetConfComputeMemSizeInfo_ZeroOnEveryDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	info, ret := dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.SUCCESS, ret,
		"the getter must answer so nvidia-smi renders 0 rather than N/A")
	require.Equal(t, nvml.ConfComputeMemSizeInfo{}, info,
		"CC mode is off, so no memory is partitioned into protected and unprotected halves")
}

// TestGetConfComputeProtectedMemoryUsage_ZeroOnEveryDevice pins the three values
// nvidia-smi renders as its "Conf Compute Protected Memory Usage" block: 0 MiB
// each, as every hardware capture in the repo reports.
func TestGetConfComputeProtectedMemoryUsage_ZeroOnEveryDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	mem, ret := dev.GetConfComputeProtectedMemoryUsage()
	require.Equal(t, nvml.SUCCESS, ret,
		"the getter must answer so nvidia-smi renders 0 MiB rather than N/A")
	require.Equal(t, nvml.Memory{}, mem,
		"with CC mode off there is no protected region, so total, used and free are all zero")
}

// TestCoCoMemory_LostDeviceReportsGpuIsLost keeps both getters on the
// same failure-injection path as every other device export: once the driver has
// marked the GPU gone they must report the loss, not a plausible zero.
//
// Neither getter advances the failure counter, so the device is tripped through
// a guarded call first.
func TestCoCoMemory_LostDeviceReportsGpuIsLost(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Failure: &FailureInjectionConfig{Mode: FailureModeLost},
	})

	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "expected the guarded call to trip the device")

	_, ret = dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetConfComputeMemSizeInfo")
	_, ret = dev.GetConfComputeProtectedMemoryUsage()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetConfComputeProtectedMemoryUsage")
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, dev.SetMockConfComputeUnprotectedMemSize(1024),
		"SetMockConfComputeUnprotectedMemSize")
}

// TestNodeWideCoCo_ReadsAllBucketOnly pins that the node-wide CC state
// is read from the `all:` bucket alone: the system getters take no device, so
// a value under one device must not be mistaken for the node's.
func TestNodeWideCoCo_ReadsAllBucketOnly(t *testing.T) {
	persistSetterWrites(t)
	w := overrideWriter().(*fileOverrideWriter)

	require.NoError(t, w.patch(0, map[string]any{
		"coco_gpus_ready":                      true,
		"coco_key_rotation_attacker_advantage": 55,
	}))
	configOverrides.invalidateAfterLocalWrite()
	ready, _, advantage := nodeWideCoCo()
	require.Nil(t, ready, "a per-device ready state is not the node's")
	require.Nil(t, advantage, "a per-device threshold is not the node's")

	// The threshold first: once the GPUs are ready it can no longer move.
	require.NoError(t, w.SetCoCoKeyRotationThreshold(57, false))
	require.NoError(t, w.SetCoCoGpusReady(true, false, true))
	configOverrides.invalidateAfterLocalWrite()
	ready, _, advantage = nodeWideCoCo()
	require.NotNil(t, ready)
	require.True(t, *ready)
	require.NotNil(t, advantage)
	require.EqualValues(t, 57, *advantage)
}

// ccEngine builds a two-GPU engine of one architecture with the given
// confidential_compute block, for the system calls, which take no device.
func ccEngine(t *testing.T, arch string, block *ConfidentialComputeConfig) *Engine {
	t.Helper()
	return NewEngine(&Config{
		NumDevices: 2,
		YAMLConfig: &YAMLConfig{
			DeviceDefaults:      DeviceConfig{Architecture: arch, Memory: &MemoryConfig{TotalBytes: 80 << 30}},
			ConfidentialCompute: block,
		},
	})
}

func TestSystemCoCo_CapsFollowArchitecture(t *testing.T) {
	t.Parallel()

	caps, ret := ccEngine(t, "hopper", &ConfidentialComputeConfig{CPUCaps: "amd_sev_snp"}).SystemGetConfComputeCapabilities()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_GPUS_CC_CAPABLE, caps.GpusCaps)
	require.EqualValues(t, nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV_SNP, caps.CpuCaps)

	caps, ret = ccEngine(t, "ampere", nil).SystemGetConfComputeCapabilities()
	require.Equal(t, nvml.SUCCESS, ret, "an old GPU still answers, as not capable")
	require.EqualValues(t, nvml.CC_SYSTEM_GPUS_CC_NOT_CAPABLE, caps.GpusCaps)
}

func TestSystemCoCo_StateAndSettingsReportConfig(t *testing.T) {
	t.Parallel()

	e := ccEngine(t, "blackwell", &ConfidentialComputeConfig{
		Mode: "devtools", MultiGPUMode: "nvle",
	})
	state, ret := e.SystemGetConfComputeState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.ConfComputeSystemState{
		Environment:  nvml.CC_SYSTEM_ENVIRONMENT_SIM,
		CcFeature:    nvml.CC_SYSTEM_FEATURE_ENABLED,
		DevToolsMode: nvml.CC_SYSTEM_DEVTOOLS_MODE_ON,
	}, state)

	settings, ret := e.SystemGetConfComputeSettings()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_SIM, settings.Environment)
	require.EqualValues(t, nvml.CC_SYSTEM_FEATURE_ENABLED, settings.CcFeature)
	require.EqualValues(t, nvml.CC_SYSTEM_DEVTOOLS_MODE_ON, settings.DevToolsMode)
	require.EqualValues(t, nvml.CC_SYSTEM_MULTIGPU_NVLE, settings.MultiGpuMode)
}

// With CC off the feature reads disabled and the multi-GPU mode none, whatever
// the profile says about the mode it would use once switched on.
func TestSystemCoCo_OffReportsFeatureDisabled(t *testing.T) {
	t.Parallel()

	e := ccEngine(t, "blackwell", nil)
	settings, ret := e.SystemGetConfComputeSettings()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_UNAVAILABLE, settings.Environment)
	require.EqualValues(t, nvml.CC_SYSTEM_FEATURE_DISABLED, settings.CcFeature)
	require.EqualValues(t, nvml.CC_SYSTEM_MULTIGPU_NONE, settings.MultiGpuMode)

	state, ret := ccEngine(t, "ampere", nil).SystemGetConfComputeState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_UNAVAILABLE, state.Environment)
	require.EqualValues(t, nvml.CC_SYSTEM_FEATURE_DISABLED, state.CcFeature)
}

func TestSystemCoCo_OffAnswersNotSupported(t *testing.T) {
	t.Parallel()

	_, ret := ccEngine(t, "ampere", nil).SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "ready get on a node that cannot do CC")

	for _, e := range []*Engine{
		ccEngine(t, "hopper", nil),
		ccEngine(t, "ampere", nil),
	} {
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, e.SystemSetConfComputeGpusReadyState(1), "ready set")
		_, ret = e.SystemGetConfComputeKeyRotationThreshold()
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "threshold get")
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, e.SystemSetConfComputeKeyRotationThreshold(55), "threshold set")
	}
}

// Two engines stand in for two processes: the ready state is driver state
// every process on the node observes.
func TestSystemCoCo_ReadyRoundTripAcrossEngines(t *testing.T) {
	persistSetterWrites(t)
	on := &ConfidentialComputeConfig{Mode: "on"}
	writer, reader := ccEngine(t, "hopper", on), ccEngine(t, "hopper", on)

	ready, ret := reader.SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE, ready, "the profile starts not ready")

	require.Equal(t, nvml.SUCCESS, writer.SystemSetConfComputeGpusReadyState(nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE))
	require.Equal(t, nvml.SUCCESS, writer.SystemSetConfComputeGpusReadyState(nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE),
		"setting the state it already has is not an error")

	ready, ret = reader.SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE, ready, "the other engine sees the write")

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, writer.SystemSetConfComputeGpusReadyState(2))
}

func TestSystemCoCo_ThresholdRules(t *testing.T) {
	persistSetterWrites(t)
	e := ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "on"})

	adv, ret := e.SystemGetConfComputeKeyRotationThreshold()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, 60, adv, "the driver default")

	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeKeyRotationThreshold(55))
	adv, _ = e.SystemGetConfComputeKeyRotationThreshold()
	require.EqualValues(t, 55, adv)

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetConfComputeKeyRotationThreshold(49))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetConfComputeKeyRotationThreshold(66))

	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(1))
	require.Equal(t, nvml.ERROR_INVALID_STATE, e.SystemSetConfComputeKeyRotationThreshold(58),
		"the threshold cannot move under accepted work")
}

// From driver 550 leaving the ready state revokes CC work until the driver
// reloads: the getter reads not ready, every later ready-state set is an
// invalid argument, and the threshold stays refused.
func TestSystemCoCo_ClearingReadyRevokes(t *testing.T) {
	persistSetterWrites(t)
	e := ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "on"})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	h, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	dev, ok := e.LookupDevice(h).(*ConfigurableDevice)
	require.True(t, ok)

	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(0), "clearing a state never set revokes nothing")
	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(1))
	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(0))

	ready, ret := e.SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE, ready)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetConfComputeGpusReadyState(1), "revoked until the driver reloads")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetConfComputeGpusReadyState(0))
	require.Equal(t, nvml.ERROR_INVALID_STATE, e.SystemSetConfComputeKeyRotationThreshold(58))
	require.Equal(t, nvml.SUCCESS, dev.SetMockConfComputeUnprotectedMemSize(1024), "the split setter never refuses")

	// A second engine on the same override document stands in for another
	// process: the revocation is the node's, not this process's.
	other := ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "on"})
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, other.SystemSetConfComputeGpusReadyState(1))
}

// Before driver 550 the ready state toggles both ways.
func TestSystemCoCo_OlderDriverTogglesReady(t *testing.T) {
	persistSetterWrites(t)
	e := NewEngine(&Config{
		NumDevices:    2,
		DriverVersion: "535.104.05",
		YAMLConfig: &YAMLConfig{
			System:              SystemConfig{DriverVersion: "535.104.05", NumDevices: 2},
			DeviceDefaults:      DeviceConfig{Architecture: "hopper", Memory: &MemoryConfig{TotalBytes: 80 << 30}},
			ConfidentialCompute: &ConfidentialComputeConfig{Mode: "on"},
		},
	})

	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(1))
	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(0))
	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeKeyRotationThreshold(58),
		"once no longer ready the threshold can move again")
	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(1))
}

// A capable node with CC off still answers its ready state, as not ready:
// `nvidia-smi conf-compute -q` reads it on every capable node and aborts the
// whole report on NOT_SUPPORTED.
func TestSystemCoCo_CapableOffReportsNotReady(t *testing.T) {
	t.Parallel()

	ready, ret := ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "off"}).SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE, ready)
}

// The ready state a node boots in follows the mode, not the profile: DevTools
// accepts work from boot unless NVLE still has its multi-GPU setup to finish,
// and CC on starts not ready so attestation cannot be skipped.
func TestSystemCoCo_BootReadyStateFollowsTheMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		arch  string
		block ConfidentialComputeConfig
		want  uint32
	}{
		{"hopper", ConfidentialComputeConfig{Mode: "on"}, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE},
		{"hopper", ConfidentialComputeConfig{Mode: "devtools"}, nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE},
		{"blackwell", ConfidentialComputeConfig{Mode: "devtools", MultiGPUMode: "nvle"}, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE},
		{"blackwell", ConfidentialComputeConfig{Mode: "on", MultiGPUMode: "nvle"}, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE},
		{"hopper", ConfidentialComputeConfig{Mode: "off", MultiGPUMode: "protected_pcie"}, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE},
	} {
		t.Run(tc.block.Mode+"/"+tc.block.MultiGPUMode, func(t *testing.T) {
			t.Parallel()
			ready, ret := ccEngine(t, tc.arch, &tc.block).SystemGetConfComputeGpusReadyState()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tc.want, ready)
		})
	}
}

// A ready state recorded while CC was on must not survive the profile
// switching CC off: the override document outlives the profile.
func TestSystemCoCo_StaleRecordIgnoredWhenOff(t *testing.T) {
	persistSetterWrites(t)
	require.Equal(t, nvml.SUCCESS,
		ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "on"}).SystemSetConfComputeGpusReadyState(1))

	ready, ret := ccEngine(t, "hopper", nil).SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE, ready, "CC off is never ready")
}

const ccFramebufferBytes = 80 << 30

// ccDevices builds a two-GPU Hopper engine with the given confidential_compute
// block and returns both devices, standing in for two GPUs of one node.
func ccDevices(t *testing.T, block *ConfidentialComputeConfig) (*ConfigurableDevice, *ConfigurableDevice) {
	t.Helper()
	return ccDevicesWithMemory(t, block, &MemoryConfig{TotalBytes: ccFramebufferBytes})
}

// ccDevicesWithMemory is ccDevices with the profile's memory block; nil leaves
// it out, so each GPU inherits the base device's memory.
func ccDevicesWithMemory(t *testing.T, block *ConfidentialComputeConfig, mem *MemoryConfig) (*ConfigurableDevice, *ConfigurableDevice) {
	t.Helper()
	e := NewEngine(&Config{
		NumDevices:    2,
		DriverVersion: "580.65.06",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System:  SystemConfig{DriverVersion: "580.65.06", NumDevices: 2},
			DeviceDefaults: DeviceConfig{
				Architecture: "hopper",
				Memory:       mem,
			},
			ConfidentialCompute: block,
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	devs := make([]*ConfigurableDevice, 2)
	for i := range devs {
		h, ret := e.DeviceGetHandleByIndex(i)
		require.Equal(t, nvml.SUCCESS, ret)
		dev, ok := e.LookupDevice(h).(*ConfigurableDevice)
		require.True(t, ok)
		devs[i] = dev
	}
	return devs[0], devs[1]
}

func TestCoCoMemory_SplitWhenOn(t *testing.T) {
	t.Parallel()
	dev, _ := ccDevices(t, &ConfidentialComputeConfig{Mode: "on", UnprotectedMemKiB: 4194304})

	info, ret := dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.ConfComputeMemSizeInfo{
		ProtectedMemSizeKib:   (ccFramebufferBytes >> 10) - 4194304,
		UnprotectedMemSizeKib: 4194304,
	}, info)

	mem, ret := dev.GetConfComputeProtectedMemoryUsage()
	require.Equal(t, nvml.SUCCESS, ret)
	protected := uint64(ccFramebufferBytes - 4194304<<10)
	require.Equal(t, nvml.Memory{Total: protected, Used: 0, Free: protected}, mem)
}

// A profile that leaves out memory inherits the base device's framebuffer;
// CC must divide that same framebuffer rather than none at all.
func TestCoCoMemory_InheritedFramebuffer(t *testing.T) {
	persistSetterWrites(t)
	dev, _ := ccDevicesWithMemory(t, &ConfidentialComputeConfig{Mode: "on"}, nil)

	mem, ret := dev.GetMemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotZero(t, mem.Total, "the base device reports a framebuffer")

	info, ret := dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.ConfComputeMemSizeInfo{ProtectedMemSizeKib: mem.Total >> 10}, info)

	split, _ := ccDevicesWithMemory(t, &ConfidentialComputeConfig{Mode: "on", UnprotectedMemKiB: 1024}, nil)
	info, _ = split.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.ConfComputeMemSizeInfo{ProtectedMemSizeKib: mem.Total>>10 - 1024, UnprotectedMemSizeKib: 1024}, info)
}

// nvmlDeviceSetConfComputeUnprotectedMemSize succeeds with CC on and changes
// nothing, as the driver's handler does: no size is checked against the
// framebuffer, no ready or revoked state refuses it, and the getter keeps
// reporting the profile's split. With CC off it is not supported.
func TestSetConfComputeUnprotectedMemSize(t *testing.T) {
	persistSetterWrites(t)

	off, _ := ccDevices(t, nil)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, off.SetMockConfComputeUnprotectedMemSize(1024), "CC off")

	block := &ConfidentialComputeConfig{Mode: "on", UnprotectedMemKiB: 4096}
	dev, _ := ccDevices(t, block)
	want, ret := dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.SUCCESS, ret)

	e := ccEngine(t, "hopper", block)
	for _, step := range []struct {
		name  string
		setup func()
	}{
		{"not ready", func() {}},
		{"ready", func() { require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(1)) }},
		{"revoked", func() { require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(0)) }},
	} {
		step.setup()
		for _, size := range []uint64{0, 2097152, (ccFramebufferBytes >> 10) + 1} {
			require.Equal(t, nvml.SUCCESS, dev.SetMockConfComputeUnprotectedMemSize(size), "%s, %d KiB", step.name, size)
			got, _ := dev.GetConfComputeMemSizeInfo()
			require.Equal(t, want, got, "%s: the split does not move", step.name)
		}
	}
}

// TestCoCoSetters_RecheckReadyUnderTheWriteLock covers a ready state
// another process recorded after this one last read the override document:
// the cached snapshot still says not ready, so the decision has to be made
// again under the lock that commits the write.
func TestCoCoSetters_RecheckReadyUnderTheWriteLock(t *testing.T) {
	persistSetterWrites(t)
	e := ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "on"})

	// Load the snapshot, then mark the GPUs ready the way another process
	// would: through the document, without invalidating this process's cache.
	_, ret := e.SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	w := overrideWriter().(*fileOverrideWriter)
	require.NoError(t, w.SetCoCoGpusReady(true, false, true))

	require.Equal(t, nvml.ERROR_INVALID_STATE, e.SystemSetConfComputeKeyRotationThreshold(55),
		"the threshold must not move once another process made the GPUs ready")

	// Another process then revokes them; this one still has the ready
	// snapshot, so only the write's lock can refuse the set.
	_, _ = e.SystemGetConfComputeGpusReadyState()
	require.NoError(t, w.SetCoCoGpusReady(false, false, true))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetConfComputeGpusReadyState(1),
		"a revocation another process made holds")
}

// A Protected PCIe node runs with the CC feature off, yet reports its
// multi-GPU mode and goes ready like a CC node: real 8x H100 PPCIe guests read
// "CC State: OFF", "Multi-GPU Mode: Protected PCIe" and "Not Ready" after boot,
// then "Ready" once a boot unit runs `nvidia-smi conf-compute -srs 1`.
func TestSystemCoCo_ProtectedPCIeWithCCOff(t *testing.T) {
	persistSetterWrites(t)
	e := ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "off", MultiGPUMode: "protected_pcie"})

	settings, ret := e.SystemGetConfComputeSettings()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_FEATURE_DISABLED, settings.CcFeature)
	require.EqualValues(t, nvml.CC_SYSTEM_MULTIGPU_PROTECTED_PCIE, settings.MultiGpuMode)

	ready, ret := e.SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE, ready)

	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE))
	ready, _ = ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "off", MultiGPUMode: "protected_pcie"}).
		SystemGetConfComputeGpusReadyState()
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE, ready, "another process sees the node ready")

	_, ret = e.SystemGetConfComputeKeyRotationThreshold()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "key rotation belongs to the CC feature, which is off")
}
