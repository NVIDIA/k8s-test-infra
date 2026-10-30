// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Values from cc-test-config.tail.yaml, the confidential_compute block the
// Confidential Compute suite runs against.
const (
	ccWantUnprotectedKiB = 4194304
	ccWantAdvantage      = 55
)

// expectReturn is one result asserting an NVML call answered want.
func expectReturn(name string, got, want nvml.Return) testResult {
	if got != want {
		return testResult{name, false, fmt.Sprintf("got %v, want %v", nvml.ErrorString(got), nvml.ErrorString(want))}
	}
	return testResult{name, true, ""}
}

// expectEqual is one result asserting a value read through the bridge.
func expectEqual[T comparable](name string, got, want T) testResult {
	if got != want {
		return testResult{name, false, fmt.Sprintf("got %v, want %v", got, want)}
	}
	return testResult{name, true, ""}
}

// testConfComputeOff drives the CC calls on the A100 fixture, a board that
// cannot do CC. The node still answers its capabilities, as not capable, while
// the calls that need CC on decline. A MIG device handle is refused too, as
// NOT_SUPPORTED like the other physical-only calls, because the per-GPU calls
// take a physical GPU.
func testConfComputeOff(deviceCount int) []testResult {
	caps, ret := nvml.SystemGetConfComputeCapabilities()
	results := []testResult{
		expectReturn("confcompute-off/caps", ret, nvml.SUCCESS),
		expectEqual("confcompute-off/caps/gpus", caps.GpusCaps, uint32(nvml.CC_SYSTEM_GPUS_CC_NOT_CAPABLE)),
	}
	_, ret = nvml.SystemGetConfComputeGpusReadyState()
	results = append(results, expectReturn("confcompute-off/ready", ret, nvml.ERROR_NOT_SUPPORTED))

	device, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		return append(results, testResult{"confcompute-off/handle", false, nvml.ErrorString(ret)})
	}
	_, ret = device.GetConfComputeGpuCertificate()
	results = append(results, expectReturn("confcompute-off/certificate", ret, nvml.ERROR_NOT_SUPPORTED))

	for index := range deviceCount {
		device, ret := nvml.DeviceGetHandleByIndex(index)
		if ret != nvml.SUCCESS {
			continue
		}
		migDevices, err := collectMigDevices(device)
		if err != nil || len(migDevices) == 0 {
			continue
		}
		_, ret = migDevices[0].GetConfComputeGpuCertificate()
		results = append(results, expectReturn("confcompute-off/mig-handle/certificate", ret, nvml.ERROR_NOT_SUPPORTED))
		ret = migDevices[0].SetConfComputeUnprotectedMemSize(1024)
		return append(results, expectReturn("confcompute-off/mig-handle/unprotected", ret, nvml.ERROR_NOT_SUPPORTED))
	}
	return append(results, skippedResult("confcompute-off/mig-handle", "no MIG device on the fixture"))
}

