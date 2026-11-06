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
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// Confidential Compute (CC).
//
// CC is one switch per node, so the profile describes it once, in the
// top-level confidential_compute block, and the engine resolves it into a
// ConfComputeSpec every device shares. Writes made by the setters live in the
// override document, so they are visible to every process on the node.
//
// With CC off — every shipped profile — the memory getters answer zero on
// every device, which is what real hardware does: every nvidia-smi capture in
// tests/e2e/go/assertions/nvidiasmi/testdata/hardware reports 0 MiB across the
// whole block on drivers 570 and later, including the A100, L40S and T4 boards
// that cannot do CC at all. Nothing partitions protected memory until CC mode
// is switched on, and NVML answers "none" rather than "unsupported" whether or
// not the part could. With CC on, the framebuffer splits into a protected and
// an unprotected half. The driver version gates the whole surface: the CC APIs
// arrived in 525, and version.go answers FUNCTION_NOT_FOUND below that.
//
// No getter advances the failure-injection counter. nvidia-smi calls the
// memory getters once per GPU on every -q run, so ticking here would make an
// "after_calls" scenario trip on a schedule that depends on how much of
// nvidia-smi's output the caller asked for. They still surface a lost GPU,
// matching the other per-query reads that answer from static state (platform
// info, C2C mode).

// GetConfComputeMemSizeInfo reports how device memory is split between the
// protected and unprotected halves of a Confidential Compute partition,
// backing nvmlDeviceGetConfComputeMemSizeInfo. Both halves are zero while CC
// mode is off, which is every profile.
func (d *ConfigurableDevice) GetConfComputeMemSizeInfo() (nvml.ConfComputeMemSizeInfo, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.ConfComputeMemSizeInfo{}, ret
	}
	info := d.confComputeMemSplit()
	debugLog("[NVML] nvmlDeviceGetConfComputeMemSizeInfo -> protected=%d KiB unprotected=%d KiB\n",
		info.ProtectedMemSizeKib, info.UnprotectedMemSizeKib)
	return info, nvml.SUCCESS
}

// GetConfComputeProtectedMemoryUsage reports occupancy of the protected memory
// region, backing nvmlDeviceGetConfComputeProtectedMemoryUsage and the "Conf
// Compute Protected Memory Usage" block of `nvidia-smi -q`. With CC mode off
// there is no protected region, so total, used and free are all zero.
func (d *ConfigurableDevice) GetConfComputeProtectedMemoryUsage() (nvml.Memory, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.Memory{}, ret
	}
	// Nothing the mock runs allocates protected memory, so all of it is free.
	total := d.confComputeMemSplit().ProtectedMemSizeKib << 10
	mem := nvml.Memory{Total: total, Free: total}
	debugLog("[NVML] nvmlDeviceGetConfComputeProtectedMemoryUsage -> total=%d used=0 free=%d\n", mem.Total, mem.Free)
	return mem, nvml.SUCCESS
}

// setConfCompute hands the device the node's Confidential Compute profile.
// The engine resolves it once for the whole node, since CC is one switch per
// node rather than a property of any one GPU's config.
func (d *ConfigurableDevice) setConfCompute(spec ConfComputeSpec) {
	d.confCompute = spec
}

// confComputeFramebufferKiB is the memory a CC partition divides: the whole
// framebuffer, in the KiB the CC APIs count in.
func (d *ConfigurableDevice) confComputeFramebufferKiB() uint64 {
	if c := d.cfg(); c != nil && c.Memory != nil {
		return c.Memory.TotalBytes >> 10
	}
	return 0
}

// confComputeMemSplit divides the framebuffer into its protected and
// unprotected halves. With CC off nothing is partitioned, so both are zero.
// A recorded size outranks the profile's, unless the framebuffer has since
// shrunk below it: the override document outlives the profile it was written
// against, and honouring the record would drive the protected half negative.
func (d *ConfigurableDevice) confComputeMemSplit() nvml.ConfComputeMemSizeInfo {
	if !d.confCompute.On {
		return nvml.ConfComputeMemSizeInfo{}
	}
	fb := d.confComputeFramebufferKiB()
	unprotected := min(d.confCompute.UnprotectedMemKiB, fb)
	if rec := d.cfg().ConfComputeUnprotectedMemKiB; rec != nil {
		if *rec <= fb {
			unprotected = *rec
		} else {
			warnLog("[NVML] GPU %d: recorded unprotected size %d KiB exceeds the %d KiB framebuffer; ignoring it\n",
				d.index, *rec, fb)
		}
	}
	return nvml.ConfComputeMemSizeInfo{ProtectedMemSizeKib: fb - unprotected, UnprotectedMemSizeKib: unprotected}
}

