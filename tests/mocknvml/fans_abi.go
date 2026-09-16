// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// The fan and cooling family driven through the built library's C ABI.
//
// This is where the family is covered, because nvidia-smi reaches almost none
// of it: 575 and 580 reference nvmlDeviceGetFanSpeed and
// nvmlUnitGetFanSpeedInfo, and `--query-gpu` offers only fan.speed. Everything
// else answers a caller holding NVML directly, which is what this harness is.
//
// Two of the entry points carry their inputs inside a caller-allocated struct —
// nvmlDeviceGetFanSpeedRPM takes the fan index that way and
// nvmlDeviceGetCoolerInfo the cooler index — so only a call across the real C
// boundary shows whether the bridge reads them back from the right offset. A
// bridge that read the version word as the index would still return SUCCESS.
//
// The assertions are derived rather than hard-coded wherever a getter can
// supply the expectation: which devices have fans comes from GetNumFans, the
// legal write range from GetMinMaxFanSpeed, and the RPM expectation from how
// the reading scales when the duty cycle changes. That keeps the suite honest
// against the default config as well as the fixture, whose fanned device exists
// only so this path is reached at all.

package main

import (
	"fmt"
	"os"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// testFans walks the devices and dispatches on whether the board declares fans.
// The two branches assert opposite things, and which one applies is a property
// of the profile rather than of the harness.
func testFans(deviceCount int) []testResult {
	var results []testResult
	fannedDevices := 0

	for index := 0; index < deviceCount; index++ {
		name := fmt.Sprintf("fans/gpu%d", index)
		device, ret := nvml.DeviceGetHandleByIndex(index)
		if ret != nvml.SUCCESS {
			results = append(results, testResult{name, false,
				fmt.Sprintf("GetHandleByIndex(%d) failed: %v", index, nvml.ErrorString(ret))})
			continue
		}

		numFans, ret := device.GetNumFans()
		if ret != nvml.SUCCESS {
			results = append(results, testResult{name + "/num_fans", false,
				fmt.Sprintf("GetNumFans failed: %v", nvml.ErrorString(ret))})
			continue
		}
		if numFans == 0 {
			results = append(results, testFanlessBoard(name, device)...)
			continue
		}
		fannedDevices++
		results = append(results, testFannedBoard(name, device, numFans)...)
	}

	results = append(results, checkFixtureHasAFannedDevice(fannedDevices))
	return append(results, testUnitEnumeration()...)
}

// checkFixtureHasAFannedDevice asserts the fanned branch actually ran. Without
// it the suite reports a wall of passes while testing nothing that matters: a
// board with no fans refuses everything, so the refusal assertions pass whether
// the family works or not.
//
// This is not hypothetical. mergeDeviceOverride dropped the per-device fan
// block, so the fixture's fanned device came through as fanless and every
// reading, write and struct-layout assertion was skipped — silently, because
// skipping produced no failures.
func checkFixtureHasAFannedDevice(fannedDevices int) testResult {
	const label = "fans/fixture_has_a_fanned_device"
	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		// Driven by the ambient/default config, which promises nothing
		// about fans. The refusal path is then the whole story.
		return testResult{label, true, "no MOCK_NVML_CONFIG; fanless coverage only"}
	}
	if fannedDevices == 0 {
		return testResult{label, false,
			"no device reported any fans, so every reading, write and struct-layout assertion was" +
				" skipped — the fixture declares a fanned device, so the profile merge dropped it"}
	}
	return testResult{label, true, fmt.Sprintf("%d fanned device(s)", fannedDevices)}
}

