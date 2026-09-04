// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
)

// defaultImexChannelCount is the nvidia module's own ImexChannelCount default,
// reported when no IMEX surface is configured.
const defaultImexChannelCount = 2048

// paramValue is one param's value in the driver's own spelling, so that
// rendering is a concatenation and the two value kinds cannot be confused at
// the point of use.
type paramValue string

// numericParam renders an integer param. The driver prints these as unsigned,
// so the module's -1 sentinels surface as 4294967295 rather than a negative
// value nvidia-modprobe's %u could not read.
func numericParam(v int) paramValue {
	return paramValue(strconv.FormatUint(uint64(uint32(v)), 10)) //nolint:gosec // sentinels are deliberately wrapped
}

// quotedParam renders a string param, which the driver prints quoted — and
// which therefore ends nvidia-modprobe's scan wherever it appears.
func quotedParam(v string) paramValue {
	return paramValue(strconv.Quote(v))
}

// param is one line of the file.
type param struct {
	key   string
	value paramValue
}

// paramsFile is /proc/driver/nvidia/params.
//
// The order is held separately from the values because it is load-bearing and
// map iteration is randomised: nvidia-modprobe stops at the first line it
// cannot consume whole, so a param's position decides whether it is reachable
// at all. Rendering walks order; the map is only ever a lookup.
type paramsFile struct {
	order  []string
	values map[string]paramValue
}

// newParamsFile builds a file from params given in the order the driver prints
// them. Both fields are derived from the one list, so they cannot disagree
// about which params exist.
func newParamsFile(params []param) paramsFile {
	f := paramsFile{
		order:  make([]string, 0, len(params)),
		values: make(map[string]paramValue, len(params)),
	}

	for _, p := range params {
		f.order = append(f.order, p.key)
		f.values[p.key] = p.value
	}

	return f
}

// set replaces an existing param's value, keeping its position.
//
// A key the file does not carry is an error rather than an insertion: appending
// would put it after the quoted params, past the point nvidia-modprobe stops
// reading, so a mistyped override would look applied and be inert. That is the
// failure this whole surface shipped with once already.
func (f paramsFile) set(key string, value paramValue) error {
	if _, ok := f.values[key]; !ok {
		return fmt.Errorf("params has no %q to override", key)
	}

	f.values[key] = value

	return nil
}

// render writes the file, one "key: value" line per param, in order.
func (f paramsFile) render() string {
	var b strings.Builder

	for _, key := range f.order {
		fmt.Fprintf(&b, "%s: %s\n", key, f.values[key])
	}

	return b.String()
}

// driverParams is the params file the real nvidia module prints, captured from
// an 8-GPU H100 node running the 580.105.08 open kernel module.
//
// Two properties of nvidia-modprobe's parse loop over this file constrain the
// order, and getting either wrong leaves the file silently inert
// (modprobe-utils/nvidia-modprobe-utils.c):
//
//	while (fscanf(fp, "%31[^:]: %u\n", name, &value) == 2)
//
// Keys are matched unprefixed. NVreg_ is the modprobe parameter name (options
// nvidia NVreg_DeviceFileMode=0666); procfs reports the resolved value under
// the bare name.
//
// The scan then ends at the first line it cannot consume whole, and every param
// below that line is unreachable. Two things end it: a value that is not a bare
// unsigned integer, and a name past the 31-char field width — which
// InitializeSystemMemoryAllocations, at 33 characters, exceeds. Both appear
// here because both appear on real hardware, where the same loop reaches only
// the first six lines: the four params nvidia-modprobe consumes sit above the
// stopper by the driver's design, not by our arrangement.
func driverParams() paramsFile {
	return newParamsFile([]param{
		{"ResmanDebugLevel", numericParam(-1)},
		{"RmLogonRC", numericParam(1)},
		{"ModifyDeviceFiles", numericParam(1)},
		{"DeviceFileUID", numericParam(0)},
		{"DeviceFileGID", numericParam(0)},
		// Decimal, as the driver reports it: 438 is 0666.
		{"DeviceFileMode", numericParam(0o666)},
		{"InitializeSystemMemoryAllocations", numericParam(1)},
		{"UsePageAttributeTable", numericParam(-1)},
		{"EnableMSI", numericParam(1)},
		{"EnablePCIeGen3", numericParam(0)},
		{"MemoryPoolSize", numericParam(0)},
		{"KMallocHeapMaxSize", numericParam(0)},
		{"VMallocHeapMaxSize", numericParam(0)},
		{"IgnoreMMIOCheck", numericParam(0)},
		{"EnableStreamMemOPs", numericParam(0)},
		{"EnableUserNUMAManagement", numericParam(0)},
		{"NvLinkDisable", numericParam(0)},
		{"RmProfilingAdminOnly", numericParam(1)},
		{"PreserveVideoMemoryAllocations", numericParam(1)},
		{"EnableS0ixPowerManagement", numericParam(1)},
		{"S0ixPowerManagementVideoMemoryThreshold", numericParam(256)},
		{"DynamicPowerManagement", numericParam(3)},
		{"DynamicPowerManagementVideoMemoryThreshold", numericParam(200)},
		{"RegisterPCIDriver", numericParam(1)},
		{"EnablePCIERelaxedOrderingMode", numericParam(0)},
		{"EnableResizableBar", numericParam(0)},
		{"EnableGpuFirmware", numericParam(18)},
		{"EnableGpuFirmwareLogs", numericParam(2)},
		{"RmNvlinkBandwidthLinkCount", numericParam(0)},
		{"EnableDbgBreakpoint", numericParam(0)},
		{"OpenRmEnableUnsupportedGpus", numericParam(1)},
		{"DmaRemapPeerMmio", numericParam(1)},
		{"ImexChannelCount", numericParam(defaultImexChannelCount)},
		{"CreateImexChannel0", numericParam(0)},
		{"GrdmaPciTopoCheckOverride", numericParam(1)},
		{"CoherentGPUMemoryMode", quotedParam("driver")},
		{"RegistryDwords", quotedParam("")},
		{"RegistryDwordsPerDevice", quotedParam("")},
		{"RmMsg", quotedParam("")},
		{"GpuBlacklist", quotedParam("")},
		{"TemporaryFilePath", quotedParam("/var/tmp")},
		{"ExcludedGpus", quotedParam("")},
	})
}

// renderParams builds /proc/driver/nvidia/params for this node: the driver's
// own file with the params the profile and the IMEX surface decide overridden
// in place, so they keep the positions that make them readable.
func renderParams(state *agent.State) (string, error) {
	imexChannels := state.IMEX.ChannelCount
	if imexChannels <= 0 {
		imexChannels = defaultImexChannelCount
	}

	modify := 0
	if state.DriverParams.ModifyDeviceFiles {
		modify = 1
	}

	f := driverParams()
	// Order of application is irrelevant — set never moves a param — so this
	// stays a map, unlike the file itself.
	for key, value := range map[string]paramValue{
		"ModifyDeviceFiles": numericParam(modify),
		"DeviceFileUID":     numericParam(state.DriverParams.DeviceFileUID),
		"DeviceFileGID":     numericParam(state.DriverParams.DeviceFileGID),
		"DeviceFileMode":    numericParam(state.DriverParams.DeviceFileMode),
		"ImexChannelCount":  numericParam(imexChannels),
	} {
		if err := f.set(key, value); err != nil {
			return "", err
		}
	}

	return f.render(), nil
}
