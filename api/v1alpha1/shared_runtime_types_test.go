// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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

	tests := []struct {
		name     string
		base     *RuntimeState
		override *RuntimeState
		want     *RuntimeState
	}{
		{
			name:     "set field replaces its value and siblings inherit",
			base:     base,
			override: &RuntimeState{Modes: &RuntimeModes{ECC: "Disabled"}},
			want:     baseWith(func(state *RuntimeState) { state.Modes.ECC = "Disabled" }),
		},
		{
			name: "explicit zero replaces a non-zero value",
			base: base,
			override: &RuntimeState{Telemetry: &RuntimeTelemetry{
				Power: &PowerTelemetry{DrawMilliWatts: ptr.To[int64](0)},
			}},
			want: baseWith(func(state *RuntimeState) { state.Telemetry.Power.DrawMilliWatts = ptr.To[int64](0) }),
		},
		{
			name: "percent range replaces the inherited range whole",
			base: base,
			override: &RuntimeState{Telemetry: &RuntimeTelemetry{Utilization: &UtilizationTelemetry{
				Pattern: &UtilizationPattern{GPUPercent: &PercentRange{Minimum: 0, Maximum: 5}},
			}}},
			want: baseWith(func(state *RuntimeState) {
				state.Telemetry.Utilization.Pattern.GPUPercent = &PercentRange{Minimum: 0, Maximum: 5}
			}),
		},
		{
			name: "group missing from the base is taken from the override",
			base: base,
			override: &RuntimeState{Telemetry: &RuntimeTelemetry{
				Temperature: &TemperatureTelemetry{GPUCelsius: ptr.To[int32](80)},
			}},
			want: baseWith(func(state *RuntimeState) {
				state.Telemetry.Temperature = &TemperatureTelemetry{GPUCelsius: ptr.To[int32](80)}
			}),
		},
		{name: "nil override copies the base", base: base, want: base},
		{name: "nil base copies the override", override: base, want: base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.base.WithOverride(tt.override))
		})
	}
}

func TestRuntimeStateWithOverrideNeverAliasesInputs(t *testing.T) {
	t.Parallel()

	base := fullRuntimeState(t, 1)
	override := fullRuntimeState(t, 2)
	merged := base.WithOverride(override)
	for _, leaf := range runtimeLeaves(t) {
		leaf.overwrite(t, merged)
	}

	require.Equal(t, fullRuntimeState(t, 1), base)
	require.Equal(t, fullRuntimeState(t, 2), override)
}

func TestRuntimeStateFieldPaths(t *testing.T) {
	t.Parallel()

	var unset *RuntimeState
	require.Empty(t, unset.FieldPaths())
	require.Empty(t, (&RuntimeState{Modes: &RuntimeModes{}, Telemetry: &RuntimeTelemetry{}}).FieldPaths(),
		"a group without set fields sets nothing")

	state := &RuntimeState{
		DeviceState: DeviceStateFailed,
		Telemetry: &RuntimeTelemetry{
			Temperature: &TemperatureTelemetry{GPUCelsius: ptr.To[int32](0)},
			Utilization: &UtilizationTelemetry{Pattern: &UtilizationPattern{
				MemoryPercent: &PercentRange{Maximum: 5},
			}},
		},
	}
	require.Equal(t, []string{
		"deviceState",
		"telemetry.temperature.gpuCelsius",
		"telemetry.utilization.pattern.memoryPercent",
	}, state.FieldPaths())
}

