// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

func TestRuntimeStateKeepsExplicitZeroThroughJSON(t *testing.T) {
	t.Parallel()

	const document = `{"telemetry":{"power":{"drawMilliWatts":0},"clocks":{"smMHz":0}}}`
	var state RuntimeState
	require.NoError(t, json.Unmarshal([]byte(document), &state))
	require.Equal(t, ptr.To[int64](0), state.Telemetry.Power.DrawMilliWatts)
	require.Equal(t, ptr.To[int32](0), state.Telemetry.Clocks.SMMHz)
	require.Nil(t, state.Telemetry.Clocks.GraphicsMHz, "an omitted field stays unset")

	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	require.JSONEq(t, document, string(encoded))
}

func TestRuntimeStateWithOverride(t *testing.T) {
	t.Parallel()

	base := &RuntimeState{
		DeviceState: DeviceStateHealthy,
		Modes:       &RuntimeModes{Persistence: "Enabled", ECC: "Enabled"},
		Telemetry: &RuntimeTelemetry{
			Power: &PowerTelemetry{Mode: "Fixed", DrawMilliWatts: ptr.To[int64](175000)},
			Utilization: &UtilizationTelemetry{Pattern: &UtilizationPattern{
				GPUPercent: &PercentRange{Minimum: 10, Maximum: 45},
			}},
		},
	}
	baseWith := func(mutate func(*RuntimeState)) *RuntimeState {
		state := base.DeepCopy()
		mutate(state)

		return state
	}
	power := func(milliwatts int64) *RuntimeState {
		return &RuntimeState{Telemetry: &RuntimeTelemetry{Power: &PowerTelemetry{DrawMilliWatts: ptr.To(milliwatts)}}}
	}

	tests := []struct {
		name      string
		base      *RuntimeState
		overrides []*RuntimeState
		want      *RuntimeState
	}{
		{
			name:      "set field replaces its value and siblings inherit",
			base:      base,
			overrides: []*RuntimeState{{Modes: &RuntimeModes{ECC: "Disabled"}}},
			want:      baseWith(func(state *RuntimeState) { state.Modes.ECC = "Disabled" }),
		},
		{
			name:      "explicit zero replaces a non-zero value",
			base:      base,
			overrides: []*RuntimeState{power(0)},
			want:      baseWith(func(state *RuntimeState) { state.Telemetry.Power.DrawMilliWatts = ptr.To[int64](0) }),
		},
		{
			name: "percent range replaces the inherited range whole",
			base: base,
			overrides: []*RuntimeState{{Telemetry: &RuntimeTelemetry{Utilization: &UtilizationTelemetry{
				Pattern: &UtilizationPattern{GPUPercent: &PercentRange{Minimum: 0, Maximum: 5}},
			}}}},
			want: baseWith(func(state *RuntimeState) {
				state.Telemetry.Utilization.Pattern.GPUPercent = &PercentRange{Minimum: 0, Maximum: 5}
			}),
		},
		{
			name: "group missing from the base is taken from the override",
			base: base,
			overrides: []*RuntimeState{{Telemetry: &RuntimeTelemetry{
				Temperature: &TemperatureTelemetry{GPUCelsius: ptr.To[int32](80)},
			}}},
			want: baseWith(func(state *RuntimeState) {
				state.Telemetry.Temperature = &TemperatureTelemetry{GPUCelsius: ptr.To[int32](80)}
			}),
		},
		{
			name:      "later override wins a field both set",
			base:      base,
			overrides: []*RuntimeState{power(100), nil, power(200)},
			want:      baseWith(func(state *RuntimeState) { state.Telemetry.Power.DrawMilliWatts = ptr.To[int64](200) }),
		},
		{
			name:      "integers beyond float64 precision survive exactly",
			base:      base,
			overrides: []*RuntimeState{power(1<<53 + 1)},
			want:      baseWith(func(state *RuntimeState) { state.Telemetry.Power.DrawMilliWatts = ptr.To[int64](1<<53 + 1) }),
		},
		{name: "no override copies the base", base: base, want: base},
		{name: "nil base copies the override", overrides: []*RuntimeState{base}, want: base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			merged, err := tt.base.WithOverride(tt.overrides...)
			require.NoError(t, err)
			require.Equal(t, tt.want, merged)
		})
	}
}

func TestRuntimeStateFieldPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state *RuntimeState
		want  []string
	}{
		{name: "nil state sets nothing"},
		{
			name:  "group without set fields sets nothing",
			state: &RuntimeState{Modes: &RuntimeModes{}, Telemetry: &RuntimeTelemetry{}},
		},
		{
			name: "every set value is listed, a range by its bounds",
			state: &RuntimeState{
				DeviceState: DeviceStateFailed,
				Telemetry: &RuntimeTelemetry{
					Temperature: &TemperatureTelemetry{GPUCelsius: ptr.To[int32](0)},
					Utilization: &UtilizationTelemetry{Pattern: &UtilizationPattern{
						MemoryPercent: &PercentRange{Maximum: 5},
					}},
				},
			},
			want: []string{
				"deviceState",
				"telemetry.temperature.gpuCelsius",
				"telemetry.utilization.pattern.memoryPercent.maximum",
				"telemetry.utilization.pattern.memoryPercent.minimum",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			paths, err := tt.state.FieldPaths()
			require.NoError(t, err)
			require.Equal(t, tt.want, paths)
		})
	}
}

// A policy must be able to leave any runtime field unset, or a merge patch
// could not tell "inherit" from a zero value. Each struct is therefore either
// a group, whose fields are all omittable and merge one by one, or a value
// such as a percent range, whose fields are all required so that a patch
// replaces it whole.
func TestRuntimeStateFieldsMergeUnambiguously(t *testing.T) {
	t.Parallel()

	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for i := range typ.NumField() {
			field := typ.Field(i)
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			fieldPath := path + name
			require.Equal(t, "omitempty", options, "%s must be omitempty so a policy can leave it unset", fieldPath)
			require.Contains(t, []reflect.Kind{reflect.String, reflect.Pointer}, field.Type.Kind(),
				"%s must be a string or a pointer to tell unset from zero", fieldPath)

			if field.Type.Kind() != reflect.Pointer {
				continue
			}

			if group := field.Type.Elem(); group.Kind() == reflect.Struct && !requiredOnly(group) {
				walk(group, fieldPath+".")
			}
		}
	}
	walk(reflect.TypeFor[RuntimeState](), "")
}

// requiredOnly reports whether every field of a struct is required, which
// makes the struct a value that a merge patch replaces whole.
func requiredOnly(typ reflect.Type) bool {
	for i := range typ.NumField() {
		if strings.Contains(typ.Field(i).Tag.Get("json"), "omitempty") {
			return false
		}
	}

	return true
}
