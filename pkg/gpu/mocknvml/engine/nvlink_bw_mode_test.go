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
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
)

// bwFabric builds a single-device fabric from an nvlink config block, the
// same shape c2c_test.go uses.
func bwFabric(t *testing.T, nvlink *NVLinkConfig) *NodeFabric {
	t.Helper()
	return BuildNodeFabric(&Config{
		NumDevices: 1,
		YAMLConfig: &YAMLConfig{NVLink: nvlink},
	})
}

// deviceBwMode is an nvlink block whose bw_mode selects the per-device trio.
func deviceBwMode(supported []uint8, mode *uint8) *NVLinkConfig {
	return &NVLinkConfig{BwMode: &NVLinkBwModeConfig{
		Scope:     NVLinkBwModeScopeDevice,
		Supported: supported,
		Mode:      mode,
	}}
}

func TestNodeFabric_NvlinkBwModeConfig(t *testing.T) {
	t.Parallel()

	mode := uint8(3)
	nvlink := deviceBwMode([]uint8{0, 3}, &mode)
	nvlink.NvleEnabled = true
	f := bwFabric(t, nvlink)

	require.Equal(t, NVLinkBwModeScopeDevice, f.NvlinkBwModeScope(), "scope")
	require.Equal(t, []uint8{0, 3}, f.NvlinkSupportedBwModes(), "supported modes")
	got, ok := f.NvlinkConfiguredBwMode()
	require.True(t, ok, "mode was configured")
	require.Equal(t, uint8(3), got, "configured mode")
	require.True(t, f.NvleEnabled(), "nvle_enabled")
}

// TestNodeFabric_NvlinkBwModeExplicitZero guards the *uint8 mode field: zero
// is a valid FULL bandwidth mode and must not be treated as "unset".
func TestNodeFabric_NvlinkBwModeExplicitZero(t *testing.T) {
	t.Parallel()

	mode := uint8(0)
	f := bwFabric(t, deviceBwMode([]uint8{0, 3}, &mode))

	got, ok := f.NvlinkConfiguredBwMode()
	require.True(t, ok, "explicit mode 0 is configured")
	require.Equal(t, uint8(0), got, "configured mode")
}

// TestNodeFabric_NvlinkBwModeSupportedOnly pins a profile that lists supported
// modes without picking one; downstream must not infer a default from the list.
func TestNodeFabric_NvlinkBwModeSupportedOnly(t *testing.T) {
	t.Parallel()

	f := bwFabric(t, deviceBwMode([]uint8{0, 3}, nil))

	require.Equal(t, []uint8{0, 3}, f.NvlinkSupportedBwModes(), "supported modes")
	_, ok := f.NvlinkConfiguredBwMode()
	require.False(t, ok, "mode configured")
}

// TestNodeFabric_NvlinkBwModeOutsideSupported pins that a self-contradictory
// block degrades instead of being honoured. Reporting a mode the setter
// answers INVALID_ARGUMENT for would have the two APIs disagree about the same
// value, and the mode is an index into nvidia-smi's name table, so an
// out-of-range one renders as an unnamed mode.
func TestNodeFabric_NvlinkBwModeOutsideSupported(t *testing.T) {
	t.Parallel()

	mode := uint8(4)
	f := bwFabric(t, deviceBwMode([]uint8{0, 3}, &mode))

	_, ok := f.NvlinkConfiguredBwMode()
	require.False(t, ok, "a mode outside the supported list must be dropped")
	require.Contains(t, strings.Join(f.Validate(), "\n"), "not in the supported list",
		"the drop has to be reported, or a typo is silent")
}

// TestNodeFabric_NvlinkBwModeAgainstDefaultSupported covers the same check
// where the profile lists no supported modes: the effective list is then the
// default five, which is what the getters answer with.
func TestNodeFabric_NvlinkBwModeAgainstDefaultSupported(t *testing.T) {
	t.Parallel()

	mode := uint8(9)
	f := bwFabric(t, deviceBwMode(nil, &mode))

	_, ok := f.NvlinkConfiguredBwMode()
	require.False(t, ok, "mode 9 is outside the default supported list")
	require.Contains(t, strings.Join(f.Validate(), "\n"), "not in the supported list", "warning")
}

