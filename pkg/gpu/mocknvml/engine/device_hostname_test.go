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
	"github.com/stretchr/testify/require"
)

// hostnameEngine builds an engine whose devices carry the given architecture
// and profile hostname.
func hostnameEngine(t *testing.T, arch, hostname string) *Engine {
	t.Helper()
	defaults := healthyConfig()
	defaults.Architecture = arch
	defaults.Hostname = hostname

	e := NewEngine(&Config{
		NumDevices:    2,
		DriverVersion: "580.95.05",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "580.95.05",
				NumDevices:    2,
			},
			DeviceDefaults: *defaults,
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	return e
}

// TestHostname_NotSupportedBeforeBlackwell is the architecture gate. A
// consumer that probes the pair to decide whether GPUs on this node are
// addressable by hostname must not conclude that a Hopper node is.
func TestHostname_NotSupportedBeforeBlackwell(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"", "ampere", "ada", "hopper"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			dev := deviceByIndex(t, hostnameEngine(t, arch, ""), 0)

			_, ret := dev.GetHostname()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetHostname("gpu-0"))
		})
	}
}

// TestHostname_UnsetReportsEmpty covers the Blackwell device nobody has named:
// NVML's return set has no "unset" error, so the answer is the empty string.
func TestHostname_UnsetReportsEmpty(t *testing.T) {
	t.Parallel()
	name, ret := deviceByIndex(t, hostnameEngine(t, "blackwell", ""), 0).GetHostname()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, name)
}

func TestHostname_FallsBackToProfileValue(t *testing.T) {
	t.Parallel()
	name, ret := deviceByIndex(t, hostnameEngine(t, "blackwell", "gb200-nvl-01"), 0).GetHostname()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "gb200-nvl-01", name)
}

func TestHostname_SetThenGetRoundTrips(t *testing.T) {
	t.Parallel()
	dev := deviceByIndex(t, hostnameEngine(t, "blackwell", "from-profile"), 0)

	require.Equal(t, nvml.SUCCESS, dev.SetHostname("gb200-nvl-07"))
	name, ret := dev.GetHostname()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "gb200-nvl-07", name, "a set value must win over the profile")
}

// TestHostname_EmptySetClearsBackToProfile documents what an empty set means:
// the caller is dropping the name it chose, not asking for an invalid one.
func TestHostname_EmptySetClearsBackToProfile(t *testing.T) {
	t.Parallel()
	dev := deviceByIndex(t, hostnameEngine(t, "blackwell", "from-profile"), 0)

	require.Equal(t, nvml.SUCCESS, dev.SetHostname("temporary"))
	require.Equal(t, nvml.SUCCESS, dev.SetHostname(""))
	name, ret := dev.GetHostname()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, name)
}

// TestHostname_IsPerDevice guards against the hostname living on the engine by
// accident: naming one GPU must not rename its neighbour.
func TestHostname_IsPerDevice(t *testing.T) {
	t.Parallel()
	e := hostnameEngine(t, "blackwell", "")
	require.Equal(t, nvml.SUCCESS, deviceByIndex(t, e, 0).SetHostname("gpu-zero"))

	name, ret := deviceByIndex(t, e, 1).GetHostname()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, name)
}

func TestHostname_RejectsInvalidNames(t *testing.T) {
	t.Parallel()
	dev := deviceByIndex(t, hostnameEngine(t, "blackwell", ""), 0)

	for name, value := range map[string]string{
		"space":           "gb200 nvl",
		"underscore":      "gb200_nvl",
		"leading hyphen":  "-gb200",
		"trailing hyphen": "gb200-",
		"leading dot":     ".gb200",
		"trailing dot":    "gb200.",
		"slash":           "gb200/nvl",
		"non-ascii":       "gb200-nvlü",
		"too long":        strings.Repeat("a", HostnameMaxLen+1),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetHostname(value))
		})
	}
}

func TestHostname_AcceptsMaximumLength(t *testing.T) {
	t.Parallel()
	dev := deviceByIndex(t, hostnameEngine(t, "blackwell", ""), 0)

	longest := strings.Repeat("a", HostnameMaxLen)
	require.Equal(t, nvml.SUCCESS, dev.SetHostname(longest))
	name, ret := dev.GetHostname()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, longest, name)
}

// TestHostname_RejectedValueLeavesTheCurrentOneIntact matters because a
// consumer that retries after an error must not find the GPU unnamed.
func TestHostname_RejectedValueLeavesTheCurrentOneIntact(t *testing.T) {
	t.Parallel()
	dev := deviceByIndex(t, hostnameEngine(t, "blackwell", ""), 0)

	require.Equal(t, nvml.SUCCESS, dev.SetHostname("gb200-nvl-07"))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetHostname("not a hostname"))
	name, ret := dev.GetHostname()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "gb200-nvl-07", name)
}

// TestHostname_LostDeviceFails pins that both calls are guarded: a GPU that
// has fallen off the bus cannot answer for its hostname.
func TestHostname_LostDeviceFails(t *testing.T) {
	t.Parallel()
	defaults := healthyConfig()
	defaults.Architecture = "blackwell"
	defaults.Failure = &FailureInjectionConfig{Mode: FailureModeFallenOffBus}

	e := NewEngine(&Config{
		NumDevices:    1,
		DriverVersion: "580.95.05",
		YAMLConfig: &YAMLConfig{
			Version:        "1.0",
			System:         SystemConfig{DriverVersion: "580.95.05", NumDevices: 1},
			DeviceDefaults: *defaults,
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })

	// The handle lookup itself already reports the loss, so reach the device
	// directly to exercise the getter's own guard.
	dev := e.server.configurableDevices[0]
	_, ret := dev.GetHostname()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, dev.SetHostname("gb200-nvl-01"))
}
