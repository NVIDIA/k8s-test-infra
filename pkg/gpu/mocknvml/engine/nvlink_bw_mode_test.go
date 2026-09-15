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

func TestNodeFabric_NvlinkBwModeConfig(t *testing.T) {
	t.Parallel()

	mode := uint8(3)
	f := bwFabric(t, &NVLinkConfig{
		NvleEnabled: true,
		BwMode: &NVLinkBwModeConfig{
			Supported: []uint8{0, 3},
			Mode:      &mode,
		},
	})

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
	f := bwFabric(t, &NVLinkConfig{
		BwMode: &NVLinkBwModeConfig{
			Supported: []uint8{0, 3},
			Mode:      &mode,
		},
	})

	got, ok := f.NvlinkConfiguredBwMode()
	require.True(t, ok, "explicit mode 0 is configured")
	require.Equal(t, uint8(0), got, "configured mode")
}

// TestNodeFabric_NvlinkBwModeSupportedOnly pins a profile that lists supported
// modes without picking one; downstream must not infer a default from the list.
func TestNodeFabric_NvlinkBwModeSupportedOnly(t *testing.T) {
	t.Parallel()

	f := bwFabric(t, &NVLinkConfig{
		BwMode: &NVLinkBwModeConfig{
			Supported: []uint8{0, 3},
		},
	})

	require.Equal(t, []uint8{0, 3}, f.NvlinkSupportedBwModes(), "supported modes")
	_, ok := f.NvlinkConfiguredBwMode()
	require.False(t, ok, "mode configured")
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
			require.Nil(t, f.NvlinkSupportedBwModes(), "supported modes")
			_, ok := f.NvlinkConfiguredBwMode()
			require.False(t, ok, "mode configured")
			require.False(t, f.NvleEnabled(), "nvle_enabled")
		})
	}
}

// bwDevice builds a single device on the given fabric with an explicit
// architecture string, which is what the bw-mode gates read.
func bwDevice(t *testing.T, arch string, fabric *NodeFabric) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*mockserver.Device)
	return NewConfigurableDevice(0, bd, &DeviceConfig{Architecture: arch},
		"GPU-00000000-0000-0000-0000-000000000000", "0000:01:00.0", 0, fabric)
}

func TestGetMockNvlinkBwMode_BlackwellDefaults(t *testing.T) {
	t.Parallel()

	dev := bwDevice(t, "blackwell", bwFabric(t, &NVLinkConfig{}))

	supported, ret := dev.GetMockNvlinkSupportedBwModes()
	require.Equal(t, nvml.SUCCESS, ret, "supported return")
	require.Equal(t, []uint8{0, 1, 2, 3, 4}, supported,
		"the five modes the bundled nvidia-smi can name")

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(0), mode, "default mode is FULL")
	require.True(t, isBest, "FULL is the best mode")
}

// TestGetMockNvlinkBwMode_RuntimeOverridesProfile pins getter precedence:
// process-local SetMockNvlinkBwMode wins over a profile initial mode, which
// in turn wins over the computed best default.
func TestGetMockNvlinkBwMode_RuntimeOverridesProfile(t *testing.T) {
	t.Parallel()

	profileMode := uint8(3)
	dev := bwDevice(t, "blackwell", bwFabric(t, &NVLinkConfig{
		BwMode: &NVLinkBwModeConfig{
			Supported: []uint8{0, 3},
			Mode:      &profileMode,
		},
	}))

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
	dev := bwDevice(t, "blackwell", bwFabric(t, &NVLinkConfig{
		BwMode: &NVLinkBwModeConfig{Supported: []uint8{0, 3}, Mode: &mode},
	}))

	got, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(3), got, "configured mode")
	require.False(t, isBest, "HALF is not the best of {0,3}")
}

func TestGetMockNvlinkBwMode_ArchitectureGate(t *testing.T) {
	t.Parallel()

	for _, arch := range []string{"", "ampere", "hopper"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			dev := bwDevice(t, arch, bwFabric(t, &NVLinkConfig{}))

			_, ret := dev.GetMockNvlinkSupportedBwModes()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "supported return")

			_, _, ret = dev.GetMockNvlinkBwMode()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "get return")

			_, ret = dev.GetMockNvLinkInfo()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "nvlink info return")
		})
	}
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
			dev := bwDevice(t, "blackwell", bwFabric(t, tc.nvlink))
			enabled, ret := dev.GetMockNvLinkInfo()
			require.Equal(t, nvml.SUCCESS, ret, "return code")
			require.Equal(t, tc.want, enabled, "isNvleEnabled")
		})
	}
}

func TestSetMockNvlinkBwMode_RoundTrip(t *testing.T) {
	t.Parallel()

	dev := bwDevice(t, "blackwell", bwFabric(t, &NVLinkConfig{}))

	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(3, false), "set HALF")

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(3), mode, "mode after set")
	require.False(t, isBest, "HALF is not best")
}

// TestSetMockNvlinkBwMode_SetBest pins that bSetBest ignores the mode field,
// which is how the upstream struct is documented to behave.
func TestSetMockNvlinkBwMode_SetBest(t *testing.T) {
	t.Parallel()

	dev := bwDevice(t, "blackwell", bwFabric(t, &NVLinkConfig{}))
	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(3, false), "move off best")
	require.Equal(t, nvml.SUCCESS, dev.SetMockNvlinkBwMode(99, true), "setBest ignores mode 99")

	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint8(0), mode, "best mode is FULL")
	require.True(t, isBest, "isBest after setBest")
}

