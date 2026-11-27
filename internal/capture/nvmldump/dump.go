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

package nvmldump

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/redact"
)

// call is one recorded NVML call.
type call struct {
	Ret   string `json:"ret"`
	Value any    `json:"value,omitempty"`
}

type deviceDump struct {
	Index int             `json:"index"`
	Calls map[string]call `json:"calls"`
}

// dump is the whole recording: process-wide answers, one entry per device, and
// the getters that were not called because they take arguments.
type dump struct {
	System  map[string]call `json:"system"`
	Devices []deviceDump    `json:"devices"`
	Skipped []string        `json:"skipped,omitempty"`
}

var returnType = reflect.TypeOf(nvml.Return(0))

func runDump(stdout, stderr io.Writer) int {
	if ret := nvml.Init(); ret != nvml.SUCCESS {
		_, _ = fmt.Fprintf(stderr, "nvmlInit: %s\n", retName(ret))
		return 1
	}
	defer func() { _ = nvml.Shutdown() }()

	system := map[string]call{}
	system["SystemGetDriverVersion"] = recordPair(nvml.SystemGetDriverVersion())
	system["SystemGetNVMLVersion"] = recordPair(nvml.SystemGetNVMLVersion())
	system["SystemGetCudaDriverVersion"] = recordPair(nvml.SystemGetCudaDriverVersion())

	count, ret := nvml.DeviceGetCount()
	system["DeviceGetCount"] = call{Ret: retName(ret), Value: count}

	out := newEmitter(stdout)
	out.begin(system)
	if ret != nvml.SUCCESS {
		return out.end(nil, 1, stderr)
	}

	skipped := map[string]bool{}
	for i := 0; i < count; i++ {
		dev, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			_, _ = fmt.Fprintf(stderr, "DeviceGetHandleByIndex(%d): %s\n", i, retName(ret))
			return out.end(nil, 1, stderr)
		}
		calls, skip := callGetters(dev)
		// Each device is written as soon as it is done, so a crash in the driver
		// on a later device keeps the earlier ones.
		out.device(deviceDump{Index: i, Calls: calls})
		for _, name := range skip {
			skipped[name] = true
		}
	}
	var list []string
	for name := range skipped {
		list = append(list, name)
	}
	sort.Strings(list)
	return out.end(list, 0, stderr)
}

// emitter writes a dump as one JSON document, a piece at a time, redacting
// identifiers (UUIDs, serial numbers, PDIs, GUIDs) on the way out: a dump is
// meant to be committed, and a real node's identifiers must not reach a file.
type emitter struct {
	w   io.Writer
	r   *redact.Redactor
	n   int
	err error
}

func newEmitter(w io.Writer) *emitter { return &emitter{w: w, r: redact.New()} }

func (e *emitter) piece(v any) string {
	raw, err := json.Marshal(v)
	if err == nil {
		raw, err = e.r.JSON(raw)
	}
	if err != nil && e.err == nil {
		e.err = err
	}
	return strings.TrimSpace(string(raw))
}

func (e *emitter) put(s string) {
	if _, err := io.WriteString(e.w, s); err != nil && e.err == nil {
		e.err = err
	}
}

// key writes an object key with the JSON encoder, never by hand.
func (e *emitter) key(name string) {
	raw, err := json.Marshal(name)
	if err != nil && e.err == nil {
		e.err = err
	}
	e.put(string(raw))
	e.put(":")
}

func (e *emitter) begin(system map[string]call) {
	e.put("{")
	e.key("system")
	e.put(e.piece(system))
	e.put(",")
	e.key("devices")
	e.put("[")
}

func (e *emitter) device(d deviceDump) {
	if e.n > 0 {
		e.put(",")
	}
	e.n++
	e.put(e.piece(d))
}

func (e *emitter) end(skipped []string, code int, stderr io.Writer) int {
	e.put("]")
	if len(skipped) > 0 {
		e.put(",")
		e.key("skipped")
		e.put(e.piece(skipped))
	}
	e.put("}\n")
	if e.err != nil {
		_, _ = fmt.Fprintf(stderr, "encode: %v\n", e.err)
		return 1
	}
	return code
}

// recordPair records the (value, Return) pair a go-nvml package function returns.
func recordPair(value any, ret nvml.Return) call {
	return call{Ret: retName(ret), Value: sanitize(reflect.ValueOf(value))}
}

// callGetters calls every getter on obj that it can and returns the answers by
// method name. A getter with no arguments is recorded as "Name". One whose
// arguments are all integers or enums is called over a fixed range of values
// and recorded as "Name(a,b)". Anything else is returned in the skipped list.
// A method counts as a getter when its name starts with Get or Is, so nothing
// that changes state is ever called.
func callGetters(obj any) (map[string]call, []string) {
	v := reflect.ValueOf(obj)
	t := v.Type()
	calls := map[string]call{}
	var skipped []string

	for i := 0; i < t.NumMethod(); i++ {
		name := t.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "Is") {
			continue
		}
		m := v.Method(i)

		switch {
		case name == "GetFieldValues" && isFieldValuesGetter(m.Type()):
			callFieldValues(m, calls)
		case m.Type().NumIn() == 0 && !m.Type().IsVariadic():
			calls[name] = invoke(m, nil)
		case callWithArgs(name, m, calls):
		default:
			skipped = append(skipped, name)
		}
	}
	return calls, skipped
}

