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

// TestConfComputeMemory_LostDeviceReportsGpuIsLost keeps both getters on the
// same failure-injection path as every other device export: once the driver has
// marked the GPU gone they must report the loss, not a plausible zero.
//
// Neither getter advances the failure counter, so the device is tripped through
// a guarded call first.
func TestConfComputeMemory_LostDeviceReportsGpuIsLost(t *testing.T) {
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

// TestNodeWideConfCompute_ReadsAllBucketOnly pins that the node-wide CC state
// is read from the `all:` bucket alone: the system getters take no device, so
// a value under one device must not be mistaken for the node's.
func TestNodeWideConfCompute_ReadsAllBucketOnly(t *testing.T) {
	persistSetterWrites(t)
	w := overrideWriter().(*fileOverrideWriter)

	require.NoError(t, w.patch(0, map[string]any{
		"conf_compute_gpus_ready":                      true,
		"conf_compute_key_rotation_attacker_advantage": 55,
	}))
	configOverrides.invalidateAfterLocalWrite()
	ready, advantage := nodeWideConfCompute()
	require.Nil(t, ready, "a per-device ready state is not the node's")
	require.Nil(t, advantage, "a per-device threshold is not the node's")

	// The threshold first: once the GPUs are ready it can no longer move.
	require.NoError(t, w.SetConfComputeKeyRotationThreshold(57, false))
	require.NoError(t, w.SetConfComputeGpusReady(true))
	configOverrides.invalidateAfterLocalWrite()
	ready, advantage = nodeWideConfCompute()
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

func TestSystemConfCompute_CapsFollowArchitecture(t *testing.T) {
	t.Parallel()

	caps, ret := ccEngine(t, "hopper", &ConfidentialComputeConfig{CPUCaps: "amd_sev_snp"}).SystemGetConfComputeCapabilities()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_GPUS_CC_CAPABLE, caps.GpusCaps)
	require.EqualValues(t, nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV_SNP, caps.CpuCaps)

	caps, ret = ccEngine(t, "ampere", nil).SystemGetConfComputeCapabilities()
	require.Equal(t, nvml.SUCCESS, ret, "an old GPU still answers, as not capable")
	require.EqualValues(t, nvml.CC_SYSTEM_GPUS_CC_NOT_CAPABLE, caps.GpusCaps)
}

func TestSystemConfCompute_StateAndSettingsReportConfig(t *testing.T) {
	t.Parallel()

	e := ccEngine(t, "blackwell", &ConfidentialComputeConfig{
		Mode: "devtools", Environment: "sim", MultiGPUMode: "nvle",
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
func TestSystemConfCompute_OffReportsFeatureDisabled(t *testing.T) {
	t.Parallel()

	e := ccEngine(t, "blackwell", nil)
	settings, ret := e.SystemGetConfComputeSettings()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_PROD, settings.Environment)
	require.EqualValues(t, nvml.CC_SYSTEM_FEATURE_DISABLED, settings.CcFeature)
	require.EqualValues(t, nvml.CC_SYSTEM_MULTIGPU_NONE, settings.MultiGpuMode)

	state, ret := ccEngine(t, "ampere", nil).SystemGetConfComputeState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_UNAVAILABLE, state.Environment)
	require.EqualValues(t, nvml.CC_SYSTEM_FEATURE_DISABLED, state.CcFeature)
}

func TestSystemConfCompute_OffAnswersNotSupported(t *testing.T) {
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
func TestSystemConfCompute_ReadyRoundTripAcrossEngines(t *testing.T) {
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

func TestSystemConfCompute_ThresholdRules(t *testing.T) {
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
	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeGpusReadyState(0))
	require.Equal(t, nvml.SUCCESS, e.SystemSetConfComputeKeyRotationThreshold(58),
		"once no longer ready the threshold can move again")
}

// A capable node with CC off still answers its ready state, as not ready:
// `nvidia-smi conf-compute -q` reads it on every capable node and aborts the
// whole report on NOT_SUPPORTED.
func TestSystemConfCompute_CapableOffReportsNotReady(t *testing.T) {
	t.Parallel()

	ready, ret := ccEngine(t, "hopper", &ConfidentialComputeConfig{Ready: true}).SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE, ready,
		"a ready: true profile with CC off is still not ready")
}

// A ready state recorded while CC was on must not survive the profile
// switching CC off: the override document outlives the profile.
func TestSystemConfCompute_StaleRecordIgnoredWhenOff(t *testing.T) {
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
	e := NewEngine(&Config{
		NumDevices:    2,
		DriverVersion: "580.65.06",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System:  SystemConfig{DriverVersion: "580.65.06", NumDevices: 2},
			DeviceDefaults: DeviceConfig{
				Architecture: "hopper",
				Memory:       &MemoryConfig{TotalBytes: ccFramebufferBytes},
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

func TestConfComputeMemory_SplitWhenOn(t *testing.T) {
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

func TestSetConfComputeUnprotectedMemSize(t *testing.T) {
	persistSetterWrites(t)

	off, _ := ccDevices(t, nil)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, off.SetMockConfComputeUnprotectedMemSize(1024), "CC off")

	dev, sibling := ccDevices(t, &ConfidentialComputeConfig{Mode: "on"})
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetMockConfComputeUnprotectedMemSize((ccFramebufferBytes>>10)+1),
		"larger than the framebuffer")

	require.Equal(t, nvml.SUCCESS, dev.SetMockConfComputeUnprotectedMemSize(2097152))
	info, ret := dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, 2097152, info.UnprotectedMemSizeKib)

	info, _ = sibling.GetConfComputeMemSizeInfo()
	require.Zero(t, info.UnprotectedMemSizeKib, "the size is per GPU")

	// A second engine on the same override document stands in for another
	// process reading the write back.
	again, _ := ccDevices(t, &ConfidentialComputeConfig{Mode: "on"})
	info, _ = again.GetConfComputeMemSizeInfo()
	require.EqualValues(t, 2097152, info.UnprotectedMemSizeKib, "another process sees the write")

	require.Equal(t, nvml.SUCCESS, ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "on"}).SystemSetConfComputeGpusReadyState(1))
	require.Equal(t, nvml.ERROR_INVALID_STATE, again.SetMockConfComputeUnprotectedMemSize(1024),
		"the split cannot move under accepted work")
}

// A recorded size outlives the profile it was written against; once it no
// longer fits the framebuffer it must not drive the protected size negative.
func TestConfComputeMemory_RecordAboveFramebufferIgnored(t *testing.T) {
	persistSetterWrites(t)
	w := overrideWriter().(*fileOverrideWriter)
	require.NoError(t, w.SetConfComputeUnprotectedMemSize(0, (ccFramebufferBytes>>10)+1, false))
	configOverrides.invalidateAfterLocalWrite()

	dev, _ := ccDevices(t, &ConfidentialComputeConfig{Mode: "on", UnprotectedMemKiB: 1024})
	info, ret := dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, 1024, info.UnprotectedMemSizeKib, "falls back to the profile's size")
	require.EqualValues(t, (ccFramebufferBytes>>10)-1024, info.ProtectedMemSizeKib)
}

// TestConfComputeSetters_RecheckReadyUnderTheWriteLock covers a ready state
// another process recorded after this one last read the override document:
// the cached snapshot still says not ready, so the decision has to be made
// again under the lock that commits the write.
func TestConfComputeSetters_RecheckReadyUnderTheWriteLock(t *testing.T) {
	persistSetterWrites(t)
	e := ccEngine(t, "hopper", &ConfidentialComputeConfig{Mode: "on"})
	dev, _ := ccDevices(t, &ConfidentialComputeConfig{Mode: "on"})

	// Load the snapshot, then mark the GPUs ready the way another process
	// would: through the document, without invalidating this process's cache.
	_, ret := e.SystemGetConfComputeGpusReadyState()
	require.Equal(t, nvml.SUCCESS, ret)
	require.NoError(t, overrideWriter().(*fileOverrideWriter).SetConfComputeGpusReady(true))

	require.Equal(t, nvml.ERROR_INVALID_STATE, e.SystemSetConfComputeKeyRotationThreshold(55),
		"the threshold must not move once another process made the GPUs ready")
	require.Equal(t, nvml.ERROR_INVALID_STATE, dev.SetMockConfComputeUnprotectedMemSize(1024),
		"nor may the memory split")
}

// A Protected PCIe node runs with the CC feature off, yet reports its
// multi-GPU mode and goes ready like a CC node: real 8x H100 PPCIe guests read
// "CC State: OFF", "Multi-GPU Mode: Protected PCIe" and "Not Ready" after boot,
// then "Ready" once a boot unit runs `nvidia-smi conf-compute -srs 1`.
func TestSystemConfCompute_ProtectedPCIeWithCCOff(t *testing.T) {
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