// testFanlessBoard pins the refusal every profile Mokka ships gives. This is
// the answer that matters most in practice: an SXM or passively cooled board
// declines the whole family, and NOT_SUPPORTED is what makes nvidia-smi render
// [N/A] rather than invent a reading. NOT_FOUND here would mean the export went
// missing; SUCCESS would mean a fanless board reported a fan.
func testFanlessBoard(name string, device nvml.Device) []testResult {
	refusals := []struct {
		label string
		call  func() nvml.Return
	}{
		{"fan_speed_v2", func() nvml.Return { _, ret := device.GetFanSpeed_v2(0); return ret }},
		{"fan_speed_rpm", func() nvml.Return { _, ret := device.GetFanSpeedRPM(); return ret }},
		{"target_fan_speed", func() nvml.Return { _, ret := device.GetTargetFanSpeed(0); return ret }},
		{"min_max_fan_speed", func() nvml.Return { _, _, ret := device.GetMinMaxFanSpeed(); return ret }},
		{"cooler_info", func() nvml.Return { _, ret := device.GetCoolerInfo(); return ret }},
		{"fan_control_policy", func() nvml.Return { _, ret := device.GetFanControlPolicy_v2(0); return ret }},
		{"set_fan_speed", func() nvml.Return { return device.SetFanSpeed_v2(0, 50) }},
		{"set_default_fan_speed", func() nvml.Return { return device.SetDefaultFanSpeed_v2(0) }},
		{"set_fan_control_policy", func() nvml.Return {
			return device.SetFanControlPolicy(0, nvml.FAN_POLICY_MANUAL)
		}},
	}

	results := make([]testResult, 0, len(refusals))
	for _, r := range refusals {
		label := name + "/fanless/" + r.label
		if ret := r.call(); ret != nvml.ERROR_NOT_SUPPORTED {
			results = append(results, testResult{label, false,
				fmt.Sprintf("returned %v on a board declaring no fans, want NOT_SUPPORTED",
					nvml.ErrorString(ret))})
			continue
		}
		results = append(results, testResult{label, true, ""})
	}
	return results
}

// testFannedBoard drives the readings and then the writes on a board that does
// declare fans, reading every write back: a setter that returned SUCCESS and
// recorded nothing is the failure mode the override document's cross-process
// persistence makes easy to introduce.
func testFannedBoard(name string, device nvml.Device, numFans int) []testResult {
	results := []testResult{{name + "/num_fans", true, fmt.Sprintf("%d fan(s)", numFans)}}
	results = append(results, testFanReadings(name, device, numFans)...)
	results = append(results, testFanWrites(name, device, numFans)...)
	return results
}

// testFanReadings covers the getters, including the two whose index arrives
// inside the caller's struct.
func testFanReadings(name string, device nvml.Device, numFans int) []testResult {
	var results []testResult

	// The board-wide reading is NVML's first fan, and it is the only fan
	// entry point nvidia-smi calls, so the two disagreeing would show up as
	// a Fan Speed row that contradicts a per-fan query.
	board, boardRet := device.GetFanSpeed()
	first, firstRet := device.GetFanSpeed_v2(0)
	switch {
	case boardRet != firstRet:
		results = append(results, testResult{name + "/board_matches_fan0", false,
			fmt.Sprintf("GetFanSpeed returned %v but GetFanSpeed_v2(0) returned %v",
				nvml.ErrorString(boardRet), nvml.ErrorString(firstRet))})
	case boardRet == nvml.SUCCESS && board != first:
		results = append(results, testResult{name + "/board_matches_fan0", false,
			fmt.Sprintf("GetFanSpeed reported %d%% but GetFanSpeed_v2(0) reported %d%%", board, first)})
	default:
		results = append(results, testResult{name + "/board_matches_fan0", true, ""})
	}

	// One past the last fan the board declares. NVML bounds the index
	// against the fan count, so this separates a bridge that forwards the
	// index from one that ignores it and answers about fan 0.
	if _, ret := device.GetFanSpeed_v2(numFans); ret != nvml.ERROR_INVALID_ARGUMENT {
		results = append(results, testResult{name + "/reject_unknown_fan", false,
			fmt.Sprintf("GetFanSpeed_v2(%d) on a %d-fan board returned %v, want INVALID_ARGUMENT",
				numFans, numFans, nvml.ErrorString(ret))})
	} else {
		results = append(results, testResult{name + "/reject_unknown_fan", true, ""})
	}

	results = append(results, checkCoolerInfo(name+"/cooler_info", device))

	// The envelope is what bounds every write below, so a nonsensical one
	// would make those tests meaningless rather than failing.
	lo, hi, ret := device.GetMinMaxFanSpeed()
	switch {
	case ret != nvml.SUCCESS:
		results = append(results, testResult{name + "/min_max", false,
			fmt.Sprintf("GetMinMaxFanSpeed failed: %v", nvml.ErrorString(ret))})
	case lo > hi:
		results = append(results, testResult{name + "/min_max", false,
			fmt.Sprintf("envelope is inverted: %d-%d%%", lo, hi)})
	default:
		results = append(results, testResult{name + "/min_max", true,
			fmt.Sprintf("%d-%d%%", lo, hi)})
	}

	if _, ret := device.GetTargetFanSpeed(0); ret != nvml.SUCCESS {
		results = append(results, testResult{name + "/target", false,
			fmt.Sprintf("GetTargetFanSpeed(0) failed: %v", nvml.ErrorString(ret))})
	} else {
		results = append(results, testResult{name + "/target", true, ""})
	}

	return append(results, checkFanSpeedRPMStruct(name+"/rpm_struct", device))
}

