// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides bridge-level integration tests for the mock NVML library.
// These tests exercise the C ABI surface through go-nvml, validating:
//   - String handling (goStringToC): buffer overflow prevention, empty strings
//   - Handle conversions: index→handle→UUID round-trip, invalid index behavior
//   - Error string caching: nvmlErrorString returns consistent, valid strings
//   - Boundary conditions: out-of-range indices, repeated init/shutdown cycles
//   - Event set wait: blocks for the caller's timeout; wait symbols exported
//
// These tests require the mock NVML .so to be built and available via
// LD_LIBRARY_PATH. They are run as part of the Docker-based integration test.

package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/dl"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// testResult tracks pass/fail for a single test case.
type testResult struct {
	name   string
	passed bool
	detail string
}

// bridgeTests runs all bridge-level tests and returns results.
// Caller must have already called nvml.Init().
func bridgeTests(deviceCount int) []testResult {
	var results []testResult
	results = append(results, testStringHandling()...)
	results = append(results, testHandleConversions(deviceCount)...)
	results = append(results, testErrorStrings()...)
	results = append(results, testInvalidIndices(deviceCount)...)
	results = append(results, testEventSetWait(deviceCount)...)
	results = append(results, testInitShutdownCycles()...)
	results = append(results, testInternalExportTable()...)
	results = append(results, testFabricHealth(deviceCount)...)
	return results
}

// --- Fabric health tests ---

// fabricHealthField reads one condition out of a health mask the way a
// consumer does, via NVML_GPU_FABRIC_HEALTH_STATUS_GET.
func fabricHealthField(mask uint32, shift, width int) uint32 {
	return (mask >> uint32(shift)) & uint32(width)
}