// SetMockConfComputeUnprotectedMemSize backs
// nvmlDeviceSetConfComputeUnprotectedMemSize. The size is recorded per GPU in
// the override document so another process reads it back. The split cannot
// move while the node is accepting work, which the driver refuses the same way
// it refuses the key rotation threshold.
//
// Named SetMock* to avoid shadowing the embedded mock Device's setter.
func (d *ConfigurableDevice) SetMockConfComputeUnprotectedMemSize(sizeKiB uint64) nvml.Return {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return ret
	}
	spec := d.confCompute.withRecordedState()
	if !spec.On {
		debugLog("[NVML] nvmlDeviceSetConfComputeUnprotectedMemSize(%d) -> NOT_SUPPORTED (CC off)\n", sizeKiB)
		return nvml.ERROR_NOT_SUPPORTED
	}
	if fb := d.confComputeFramebufferKiB(); sizeKiB > fb {
		debugLog("[NVML] nvmlDeviceSetConfComputeUnprotectedMemSize(%d) -> INVALID_ARGUMENT; framebuffer is %d KiB\n",
			sizeKiB, fb)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if spec.Ready {
		debugLog("[NVML] nvmlDeviceSetConfComputeUnprotectedMemSize(%d) -> INVALID_STATE (GPUs ready)\n", sizeKiB)
		return nvml.ERROR_INVALID_STATE
	}
	return recordSetterWrite(
		fmt.Sprintf("nvmlDeviceSetConfComputeUnprotectedMemSize(%d)", sizeKiB),
		func(w OverrideWriter) error {
			return w.SetConfComputeUnprotectedMemSize(d.PhysicalIndex(), sizeKiB, d.confCompute.Ready)
		})
}

// nodeWideConfCompute reads the Confidential Compute state the node-wide
// setters recorded. Like nodeWideNvlinkBwMode it reads the `all:` bucket
// directly: these APIs take no device, so a value under one device is not
// the node's. A nil result means nothing was recorded.
func nodeWideConfCompute() (ready *bool, advantage *uint64) {
	_, doc := configOverrides.snapshot()
	if doc == nil || len(doc.All) == 0 {
		return nil, nil
	}
	merged, err := MergeDeviceConfig(&DeviceConfig{}, doc.All)
	if err != nil {
		warnLog("[NVML] confidential compute: ignoring unreadable all: overrides: %v\n", err)
		return nil, nil
	}
	return merged.ConfComputeGpusReady, merged.ConfComputeAttackerAdvantage
}

// confCompute resolves the node's Confidential Compute state: the profile's
// block over the architecture of every GPU, with whatever the node-wide setters
// recorded laid on top. A recorded ready state only counts while the profile
// keeps a protected mode, and a recorded threshold only while CC is on, so a
// value written before the profile switched them off cannot resurrect the
// calls that need them.
func (e *Engine) confCompute() ConfComputeSpec {
	return e.confComputeProfile().withRecordedState()
}

// confComputeProfile is the node's Confidential Compute state as the profile
// describes it, before any setter write. Devices take a copy at construction;
// the profile cannot change under a running engine, while the writes can.
func (e *Engine) confComputeProfile() ConfComputeSpec {
	archs := make([]gpuarch.Arch, 0, e.config.NumDevices)
	for i := range e.config.NumDevices {
		var arch gpuarch.Arch
		if dc := e.config.GetDeviceConfig(i); dc != nil {
			// An unrecognised name parses as Unknown, which is not capable.
			arch, _ = gpuarch.Parse(dc.Architecture)
		}
		archs = append(archs, arch)
	}
	return resolveConfCompute(e.config.YAMLConfig, archs)
}

