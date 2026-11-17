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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// HexID is a 64-bit identifier the driver reports for one GPU, such as its PDI,
// configured the way nvidia-smi prints it: "0x" followed by hex digits.
//
// Only a quoted string is accepted. YAML reads an unquoted 0x literal as an
// integer, and a runtime config override carries numbers through a float64,
// which silently rounds any identifier above 2^53 to a different one.
type HexID uint64

// MarshalJSON writes the identifier as nvidia-smi prints it, so a config that
// round-trips through a runtime override decodes back to the same value.
func (h HexID) MarshalJSON() ([]byte, error) {
	return json.Marshal(formatHexID(uint64(h)))
}

// UnmarshalJSON reads a quoted "0x…" string.
func (h *HexID) UnmarshalJSON(data []byte) error {
	v, err := parseHexIDJSON(data)
	if err != nil {
		return err
	}
	*h = HexID(v)
	return nil
}

// fabricGUIDFromPDI is the gpu_fabric_guid value that reports the GPU's PDI.
const fabricGUIDFromPDI = "pdi"

// FabricGUIDConfig is the GPU fabric GUID a platform block declares: a fixed
// value, or "pdi" for the GPU's own PDI. Grace-Blackwell reports its PDI there,
// while B200 and Vera Rubin report a GUID of their own.
type FabricGUIDConfig struct {
	// FromPDI reports the device's PDI, configured or derived. Value is
	// ignored when it is set.
	FromPDI bool
	Value   uint64
}

// MarshalJSON writes "pdi" or the fixed value.
func (f FabricGUIDConfig) MarshalJSON() ([]byte, error) {
	if f.FromPDI {
		return json.Marshal(fabricGUIDFromPDI)
	}
	return json.Marshal(formatHexID(f.Value))
}

// UnmarshalJSON reads "pdi" or a quoted "0x…" string.
func (f *FabricGUIDConfig) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil && strings.EqualFold(strings.TrimSpace(s), fabricGUIDFromPDI) {
		*f = FabricGUIDConfig{FromPDI: true}
		return nil
	}
	v, err := parseHexIDJSON(data)
	if err != nil {
		return fmt.Errorf("%w, or %q to report the GPU's PDI", err, fabricGUIDFromPDI)
	}
	*f = FabricGUIDConfig{Value: v}
	return nil
}

// resolve is the fabric GUID a device with the given PDI reports.
func (f FabricGUIDConfig) resolve(pdi uint64) uint64 {
	if f.FromPDI {
		return pdi
	}
	return f.Value
}

func formatHexID(v uint64) string {
	return fmt.Sprintf("0x%016x", v)
}

// parseHexIDJSON decodes a JSON string holding a hex identifier. The error
// names both fields that use it, because encoding/json does not say which field
// a custom decoder failed on.
func parseHexIDJSON(data []byte) (uint64, error) {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return 0, fmt.Errorf("a pdi or gpu_fabric_guid must be a quoted hex string such as %q, got %s "+
			"(YAML reads an unquoted 0x value as a number)", formatHexID(0xab), data)
	}
	return parseHexID(s)
}

// parseHexID requires the 0x prefix so that a value such as "10" cannot be read
// as decimal by one tool and as hex by another.
func parseHexID(s string) (uint64, error) {
	digits, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if !ok {
		return 0, fmt.Errorf("a pdi or gpu_fabric_guid must start with 0x, got %q", s)
	}
	v, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("a pdi or gpu_fabric_guid must be at most 16 hex digits after 0x, got %q: %w", s, err)
	}
	return v, nil
}