// A new RuntimeState field must be able to express "unset", and both
// WithOverride and FieldPaths must handle it, or a policy that sets it would
// be silently ignored or never reported as conflicting.
func TestRuntimeStateOverrideCoversEveryField(t *testing.T) {
	t.Parallel()

	leaves := runtimeLeaves(t)
	paths := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		paths = append(paths, leaf.path)
	}
	slices.Sort(paths)
	require.Equal(t, paths, fullRuntimeState(t, 1).FieldPaths())

	for _, leaf := range leaves {
		t.Run(leaf.path, func(t *testing.T) {
			t.Parallel()
			// Sample 0 is an explicit zero for numbers and replaces a range whole.
			for _, sample := range []int32{0, 7} {
				only := &RuntimeState{}
				leaf.set(t, only, sample)
				require.Equal(t, []string{leaf.path}, only.FieldPaths())
				require.Equal(t, only, (*RuntimeState)(nil).WithOverride(only))
				require.Equal(t, only, only.WithOverride(nil))

				want := fullRuntimeState(t, 1)
				leaf.set(t, want, sample)
				require.Equal(t, want, fullRuntimeState(t, 1).WithOverride(only))
			}
		})
	}
}

// runtimeLeaf is one overridable RuntimeState field, addressed by its JSON
// path and by the struct field indexes leading to it.
type runtimeLeaf struct {
	path  string
	index []int
}

func runtimeLeaves(t *testing.T) []runtimeLeaf {
	t.Helper()

	var leaves []runtimeLeaf
	var walk func(typ reflect.Type, prefix string, index []int)
	walk = func(typ reflect.Type, prefix string, index []int) {
		for i := range typ.NumField() {
			field := typ.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			leaf := runtimeLeaf{path: prefix + name, index: append(slices.Clone(index), i)}
			switch {
			case field.Type.Kind() == reflect.String:
				leaves = append(leaves, leaf)
			case field.Type == reflect.TypeFor[*PercentRange]():
				leaves = append(leaves, leaf)
			case field.Type.Kind() == reflect.Pointer && field.Type.Elem().Kind() == reflect.Struct:
				walk(field.Type.Elem(), leaf.path+".", leaf.index)
			case field.Type.Kind() == reflect.Pointer:
				leaves = append(leaves, leaf)
			default:
				t.Fatalf("%s is a %s; a runtime field must be a string or a pointer so that omitting it "+
					"differs from setting its zero value", leaf.path, field.Type)
			}
		}
	}
	walk(reflect.TypeFor[RuntimeState](), "", nil)
	return leaves
}

// set assigns the leaf a sample value derived from n, allocating the groups
// on its path.
func (l runtimeLeaf) set(t *testing.T, state *RuntimeState, n int32) {
	t.Helper()

	field := reflect.ValueOf(state).Elem()
	for depth, i := range l.index {
		field = field.Field(i)
		if depth == len(l.index)-1 {
			break
		}
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}
	field.Set(runtimeSample(t, field.Type(), n))
}

// overwrite writes a new value through the leaf's existing storage, so any
// other state sharing that storage changes too.
func (l runtimeLeaf) overwrite(t *testing.T, state *RuntimeState) {
	t.Helper()

	field := reflect.ValueOf(state).Elem()
	for depth, i := range l.index {
		field = field.Field(i)
		if depth < len(l.index)-1 {
			field = field.Elem()
		}
	}
	sample := runtimeSample(t, field.Type(), 9)
	if field.Kind() == reflect.Pointer {
		field.Elem().Set(sample.Elem())
		return
	}
	field.Set(sample)
}

func runtimeSample(t *testing.T, typ reflect.Type, n int32) reflect.Value {
	t.Helper()

	switch {
	case typ.Kind() == reflect.String:
		return reflect.ValueOf(fmt.Sprintf("value-%d", n)).Convert(typ)
	case typ == reflect.TypeFor[*PercentRange]():
		return reflect.ValueOf(&PercentRange{Minimum: n, Maximum: n + 1})
	case typ.Kind() == reflect.Pointer && reflect.New(typ.Elem()).Elem().CanInt():
		value := reflect.New(typ.Elem())
		value.Elem().SetInt(int64(n))
		return value
	default:
		t.Fatalf("add a sample value for runtime field type %s", typ)
		return reflect.Value{}
	}
}

func fullRuntimeState(t *testing.T, n int32) *RuntimeState {
	t.Helper()

	state := &RuntimeState{}
	for _, leaf := range runtimeLeaves(t) {
		leaf.set(t, state, n)
	}
	return state
}
