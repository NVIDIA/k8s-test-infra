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
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// HostnameMaxLen is the longest hostname that fits NVML_DEVICE_HOSTNAME_BUFFER_SIZE
// (64) alongside its NUL terminator.
const HostnameMaxLen = 63

// reportsHostname is the architecture gate for the device hostname pair. NVML
// documents both calls as "Blackwell or newer fully supported devices", so
// earlier profiles must answer NOT_SUPPORTED — a consumer that probes the pair
// to decide whether the node can be addressed by GPU hostname would otherwise
// conclude a Hopper node supports it.
func (d *ConfigurableDevice) reportsHostname() bool {
	return d.Config.Architecture >= nvml.DEVICE_ARCH_BLACKWELL &&
		d.Config.Architecture != nvml.DEVICE_ARCH_UNKNOWN
}

// GetHostname returns the hostname set on the device, falling back to the
// profile's `hostname` key. A device that has never been given one reports the
// empty string with SUCCESS: NVML's documented return set has no "unset" error,
// and the string is what a caller reads back after a successful set.
func (d *ConfigurableDevice) GetHostname() (string, nvml.Return) {
	if !d.reportsHostname() {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return "", ret
	}
	if set := d.hostname.Load(); set != nil {
		return *set, nvml.SUCCESS
	}
	return d.cfg().Hostname, nvml.SUCCESS
}

// SetHostname records a hostname for the device.
//
// The value is process-local. NVML itself says the hostname does not survive a
// GPU reset or driver reload, but on real hardware it is driver state that
// every process on the node reads back; here only the process that set it sees
// the new value, the same limitation SetPersistenceMode carries (#849).
func (d *ConfigurableDevice) SetHostname(name string) nvml.Return {
	if !d.reportsHostname() {
		return nvml.ERROR_NOT_SUPPORTED
	}
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return ret
	}
	if !validHostname(name) {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.hostname.Store(&name)
	debugLog("[DEVICE %d] hostname set to %q\n", d.index, name)
	return nvml.SUCCESS
}

// validHostname applies the RFC 1123 host label rules NVML's "contains invalid
// characters" rejection stands for: letters, digits, dot and hyphen only, and
// no leading or trailing separator. The empty string is accepted so a caller
// can clear a hostname it previously set.
func validHostname(name string) bool {
	if name == "" {
		return true
	}
	if len(name) > HostnameMaxLen {
		return false
	}
	if isHostnameSeparator(name[0]) || isHostnameSeparator(name[len(name)-1]) {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isHostnameByte(name[i]) {
			return false
		}
	}
	return true
}

func isHostnameByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return isHostnameSeparator(c)
	}
}

func isHostnameSeparator(c byte) bool {
	return c == '.' || c == '-'
}