// confComputeOnTests drives every CC call against the H100 fixture with CC on.
// Each struct is caller-allocated, so a field written at the wrong offset or
// width only shows up here; the fixture uses non-default CC values for that
// reason, and the separate Protected PCIe fixture exercises multi-GPU mode.
func confComputeOnTests(deviceCount int) []testResult {
	var results []testResult

	caps, ret := nvml.SystemGetConfComputeCapabilities()
	results = append(results,
		expectReturn("confcompute/caps", ret, nvml.SUCCESS),
		expectEqual("confcompute/caps/cpu", caps.CpuCaps, uint32(nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV_SNP)),
		expectEqual("confcompute/caps/gpus", caps.GpusCaps, uint32(nvml.CC_SYSTEM_GPUS_CC_CAPABLE)))

	state, ret := nvml.SystemGetConfComputeState()
	results = append(results,
		expectReturn("confcompute/state", ret, nvml.SUCCESS),
		expectEqual("confcompute/state", state, nvml.ConfComputeSystemState{
			Environment:  nvml.CC_SYSTEM_ENVIRONMENT_SIM,
			CcFeature:    nvml.CC_SYSTEM_FEATURE_ENABLED,
			DevToolsMode: nvml.CC_SYSTEM_DEVTOOLS_MODE_ON,
		}))

	settings, ret := nvml.SystemGetConfComputeSettings()
	results = append(results,
		expectReturn("confcompute/settings", ret, nvml.SUCCESS),
		expectEqual("confcompute/settings/multi-gpu", settings.MultiGpuMode, uint32(nvml.CC_SYSTEM_MULTIGPU_NONE)),
		expectEqual("confcompute/settings/environment", settings.Environment, uint32(nvml.CC_SYSTEM_ENVIRONMENT_SIM)))

	threshold, ret := nvml.SystemGetConfComputeKeyRotationThresholdInfo()
	results = append(results,
		expectReturn("confcompute/key-rotation", ret, nvml.SUCCESS),
		expectEqual("confcompute/key-rotation/advantage", threshold.AttackerAdvantage, uint64(ccWantAdvantage)))
	ret = nvml.SystemSetConfComputeKeyRotationThresholdInfo(nvml.ConfComputeSetKeyRotationThresholdInfo{
		Version:              nvml.STRUCT_VERSION(nvml.ConfComputeSetKeyRotationThresholdInfo{}, 1),
		MaxAttackerAdvantage: 60,
	})
	results = append(results, expectReturn("confcompute/key-rotation/set", ret, nvml.SUCCESS))
	threshold, _ = nvml.SystemGetConfComputeKeyRotationThresholdInfo()
	results = append(results, expectEqual("confcompute/key-rotation/read-back", threshold.AttackerAdvantage, uint64(60)))

	results = append(results, confComputeDeviceTests(deviceCount)...)
	return append(results, confComputeReadyTests()...)
}

// confComputeDeviceTests covers the per-GPU calls: the memory split, the
// unprotected size setter, the attestation report and the certificate.
func confComputeDeviceTests(deviceCount int) []testResult {
	if deviceCount < 2 {
		return []testResult{{"confcompute/devices", false, fmt.Sprintf("need 2 GPUs, have %d", deviceCount)}}
	}
	gpu0, _ := nvml.DeviceGetHandleByIndex(0)
	gpu1, _ := nvml.DeviceGetHandleByIndex(1)
	fb, ret := gpu0.GetMemoryInfo()
	if ret != nvml.SUCCESS {
		return []testResult{{"confcompute/memory", false, nvml.ErrorString(ret)}}
	}
	fbKiB := fb.Total >> 10

	info, ret := gpu0.GetConfComputeMemSizeInfo()
	results := []testResult{
		expectReturn("confcompute/memsize", ret, nvml.SUCCESS),
		expectEqual("confcompute/memsize", info, nvml.ConfComputeMemSizeInfo{
			ProtectedMemSizeKib:   fbKiB - ccWantUnprotectedKiB,
			UnprotectedMemSizeKib: ccWantUnprotectedKiB,
		}),
	}
	usage, _ := gpu0.GetConfComputeProtectedMemoryUsage()
	results = append(results, expectEqual("confcompute/protected-usage/total", usage.Total, (fbKiB-ccWantUnprotectedKiB)<<10))

	results = append(results, expectReturn("confcompute/unprotected/set", gpu1.SetConfComputeUnprotectedMemSize(2097152), nvml.SUCCESS))
	info, _ = gpu1.GetConfComputeMemSizeInfo()
	results = append(results, expectEqual("confcompute/unprotected/read-back", info.UnprotectedMemSizeKib, uint64(2097152)))
	info, _ = gpu0.GetConfComputeMemSizeInfo()
	results = append(results, expectEqual("confcompute/unprotected/other-gpu", info.UnprotectedMemSizeKib, uint64(ccWantUnprotectedKiB)))

	return append(results, confComputeAttestationTests(gpu0)...)
}

func confComputeAttestationTests(gpu nvml.Device) []testResult {
	var results []testResult
	var report nvml.ConfComputeGpuAttestationReport
	for i := range report.Nonce {
		report.Nonce[i] = 0xA5
	}
	nonce := report.Nonce
	ret := gpu.GetConfComputeGpuAttestationReport(&report)
	results = append(results,
		expectReturn("confcompute/attestation", ret, nvml.SUCCESS),
		expectEqual("confcompute/attestation/size", report.AttestationReportSize, uint32(0x1000)),
		expectEqual("confcompute/attestation/nonce-echo",
			bytes.Equal(report.AttestationReport[0x20:0x40], nonce[:]), true))

	cert, ret := gpu.GetConfComputeGpuCertificate()
	return append(results,
		expectReturn("confcompute/certificate", ret, nvml.SUCCESS),
		expectEqual("confcompute/certificate/sizes",
			[2]uint32{cert.CertChainSize, cert.AttestationCertChainSize}, [2]uint32{0x800, 0xC00}))
}