// TestNodeFabric_NvlinkBwModeUnnameable pins a warning for a supported value
// the bundled nvidia-smi has no name for. It stays in the list — the indices
// are the driver's, and a later one may define more — but an operator should
// not have to work out why nvidia-smi prints an unnamed mode.
func TestNodeFabric_NvlinkBwModeUnnameable(t *testing.T) {
	t.Parallel()

	mode := uint8(7)
	f := bwFabric(t, deviceBwMode([]uint8{0, 7}, &mode))

	got, ok := f.NvlinkConfiguredBwMode()
	require.True(t, ok, "a value in the supported list is honoured")
	require.Equal(t, uint8(7), got, "configured mode")
	require.Contains(t, strings.Join(f.Validate(), "\n"), "nvidia-smi can name", "warning")
}

// TestNodeFabric_NvlinkBwModeValidConfigIsSilent keeps the warnings above from
// firing on a well-formed block, which every shipped profile relies on: the
// built-in profile test asserts Validate is empty.
func TestNodeFabric_NvlinkBwModeValidConfigIsSilent(t *testing.T) {
	t.Parallel()

	mode := uint8(3)
	for name, scope := range map[string]NVLinkBwModeScope{
		"system": NVLinkBwModeScopeSystem,
		"device": NVLinkBwModeScopeDevice,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := bwFabric(t, &NVLinkConfig{BwMode: &NVLinkBwModeConfig{
				Scope:     scope,
				Supported: []uint8{0, 3},
				Mode:      &mode,
			}})
			require.Empty(t, f.Validate(), "a valid bw_mode block must warn about nothing")
		})
	}
}

// TestNodeFabric_NvlinkBwModeInvalidScope pins that a block naming neither
// pair turns the surface off and says so. Guessing a pair would have a typo
// silently answer on the wrong set of NVML calls.
func TestNodeFabric_NvlinkBwModeInvalidScope(t *testing.T) {
	t.Parallel()

	for name, scope := range map[string]NVLinkBwModeScope{
		"omitted": "",
		"unknown": "node",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := bwFabric(t, &NVLinkConfig{BwMode: &NVLinkBwModeConfig{
				Scope:     scope,
				Supported: []uint8{0, 3},
			}})
			require.Empty(t, f.NvlinkBwModeScope(), "no pair answers")
			require.Nil(t, f.NvlinkSupportedBwModes(), "supported modes")
			require.Contains(t, strings.Join(f.Validate(), "\n"), "nvlink.bw_mode.scope", "warning")
		})
	}
}

// TestNodeFabric_NvlinkBwModeUnset pins that an absent block reports "not
// configured" rather than a zero value, so the device layer can tell the
// difference between an explicit mode 0 (FULL) and no config at all.
func TestNodeFabric_NvlinkBwModeUnset(t *testing.T) {
	t.Parallel()

	cases := map[string]*NodeFabric{
		"no bw_mode block": bwFabric(t, &NVLinkConfig{}),
		"no nvlink block":  bwFabric(t, nil),
		"nil fabric":       (*NodeFabric)(nil),
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Empty(t, f.NvlinkBwModeScope(), "scope")
			require.Nil(t, f.NvlinkSupportedBwModes(), "supported modes")
			_, ok := f.NvlinkConfiguredBwMode()
			require.False(t, ok, "mode configured")
			require.False(t, f.NvleEnabled(), "nvle_enabled")
		})
	}
}

// bwDevice builds a single device on the given fabric. The device config is
// empty on purpose: every gate in this file reads the profile's nvlink block,
// never the architecture.
func bwDevice(t *testing.T, fabric *NodeFabric) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*mockserver.Device)
	return NewConfigurableDevice(0, bd, &DeviceConfig{},
		"GPU-00000000-0000-0000-0000-000000000000", "0000:01:00.0", 0, fabric)
}

func TestGetMockNvlinkBwMode_Defaults(t *testing.T) {
	t.Parallel()

	dev := bwDevice(t, bwFabric(t, deviceBwMode(nil, nil)))

	supported, ret := dev.GetMockNvlinkSupportedBwModes()
	require.Equal(t, nvml.SUCCESS, ret, "supported return")
	require.Equal(t, []uint8{0, 1, 2, 3, 4}, supported,
		"the five modes the bundled nvidia-smi can name")

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(0), mode, "default mode is FULL")
	require.True(t, isBest, "FULL is the best mode")
}