// testFabricHealth drives nvmlDeviceGetGpuFabricInfo (v1) and
// nvmlDeviceGetGpuFabricInfoV (v2 and v3) through the built library, which is
// the only place the version dispatch runs against real caller-allocated
// buffers: go-nvml's V2() hands the bridge a 36-byte GpuFabricInfo_v2 cast to
// the 40-byte *GpuFabricInfoV, so a v2 caller must come back with its own
// version tag and no health summary, while a v3 caller gets both.
//
// The fixture's device_defaults declare a fabric with no health block (so it
// must report Healthy, not the N/A that #677 fixed) and device 1 overrides one
// condition (so a mask that ignored config, or applied a fault wholesale,
// fails here).
func testFabricHealth(deviceCount int) []testResult {
	var results []testResult

	if deviceCount == 0 {
		return results
	}

	healthyMask := uint32(nvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW |
		nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY |
		nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY |
		nvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY |
		nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_INCORRECT_CONFIGURATION)

	cases := []struct {
		index       int
		label       string
		wantMask    uint32
		wantSummary uint8
	}{
		{0, "healthy", healthyMask, nvml.GPU_FABRIC_HEALTH_SUMMARY_HEALTHY},
		{
			1, "route_unhealthy",
			// Only the route-unhealthy slot differs from the healthy mask.
			healthyMask ^ uint32((nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE^nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_TRUE)<<
				nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY),
			nvml.GPU_FABRIC_HEALTH_SUMMARY_UNHEALTHY,
		},
	}

	for _, tc := range cases {
		if tc.index >= deviceCount {
			continue
		}
		name := "fabric/" + tc.label
		device, ret := nvml.DeviceGetHandleByIndex(tc.index)
		if ret != nvml.SUCCESS {
			results = append(results, testResult{name, false, fmt.Sprintf("GetHandleByIndex(%d) failed: %v", tc.index, nvml.ErrorString(ret))})
			continue
		}

		// v1 carries no health fields at all; it must keep working now that
		// the engine reports a non-zero summary.
		v1, ret := device.GetGpuFabricInfo()
		switch {
		case ret != nvml.SUCCESS:
			results = append(results, testResult{name + "/v1", false, fmt.Sprintf("GetGpuFabricInfo failed: %v", nvml.ErrorString(ret))})
		case v1.CliqueId != 3:
			results = append(results, testResult{name + "/v1", false, fmt.Sprintf("cliqueId = %d, want 3", v1.CliqueId)})
		default:
			results = append(results, testResult{name + "/v1", true, ""})
		}

		v2, ret := device.GetGpuFabricInfoV().V2()
		wantV2Version := nvml.STRUCT_VERSION(nvml.GpuFabricInfo_v2{}, 2)
		switch {
		case ret != nvml.SUCCESS:
			results = append(results, testResult{name + "/v2", false, fmt.Sprintf("V2 failed: %v", nvml.ErrorString(ret))})
		case v2.Version != wantV2Version:
			results = append(results, testResult{name + "/v2", false, fmt.Sprintf("version = 0x%x, want the v2 tag 0x%x", v2.Version, wantV2Version)})
		case v2.HealthMask != tc.wantMask:
			results = append(results, testResult{name + "/v2", false, fmt.Sprintf("healthMask = 0x%x, want 0x%x", v2.HealthMask, tc.wantMask)})
		default:
			results = append(results, testResult{name + "/v2", true, fmt.Sprintf("healthMask=0x%x", v2.HealthMask)})
		}

		v3, ret := device.GetGpuFabricInfoV().V3()
		switch {
		case ret != nvml.SUCCESS:
			results = append(results, testResult{name + "/v3", false, fmt.Sprintf("V3 failed: %v", nvml.ErrorString(ret))})
		case v3.HealthMask != tc.wantMask:
			results = append(results, testResult{name + "/v3", false, fmt.Sprintf("healthMask = 0x%x, want 0x%x", v3.HealthMask, tc.wantMask)})
		case v3.HealthSummary != tc.wantSummary:
			results = append(results, testResult{name + "/v3", false, fmt.Sprintf("healthSummary = %d, want %d", v3.HealthSummary, tc.wantSummary)})
		default:
			results = append(results, testResult{name + "/v3", true, fmt.Sprintf("summary=%d", v3.HealthSummary)})
		}

		// The per-condition rows nvidia-smi renders: the injected fault must
		// be the only one that moved.
		routeUnhealthy := fabricHealthField(v3.HealthMask,
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY, nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY)
		routeRecovery := fabricHealthField(v3.HealthMask,
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY, nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_RECOVERY)
		wantRouteUnhealthy := uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE)
		if tc.label == "route_unhealthy" {
			wantRouteUnhealthy = nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_TRUE
		}
		switch {
		case routeUnhealthy != wantRouteUnhealthy:
			results = append(results, testResult{name + "/conditions", false, fmt.Sprintf("route unhealthy = %d, want %d", routeUnhealthy, wantRouteUnhealthy)})
		case routeRecovery != nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_FALSE:
			results = append(results, testResult{name + "/conditions", false, fmt.Sprintf("route recovery = %d, want False (%d)", routeRecovery, nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_FALSE)})
		default:
			results = append(results, testResult{name + "/conditions", true, ""})
		}
	}

	return results
}

// --- Event set wait tests ---

// eventWaitSlack absorbs scheduler jitter in the container.
const eventWaitSlack = 2 * time.Second

