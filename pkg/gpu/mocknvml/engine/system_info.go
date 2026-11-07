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
	"fmt"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// HICFirmwareVersionMaxLen mirrors the firmwareVersion array in
// nvmlHwbcEntry_t. A longer configured string is truncated rather than
// rejected: a profile typo should not fail the whole query.
const HICFirmwareVersionMaxLen = 31

// HICEntry is one host interface card, the engine's view of nvmlHwbcEntry_t.
type HICEntry struct {
	ID              uint32
	FirmwareVersion string
}

// SystemGetDriverBranch returns the driver branch string. Callable before
// Init() for the same reason SystemGetDriverVersion is: nvidia-smi reads
// system-level strings during startup.
func (e *Engine) SystemGetDriverBranch() (string, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.config.YAMLConfig != nil && e.config.YAMLConfig.System.DriverBranch != "" {
		return e.config.YAMLConfig.System.DriverBranch, nvml.SUCCESS
	}
	return deriveDriverBranch(e.config.DriverVersion), nvml.SUCCESS
}

// deriveDriverBranch turns a driver version into the branch identifier NVML
// reports for it: the major version, prefixed "r" and suffixed "_00", so
// 580.65.06 becomes r580_00. Profiles that model a point-release branch such
// as r570_40 state it outright via system.driver_branch.
//
// A driver version with no recognisable major component yields an empty
// branch. NVML documents the field as an alphanumeric string with no fallback
// value, and inventing one would put a fabricated branch in front of a
// consumer that keys upgrade decisions off it.
func deriveDriverBranch(driverVersion string) string {
	major, _, _ := strings.Cut(driverVersion, ".")
	if major == "" || strings.IndexFunc(major, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return ""
	}
	return fmt.Sprintf("r%s_00", major)
}

// SystemGetHicVersion returns the configured host interface cards. Nodes
// without any — every modern DGX/HGX/NVL system, since HICs belong to the
// retired S-class enclosures — answer with an empty set and SUCCESS, which is
// what NVML documents: the call has no NOT_SUPPORTED return.
func (e *Engine) SystemGetHicVersion() ([]HICEntry, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.config.YAMLConfig == nil {
		return nil, nvml.SUCCESS
	}

	configured := e.config.YAMLConfig.System.HIC
	entries := make([]HICEntry, 0, len(configured))
	for _, hic := range configured {
		firmware := hic.FirmwareVersion
		if len(firmware) > HICFirmwareVersionMaxLen {
			firmware = firmware[:HICFirmwareVersionMaxLen]
		}
		entries = append(entries, HICEntry{ID: hic.ID, FirmwareVersion: firmware})
	}
	return entries, nvml.SUCCESS
}