// TestGetMockNvlinkBwMode_RuntimeOverridesProfile pins getter precedence: a
// recorded SetMockNvlinkBwMode wins over a profile initial mode, which in turn
// wins over the computed best default.
func TestGetMockNvlinkBwMode_RuntimeOverridesProfile(t *testing.T) {
	persistSetterWrites(t)

	profileMode := uint8(3)
	dev := bwDevice(t, bwFabric(t, deviceBwMode([]uint8{0, 3}, &profileMode)))

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return before override")
	require.Equal(t, uint8(3), mode, "profile-configured mode")
	require.False(t, isBest, "HALF is not best of {0,3}")

	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(0, false), "runtime override to FULL")

	mode, isBest, ret = dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return after override")
	require.Equal(t, uint8(0), mode, "runtime override wins over profile")
	require.True(t, isBest, "FULL is best of {0,3}")
}

// TestGetMockNvlinkBwMode_NotBest pins that bIsBest is computed rather than
// hardcoded true, which is the whole point of the flag.
func TestGetMockNvlinkBwMode_NotBest(t *testing.T) {
	t.Parallel()

	mode := uint8(3)
	dev := bwDevice(t, bwFabric(t, deviceBwMode([]uint8{0, 3}, &mode)))

	got, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(3), got, "configured mode")
	require.False(t, isBest, "HALF is not the best of {0,3}")
}

// TestGetMockNvlinkBwMode_ScopeGate pins that the per-device trio answers only
// for a profile that selects it: the system pair is the other half of the same
// feature, and a real board answers one or the other, never both.
func TestGetMockNvlinkBwMode_ScopeGate(t *testing.T) {
	t.Parallel()

	for name, fabric := range map[string]*NodeFabric{
		"no fabric":        nil,
		"no bw_mode block": bwFabric(t, &NVLinkConfig{LinksPerGPU: 18}),
		"system scope": bwFabric(t, &NVLinkConfig{
			BwMode: &NVLinkBwModeConfig{Scope: NVLinkBwModeScopeSystem},
		}),
		"invalid scope": bwFabric(t, &NVLinkConfig{
			BwMode: &NVLinkBwModeConfig{Scope: "node"},
		}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dev := bwDevice(t, fabric)

			_, ret := dev.GetMockNvlinkSupportedBwModes()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "supported return")

			_, _, ret = dev.GetMockNvlinkBwMode()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "get return")

			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetMockNvlinkBwMode(0, false), "set return")
		})
	}
}

// TestGetMockNvLinkInfo_NvlinkPresenceGate pins the one NVLink call the header
// puts no architecture floor on: a real H100 answers it with "NVLE: disabled"
// and an empty firmware table, so the gate is whether the board has NVLink at
// all.
func TestGetMockNvLinkInfo_NvlinkPresenceGate(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		nvlink *NVLinkConfig
		want   nvml.Return
	}{
		"links declared":  {&NVLinkConfig{LinksPerGPU: 18}, nvml.SUCCESS},
		"no links":        {&NVLinkConfig{}, nvml.ERROR_NOT_SUPPORTED},
		"no nvlink block": {nil, nvml.ERROR_NOT_SUPPORTED},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dev := bwDevice(t, bwFabric(t, tc.nvlink))

			info, ret := dev.GetMockNvLinkInfo()
			require.Equal(t, tc.want, ret, "nvlink info return")
			if tc.want == nvml.SUCCESS {
				require.False(t, info.NvleEnabled, "NVLE is off unless declared")
				require.Empty(t, info.Firmware, "no firmware unless declared")
			}
		})
	}
}

// infoFabric is a fabric for a board that has NVLink, which is what the
// nvmlDeviceGetNvLinkInfo gate asks for. The link count is incidental to what
// the info tests assert, so it is supplied here rather than in every case.
func infoFabric(t *testing.T, nvlink *NVLinkConfig) *NodeFabric {
	t.Helper()
	withLinks := *nvlink
	withLinks.LinksPerGPU = 18
	return bwFabric(t, &withLinks)
}

func TestGetMockNvLinkInfo_Nvle(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		nvlink *NVLinkConfig
		want   bool
	}{
		"enabled":  {&NVLinkConfig{NvleEnabled: true}, true},
		"disabled": {&NVLinkConfig{NvleEnabled: false}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dev := bwDevice(t, infoFabric(t, tc.nvlink))
			info, ret := dev.GetMockNvLinkInfo()
			require.Equal(t, nvml.SUCCESS, ret, "return code")
			require.Equal(t, tc.want, info.NvleEnabled, "isNvleEnabled")
		})
	}
}

