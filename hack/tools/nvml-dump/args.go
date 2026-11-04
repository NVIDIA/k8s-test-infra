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

package main

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// maxCombinations bounds how many argument tuples one getter is called with,
// so a getter with several wide arguments cannot dominate the dump.
const maxCombinations = 256

// fieldIDLimit is the number of NVML field IDs probed. go-nvml's header ends at
// FI_MAX (308); a newer driver may define more, so the probe goes well past it.
const fieldIDLimit = 400

// linkRange is wider than any NVLink count in service, so the first
// out-of-range index shows where the device stops answering.
var linkRange = span(0, 39)

func span(lo, hi int64) []int64 {
	out := make([]int64, 0, hi-lo+1)
	for v := lo; v <= hi; v++ {
		out = append(out, v)
	}
	return out
}

// enumValues lists the values tried for an argument of an NVML enum type, by
// type name. A value past the end of an enum answers INVALID_ARGUMENT, which
// is recorded like any other answer. A type missing here falls back to 0..3.
var enumValues = map[string][]int64{
	"ClockType":             span(0, 3),
	"ClockId":               span(0, 3),
	"RestrictedAPI":         span(0, 1),
	"MemoryErrorType":       span(0, 1),
	"EccCounterType":        span(0, 1),
	"MemoryLocation":        span(0, 7),
	"InforomObject":         span(0, 3),
	"EncoderType":           span(0, 2),
	"TemperatureSensors":    span(0, 1),
	"TemperatureThresholds": span(0, 7),
	"PerfPolicyType":        append(span(0, 5), 10, 11),
	"PcieUtilCounter":       span(0, 1),
	"NvLinkCapability":      span(0, 5),
	"NvLinkErrorCounter":    span(0, 4),
	"PageRetirementCause":   span(0, 1),
	"Pstates":               span(0, 15),
	"AffinityScope":         span(0, 1),
	"GpuTopologyLevel":      {0, 10, 20, 30, 40, 50},
	"DeviceVgpuCapability":  span(0, 6),
}

// methodArgs overrides the values for particular getters, one list per
// argument. Plain integer arguments are indexes or sizes whose meaning a type
// name does not carry.
var methodArgs = map[string][][]int64{
	"GetCpuAffinity":              {{8}},
	"GetCpuAffinityWithinScope":   {{8}, {0, 1}},
	"GetMemoryAffinity":           {{8}, {0, 1}},
	"GetGpuInstanceProfileInfo":   {span(0, 19)},
	"GetFanSpeed_v2":              {span(0, 3)},
	"GetFanControlPolicy_v2":      {span(0, 3)},
	"GetTargetFanSpeed":           {span(0, 3)},
	"GetNvLinkCapability":         {linkRange, span(0, 5)},
	"GetNvLinkErrorCounter":       {linkRange, span(0, 4)},
	"GetNvLinkRemoteDeviceType":   {linkRange},
	"GetNvLinkRemotePciInfo":      {linkRange},
	"GetNvLinkState":              {linkRange},
	"GetNvLinkVersion":            {linkRange},
	"GetNvLinkUtilizationControl": {linkRange, {0, 1}},
	"GetNvLinkUtilizationCounter": {linkRange, {0, 1}},
}

// argLists returns the values to try for each argument of a getter, and false
// when some argument is not an integer type and so cannot be enumerated.
func argLists(name string, mt reflect.Type) ([][]int64, bool) {
	if mt.IsVariadic() {
		return nil, false
	}
	overrides := methodArgs[name]
	lists := make([][]int64, mt.NumIn())
	for i := range lists {
		t := mt.In(i)
		switch t.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		default:
			return nil, false
		}
		switch {
		case i < len(overrides):
			lists[i] = overrides[i]
		case enumValues[t.Name()] != nil:
			lists[i] = enumValues[t.Name()]
		case t.PkgPath() != "":
			lists[i] = span(0, 3)
		case t.Kind() == reflect.Uint64:
			lists[i] = []int64{0}
		default:
			lists[i] = []int64{0, 1}
		}
	}
	return lists, true
}

// callWithArgs calls a getter once per tuple of argument values and records
// each answer under "Name(a,b)". It reports false, calling nothing, when the
// getter takes an argument it cannot enumerate.
func callWithArgs(name string, m reflect.Value, calls map[string]call) bool {
	mt := m.Type()
	if mt.NumOut() == 0 || mt.Out(mt.NumOut()-1) != returnType {
		return false
	}
	lists, ok := argLists(name, mt)
	if !ok {
		return false
	}

	tuple := make([]int64, len(lists))
	count := 0
	var walk func(i int)
	walk = func(i int) {
		if count >= maxCombinations {
			return
		}
		if i == len(lists) {
			count++
			args := make([]reflect.Value, len(tuple))
			parts := make([]string, len(tuple))
			for j, v := range tuple {
				arg := reflect.New(mt.In(j)).Elem()
				if arg.CanInt() {
					arg.SetInt(v)
				} else {
					arg.SetUint(uint64(v))
				}
				args[j] = arg
				parts[j] = strconv.FormatInt(v, 10)
			}
			calls[fmt.Sprintf("%s(%s)", name, strings.Join(parts, ","))] = invoke(m, args)
			return
		}
		for _, v := range lists[i] {
			tuple[i] = v
			walk(i + 1)
		}
	}
	walk(0)
	return true
}

// callFieldValues probes every field ID one at a time. The IDs a driver defines
// differ between releases, and which of them it answers, and how, is what a
// consumer such as DCGM depends on.
func callFieldValues(m reflect.Value, calls map[string]call) {
	for id := 0; id < fieldIDLimit; id++ {
		key := fmt.Sprintf("GetFieldValues(id=%d)", id)
		values := []nvml.FieldValue{{FieldId: uint32(id)}}

		got := invokeFieldValues(m, values)
		if got.Ret != "SUCCESS" {
			calls[key] = got
			continue
		}
		fv := values[0]
		calls[key] = call{
			Ret: retName(nvml.Return(fv.NvmlReturn)),
			Value: map[string]any{
				"valueType": fv.ValueType,
				"value":     hex.EncodeToString(fv.Value[:]),
			},
		}
	}
}

func invokeFieldValues(m reflect.Value, values []nvml.FieldValue) (c call) {
	defer func() {
		if r := recover(); r != nil {
			c = call{Ret: fmt.Sprintf("PANIC: %v", r)}
		}
	}()
	out := m.Call([]reflect.Value{reflect.ValueOf(values)})
	return call{Ret: retName(out[0].Interface().(nvml.Return))}
}