// testEventSetWait drives nvmlEventSetWait through the built library. Clients
// loop on this call with no sleep of their own, so an immediate return spins
// their health loop — hence the timing assertions.
func testEventSetWait(deviceCount int) []testResult {
	var results []testResult

	if deviceCount == 0 {
		return results
	}

	set, ret := nvml.EventSetCreate()
	if ret != nvml.SUCCESS {
		return append(results, testResult{"event/set_create", false, fmt.Sprintf("EventSetCreate failed: %v", nvml.ErrorString(ret))})
	}
	results = append(results, testResult{"event/set_create", true, ""})
	defer func() {
		if ret := set.Free(); ret != nvml.SUCCESS {
			log.Printf("  WARN  event/set_free: %v", nvml.ErrorString(ret))
		}
	}()

	device, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		return append(results, testResult{"event/get_device", false, fmt.Sprintf("GetHandleByIndex(0) failed: %v", nvml.ErrorString(ret))})
	}
	if ret := device.RegisterEvents(nvml.EventTypeXidCriticalError, set); ret != nvml.SUCCESS {
		results = append(results, testResult{"event/register", false, fmt.Sprintf("RegisterEvents failed: %v", nvml.ErrorString(ret))})
	} else {
		results = append(results, testResult{"event/register", true, "XidCriticalError"})
	}

	// Drain anything the config queued so the checks below measure an idle wait.
	drained := 0
	for ; drained < deviceCount; drained++ {
		if _, ret := set.Wait(0); ret != nvml.SUCCESS {
			break
		}
	}

	start := time.Now()
	_, ret = set.Wait(0)
	elapsed := time.Since(start)
	switch {
	case ret != nvml.ERROR_TIMEOUT:
		results = append(results, testResult{"event/wait_zero_timeout", false, fmt.Sprintf("expected TIMEOUT, got %v (after draining %d)", nvml.ErrorString(ret), drained)})
	case elapsed > eventWaitSlack:
		results = append(results, testResult{"event/wait_zero_timeout", false, fmt.Sprintf("zero timeout blocked for %s", elapsed)})
	default:
		results = append(results, testResult{"event/wait_zero_timeout", true, elapsed.String()})
	}

	// Regression guard: an idle wait must consume the requested timeout.
	const waitFor = 500 * time.Millisecond
	start = time.Now()
	_, ret = set.Wait(uint32(waitFor.Milliseconds()))
	elapsed = time.Since(start)
	switch {
	case ret != nvml.ERROR_TIMEOUT:
		results = append(results, testResult{"event/wait_blocks", false, fmt.Sprintf("expected TIMEOUT, got %v", nvml.ErrorString(ret))})
	case elapsed < waitFor:
		results = append(results, testResult{"event/wait_blocks", false, fmt.Sprintf("returned after %s, want at least %s (clients busy-spin on early returns)", elapsed, waitFor)})
	case elapsed > waitFor+eventWaitSlack:
		results = append(results, testResult{"event/wait_blocks", false, fmt.Sprintf("blocked for %s, want about %s", elapsed, waitFor)})
	default:
		results = append(results, testResult{"event/wait_blocks", true, elapsed.String()})
	}

	results = append(results, testEventSetWaitSymbols()...)

	return results
}

// testEventSetWaitSymbols checks the wait symbols the library claims. go-nvml
// only calls v2 (via the unversioned alias), so v1 needs an explicit dlsym.
func testEventSetWaitSymbols() []testResult {
	var results []testResult

	lib := dl.New("libnvidia-ml.so.1", dl.RTLD_LAZY|dl.RTLD_GLOBAL)
	if err := lib.Open(); err != nil {
		return append(results, testResult{"event/dlopen", false, fmt.Sprintf("dlopen libnvidia-ml.so.1: %v", err)})
	}
	defer func() {
		if err := lib.Close(); err != nil {
			log.Printf("  WARN  event/dlclose: %v", err)
		}
	}()

	for _, symbol := range []string{"nvmlEventSetWait", "nvmlEventSetWait_v1", "nvmlEventSetWait_v2"} {
		if err := lib.Lookup(symbol); err != nil {
			results = append(results, testResult{fmt.Sprintf("event/symbol_%s", symbol), false, err.Error()})
		} else {
			results = append(results, testResult{fmt.Sprintf("event/symbol_%s", symbol), true, ""})
		}
	}

	return results
}

// --- String handling tests (goStringToC) ---