func TestGetMockNvLinkInfo_Firmware(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		nvlink *NVLinkConfig
		want   []NvlinkFirmwareVersion
	}{
		// An undeclared block reports nothing, which renders as the "N/A"
		// nvidia-smi prints for an empty table.
		"undeclared": {&NVLinkConfig{}, nil},
		"empty":      {&NVLinkConfig{Firmware: &NVLinkFirmwareConfig{}}, nil},
		// Entries come back in ucodeType order whatever order the keys were
		// written in, because that is the order nvidia-smi renders them.
		"full set": {
			&NVLinkConfig{Firmware: &NVLinkFirmwareConfig{
				NETIRDLN:  "0:10:0",
				MSE:       "15:3:0",
				NETIRCLN:  "0:10:0",
				NETIR:     "36:2014:4784",
				NETIRUPHY: "7:0:0",
			}},
			[]NvlinkFirmwareVersion{
				{UcodeType: nvlinkUcodeMSE, Major: 15, Minor: 3, SubMinor: 0},
				{UcodeType: nvlinkUcodeNETIR, Major: 36, Minor: 2014, SubMinor: 4784},
				{UcodeType: nvlinkUcodeNETIRUPHY, Major: 7, Minor: 0, SubMinor: 0},
				{UcodeType: nvlinkUcodeNETIRCLN, Major: 0, Minor: 10, SubMinor: 0},
				{UcodeType: nvlinkUcodeNETIRDLN, Major: 0, Minor: 10, SubMinor: 0},
			},
		},
		"partial": {
			&NVLinkConfig{Firmware: &NVLinkFirmwareConfig{MSE: "1:2:3"}},
			[]NvlinkFirmwareVersion{{UcodeType: nvlinkUcodeMSE, Major: 1, Minor: 2, SubMinor: 3}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dev := bwDevice(t, infoFabric(t, tc.nvlink))
			info, ret := dev.GetMockNvLinkInfo()
			require.Equal(t, nvml.SUCCESS, ret, "return code")
			require.Equal(t, tc.want, info.Firmware, "firmware table")
		})
	}
}

func TestResolveNvlinkFirmware_RejectsMalformed(t *testing.T) {
	t.Parallel()

	// A malformed version is dropped with a warning rather than failing the
	// load: an entry nvidia-smi cannot render is worth a warning, but it is
	// not worth refusing to start the mock over.
	for name, version := range map[string]string{
		"not a version": "abc",
		"too few parts": "1:2",
		"empty part":    "1::3",
		"negative":      "1:-2:3",
		"overflows":     "1:2:4294967296",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, warnings := resolveNvlinkFirmware(&Config{YAMLConfig: &YAMLConfig{
				NVLink: &NVLinkConfig{Firmware: &NVLinkFirmwareConfig{MSE: version}},
			}})
			require.Empty(t, got, "no entry for a malformed version")
			require.Len(t, warnings, 1, "one warning")
			require.Contains(t, warnings[0], "nvlink.firmware.mse")
		})
	}
}

func TestSetMockNvlinkBwMode_RoundTrip(t *testing.T) {
	persistSetterWrites(t)

	dev := bwDevice(t, bwFabric(t, deviceBwMode(nil, nil)))

	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(3, false), "set HALF")

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(3), mode, "mode after set")
	require.False(t, isBest, "HALF is not best")
}

// TestSetMockNvlinkBwMode_SetBest pins that bSetBest ignores the mode field,
// which is how the upstream struct is documented to behave.
func TestSetMockNvlinkBwMode_SetBest(t *testing.T) {
	persistSetterWrites(t)

	dev := bwDevice(t, bwFabric(t, deviceBwMode(nil, nil)))
	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(3, false), "move off best")
	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(99, true), "setBest ignores mode 99")

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(0), mode, "best mode is FULL")
	require.True(t, isBest, "isBest after setBest")
}

func TestSetMockNvlinkBwMode_OutsideSupported(t *testing.T) {
	t.Parallel()

	dev := bwDevice(t, bwFabric(t, deviceBwMode([]uint8{0, 3}, nil)))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetMockNvlinkBwMode(2, false))
}

