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
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"sigs.k8s.io/yaml"
)

// OverlayDoc is the runtime override document written by nvml-mock-ctl and
// read by the engine. It is intentionally schema-light: All and per-device
// patches are generic maps deep-merged over the pristine DeviceConfig, so any
// config field is controllable without per-field plumbing.
type OverlayDoc struct {
	Version int                       `json:"version,omitempty"`
	All     map[string]any            `json:"all,omitempty"`
	Devices map[string]map[string]any `json:"devices,omitempty"`
}

// ParseOverlay strictly parses overlay bytes. Empty/whitespace input returns
// (nil, nil) so an absent or empty overlay is treated as "no overrides".
func ParseOverlay(data []byte) (*OverlayDoc, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc OverlayDoc
	if err := yaml.UnmarshalStrict(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing overlay: %w", err)
	}
	return &doc, nil
}

// DeviceOverlay returns the deep-merged patch for a device index: All first,
// then the per-index entry (which wins). Returns nil when neither is present.
func (o *OverlayDoc) DeviceOverlay(index int) map[string]any {
	if o == nil {
		return nil
	}
	out := map[string]any{}
	if o.All != nil {
		deepMergeMaps(out, deepCopyMap(o.All))
	}
	if per, ok := o.Devices[strconv.Itoa(index)]; ok {
		deepMergeMaps(out, deepCopyMap(per))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeDeviceConfig deep-merges patch over a JSON view of base and unmarshals
// the result into a new *DeviceConfig. Unknown fields are rejected so typos in
// overlays fail loudly. base is never mutated.
func MergeDeviceConfig(base *DeviceConfig, patch map[string]any) (*DeviceConfig, error) {
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshaling base config: %w", err)
	}
	baseMap := map[string]any{}
	if err := json.Unmarshal(baseJSON, &baseMap); err != nil {
		return nil, fmt.Errorf("unmarshaling base config to map: %w", err)
	}
	if len(patch) > 0 {
		deepMergeMaps(baseMap, patch)
	}
	mergedJSON, err := json.Marshal(baseMap)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(mergedJSON))
	dec.DisallowUnknownFields()
	var out DeviceConfig
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding merged config: %w", err)
	}
	return &out, nil
}

// deepMergeMaps recursively merges src into dst. Nested maps are merged; all
// other values (scalars, slices) replace dst wholesale.
func deepMergeMaps(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMergeMaps(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if vm, ok := v.(map[string]any); ok {
			out[k] = deepCopyMap(vm)
			continue
		}
		out[k] = v
	}
	return out
}