func testStringHandling() []testResult {
	var results []testResult

	// Test: driver version string is non-empty and properly terminated
	// This exercises goStringToC with a real version string (e.g. "550.54.15")
	version, ret := nvml.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"string/driver_version", false, fmt.Sprintf("SystemGetDriverVersion failed: %v", nvml.ErrorString(ret))})
		return results
	}
	if version == "" {
		results = append(results, testResult{"string/driver_version_nonempty", false, "driver version is empty"})
	} else if strings.Contains(version, "\x00") {
		results = append(results, testResult{"string/driver_version_no_null", false, fmt.Sprintf("driver version contains embedded null: %q", version)})
	} else {
		results = append(results, testResult{"string/driver_version_nonempty", true, version})
	}

	// Test: NVML version string
	nvmlVersion, ret := nvml.SystemGetNVMLVersion()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"string/nvml_version", false, fmt.Sprintf("SystemGetNVMLVersion failed: %v", nvml.ErrorString(ret))})
	} else if nvmlVersion == "" {
		results = append(results, testResult{"string/nvml_version_nonempty", false, "NVML version is empty"})
	} else {
		results = append(results, testResult{"string/nvml_version_nonempty", true, nvmlVersion})
	}

	// Test: device name string (exercises goStringToC with longer strings)
	if device, ret := nvml.DeviceGetHandleByIndex(0); ret == nvml.SUCCESS {
		name, ret := device.GetName()
		if ret != nvml.SUCCESS {
			results = append(results, testResult{"string/device_name", false, fmt.Sprintf("GetName failed: %v", nvml.ErrorString(ret))})
		} else if name == "" {
			results = append(results, testResult{"string/device_name_nonempty", false, "device name is empty"})
		} else if len(name) > 96 {
			// NVML_DEVICE_NAME_V2_BUFFER_SIZE is 96
			results = append(results, testResult{"string/device_name_length", false, fmt.Sprintf("device name exceeds buffer: len=%d", len(name))})
		} else {
			results = append(results, testResult{"string/device_name_valid", true, name})
		}

		// Test: UUID string format (GPU-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
		uuid, ret := device.GetUUID()
		if ret != nvml.SUCCESS {
			results = append(results, testResult{"string/uuid", false, fmt.Sprintf("GetUUID failed: %v", nvml.ErrorString(ret))})
		} else if !strings.HasPrefix(uuid, "GPU-") {
			results = append(results, testResult{"string/uuid_format", false, fmt.Sprintf("UUID doesn't start with GPU-: %q", uuid)})
		} else if len(uuid) != 40 {
			// GPU- (4) + 8-4-4-4-12 (36) = 40
			results = append(results, testResult{"string/uuid_length", false, fmt.Sprintf("UUID unexpected length: %d (%q)", len(uuid), uuid)})
		} else {
			results = append(results, testResult{"string/uuid_format", true, uuid})
		}
	}

	return results
}

// --- Handle conversion tests ---