// bwSwitchFabricDevice builds a device on a switch-attached fabric, which is
// what makes links *active* — the power-threshold field values are gated on an
// active link, and LinksPerGPU alone does not produce one. Copied from
// newSwitchFabricDevice in nvlink_fields_test.go, the proven construction.
//
// No t.Parallel() in its callers: Init()/Shutdown() drive engine-wide state.
func bwSwitchFabricDevice(t *testing.T) *ConfigurableDevice {
	t.Helper()
	yaml := &YAMLConfig{
		System: SystemConfig{DriverVersion: "580.65.06", NumDevices: 2},
		NVLink: &NVLinkConfig{
			Version:              5,
			LinksPerGPU:          4,
			BandwidthPerLinkMbps: 53000,
			Switches:             []NVSwitchConfig{{BDF: "0000:01:00.0"}},
		},
	}
	cfg := &Config{NumDevices: 2, DriverVersion: "580.65.06", YAMLConfig: yaml}
	e := NewEngine(cfg)
	require.Equal(t, nvml.SUCCESS, e.Init(), "engine init")
	t.Cleanup(func() { _ = e.Shutdown() })

	handle, _ := e.DeviceGetHandleByIndex(0)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok, "expected ConfigurableDevice")
	return cd
}

func TestSetMockNvLinkLowPowerThreshold(t *testing.T) {
	persistSetterWrites(t)
	dev := bwSwitchFabricDevice(t)

	// Baseline: the field reports the built-in default.
	_, v, ret := dev.GetNvLinkFieldValue(fiNvlinkGetPowerThreshold, 0)
	require.Equal(t, nvml.SUCCESS, ret, "baseline field return")
	require.Equal(t, uint64(lowPowerThresholdDefault), v, "baseline threshold")

	// An in-range write is visible through the field value.
	require.Equal(t, nvml.SUCCESS, dev.SetMockNvLinkLowPowerThreshold(500), "set 500")
	_, v, ret = dev.GetNvLinkFieldValue(fiNvlinkGetPowerThreshold, 0)
	require.Equal(t, nvml.SUCCESS, ret, "field return after set")
	require.Equal(t, uint64(500), v, "threshold after set")

	// The reset sentinel (what `nvlink -sLowPwrThres default` sends) clears
	// the override rather than being rejected as out of range.
	require.Equal(t, nvml.SUCCESS,
		dev.SetMockNvLinkLowPowerThreshold(nvlinkLowPowerThresholdReset), "reset")
	_, v, ret = dev.GetNvLinkFieldValue(fiNvlinkGetPowerThreshold, 0)
	require.Equal(t, nvml.SUCCESS, ret, "field return after reset")
	require.Equal(t, uint64(lowPowerThresholdDefault), v, "threshold after reset")
}

func TestSetMockNvLinkLowPowerThreshold_OutOfRange(t *testing.T) {
	dev := bwSwitchFabricDevice(t)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
		dev.SetMockNvLinkLowPowerThreshold(0), "below min")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
		dev.SetMockNvLinkLowPowerThreshold(1024), "above max")
}

// TestSetMockNvLinkLowPowerThreshold_NoActiveLinks pins the setter to the
// gate its read side already uses: -gLowPwrInfo reports the threshold
// supported only on a GPU with an active link, and a setter answering where
// the getter declines would accept a value nothing can read back.
func TestSetMockNvLinkLowPowerThreshold_NoActiveLinks(t *testing.T) {
	t.Parallel()

	for name, fabric := range map[string]*NodeFabric{
		"no fabric":                nil,
		"no nvlink block":          bwFabric(t, nil),
		"links but none connected": bwFabric(t, &NVLinkConfig{LinksPerGPU: 18}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED,
				bwDevice(t, fabric).SetMockNvLinkLowPowerThreshold(500))
		})
	}
}

// bwEngine builds an engine from an nvlink.bw_mode block, which is what the
// system-level gate reads (the API takes no device).
func bwEngine(t *testing.T, bw *NVLinkBwModeConfig) *Engine {
	t.Helper()
	return NewEngine(&Config{
		NumDevices: 1,
		YAMLConfig: &YAMLConfig{NVLink: &NVLinkConfig{BwMode: bw}},
	})
}

// systemBwMode is the bw_mode block of a profile on the system pair.
func systemBwMode(supported []uint8, mode *uint8) *NVLinkBwModeConfig {
	return &NVLinkBwModeConfig{Scope: NVLinkBwModeScopeSystem, Supported: supported, Mode: mode}
}

