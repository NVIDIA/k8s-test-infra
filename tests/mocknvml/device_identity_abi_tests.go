// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// testIdentityLookups resolves every GPU by board serial and by both forms of
// the versioned nvmlUUID_t, and checks each lands on the device the index
// names. nvidia-smi never reaches these symbols — `-i` matches serials and
// UUIDs itself after enumerating by index — so a go-nvml consumer is the only
// thing that exercises them through the built library. See issue #1042.
func testIdentityLookups(deviceCount int) []testResult {
	var results []testResult
	for i := range deviceCount {
		results = append(results, lookupsResolveGPU(i)...)
	}

	_, ret := nvml.DeviceGetHandleBySerial("0000000000000")
	results = append(results, wantReturn("lookup/serial_unknown", ret, nvml.ERROR_NOT_FOUND))
	_, ret = nvml.DeviceGetHandleBySerial("")
	results = append(results, wantReturn("lookup/serial_empty", ret, nvml.ERROR_INVALID_ARGUMENT))

	// A caller built against a different nvmlUUID_t layout must be refused
	// rather than have its buffer read at the wrong offsets.
	_, ret = nvml.DeviceGetHandleByUUIDV(&nvml.UUID{Type: uint32(nvml.UUID_TYPE_ASCII)})
	results = append(results, wantReturn("lookup/uuidv_unversioned", ret, nvml.ERROR_ARGUMENT_VERSION_MISMATCH))
	return results
}

// lookupsResolveGPU checks the serial, ASCII-UUID and binary-UUID lookups of
// one GPU all return a handle to it.
func lookupsResolveGPU(index int) []testResult {
	name := fmt.Sprintf("lookup/gpu%d", index)
	device, ret := nvml.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetHandleByIndex: %v", nvml.ErrorString(ret))}}
	}
	uuid, ret := device.GetUUID()
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetUUID: %v", nvml.ErrorString(ret))}}
	}
	serial, ret := device.GetSerial()
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetSerial: %v", nvml.ErrorString(ret))}}
	}

	resolves := func(step string, found nvml.Device, ret nvml.Return) testResult {
		if ret != nvml.SUCCESS {
			return testResult{name + "/" + step, false, nvml.ErrorString(ret)}
		}
		got, ret := found.GetUUID()
		if ret != nvml.SUCCESS || got != uuid {
			return testResult{name + "/" + step, false, fmt.Sprintf("resolved %q (%v), want %q", got, nvml.ErrorString(ret), uuid)}
		}
		return testResult{name + "/" + step, true, ""}
	}

	bySerial, ret := nvml.DeviceGetHandleBySerial(serial)
	results := []testResult{resolves("serial", bySerial, ret)}

	ascii := versionedUUID(nvml.UUID_TYPE_ASCII)
	copy(ascii.Value[:nvml.DEVICE_UUID_ASCII_LEN-1], uuid)
	byASCII, ret := nvml.DeviceGetHandleByUUIDV(&ascii)
	results = append(results, resolves("uuidv_ascii", byASCII, ret))

	raw, err := hex.DecodeString(strings.ReplaceAll(strings.TrimPrefix(uuid, "GPU-"), "-", ""))
	if err != nil || len(raw) != nvml.DEVICE_UUID_BINARY_LEN {
		return append(results, testResult{name + "/uuidv_binary", false, fmt.Sprintf("UUID %q has no 16-byte form", uuid)})
	}
	binary := versionedUUID(nvml.UUID_TYPE_BINARY)
	copy(binary.Value[:], raw)
	byBinary, ret := nvml.DeviceGetHandleByUUIDV(&binary)
	return append(results, resolves("uuidv_binary", byBinary, ret))
}

func versionedUUID(kind nvml.UUIDType) nvml.UUID {
	u := nvml.UUID{Type: uint32(kind)}
	u.Version = nvml.STRUCT_VERSION(u, 1)
	return u
}

func wantReturn(name string, got, want nvml.Return) testResult {
	if got != want {
		return testResult{name, false, fmt.Sprintf("returned %v, want %v", nvml.ErrorString(got), nvml.ErrorString(want))}
	}
	return testResult{name, true, ""}
}