// invoke calls a getter. A panic is recorded as an error
// instead of ending the dump: one getter that misbehaves must not hide the
// answers of the rest.
func invoke(m reflect.Value, args []reflect.Value) (c call) {
	defer func() {
		if r := recover(); r != nil {
			c = call{Ret: fmt.Sprintf("PANIC: %v", r)}
		}
	}()

	out := m.Call(args)
	if len(out) == 0 {
		return call{Ret: "NO_RETURN"}
	}

	last := out[len(out)-1]
	if last.Type() != returnType {
		// A getter with no NVML return code, such as one returning a bare value.
		return call{Ret: "NONE", Value: sanitizeAll(out)}
	}

	c.Ret = retName(last.Interface().(nvml.Return))
	if len(out) > 1 {
		c.Value = sanitizeAll(out[:len(out)-1])
	}
	return c
}

func sanitizeAll(vs []reflect.Value) any {
	if len(vs) == 1 {
		return sanitize(vs[0])
	}
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = sanitize(v)
	}
	return out
}

// sanitize turns a returned value into something encoding/json writes
// faithfully. Handles and other interfaces carry nothing to record.
func sanitize(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return "<" + v.Kind().String() + ">"
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return sanitize(v.Elem())
	default:
		// Everything else is handled below.
	}
	if !v.CanInterface() {
		return fmt.Sprintf("%v", v)
	}
	value := v.Interface()
	if _, err := json.Marshal(value); err != nil {
		return fmt.Sprintf("%+v", value)
	}
	return value
}

// returnNames gives each NVML return code its symbolic name. go-nvml's
// Return.String asks the loaded library for a message ("Not Supported"), which
// differs between the real library and a mock; a dump that is meant to be
// compared across the two needs a name that does not depend on either.
var returnNames = map[nvml.Return]string{
	nvml.SUCCESS:                         "SUCCESS",
	nvml.ERROR_UNINITIALIZED:             "ERROR_UNINITIALIZED",
	nvml.ERROR_INVALID_ARGUMENT:          "ERROR_INVALID_ARGUMENT",
	nvml.ERROR_NOT_SUPPORTED:             "ERROR_NOT_SUPPORTED",
	nvml.ERROR_NO_PERMISSION:             "ERROR_NO_PERMISSION",
	nvml.ERROR_ALREADY_INITIALIZED:       "ERROR_ALREADY_INITIALIZED",
	nvml.ERROR_NOT_FOUND:                 "ERROR_NOT_FOUND",
	nvml.ERROR_INSUFFICIENT_SIZE:         "ERROR_INSUFFICIENT_SIZE",
	nvml.ERROR_INSUFFICIENT_POWER:        "ERROR_INSUFFICIENT_POWER",
	nvml.ERROR_DRIVER_NOT_LOADED:         "ERROR_DRIVER_NOT_LOADED",
	nvml.ERROR_TIMEOUT:                   "ERROR_TIMEOUT",
	nvml.ERROR_IRQ_ISSUE:                 "ERROR_IRQ_ISSUE",
	nvml.ERROR_LIBRARY_NOT_FOUND:         "ERROR_LIBRARY_NOT_FOUND",
	nvml.ERROR_FUNCTION_NOT_FOUND:        "ERROR_FUNCTION_NOT_FOUND",
	nvml.ERROR_CORRUPTED_INFOROM:         "ERROR_CORRUPTED_INFOROM",
	nvml.ERROR_GPU_IS_LOST:               "ERROR_GPU_IS_LOST",
	nvml.ERROR_RESET_REQUIRED:            "ERROR_RESET_REQUIRED",
	nvml.ERROR_OPERATING_SYSTEM:          "ERROR_OPERATING_SYSTEM",
	nvml.ERROR_LIB_RM_VERSION_MISMATCH:   "ERROR_LIB_RM_VERSION_MISMATCH",
	nvml.ERROR_IN_USE:                    "ERROR_IN_USE",
	nvml.ERROR_MEMORY:                    "ERROR_MEMORY",
	nvml.ERROR_NO_DATA:                   "ERROR_NO_DATA",
	nvml.ERROR_VGPU_ECC_NOT_SUPPORTED:    "ERROR_VGPU_ECC_NOT_SUPPORTED",
	nvml.ERROR_INSUFFICIENT_RESOURCES:    "ERROR_INSUFFICIENT_RESOURCES",
	nvml.ERROR_FREQ_NOT_SUPPORTED:        "ERROR_FREQ_NOT_SUPPORTED",
	nvml.ERROR_ARGUMENT_VERSION_MISMATCH: "ERROR_ARGUMENT_VERSION_MISMATCH",
	nvml.ERROR_DEPRECATED:                "ERROR_DEPRECATED",
	nvml.ERROR_NOT_READY:                 "ERROR_NOT_READY",
	nvml.ERROR_GPU_NOT_FOUND:             "ERROR_GPU_NOT_FOUND",
	nvml.ERROR_INVALID_STATE:             "ERROR_INVALID_STATE",
	nvml.ERROR_UNKNOWN:                   "ERROR_UNKNOWN",
}

func retName(r nvml.Return) string {
	if name, ok := returnNames[r]; ok {
		return name
	}
	return fmt.Sprintf("ERROR_%d", int32(r))
}

// isFieldValuesGetter reports whether t has the shape of NVML's field-value
// query, func([]nvml.FieldValue) nvml.Return.
func isFieldValuesGetter(t reflect.Type) bool {
	return t.NumIn() == 1 && t.In(0) == reflect.TypeOf([]nvml.FieldValue(nil)) &&
		t.NumOut() == 1 && t.Out(0) == returnType
}
