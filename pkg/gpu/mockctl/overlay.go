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

// TemperaturePatch builds an overlay patch that pins the reported GPU
// temperature to celsius. It writes both the static thermal block and a
// zero-variation dynamic block: profiles that enable dynamic metrics (the
// demo/e2e default) drive temperature.gpu through the simulator, which masks
// the static thermal reading, so setting only thermal.temperature_gpu_c would
// have no visible effect. Zeroing ramp_c/variance_c makes the simulator emit
// base_c verbatim, so the reading is deterministic whether or not the profile
// runs the simulator. `reset` removes both, restoring the profile baseline.
func TemperaturePatch(celsius int) map[string]any {
	return map[string]any{
		"thermal": map[string]any{"temperature_gpu_c": celsius},
		"dynamic_metrics": map[string]any{
			"temperature": map[string]any{
				"base_c":     celsius,
				"ramp_c":     0,
				"variance_c": 0,
			},
		},
	}
}

// PowerPatch builds an overlay patch that pins the reported power draw to
// milliwatts. Like TemperaturePatch it writes both the static power block and a
// zero-variation dynamic block so the reading is deterministic in either mode.
// The engine still clamps the value to the profile's [min_limit_mw,
// max_limit_mw] envelope, so a value outside that window will read as the
// nearest bound.
func PowerPatch(milliwatts uint32) map[string]any {
	return map[string]any{
		"power": map[string]any{"current_draw_mw": milliwatts},
		"dynamic_metrics": map[string]any{
			"power": map[string]any{
				"base_mw":     milliwatts,
				"variance_mw": 0,
			},
		},
	}
}

// FanPatch builds an overlay patch that pins the reported fan speed to percent.
// There is no dynamic fan simulator, so this only touches the static fan block.
// GetFanSpeed reports ERROR_NOT_SUPPORTED (nvidia-smi shows [N/A]) whenever
// count is 0 — the case for every liquid/passively-cooled profile — so we force
// count to at least 1 (preserving a larger baseCount) to make the speed
// observable. speed_percent is a string field in the schema, hence the itoa.
func FanPatch(percent, baseCount int) map[string]any {
	count := baseCount
	if count < 1 {
		count = 1
	}
	return map[string]any{
		"fan": map[string]any{
			"count":         count,
			"speed_percent": strconv.Itoa(percent),
		},
	}
}

// UtilizationPatch builds an overlay patch that pins GPU and memory
// utilization to percent. Profiles that enable dynamic metrics (the demo/e2e
// default) drive utilization through the simulator, which masks the static
// UtilizationConfig; unlike temperature/power we cannot pin it with a
// zero-variation dynamic block because the simulator treats min==max==0 as
// "unbounded" (0..100), so `util 0` would not be deterministic. Instead we set
// the static block and disable the dynamic utilization sub-simulator (null), so
// the static value is authoritative for any percent in [0,100]. `reset`
// restores the profile baseline.
func UtilizationPatch(percent int) map[string]any {
	return map[string]any{
		"utilization": map[string]any{"gpu": percent, "memory": percent},
		"dynamic_metrics": map[string]any{
			"utilization": nil,
		},
	}
}

// ClocksPatch builds an overlay patch that pins the reported SM and graphics
// clocks to mhz. There is no dynamic clock simulator, so this hot-reloads
// directly from the static clocks block. Memory/video clocks are left at their
// profile baseline (use `set clocks.memory_current=...` to change those). mhz
// is an int (the CLI validates it as non-negative); the generic overlay map
// carries it as-is and the JSON round-trip in MergeDeviceConfig decodes it into
// the uint32 schema field.
func ClocksPatch(mhz int) map[string]any {
	return map[string]any{
		"clocks": map[string]any{
			"graphics_current": mhz,
			"sm_current":       mhz,
		},
	}
}

// PStatePatch builds an overlay patch that pins the reported performance state
// to P<n>. GetPerformanceState reads the static performance_state string, so it
// hot-reloads directly.
func PStatePatch(n int) map[string]any {
	return map[string]any{"performance_state": fmt.Sprintf("P%d", n)}
}

// throttleReasonKeys maps CLI-friendly throttle reason names to the
// clocks_throttle_reasons config field they enable. The canonical JSON keys are
// accepted directly; short aliases cover the common thermal/power cases.
var throttleReasonKeys = map[string]string{
	"gpu_idle":                    "gpu_idle",
	"idle":                        "gpu_idle",
	"applications_clocks_setting": "applications_clocks_setting",
	"app_clocks":                  "applications_clocks_setting",
	"sw_power_cap":                "sw_power_cap",
	"power":                       "sw_power_cap",
	"hw_slowdown":                 "hw_slowdown",
	"hw_thermal_slowdown":         "hw_thermal_slowdown",
	"thermal":                     "hw_thermal_slowdown",
	"hw_power_brake_slowdown":     "hw_power_brake_slowdown",
	"power_brake":                 "hw_power_brake_slowdown",
	"sync_boost":                  "sync_boost",
	"sw_thermal_slowdown":         "sw_thermal_slowdown",
	"sw_thermal":                  "sw_thermal_slowdown",
	"display_clocks_setting":      "display_clocks_setting",
	"display_clocks":              "display_clocks_setting",
}

// allThrottleKeys is the full set of clocks_throttle_reasons config flags, used
// to write an authoritative all-false baseline so a ThrottlePatch represents
// exactly the requested reasons (and "none" clears them all).
var allThrottleKeys = []string{
	"gpu_idle", "applications_clocks_setting", "sw_power_cap", "hw_slowdown",
	"hw_thermal_slowdown", "hw_power_brake_slowdown", "sync_boost",
	"sw_thermal_slowdown", "display_clocks_setting",
}

// ThrottlePatch builds an overlay patch that sets the GPU's active clock
// throttle reasons. reasons are CLI-friendly names (see throttleReasonKeys);
// the special reason "none" clears all reasons and cannot be combined with
// others. The patch is authoritative: every known flag is written (requested
// ones true, the rest false) so repeated invocations replace rather than
// accumulate state.
func ThrottlePatch(reasons []string) (map[string]any, error) {
	if len(reasons) == 0 {
		return nil, errors.New("throttle requires at least one reason (or 'none')")
	}
	flags := map[string]any{}
	for _, k := range allThrottleKeys {
		flags[k] = false
	}
	for _, r := range reasons {
		key := strings.ToLower(strings.TrimSpace(r))
		if key == "none" {
			if len(reasons) != 1 {
				return nil, errors.New("throttle 'none' cannot be combined with other reasons")
			}
			break
		}
		canonical, ok := throttleReasonKeys[key]
		if !ok {
			return nil, fmt.Errorf("unknown throttle reason %q", r)
		}
		flags[canonical] = true
	}
	return map[string]any{"clocks_throttle_reasons": flags}, nil
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