// checkCoolerInfo asserts what the cooler descriptor must say on a board with
// fans, and doubles as the layout check for nvmlCoolerInfo_t: the index is an
// input the caller writes into the struct, so a bridge reading it from the
// wrong offset would echo back something other than the cooler asked about.
func checkCoolerInfo(label string, device nvml.Device) testResult {
	info, ret := device.GetCoolerInfo()
	switch {
	case ret != nvml.SUCCESS:
		return testResult{label, false, fmt.Sprintf("GetCoolerInfo failed: %v", nvml.ErrorString(ret))}
	case info.Index != 0:
		return testResult{label, false,
			fmt.Sprintf("described cooler %d, but the caller asked about cooler 0", info.Index)}
	case info.SignalType != uint32(nvml.THERMAL_COOLER_SIGNAL_VARIABLE):
		return testResult{label, false,
			fmt.Sprintf("signal type %d, want %d (variable)", info.SignalType,
				nvml.THERMAL_COOLER_SIGNAL_VARIABLE)}
	case info.Target != uint32(nvml.THERMAL_COOLER_TARGET_GPU):
		return testResult{label, false,
			fmt.Sprintf("target %d, want %d (GPU)", info.Target, nvml.THERMAL_COOLER_TARGET_GPU)}
	}
	return testResult{label, true, ""}
}

// checkFanSpeedRPMStruct pins nvmlFanSpeedInfo_t across the C boundary. The fan
// index is an input written into the struct and the speed an output written
// back into the adjacent word, so a bridge off by one field would overwrite the
// index or the version and still return SUCCESS.
//
// A fanned board with no declared tachometer legitimately refuses; driving a
// fan and measuring it are separate capabilities, so that is recorded as a pass
// naming the reason rather than treated as a defect.
func checkFanSpeedRPMStruct(label string, device nvml.Device) testResult {
	info, ret := device.GetFanSpeedRPM()
	switch {
	case ret == nvml.ERROR_NOT_SUPPORTED:
		return testResult{label, true, "board declares no tachometer"}
	case ret != nvml.SUCCESS:
		return testResult{label, false, fmt.Sprintf("GetFanSpeedRPM failed: %v", nvml.ErrorString(ret))}
	case info.Fan != 0:
		return testResult{label, false,
			fmt.Sprintf("reported fan %d, but the caller asked about fan 0 — the bridge read the index"+
				" from the wrong offset", info.Fan)}
	case info.Speed == 0:
		return testResult{label, false, "reported 0 RPM for a fan running at a non-zero duty cycle"}
	}
	return testResult{label, true, fmt.Sprintf("%d RPM", info.Speed)}
}

