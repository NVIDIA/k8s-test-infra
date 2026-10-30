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

package main

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// blackwellProfile is the device_defaults body the hostname pair needs: NVML
// documents both calls as Blackwell or newer.
func blackwellProfile(driverVersion, hostname string) string {
	defaults := "  architecture: blackwell\n"
	if hostname != "" {
		defaults += "  hostname: " + hostname + "\n"
	}
	return profile(driverVersion, 2, defaults)
}

func TestHostname_RoundTripsThroughTheCStruct(t *testing.T) {
	b := newBridgeEngine(t, blackwellProfile(driverWithHostname, ""))
	handle := b.handle(t, 0)

	require.Equal(t, uint32(nvml.SUCCESS), hostnameSetForTest(handle, "gb200-nvl-07"))

	name, status := hostnameGetForTest(handle)
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, "gb200-nvl-07", name)
}

func TestHostname_SeededFromProfile(t *testing.T) {
	b := newBridgeEngine(t, blackwellProfile(driverWithHostname, "gb200-nvl-01"))

	name, status := hostnameGetForTest(b.handle(t, 0))
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, "gb200-nvl-01", name)
}

// TestHostname_FillsTheBufferToItsLastByte is the pair of bounds the C structs
// impose: the longest name that fits NVML_DEVICE_HOSTNAME_BUFFER_SIZE must
// survive the round trip through both fixed arrays.
func TestHostname_FillsTheBufferToItsLastByte(t *testing.T) {
	b := newBridgeEngine(t, blackwellProfile(driverWithHostname, ""))
	handle := b.handle(t, 0)

	longest := strings.Repeat("a", engine.HostnameMaxLen)
	require.Equal(t, uint32(nvml.SUCCESS), hostnameSetForTest(handle, longest))

	name, status := hostnameGetForTest(handle)
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, longest, name)
}

func TestHostname_RejectsInvalidName(t *testing.T) {
	b := newBridgeEngine(t, blackwellProfile(driverWithHostname, ""))

	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT),
		hostnameSetForTest(b.handle(t, 0), "not a hostname"))
}

func TestHostname_IsPerDevice(t *testing.T) {
	b := newBridgeEngine(t, blackwellProfile(driverWithHostname, ""))

	require.Equal(t, uint32(nvml.SUCCESS), hostnameSetForTest(b.handle(t, 0), "gpu-zero"))

	name, status := hostnameGetForTest(b.handle(t, 1))
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Empty(t, name, "naming one GPU must not rename its neighbour")
}

// TestHostname_NotSupportedBeforeBlackwell keeps a Hopper profile from
// claiming a Blackwell-only surface.
func TestHostname_NotSupportedBeforeBlackwell(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithHostname, 2, "  architecture: hopper\n"))
	handle := b.handle(t, 0)

	_, status := hostnameGetForTest(handle)
	require.Equal(t, uint32(nvml.ERROR_NOT_SUPPORTED), status)
	require.Equal(t, uint32(nvml.ERROR_NOT_SUPPORTED), hostnameSetForTest(handle, "gb200-nvl-01"))
}

// TestHostname_RejectsUnknownHandle covers the lookup: a handle the engine
// never issued must not reach a device.
func TestHostname_RejectsUnknownHandle(t *testing.T) {
	newBridgeEngine(t, blackwellProfile(driverWithHostname, ""))

	stale := unsafe.Pointer(new(byte))
	_, status := hostnameGetForTest(stale)
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), status)
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), hostnameSetForTest(stale, "gb200-nvl-01"))

	_, status = hostnameGetForTest(nil)
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), status)
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), hostnameSetForTest(nil, "gb200-nvl-01"))
}

// TestHostname_FunctionNotFoundBefore580_95 is the version gate: the pair
// first appears in the r580 branch at 580.95, so the driver the mock pins by
// default must report the symbol as absent rather than answering.
func TestHostname_FunctionNotFoundBefore580_95(t *testing.T) {
	b := newBridgeEngine(t, blackwellProfile(driverWithSystemEvents, ""))
	handle := b.handle(t, 0)

	_, status := hostnameGetForTest(handle)
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND), status)
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND),
		hostnameSetForTest(handle, "gb200-nvl-01"))
}