func testHandleConversions(deviceCount int) []testResult {
	var results []testResult

	if deviceCount == 0 {
		return results
	}

	// Test: index→handle→UUID→handle round-trip
	device, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"handle/index_to_handle", false, fmt.Sprintf("GetHandleByIndex(0) failed: %v", nvml.ErrorString(ret))})
		return results
	}
	results = append(results, testResult{"handle/index_to_handle", true, ""})

	uuid, ret := device.GetUUID()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"handle/handle_to_uuid", false, fmt.Sprintf("GetUUID failed: %v", nvml.ErrorString(ret))})
		return results
	}

	deviceByUUID, ret := nvml.DeviceGetHandleByUUID(uuid)
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"handle/uuid_to_handle", false, fmt.Sprintf("GetHandleByUUID(%q) failed: %v", uuid, nvml.ErrorString(ret))})
		return results
	}

	uuidCheck, ret := deviceByUUID.GetUUID()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"handle/roundtrip_uuid", false, fmt.Sprintf("GetUUID on round-tripped handle failed: %v", nvml.ErrorString(ret))})
	} else if uuidCheck != uuid {
		results = append(results, testResult{"handle/roundtrip_uuid", false, fmt.Sprintf("UUID mismatch: %q != %q", uuidCheck, uuid)})
	} else {
		results = append(results, testResult{"handle/roundtrip_uuid", true, uuid})
	}

	// Test: index→handle→PCI→handle round-trip
	pciInfo, ret := device.GetPciInfo()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"handle/pci_roundtrip", false, fmt.Sprintf("GetPciInfo failed: %v", nvml.ErrorString(ret))})
		return results
	}

	busId := busIdToString(pciInfo.BusId)
	deviceByPCI, ret := nvml.DeviceGetHandleByPciBusId(busId)
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"handle/pci_to_handle", false, fmt.Sprintf("GetHandleByPciBusId(%q) failed: %v", busId, nvml.ErrorString(ret))})
		return results
	}

	uuidFromPCI, ret := deviceByPCI.GetUUID()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"handle/pci_roundtrip_uuid", false, fmt.Sprintf("GetUUID from PCI handle failed: %v", nvml.ErrorString(ret))})
	} else if uuidFromPCI != uuid {
		results = append(results, testResult{"handle/pci_roundtrip_uuid", false, fmt.Sprintf("UUID mismatch via PCI: %q != %q", uuidFromPCI, uuid)})
	} else {
		results = append(results, testResult{"handle/pci_roundtrip_uuid", true, busId})
	}

	// Test: all device indices produce valid handles
	for i := 0; i < deviceCount; i++ {
		dev, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			results = append(results, testResult{fmt.Sprintf("handle/index_%d", i), false, fmt.Sprintf("failed: %v", nvml.ErrorString(ret))})
			continue
		}
		u, ret := dev.GetUUID()
		if ret != nvml.SUCCESS || u == "" {
			results = append(results, testResult{fmt.Sprintf("handle/index_%d_uuid", i), false, "handle returned but UUID empty/failed"})
		} else {
			results = append(results, testResult{fmt.Sprintf("handle/index_%d", i), true, u})
		}
	}

	return results
}

// --- Error string tests ---

func testErrorStrings() []testResult {
	var results []testResult

	// Test: common error codes return non-empty, human-readable strings
	codes := []struct {
		code nvml.Return
		name string
	}{
		{nvml.SUCCESS, "SUCCESS"},
		{nvml.ERROR_UNINITIALIZED, "UNINITIALIZED"},
		{nvml.ERROR_INVALID_ARGUMENT, "INVALID_ARGUMENT"},
		{nvml.ERROR_NOT_SUPPORTED, "NOT_SUPPORTED"},
		{nvml.ERROR_NOT_FOUND, "NOT_FOUND"},
		{nvml.ERROR_FUNCTION_NOT_FOUND, "FUNCTION_NOT_FOUND"},
	}

	for _, c := range codes {
		str := nvml.ErrorString(c.code)
		if str == "" {
			results = append(results, testResult{fmt.Sprintf("errstr/%s", c.name), false, "empty error string"})
		} else {
			results = append(results, testResult{fmt.Sprintf("errstr/%s", c.name), true, str})
		}
	}

	// Test: repeated calls return the same string (caching behavior)
	str1 := nvml.ErrorString(nvml.SUCCESS)
	str2 := nvml.ErrorString(nvml.SUCCESS)
	if str1 != str2 {
		results = append(results, testResult{"errstr/cache_consistency", false, fmt.Sprintf("%q != %q", str1, str2)})
	} else {
		results = append(results, testResult{"errstr/cache_consistency", true, ""})
	}

	return results
}

// --- Invalid index / boundary tests ---