func TestSetMockNvlinkBwMode_Rejected(t *testing.T) {
	t.Parallel()

	t.Run("mode outside supported list", func(t *testing.T) {
		t.Parallel()
		dev := bwDevice(t, "blackwell", bwFabric(t, &NVLinkConfig{
			BwMode: &NVLinkBwModeConfig{Supported: []uint8{0, 3}},
		}))
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetMockNvlinkBwMode(2, false))
	})

	t.Run("architecture below blackwell", func(t *testing.T) {
		t.Parallel()
		dev := bwDevice(t, "hopper", bwFabric(t, &NVLinkConfig{}))
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetMockNvlinkBwMode(0, false))
	})
}

// bwSwitchFabricDevice builds a device on a switch-attached fabric, which is
// what makes links *active* — the power-threshold field values are gated on an
// active link, and LinksPerGPU alone does not produce one. Copied from
// newSwitchFabricDevice in nvlink_fields_test.go, the proven construction.
//
// No t.Parallel() in its callers: Init()/Shutdown() drive engine-wide state.
func bwSwitchFabricDevice(t *testing.T, arch string) *ConfigurableDevice {
	t.Helper()
	yaml := &YAMLConfig{
		System:         SystemConfig{DriverVersion: "580.65.06", NumDevices: 2},
		DeviceDefaults: DeviceConfig{Architecture: arch},
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
	dev := bwSwitchFabricDevice(t, "hopper")

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

func TestSetMockNvLinkLowPowerThreshold_Rejected(t *testing.T) {
	t.Parallel()

	t.Run("out of range", func(t *testing.T) {
		t.Parallel()
		dev := bwDevice(t, "hopper", bwFabric(t, &NVLinkConfig{}))
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
			dev.SetMockNvLinkLowPowerThreshold(0), "below min")
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
			dev.SetMockNvLinkLowPowerThreshold(1024), "above max")
	})

	t.Run("architecture below hopper", func(t *testing.T) {
		t.Parallel()
		dev := bwDevice(t, "ampere", bwFabric(t, &NVLinkConfig{}))
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED,
			dev.SetMockNvLinkLowPowerThreshold(500))
	})

	t.Run("device_arch_unknown", func(t *testing.T) {
		t.Parallel()
		// UNKNOWN (0xffffffff) satisfies >= HOPPER numerically; the explicit
		// exclusion is what keeps unconfigured profiles from claiming Hopper APIs.
		dev := bwDevice(t, "unrecognized_arch", bwFabric(t, &NVLinkConfig{}))
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED,
			dev.SetMockNvLinkLowPowerThreshold(500), "UNKNOWN architecture")
	})
}

// bwEngine builds an engine whose device_defaults declare an architecture,
// which is what the system-level gate reads (the API has no device param).
func bwEngine(t *testing.T, arch string) *Engine {
	t.Helper()
	return NewEngine(&Config{
		NumDevices: 1,
		YAMLConfig: &YAMLConfig{
			DeviceDefaults: DeviceConfig{Architecture: arch},
		},
	})
}

func TestSystemNvlinkBwMode_RoundTrip(t *testing.T) {
	t.Parallel()

	e := bwEngine(t, "hopper")

	mode, ret := e.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return")
	require.Equal(t, uint32(0), mode, "default global mode is FULL")

	require.Equal(t, nvml.SUCCESS, e.SystemSetNvlinkBwMode(3), "set HALF")

	mode, ret = e.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return after set")
	require.Equal(t, uint32(3), mode, "global mode after set")
}

// TestSystemNvlinkBwMode_PerEngineIsolation pins that the node-wide mode is
// engine state, not process state. Two engines are involved because that is
// the invariant: a mode set through one engine must be invisible to any other
// engine in the same process, and so cannot outlive the engine that set it.
func TestSystemNvlinkBwMode_PerEngineIsolation(t *testing.T) {
	t.Parallel()

	first := bwEngine(t, "hopper")
	second := bwEngine(t, "hopper")

	require.Equal(t, nvml.SUCCESS, first.SystemSetNvlinkBwMode(3), "set HALF on the first engine")

	mode, ret := first.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return on the first engine")
	require.Equal(t, uint32(3), mode, "first engine reports the mode it was set to")

	mode, ret = second.SystemGetNvlinkBwMode()
	require.Equal(t, nvml.SUCCESS, ret, "get return on the second engine")
	require.Equal(t, uint32(0), mode, "second engine still reports the default FULL")
}

func TestSystemNvlinkBwMode_Rejected(t *testing.T) {
	t.Parallel()

	t.Run("mode outside supported list", func(t *testing.T) {
		t.Parallel()
		e := bwEngine(t, "hopper")
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetNvlinkBwMode(5),
			"mode 5 is past the bundled nvidia-smi name table")
	})

	t.Run("mode truncating to a supported uint8", func(t *testing.T) {
		t.Parallel()
		// 256 narrows to uint8(0), a valid FULL, so the width check has to
		// reject it before the supported-list lookup ever sees it.
		e := bwEngine(t, "hopper")
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemSetNvlinkBwMode(256),
			"mode 256 rejected rather than truncated to FULL")
	})

	t.Run("architecture below hopper", func(t *testing.T) {
		t.Parallel()
		e := bwEngine(t, "ampere")
		_, ret := e.SystemGetNvlinkBwMode()
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "get on ampere")
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, e.SystemSetNvlinkBwMode(0),
			"set on ampere")
	})

	t.Run("device_arch_unknown", func(t *testing.T) {
		t.Parallel()
		// An empty device_defaults architecture parses to UNKNOWN
		// (0xffffffff), which satisfies >= HOPPER numerically; the explicit
		// exclusion is what keeps unconfigured profiles from claiming
		// Hopper APIs.
		e := bwEngine(t, "")
		_, ret := e.SystemGetNvlinkBwMode()
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "get on UNKNOWN architecture")
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, e.SystemSetNvlinkBwMode(0),
			"set on UNKNOWN architecture")
	})
}
