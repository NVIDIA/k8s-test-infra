// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <string.h>

// Layout of nvmlDevicePowerMizerModes_v1_t from nvml.h.
typedef struct {
    unsigned int currentMode;
    unsigned int mode;
    unsigned int supportedPowerMizerModes;
} mockPowerMizerModes_v1;

#define MOCK_MIZER_NO_LIB    0xFFFFFFFFu
#define MOCK_MIZER_NO_SYMBOL 0xFFFFFFFEu

// mockSetPowerMizerMode calls the setter by symbol, the same way
// mockWorkloadUpdateProfiles does, because the pinned go-nvml binds only the
// getter.
static unsigned int mockSetPowerMizerMode(unsigned int index, unsigned int mode) {
    void* lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_GLOBAL);
    if (lib == NULL) {
        return MOCK_MIZER_NO_LIB;
    }
    unsigned int (*getHandle)(unsigned int, void**) =
        (unsigned int (*)(unsigned int, void**))dlsym(lib, "nvmlDeviceGetHandleByIndex_v2");
    unsigned int (*set)(void*, mockPowerMizerModes_v1*) =
        (unsigned int (*)(void*, mockPowerMizerModes_v1*))dlsym(lib, "nvmlDeviceSetPowerMizerMode_v1");
    if (getHandle == NULL || set == NULL) {
        return MOCK_MIZER_NO_SYMBOL;
    }
    void* device = NULL;
    unsigned int ret = getHandle(index, &device);
    if (ret != 0) {
        return ret;
    }
    mockPowerMizerModes_v1 req;
    memset(&req, 0, sizeof(req));
    req.mode = mode;
    return set(device, &req);
}
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// setPowerMizerModeRaw maps the two resolution failures onto errors, so a
// missing export is not reported as an NVML return code it never produced.
func setPowerMizerModeRaw(index int, mode uint32) (nvml.Return, error) {
	ret := C.mockSetPowerMizerMode(C.uint(index), C.uint(mode))
	switch uint32(ret) {
	case 0xFFFFFFFF:
		return nvml.ERROR_UNKNOWN, errors.New("dlopen libnvidia-ml.so.1 failed")
	case 0xFFFFFFFE:
		return nvml.ERROR_UNKNOWN, errors.New("nvmlDeviceSetPowerMizerMode_v1 is not exported by the built library")
	}
	return nvml.Return(ret), nil
}

// smoothingDevice is the fixture's one GPU that declares
// power.smoothing.supported. The fixture is an A100, so every other device
// must refuse power smoothing, and the two together cover both answers.
const smoothingDevice = 5

// allPowerMizerModes is the supported-modes mask: one bit per
// NVML_POWER_MIZER_MODE_* value, all four of which the mock accepts.
const allPowerMizerModes = 1<<nvml.POWER_MIZER_MODE_ADAPTIVE |
	1<<nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE |
	1<<nvml.POWER_MIZER_MODE_AUTO |
	1<<nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE

// testPowerPolicy drives the power source, PowerMizer and power smoothing
// entry points through the built library.
//
// Power smoothing has setters but no getter, so every write is read back
// through nvmlDeviceGetFieldValues, the path nvidia-smi takes. The field ids
// come from this module's go-nvml rather than from the engine, so a mock that
// numbered a field differently from the header the simulated driver ships
// fails here.
func testPowerPolicy(deviceCount int) []testResult {
	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		// The default config is a pre-580 driver, which has none of these.
		return []testResult{skippedResult("powerpolicy", "no MOCK_NVML_CONFIG")}
	}
	if deviceCount <= smoothingDevice {
		return []testResult{{"powerpolicy/fixture", false, fmt.Sprintf(
			"%d devices, want at least %d: the fixture declares power smoothing on device %d",
			deviceCount, smoothingDevice+1, smoothingDevice)}}
	}
	plain, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		return []testResult{{"powerpolicy/gpu0", false, "GetHandleByIndex(0): " + nvml.ErrorString(ret)}}
	}
	smoothing, ret := nvml.DeviceGetHandleByIndex(smoothingDevice)
	if ret != nvml.SUCCESS {
		return []testResult{{fmt.Sprintf("powerpolicy/gpu%d", smoothingDevice), false,
			fmt.Sprintf("GetHandleByIndex(%d): %s", smoothingDevice, nvml.ErrorString(ret))}}
	}

	results := []testResult{checkPowerSource("powerpolicy/gpu0/power_source", plain)}
	results = append(results, testPowerMizer("powerpolicy/gpu0/powermizer", 0, plain)...)
	results = append(results, testPowerSmoothingRefused("powerpolicy/gpu0/smoothing", plain)...)
	results = append(results, testPowerSmoothing(fmt.Sprintf("powerpolicy/gpu%d/smoothing", smoothingDevice),
		smoothing)...)
	return results
}