func TestSystemNvlinkBwMode_RoundTrip(t *testing.T) {
	persistSetterWrites(t)

	e := bwEngine(t, systemBwMode(nil, nil))

	mode, ret := e.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint32(0), mode, "default global mode is FULL")

	require.Equal(t, nvml.SUCCESS, e.SystemSetNvlinkBwMode(3), "set HALF")

	mode, ret = e.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return after set")
	require.Equal(t, uint32(3), mode, "global mode after set")
}

// TestSystemNvlinkBwMode_ProfileShapesAnswer pins that the system pair reads
// the same bw_mode keys as the per-device trio, so a profile describes the
// feature once whichever pair its board answers on.
func TestSystemNvlinkBwMode_ProfileShapesAnswer(t *testing.T) {
	t.Parallel()

	initial := uint8(3)
	e := bwEngine(t, systemBwMode([]uint8{0, 3}, &initial))

	mode, ret := e.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint32(3), mode, "initial mode from the profile")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetNvlinkBwMode(2),
		"MIN is outside the profile's supported list")
}

// TestSystemNvlinkBwMode_SharedAcrossEngines covers the read-after-write a
// consumer performs across two processes: `nvidia-smi nvlink -sBwMode` in one
// and a plain read in another. Two engines stand in for those two processes,
// since each consumer loads its own copy of the mock and gets its own engine.
// Reporting the old mode there is the failure this guards against — on real
// hardware the node-wide mode is driver state every process observes.
func TestSystemNvlinkBwMode_SharedAcrossEngines(t *testing.T) {
	persistSetterWrites(t)

	writer := bwEngine(t, systemBwMode(nil, nil))
	reader := bwEngine(t, systemBwMode(nil, nil))

	require.Equal(t, nvml.SUCCESS, writer.SystemSetNvlinkBwMode(3), "set HALF on the writing engine")

	mode, ret := reader.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return on the reading engine")
	require.Equal(t, uint32(3), mode, "a second engine must observe the node-wide write")
}

// TestNvlinkBwMode_IgnoresRecordedModeOutsideSupported covers a recorded mode
// that outlived its profile: the writer supports HALF, the reader's profile no
// longer does. Both getters have to fall back to the best supported mode
// rather than report a value their own setter would reject.
func TestNvlinkBwMode_IgnoresRecordedModeOutsideSupported(t *testing.T) {
	t.Run("node-wide", func(t *testing.T) {
		persistSetterWrites(t)
		writer := bwEngine(t, systemBwMode(nil, nil))
		reader := bwEngine(t, systemBwMode([]uint8{0, 2}, nil))
		require.Equal(t, nvml.SUCCESS, writer.SystemSetNvlinkBwMode(3), "set HALF")

		mode, ret := reader.SystemGetNvlinkBwMode()
		require.Equal(t, nvml.SUCCESS, ret, "get return")
		require.Equal(t, uint32(0), mode, "HALF is not in {0,2}; best supported mode reported")
	})

	t.Run("per-device", func(t *testing.T) {
		persistSetterWrites(t)
		writer := bwDevice(t, bwFabric(t, deviceBwMode(nil, nil)))
		reader := bwDevice(t, bwFabric(t, deviceBwMode([]uint8{0, 2}, nil)))
		require.Equal(t, nvml.SUCCESS, writer.SetMockNvlinkBwMode(3, false), "set HALF")

		mode, isBest, ret := reader.GetMockNvlinkBwMode()
		require.Equal(t, nvml.SUCCESS, ret, "get return")
		require.Equal(t, uint8(0), mode, "HALF is not in {0,2}; best supported mode reported")
		require.True(t, isBest, "the fallback is the best mode")
	})
}

// TestSystemNvlinkBwMode_IgnoresPerDeviceWrites keeps the two NVML pairs
// independent. The node-wide getter takes no device, so folding a per-device
// write into its answer would report a mode nobody set node-wide.
func TestSystemNvlinkBwMode_IgnoresPerDeviceWrites(t *testing.T) {
	persistSetterWrites(t)

	e := bwEngine(t, systemBwMode(nil, nil))
	dev := bwDevice(t, bwFabric(t, deviceBwMode(nil, nil)))

	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(3, false), "per-device set HALF")

	mode, ret := e.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "node-wide get return")
	require.Equal(t, uint32(0), mode, "a per-device write must not move the node-wide mode")
}