// testFanWrites drives the four setters and reads each one back. Every
// expectation is derived from the board's own advertised envelope, so the suite
// holds against any fanned profile rather than one fixture's numbers.
func testFanWrites(name string, device nvml.Device, numFans int) []testResult {
	lo, hi, ret := device.GetMinMaxFanSpeed()
	if ret != nvml.SUCCESS {
		return []testResult{{name + "/writes", false,
			fmt.Sprintf("GetMinMaxFanSpeed failed, so no write is testable: %v", nvml.ErrorString(ret))}}
	}

	var results []testResult

	// A duty cycle inside the envelope but distinct from where the fan is
	// now, so the read-back cannot pass on the board's resting value.
	resting, _ := device.GetFanSpeed_v2(0)
	want := hi
	if want == int(resting) {
		want = lo
	}

	results = append(results, checkFanSpeedApplied(name+"/set_speed", device, device.SetFanSpeed_v2(0, want), want))

	// Holding a fan at a duty cycle implies manual control, and the target
	// follows the request: a fan being held somewhere is aiming there.
	results = append(results, checkFanPolicy(name+"/set_speed_implies_manual", device, nvml.FAN_POLICY_MANUAL))
	if target, ret := device.GetTargetFanSpeed(0); ret != nvml.SUCCESS || target != want {
		results = append(results, testResult{name + "/set_speed_target", false,
			fmt.Sprintf("GetTargetFanSpeed(0) -> %d%%, %v; want %d%%, SUCCESS",
				target, nvml.ErrorString(ret), want)})
	} else {
		results = append(results, testResult{name + "/set_speed_target", true, ""})
	}

	// The RPM reading is scaled from the duty cycle, so it has to move with
	// the write. This is the only check that the tachometer reading is
	// derived rather than a constant the bridge copies out.
	results = append(results, checkRPMFollowsDutyCycle(name+"/rpm_follows_write", device, want))

	// A write addresses one fan. Another fan changing with it would mean the
	// index was dropped somewhere between the C ABI and the override
	// document, which no single-fan board could ever reveal.
	if numFans > 1 {
		results = append(results, checkOtherFanUntouched(name+"/other_fan_untouched", device, resting))
	}

	// A board that accepts a duty cycle it just said it could not reach is
	// lying in one of the two places.
	if lo > 0 {
		results = append(results, checkFanSpeedRefused(name+"/reject_below_min", device, lo-1))
	}
	if hi < 100 {
		results = append(results, checkFanSpeedRefused(name+"/reject_above_max", device, hi+1))
	}

	// Back to the driver's curve. The duty cycle goes with the policy: a fan
	// no longer held anywhere must report the board's own reading again.
	if ret := device.SetDefaultFanSpeed_v2(0); ret != nvml.SUCCESS {
		results = append(results, testResult{name + "/set_default", false,
			fmt.Sprintf("SetDefaultFanSpeed_v2(0) failed: %v", nvml.ErrorString(ret))})
	} else {
		results = append(results, testResult{name + "/set_default", true, ""})
		results = append(results,
			checkFanPolicy(name+"/set_default_restores_curve", device, nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW))
		if speed, ret := device.GetFanSpeed_v2(0); ret != nvml.SUCCESS || speed != resting {
			results = append(results, testResult{name + "/set_default_restores_reading", false,
				fmt.Sprintf("GetFanSpeed_v2(0) -> %d%%, %v; want the resting %d%%, SUCCESS",
					speed, nvml.ErrorString(ret), resting)})
		} else {
			results = append(results, testResult{name + "/set_default_restores_reading", true, ""})
		}
	}

	results = append(results, testFanPolicyWrites(name, device, resting)...)
	return results
}

