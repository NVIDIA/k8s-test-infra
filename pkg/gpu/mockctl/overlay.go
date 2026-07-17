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

// Package mockctl holds the pure overlay-mutation logic used by the
// nvml-mock-ctl CLI. It lives outside the engine package so the CLI never
// links the CGo bridge/build tags, while still reusing engine's Go types and
// merge/validation helpers for a single source of truth on the schema.
package mockctl

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// Doc mirrors engine.OverlayDoc but lives in the CLI package so nvml-mock-ctl
// never links the CGo bridge/build tags. The on-disk schema is identical.
type Doc struct {
	Version int                       `json:"version,omitempty"`
	All     map[string]any            `json:"all,omitempty"`
	Devices map[string]map[string]any `json:"devices,omitempty"`
}

// Target identifies which overlay bucket a mutation applies to: the shared
// "all" block, or a specific device index.
type Target struct {
	All   bool
	Index int
}

// Load reads an overlay document from path. A missing or empty file yields an
// empty *Doc (never nil) so callers can mutate and write it back.
func Load(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Doc{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &Doc{}, nil
	}
	var d Doc
	if err := yaml.UnmarshalStrict(data, &d); err != nil {
		return nil, fmt.Errorf("parsing overrides: %w", err)
	}
	return &d, nil
}

// Bytes marshals the document to YAML, defaulting Version to 1.
func (d *Doc) Bytes() ([]byte, error) {
	if d.Version == 0 {
		d.Version = 1
	}
	return yaml.Marshal(d)
}

// bucket returns (creating if needed) the mutable map for the target.
func (d *Doc) bucket(t Target) map[string]any {
	if t.All {
		if d.All == nil {
			d.All = map[string]any{}
		}
		return d.All
	}
	if d.Devices == nil {
		d.Devices = map[string]map[string]any{}
	}
	key := strconv.Itoa(t.Index)
	if d.Devices[key] == nil {
		d.Devices[key] = map[string]any{}
	}
	return d.Devices[key]
}

// SetFields deep-merges kv into the target bucket.
func (d *Doc) SetFields(t Target, kv map[string]any) {
	mergeInto(d.bucket(t), kv)
}

// Fail records a failure override for the target. mode "healthy" removes any
// existing failure block instead of adding one.
func (d *Doc) Fail(t Target, mode string, afterCalls int, xidCode uint64) error {
	switch mode {
	case engine.FailureModeHealthy, engine.FailureModeLost,
		engine.FailureModeFallenOffBus, engine.FailureModeECCUncorrectable:
	default:
		return fmt.Errorf("invalid mode %q (want healthy|lost|fallen_off_bus|ecc_uncorrectable)", mode)
	}
	if mode == engine.FailureModeHealthy {
		// healthy == remove the failure block for the target.
		b := d.bucket(t)
		delete(b, "failure")
		return nil
	}
	failure := map[string]any{"mode": mode}
	if afterCalls > 0 {
		failure["after_calls"] = afterCalls
	}
	if xidCode > 0 {
		failure["xid"] = map[string]any{"code": xidCode}
	}
	d.SetFields(t, map[string]any{"failure": failure})
	return nil
}

// Reset removes overrides for the target, or clears everything when All is set.
func (d *Doc) Reset(t Target) {
	if t.All {
		d.All = nil
		d.Devices = nil
		return
	}
	delete(d.Devices, strconv.Itoa(t.Index))
}

// ParseSet converts ["a.b=1","c=x"] into a nested map, parsing each value as a
// YAML scalar so numbers/bools/strings get their natural type.
func ParseSet(pairs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return nil, fmt.Errorf("invalid set %q (want path=value)", p)
		}
		path, raw := p[:eq], p[eq+1:]
		var val any
		if err := yaml.Unmarshal([]byte(raw), &val); err != nil {
			return nil, fmt.Errorf("parsing value for %q: %w", path, err)
		}
		cur := out
		keys := strings.Split(path, ".")
		for i, k := range keys {
			if k == "" {
				return nil, fmt.Errorf("invalid path %q", path)
			}
			if i == len(keys)-1 {
				cur[k] = val
				break
			}
			next, ok := cur[k].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[k] = next
			}
			cur = next
		}
	}
	return out, nil
}

// Validate wraps engine.MergeDeviceConfig and discards the result, surfacing
// unknown-field/type errors from applying patch over base.
func Validate(base *engine.DeviceConfig, patch map[string]any) error {
	_, err := engine.MergeDeviceConfig(base, patch)
	return err
}

// ResolveTarget interprets a --gpu spec: "all", a device index, or a UUID.
func ResolveTarget(spec string, cfg *engine.Config) (Target, error) {
	if spec == "all" {
		return Target{All: true}, nil
	}
	if idx, err := strconv.Atoi(spec); err == nil {
		return Target{Index: idx}, nil
	}
	if cfg != nil && cfg.YAMLConfig != nil {
		for _, dev := range cfg.YAMLConfig.Devices {
			if dev.UUID == spec {
				return Target{Index: dev.Index}, nil
			}
		}
	}
	return Target{}, fmt.Errorf("cannot resolve --gpu %q (not 'all', an index, or a known UUID)", spec)
}

// mergeInto recursively merges src into dst. Nested maps are merged; all other
// values replace the destination wholesale.
func mergeInto(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeInto(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}