func checkPowerSource(label string, device nvml.Device) testResult {
	source, ret := device.GetPowerSource()
	if ret != nvml.SUCCESS || source != nvml.POWER_SOURCE_AC {
		return testResult{label, false, fmt.Sprintf("GetPowerSource -> %d, %s; want AC (%d), SUCCESS",
			source, nvml.ErrorString(ret), nvml.POWER_SOURCE_AC)}
	}
	return testResult{label, true, ""}
}

// testPowerMizer reads the default mode, writes another, checks an undefined
// mode is refused without disturbing it, and restores the default. The write
// goes in through the raw struct and comes back through go-nvml, so a mode
// read from the wrong member of the struct shows up as a wrong read-back.
func testPowerMizer(name string, index int, device nvml.Device) []testResult {
	results := []testResult{checkPowerMizerMode(name+"/default", device, nvml.POWER_MIZER_MODE_AUTO)}
	set := func(label string, mode uint32, want nvml.Return) {
		ret, err := setPowerMizerModeRaw(index, mode)
		if err != nil {
			results = append(results, testResult{label, false, err.Error()})
			return
		}
		results = append(results, checkReturn(label, ret, want))
	}

	set(name+"/set", nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE, nvml.SUCCESS)
	results = append(results,
		checkPowerMizerMode(name+"/read_back", device, nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE))
	set(name+"/reject_undefined", nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE+1, nvml.ERROR_INVALID_ARGUMENT)
	results = append(results,
		checkPowerMizerMode(name+"/kept_after_reject", device, nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE))
	set(name+"/restore", nvml.POWER_MIZER_MODE_AUTO, nvml.SUCCESS)
	return results
}

func checkPowerMizerMode(label string, device nvml.Device, want uint32) testResult {
	modes, ret := device.GetPowerMizerMode_v1()
	if ret != nvml.SUCCESS {
		return testResult{label, false, "GetPowerMizerMode_v1: " + nvml.ErrorString(ret)}
	}
	if modes.CurrentMode != want || modes.SupportedPowerMizerModes != allPowerMizerModes {
		return testResult{label, false, fmt.Sprintf("current mode %d, supported %#x; want %d, %#x",
			modes.CurrentMode, modes.SupportedPowerMizerModes, want, allPowerMizerModes)}
	}
	return testResult{label, true, ""}
}

// testPowerSmoothingRefused pins the answer an A100 gives: NOT_SUPPORTED from
// the setters and from the fields, which nvidia-smi renders as N/A.
func testPowerSmoothingRefused(name string, device nvml.Device) []testResult {
	ret := device.PowerSmoothingSetState(&nvml.PowerSmoothingState{State: uint32(nvml.FEATURE_ENABLED)})
	_, fieldRet := readField(device, nvml.FI_PWR_SMOOTHING_ENABLED, 0)
	return []testResult{
		checkReturn(name+"/set_state", ret, nvml.ERROR_NOT_SUPPORTED),
		checkReturn(name+"/enabled_field", fieldRet, nvml.ERROR_NOT_SUPPORTED),
	}
}