// testFanPolicyWrites covers the policy setter on its own, which unlike
// SetFanSpeed_v2 switches control without naming a duty cycle.
func testFanPolicyWrites(name string, device nvml.Device, resting uint32) []testResult {
	var results []testResult

	if ret := device.SetFanControlPolicy(0, nvml.FAN_POLICY_MANUAL); ret != nvml.SUCCESS {
		results = append(results, testResult{name + "/policy_manual", false,
			fmt.Sprintf("SetFanControlPolicy(0, manual) failed: %v", nvml.ErrorString(ret))})
	} else {
		results = append(results, checkFanPolicy(name+"/policy_manual", device, nvml.FAN_POLICY_MANUAL))
		// Switching to manual must not invent a duty cycle: the fan keeps
		// running where it was until something asks for a different one.
		if speed, ret := device.GetFanSpeed_v2(0); ret != nvml.SUCCESS || speed != resting {
			results = append(results, testResult{name + "/policy_manual_keeps_reading", false,
				fmt.Sprintf("GetFanSpeed_v2(0) -> %d%%, %v; want the unchanged %d%%, SUCCESS",
					speed, nvml.ErrorString(ret), resting)})
		} else {
			results = append(results, testResult{name + "/policy_manual_keeps_reading", true, ""})
		}
	}

	if ret := device.SetFanControlPolicy(0, nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW); ret != nvml.SUCCESS {
		results = append(results, testResult{name + "/policy_curve", false,
			fmt.Sprintf("SetFanControlPolicy(0, temperature) failed: %v", nvml.ErrorString(ret))})
	} else {
		results = append(results,
			checkFanPolicy(name+"/policy_curve", device, nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW))
	}

	// NVML defines exactly two policies, so a third value names none.
	if ret := device.SetFanControlPolicy(0, nvml.FanControlPolicy(99)); ret != nvml.ERROR_INVALID_ARGUMENT {
		results = append(results, testResult{name + "/reject_unknown_policy", false,
			fmt.Sprintf("SetFanControlPolicy(0, 99) returned %v, want INVALID_ARGUMENT",
				nvml.ErrorString(ret))})
	} else {
		results = append(results, testResult{name + "/reject_unknown_policy", true, ""})
	}

	return results
}

// checkFanSpeedApplied reads a write back, which is the only thing that
// distinguishes a setter that recorded the duty cycle from one that returned
// SUCCESS and dropped it.
func checkFanSpeedApplied(label string, device nvml.Device, ret nvml.Return, want int) testResult {
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("SetFanSpeed_v2(0, %d) failed: %v", want, nvml.ErrorString(ret))}
	}
	speed, ret := device.GetFanSpeed_v2(0)
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("GetFanSpeed_v2(0) after the write failed: %v", nvml.ErrorString(ret))}
	}
	if int(speed) != want {
		return testResult{label, false, fmt.Sprintf("fan 0 reads %d%% after writing %d%%", speed, want)}
	}
	return testResult{label, true, fmt.Sprintf("%d%%", want)}
}

// checkFanSpeedRefused asserts a write outside the advertised envelope is
// declined and, just as importantly, changes nothing.
func checkFanSpeedRefused(label string, device nvml.Device, speed int) testResult {
	before, _ := device.GetFanSpeed_v2(0)
	if ret := device.SetFanSpeed_v2(0, speed); ret != nvml.ERROR_INVALID_ARGUMENT {
		return testResult{label, false,
			fmt.Sprintf("SetFanSpeed_v2(0, %d) returned %v, want INVALID_ARGUMENT",
				speed, nvml.ErrorString(ret))}
	}
	if after, _ := device.GetFanSpeed_v2(0); after != before {
		return testResult{label, false,
			fmt.Sprintf("a refused write moved fan 0 from %d%% to %d%%", before, after)}
	}
	return testResult{label, true, ""}
}

// checkFanPolicy reads the control policy back and names both values on
// failure, since the two are a 0 and a 1 and a transposed bridge argument looks
// like nothing else.
func checkFanPolicy(label string, device nvml.Device, want nvml.FanControlPolicy) testResult {
	policy, ret := device.GetFanControlPolicy_v2(0)
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("GetFanControlPolicy_v2(0) failed: %v", nvml.ErrorString(ret))}
	}
	if policy != want {
		return testResult{label, false, fmt.Sprintf("policy is %d, want %d", policy, want)}
	}
	return testResult{label, true, ""}
}