// withRecordedState lays the node-wide setter writes over the profile's state.
func (spec ConfComputeSpec) withRecordedState() ConfComputeSpec {
	if !spec.protected() {
		return spec
	}
	ready, advantage := nodeWideConfCompute()
	if ready != nil {
		spec.Ready = *ready
	}
	if advantage != nil && spec.On {
		spec.AttackerAdvantage = *advantage
	}
	return spec
}

// systemState is the environment, feature and devtools triple both
// the State and Settings getters report. With CC off the feature reads
// disabled whatever mode the profile would use once switched on.
func (spec ConfComputeSpec) systemState() nvml.ConfComputeSystemState {
	state := nvml.ConfComputeSystemState{
		Environment:  spec.Environment,
		CcFeature:    nvml.CC_SYSTEM_FEATURE_DISABLED,
		DevToolsMode: nvml.CC_SYSTEM_DEVTOOLS_MODE_OFF,
	}
	if spec.On {
		state.CcFeature = nvml.CC_SYSTEM_FEATURE_ENABLED
		if spec.DevTools {
			state.DevToolsMode = nvml.CC_SYSTEM_DEVTOOLS_MODE_ON
		}
	}
	return state
}

// SystemGetConfComputeCapabilities backs nvmlSystemGetConfComputeCapabilities.
// It answers on every node: a board that cannot do CC reports not capable
// rather than refusing the query.
func (e *Engine) SystemGetConfComputeCapabilities() (nvml.ConfComputeSystemCaps, nvml.Return) {
	spec := e.confCompute()
	caps := nvml.ConfComputeSystemCaps{CpuCaps: spec.CPUCaps, GpusCaps: nvml.CC_SYSTEM_GPUS_CC_NOT_CAPABLE}
	if spec.Capable {
		caps.GpusCaps = nvml.CC_SYSTEM_GPUS_CC_CAPABLE
	}
	debugLog("[NVML] nvmlSystemGetConfComputeCapabilities -> cpuCaps=%d gpusCaps=%d\n", caps.CpuCaps, caps.GpusCaps)
	return caps, nvml.SUCCESS
}

// SystemGetConfComputeState backs nvmlSystemGetConfComputeState.
func (e *Engine) SystemGetConfComputeState() (nvml.ConfComputeSystemState, nvml.Return) {
	state := e.confCompute().systemState()
	debugLog("[NVML] nvmlSystemGetConfComputeState -> environment=%d ccFeature=%d devToolsMode=%d\n",
		state.Environment, state.CcFeature, state.DevToolsMode)
	return state, nvml.SUCCESS
}

// SystemGetConfComputeSettings backs nvmlSystemGetConfComputeSettings. The
// bridge stamps the struct version, which is a property of the C layout.
func (e *Engine) SystemGetConfComputeSettings() (nvml.SystemConfComputeSettings, nvml.Return) {
	spec := e.confCompute()
	state := spec.systemState()
	settings := nvml.SystemConfComputeSettings{
		Environment:  state.Environment,
		CcFeature:    state.CcFeature,
		DevToolsMode: state.DevToolsMode,
		MultiGpuMode: nvml.CC_SYSTEM_MULTIGPU_NONE,
	}
	if spec.protected() {
		settings.MultiGpuMode = spec.MultiGPUMode
	}
	debugLog("[NVML] nvmlSystemGetConfComputeSettings -> ccFeature=%d multiGpuMode=%d\n",
		settings.CcFeature, settings.MultiGpuMode)
	return settings, nvml.SUCCESS
}