func testInvalidIndices(deviceCount int) []testResult {
	var results []testResult

	// Test: out-of-range index returns error (not crash)
	_, ret := nvml.DeviceGetHandleByIndex(deviceCount) // one past the end
	if ret == nvml.SUCCESS {
		results = append(results, testResult{"boundary/index_oob", false, "expected error for out-of-range index, got SUCCESS"})
	} else {
		results = append(results, testResult{"boundary/index_oob", true, fmt.Sprintf("correctly returned: %v", nvml.ErrorString(ret))})
	}

	// Test: very large index
	_, ret = nvml.DeviceGetHandleByIndex(99999)
	if ret == nvml.SUCCESS {
		results = append(results, testResult{"boundary/index_huge", false, "expected error for index 99999, got SUCCESS"})
	} else {
		results = append(results, testResult{"boundary/index_huge", true, fmt.Sprintf("correctly returned: %v", nvml.ErrorString(ret))})
	}

	// Test: invalid UUID
	_, ret = nvml.DeviceGetHandleByUUID("GPU-00000000-0000-0000-0000-000000000000")
	if ret == nvml.SUCCESS {
		results = append(results, testResult{"boundary/uuid_invalid", false, "expected error for fake UUID, got SUCCESS"})
	} else {
		results = append(results, testResult{"boundary/uuid_invalid", true, fmt.Sprintf("correctly returned: %v", nvml.ErrorString(ret))})
	}

	// Test: invalid PCI Bus ID
	_, ret = nvml.DeviceGetHandleByPciBusId("0000:FF:FF.F")
	if ret == nvml.SUCCESS {
		results = append(results, testResult{"boundary/pci_invalid", false, "expected error for fake PCI Bus ID, got SUCCESS"})
	} else {
		results = append(results, testResult{"boundary/pci_invalid", true, fmt.Sprintf("correctly returned: %v", nvml.ErrorString(ret))})
	}

	return results
}

// --- Init/Shutdown cycle tests ---

func testInitShutdownCycles() []testResult {
	var results []testResult

	// We're currently initialized. Shut down, then re-init to test the cycle.
	ret := nvml.Shutdown()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"lifecycle/shutdown", false, fmt.Sprintf("Shutdown failed: %v", nvml.ErrorString(ret))})
		// Try to re-init anyway
		nvml.Init()
		return results
	}

	// Test: after shutdown, operations should fail
	_, ret = nvml.DeviceGetCount()
	if ret == nvml.SUCCESS {
		results = append(results, testResult{"lifecycle/after_shutdown", false, "DeviceGetCount succeeded after Shutdown"})
	} else {
		results = append(results, testResult{"lifecycle/after_shutdown", true, fmt.Sprintf("correctly returned: %v", nvml.ErrorString(ret))})
	}

	// Test: re-init succeeds
	ret = nvml.Init()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"lifecycle/reinit", false, fmt.Sprintf("Re-Init failed: %v", nvml.ErrorString(ret))})
		return results
	}
	results = append(results, testResult{"lifecycle/reinit", true, ""})

	// Test: after re-init, devices are accessible again
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		results = append(results, testResult{"lifecycle/reinit_devices", false, fmt.Sprintf("DeviceGetCount after re-Init failed: %v", nvml.ErrorString(ret))})
	} else if count == 0 {
		results = append(results, testResult{"lifecycle/reinit_devices", false, "no devices after re-Init"})
	} else {
		results = append(results, testResult{"lifecycle/reinit_devices", true, fmt.Sprintf("%d devices", count)})
	}

	return results
}

// runBridgeTests executes all bridge tests and reports results.
// Returns the number of failures.
func runBridgeTests(deviceCount int) int {
	log.Println("\n=== Bridge Edge-Case Tests ===")

	results := bridgeTests(deviceCount)
	failures := 0

	for _, r := range results {
		if r.passed {
			if r.detail != "" {
				log.Printf("  PASS  %s (%s)", r.name, r.detail)
			} else {
				log.Printf("  PASS  %s", r.name)
			}
		} else {
			log.Printf("  FAIL  %s: %s", r.name, r.detail)
			failures++
		}
	}

	log.Printf("\n=== Bridge Tests: %d passed, %d failed ===", len(results)-failures, failures)
	return failures
}