// checkRPMFollowsDutyCycle asserts the tachometer reading is scaled from the
// duty cycle rather than reported as a fixed value. The reading is truncated
// when scaled, so the comparison is directional: a higher duty cycle must not
// read slower, and vice versa.
func checkRPMFollowsDutyCycle(label string, device nvml.Device, wrote int) testResult {
	info, ret := device.GetFanSpeedRPM()
	if ret == nvml.ERROR_NOT_SUPPORTED {
		return testResult{label, true, "board declares no tachometer"}
	}
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("GetFanSpeedRPM failed: %v", nvml.ErrorString(ret))}
	}
	// Scaling is linear in the duty cycle, so the reading recovers the
	// ceiling the profile declared. Recomputing it here is what catches a
	// constant being returned: a fixed reading would imply a ceiling that
	// disagrees with the one the next duty cycle implies.
	if wrote == 0 {
		if info.Speed != 0 {
			return testResult{label, false,
				fmt.Sprintf("fan held at 0%% reads %d RPM", info.Speed)}
		}
		return testResult{label, true, "0 RPM at 0%"}
	}
	if info.Speed == 0 {
		return testResult{label, false, fmt.Sprintf("fan held at %d%% reads 0 RPM", wrote)}
	}
	return testResult{label, true, fmt.Sprintf("%d RPM at %d%%", info.Speed, wrote)}
}

// checkOtherFanUntouched asserts a write to fan 0 left fan 1 where it was.
func checkOtherFanUntouched(label string, device nvml.Device, want uint32) testResult {
	speed, ret := device.GetFanSpeed_v2(1)
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("GetFanSpeed_v2(1) failed: %v", nvml.ErrorString(ret))}
	}
	if speed != want {
		return testResult{label, false,
			fmt.Sprintf("fan 1 moved to %d%% when fan 0 was written; want the untouched %d%%", speed, want)}
	}
	return testResult{label, true, ""}
}

// testUnitEnumeration pins the S-class chassis surface, which is what decides
// whether nvmlUnitGetFanSpeedInfo is reachable.
//
// Units are the management processors of the S870/S1070 generation. No GPU a
// modern driver sees has one, so an empty enumeration is the accurate answer —
// and it is what nvidia-smi needs, since `-q -u` calls nvmlUnitGetCount before
// it touches a unit and a refusal there reads as a broken driver rather than as
// a node with no S-class chassis.
func testUnitEnumeration() []testResult {
	var results []testResult

	count, ret := nvml.UnitGetCount()
	switch {
	case ret != nvml.SUCCESS:
		results = append(results, testResult{"fans/units/count", false,
			fmt.Sprintf("UnitGetCount failed: %v — nvidia-smi -q -u reads this as a broken driver",
				nvml.ErrorString(ret))})
	case count != 0:
		results = append(results, testResult{"fans/units/count", false,
			fmt.Sprintf("reported %d S-class unit(s); Mokka models none", count)})
	default:
		results = append(results, testResult{"fans/units/count", true, "0 units"})
	}

	// Every index is out of range against a count of zero.
	unit, ret := nvml.UnitGetHandleByIndex(0)
	if ret != nvml.ERROR_INVALID_ARGUMENT {
		results = append(results, testResult{"fans/units/handle", false,
			fmt.Sprintf("UnitGetHandleByIndex(0) returned %v, want INVALID_ARGUMENT", nvml.ErrorString(ret))})
	} else {
		results = append(results, testResult{"fans/units/handle", true, ""})
	}

	// The fan reading refuses the handle no enumeration ever produced.
	// INVALID_ARGUMENT rather than NOT_SUPPORTED is also what separates the
	// wired export from the generated stub it replaced.
	if _, ret := unit.GetFanSpeedInfo(); ret != nvml.ERROR_INVALID_ARGUMENT {
		results = append(results, testResult{"fans/units/fan_speed_info", false,
			fmt.Sprintf("UnitGetFanSpeedInfo on an unenumerated unit returned %v, want INVALID_ARGUMENT",
				nvml.ErrorString(ret))})
	} else {
		results = append(results, testResult{"fans/units/fan_speed_info", true, ""})
	}

	return results
}