// SystemGetConfComputeGpusReadyState backs
// nvmlSystemGetConfComputeGpusReadyState. A capable node answers even with no
// protected mode, as not ready — confCompute ignores any recorded state then —
// because `nvidia-smi conf-compute -q` reads it on every capable node and
// aborts the whole report on NOT_SUPPORTED. Only a node that cannot do CC
// declines.
func (e *Engine) SystemGetConfComputeGpusReadyState() (uint32, nvml.Return) {
	spec := e.confCompute()
	if !spec.Capable {
		debugLog("[NVML] nvmlSystemGetConfComputeGpusReadyState -> NOT_SUPPORTED (not CC capable)\n")
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	var ready uint32 = nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE
	if spec.protected() && spec.Ready {
		ready = nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE
	}
	debugLog("[NVML] nvmlSystemGetConfComputeGpusReadyState -> %d\n", ready)
	return ready, nvml.SUCCESS
}

// SystemSetConfComputeGpusReadyState backs
// nvmlSystemSetConfComputeGpusReadyState. The state is driver state the whole
// node observes, so it is recorded in the override document's `all:` bucket.
// It is writable in any protected mode, Protected PCIe with the CC feature off
// included.
//
// The docs also list NO_PERMISSION for a non-root caller; the mock models no
// notion of privilege.
func (e *Engine) SystemSetConfComputeGpusReadyState(isAcceptingWork uint32) nvml.Return {
	if !e.confCompute().protected() {
		debugLog("[NVML] nvmlSystemSetConfComputeGpusReadyState(%d) -> NOT_SUPPORTED (no protected mode)\n", isAcceptingWork)
		return nvml.ERROR_NOT_SUPPORTED
	}
	if isAcceptingWork != nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE &&
		isAcceptingWork != nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE {
		debugLog("[NVML] nvmlSystemSetConfComputeGpusReadyState(%d) -> INVALID_ARGUMENT\n", isAcceptingWork)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	ready := isAcceptingWork == nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE
	return recordSetterWrite(
		fmt.Sprintf("nvmlSystemSetConfComputeGpusReadyState(%d)", isAcceptingWork),
		func(w OverrideWriter) error { return w.SetConfComputeGpusReady(ready) })
}

// SystemGetConfComputeKeyRotationThreshold backs
// nvmlSystemGetConfComputeKeyRotationThresholdInfo: the attacker advantage the
// driver rotates keys at.
func (e *Engine) SystemGetConfComputeKeyRotationThreshold() (uint64, nvml.Return) {
	spec := e.confCompute()
	if !spec.On {
		debugLog("[NVML] nvmlSystemGetConfComputeKeyRotationThresholdInfo -> NOT_SUPPORTED (CC off)\n")
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlSystemGetConfComputeKeyRotationThresholdInfo -> attackerAdvantage=%d\n",
		spec.AttackerAdvantage)
	return spec.AttackerAdvantage, nvml.SUCCESS
}

// SystemSetConfComputeKeyRotationThreshold backs
// nvmlSystemSetConfComputeKeyRotationThresholdInfo. The header names
// INVALID_STATE while the GPUs are ready: the threshold cannot move under work
// already accepted.
func (e *Engine) SystemSetConfComputeKeyRotationThreshold(maxAttackerAdvantage uint64) nvml.Return {
	spec := e.confCompute()
	if !spec.On {
		debugLog("[NVML] nvmlSystemSetConfComputeKeyRotationThresholdInfo(%d) -> NOT_SUPPORTED (CC off)\n",
			maxAttackerAdvantage)
		return nvml.ERROR_NOT_SUPPORTED
	}
	if maxAttackerAdvantage < minKeyRotationAttackerAdvantage || maxAttackerAdvantage > maxKeyRotationAttackerAdvantage {
		debugLog("[NVML] nvmlSystemSetConfComputeKeyRotationThresholdInfo(%d) -> INVALID_ARGUMENT; range %d..%d\n",
			maxAttackerAdvantage, minKeyRotationAttackerAdvantage, maxKeyRotationAttackerAdvantage)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if spec.Ready {
		debugLog("[NVML] nvmlSystemSetConfComputeKeyRotationThresholdInfo(%d) -> INVALID_STATE (GPUs ready)\n",
			maxAttackerAdvantage)
		return nvml.ERROR_INVALID_STATE
	}
	return recordSetterWrite(
		fmt.Sprintf("nvmlSystemSetConfComputeKeyRotationThresholdInfo(%d)", maxAttackerAdvantage),
		func(w OverrideWriter) error {
			return w.SetConfComputeKeyRotationThreshold(maxAttackerAdvantage, e.confComputeProfile().Ready)
		})
}