// confComputeReadyTests flips the node to ready and checks that the threshold
// and the memory split then refuse to move, then flips it back.
func confComputeReadyTests() []testResult {
	ready, ret := nvml.SystemGetConfComputeGpusReadyState()
	results := []testResult{
		expectReturn("confcompute/ready", ret, nvml.SUCCESS),
		expectEqual("confcompute/ready/initial", ready, uint32(nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE)),
		expectReturn("confcompute/ready/set",
			nvml.SystemSetConfComputeGpusReadyState(nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE), nvml.SUCCESS),
	}
	ready, _ = nvml.SystemGetConfComputeGpusReadyState()
	results = append(results, expectEqual("confcompute/ready/read-back", ready, uint32(nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE)))

	ret = nvml.SystemSetConfComputeKeyRotationThresholdInfo(nvml.ConfComputeSetKeyRotationThresholdInfo{
		Version:              nvml.STRUCT_VERSION(nvml.ConfComputeSetKeyRotationThresholdInfo{}, 1),
		MaxAttackerAdvantage: 50,
	})
	results = append(results, expectReturn("confcompute/ready/key-rotation-refused", ret, nvml.ERROR_INVALID_STATE))
	gpu0, _ := nvml.DeviceGetHandleByIndex(0)
	results = append(results, expectReturn("confcompute/ready/unprotected-refused",
		gpu0.SetConfComputeUnprotectedMemSize(1024), nvml.ERROR_INVALID_STATE))

	return append(results, expectReturn("confcompute/ready/unset",
		nvml.SystemSetConfComputeGpusReadyState(nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE), nvml.SUCCESS))
}

func protectedPCIeTests(_ int) []testResult {
	settings, ret := nvml.SystemGetConfComputeSettings()
	results := []testResult{
		expectReturn("ppcie/settings", ret, nvml.SUCCESS),
		expectEqual("ppcie/settings/cc", settings.CcFeature, uint32(nvml.CC_SYSTEM_FEATURE_DISABLED)),
		expectEqual("ppcie/settings/multi-gpu", settings.MultiGpuMode, uint32(nvml.CC_SYSTEM_MULTIGPU_PROTECTED_PCIE)),
	}
	gpu, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		return append(results, testResult{"ppcie/handle", false, nvml.ErrorString(ret)})
	}
	results = append(results, confComputeAttestationTests(gpu)...)
	info, ret := gpu.GetConfComputeMemSizeInfo()
	results = append(results,
		expectReturn("ppcie/memsize", ret, nvml.SUCCESS),
		expectEqual("ppcie/memsize", info, nvml.ConfComputeMemSizeInfo{}),
		expectReturn("ppcie/unprotected/refused", gpu.SetConfComputeUnprotectedMemSize(1024), nvml.ERROR_NOT_SUPPORTED))
	_, ret = nvml.SystemGetConfComputeKeyRotationThresholdInfo()
	results = append(results, expectReturn("ppcie/key-rotation/refused", ret, nvml.ERROR_NOT_SUPPORTED))

	ready, ret := nvml.SystemGetConfComputeGpusReadyState()
	results = append(results,
		expectReturn("ppcie/ready", ret, nvml.SUCCESS),
		expectEqual("ppcie/ready/initial", ready, uint32(nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE)),
		expectReturn("ppcie/ready/set", nvml.SystemSetConfComputeGpusReadyState(nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE), nvml.SUCCESS))
	ready, ret = nvml.SystemGetConfComputeGpusReadyState()
	return append(results,
		expectReturn("ppcie/ready/read-back", ret, nvml.SUCCESS),
		expectEqual("ppcie/ready/read-back", ready, uint32(nvml.CC_ACCEPTING_CLIENT_REQUESTS_TRUE)),
		expectReturn("ppcie/ready/unset", nvml.SystemSetConfComputeGpusReadyState(nvml.CC_ACCEPTING_CLIENT_REQUESTS_FALSE), nvml.SUCCESS))
}