// testPowerSmoothing drives the three setters on the device that models the
// feature and restores the fixture's state afterwards. Tuning preset 2 rather
// than the active preset 0 is what shows the scope id selects the preset: a
// field that ignored it would read back 0.
func testPowerSmoothing(name string, device nvml.Device) []testResult {
	const preset = 2
	setState := func(state nvml.EnableState) nvml.Return {
		return device.PowerSmoothingSetState(&nvml.PowerSmoothingState{State: uint32(state)})
	}
	activate := func(id uint32) nvml.Return {
		return device.PowerSmoothingActivatePresetProfile(&nvml.PowerSmoothingProfile{ProfileId: id})
	}
	update := func(id, param uint32, value float64) nvml.Return {
		return device.PowerSmoothingUpdatePresetProfileParam(
			&nvml.PowerSmoothingProfile{ProfileId: id, ParamId: param, Value: value})
	}

	results := []testResult{
		checkUintField(name+"/default_disabled", device, nvml.FI_PWR_SMOOTHING_ENABLED, 0, 0),
		checkUintField(name+"/preset_count", device, nvml.FI_PWR_SMOOTHING_MAX_NUM_PRESET_PROFILES, 0, 5),

		checkReturn(name+"/enable", setState(nvml.FEATURE_ENABLED), nvml.SUCCESS),
		checkUintField(name+"/enabled", device, nvml.FI_PWR_SMOOTHING_ENABLED, 0, 1),

		checkReturn(name+"/update_floor",
			update(preset, nvml.POWER_SMOOTHING_PROFILE_PARAM_PERCENT_TMP_FLOOR, 65.5), nvml.SUCCESS),
		checkDoubleField(name+"/floor", device, nvml.FI_PWR_SMOOTHING_PROFILE_PERCENT_TMP_FLOOR, preset, 65.5),
		checkReturn(name+"/update_hysteresis",
			update(preset, nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_DOWN_HYSTERESIS, 250), nvml.SUCCESS),
		checkUintField(name+"/hysteresis", device, nvml.FI_PWR_SMOOTHING_PROFILE_RAMP_DOWN_HYST_VAL, preset, 250),
		checkReturn(name+"/reject_out_of_range_floor",
			update(preset, nvml.POWER_SMOOTHING_PROFILE_PARAM_PERCENT_TMP_FLOOR, 101), nvml.ERROR_INVALID_ARGUMENT),

		checkReturn(name+"/activate", activate(preset), nvml.SUCCESS),
		checkUintField(name+"/active_preset", device, nvml.FI_PWR_SMOOTHING_ACTIVE_PRESET_PROFILE, 0, preset),
		checkReturn(name+"/reject_unknown_preset", activate(7), nvml.ERROR_INVALID_ARGUMENT),
		checkUintField(name+"/kept_after_reject", device, nvml.FI_PWR_SMOOTHING_ACTIVE_PRESET_PROFILE, 0, preset),
	}

	results = append(results,
		checkReturn(name+"/restore_state", setState(nvml.FEATURE_DISABLED), nvml.SUCCESS),
		checkReturn(name+"/restore_preset", activate(0), nvml.SUCCESS),
		checkReturn(name+"/restore_floor",
			update(preset, nvml.POWER_SMOOTHING_PROFILE_PARAM_PERCENT_TMP_FLOOR, 0), nvml.SUCCESS),
		checkReturn(name+"/restore_hysteresis",
			update(preset, nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_DOWN_HYSTERESIS, 0), nvml.SUCCESS),
	)
	return results
}

func readField(device nvml.Device, fieldID, scopeID uint32) (nvml.FieldValue, nvml.Return) {
	values := []nvml.FieldValue{{FieldId: fieldID, ScopeId: scopeID}}
	if ret := device.GetFieldValues(values); ret != nvml.SUCCESS {
		return nvml.FieldValue{}, ret
	}
	return values[0], nvml.Return(values[0].NvmlReturn)
}

func checkUintField(label string, device nvml.Device, fieldID, scopeID, want uint32) testResult {
	fv, ret := readField(device, fieldID, scopeID)
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("field %d: %s", fieldID, nvml.ErrorString(ret))}
	}
	got := binary.NativeEndian.Uint32(fv.Value[:4])
	if nvml.ValueType(fv.ValueType) != nvml.VALUE_TYPE_UNSIGNED_INT || got != want {
		return testResult{label, false, fmt.Sprintf("field %d = %d (value type %d), want %d (UNSIGNED_INT)",
			fieldID, got, fv.ValueType, want)}
	}
	return testResult{label, true, ""}
}

func checkDoubleField(label string, device nvml.Device, fieldID, scopeID uint32, want float64) testResult {
	fv, ret := readField(device, fieldID, scopeID)
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("field %d: %s", fieldID, nvml.ErrorString(ret))}
	}
	got := math.Float64frombits(binary.NativeEndian.Uint64(fv.Value[:]))
	if nvml.ValueType(fv.ValueType) != nvml.VALUE_TYPE_DOUBLE || got != want {
		return testResult{label, false, fmt.Sprintf("field %d = %v (value type %d), want %v (DOUBLE)",
			fieldID, got, fv.ValueType, want)}
	}
	return testResult{label, true, ""}
}

func checkReturn(label string, got, want nvml.Return) testResult {
	if got != want {
		return testResult{label, false, fmt.Sprintf("%s, want %s", nvml.ErrorString(got), nvml.ErrorString(want))}
	}
	return testResult{label, true, ""}
}