// TestGetMockNvlinkBwMode_ObservesNodeWideWrite is the other direction: the
// node-wide setter writes the `all:` bucket, so every device must report it.
// That is how the driver behaves — a node-wide mode change moves every GPU.
func TestGetMockNvlinkBwMode_ObservesNodeWideWrite(t *testing.T) {
	persistSetterWrites(t)

	e := bwEngine(t, systemBwMode(nil, nil))
	dev := bwDevice(t, bwFabric(t, deviceBwMode(nil, nil)))

	require.Equal(t, nvml.SUCCESS, e.SystemSetNvlinkBwMode(3), "node-wide set HALF")

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "device get return")
	require.Equal(t, uint8(3), mode, "the device reports the node-wide mode")
	require.False(t, isBest, "HALF is not best")
}

// TestGetMockNvlinkBwMode_NodeWideWriteOutranksEarlierDeviceWrite is that same
// direction for a device the per-device setter had already moved. The
// per-device field wins the merge, so the node-wide write has to drop it or the
// device would go on reporting the mode it was set to individually — a
// node-wide change no GPU honours is not one the driver would make.
func TestGetMockNvlinkBwMode_NodeWideWriteOutranksEarlierDeviceWrite(t *testing.T) {
	persistSetterWrites(t)

	e := bwEngine(t, systemBwMode(nil, nil))
	dev := bwDevice(t, bwFabric(t, deviceBwMode(nil, nil)))

	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(2, false), "per-device set MIN")
	require.Equal(t, nvml.SUCCESS, e.SystemSetNvlinkBwMode(3), "node-wide set HALF")

	mode, _, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "device get return")
	require.Equal(t, uint8(3), mode, "the later node-wide mode wins")

	// The opposite order still leaves the per-device write in charge: it is the
	// more specific scope and, here, also the more recent write.
	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(2, false), "per-device set MIN again")
	mode, _, ret = dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "device get return")
	require.Equal(t, uint8(2), mode, "a later per-device mode wins")
}

// TestSetMockNvLinkLowPowerThreshold_SharedAcrossDevices covers the same
// cross-process read-after-write for the low-power threshold, which a consumer
// reads back through NVML_FI_DEV_NVLINK_GET_POWER_THRESHOLD rather than a
// dedicated getter.
func TestSetMockNvLinkLowPowerThreshold_SharedAcrossDevices(t *testing.T) {
	persistSetterWrites(t)

	writer := bwSwitchFabricDevice(t)
	require.Equal(t, nvml.SUCCESS, writer.SetMockNvLinkLowPowerThreshold(500), "set 500")

	reader := bwSwitchFabricDevice(t)
	_, v, ret := reader.GetNvLinkFieldValue(fiNvlinkGetPowerThreshold, 0)
	require.Equal(t, nvml.SUCCESS, ret, "field return on the reading device")
	require.Equal(t, uint64(500), v, "a second device must observe the recorded threshold")
}

func TestSystemNvlinkBwMode_Rejected(t *testing.T) {
	t.Parallel()

	t.Run("mode outside supported list", func(t *testing.T) {
		t.Parallel()
		e := bwEngine(t, systemBwMode(nil, nil))
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetNvlinkBwMode(5),
			"mode 5 is past the bundled nvidia-smi name table")
	})

	t.Run("mode truncating to a supported uint8", func(t *testing.T) {
		t.Parallel()
		// 256 narrows to uint8(0), a valid FULL, so the width check has to
		// reject it before the supported-list lookup ever sees it.
		e := bwEngine(t, systemBwMode(nil, nil))
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetNvlinkBwMode(256),
			"mode 256 rejected rather than truncated to FULL")
	})

	// A real GB300 answers `nvidia-smi nvlink -gBwMode` with "Getting nvlink
	// bandwidth mode is not supported", which is what a profile on the
	// per-device trio, or one with no bw_mode block, has to reproduce.
	for name, bw := range map[string]*NVLinkBwModeConfig{
		"no bw_mode block": nil,
		"device scope":     {Scope: NVLinkBwModeScopeDevice},
		"invalid scope":    {Scope: "node"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := bwEngine(t, bw)
			_, ret := e.SystemGetNvlinkBwMode()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "get return")
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, e.SystemSetNvlinkBwMode(0), "set return")
		})
	}
}
