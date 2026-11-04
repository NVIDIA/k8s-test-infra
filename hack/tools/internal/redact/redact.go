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

// Package redact replaces the identifiers in an nvml-dump recording.
//
// A dump holds decoded NVML structures, and NVML hands identifiers back as C
// byte arrays (a chassis serial is a list of character codes, a fabric cluster
// UUID a list of 16 bytes) and as plain integers (a PDI). A text scrubber that
// looks for hex, UUID or serial shapes never sees them. This package works on the
// structure instead: it decides by the name of a field or getter, not by the
// shape of its value, so a new identifier field is redacted as soon as its name
// says what it is.
//
// One placeholder is used per distinct value, so GPUs that shared an identifier
// still share one. Values that are already placeholders, or that report nothing
// (all zeros), are left alone, which makes redaction idempotent.
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Redactor numbers the distinct identifiers it meets.
type Redactor struct {
	seen map[string]map[string]int
}

// New returns a Redactor with no identifiers seen yet.
func New() *Redactor { return &Redactor{seen: map[string]map[string]int{}} }

// number returns the placeholder number of a value, handing out the next one
// the first time it is seen. GPU UUIDs count from 0, in the order the GPUs are
// met (the dump lists devices by index), as the other captures and scrub-capture
// do; every other kind counts from 1, because zero is how NVML says "none".
func (r *Redactor) number(kind, value string) int {
	m := r.seen[kind]
	if m == nil {
		m = map[string]int{}
		r.seen[kind] = m
	}
	if n, ok := m[value]; ok {
		return n
	}
	n := len(m) + 1
	if kind == kindGPU {
		n = len(m)
	}
	m[value] = n
	return n
}

// Kinds of identifier. A GPU UUID, a fabric cluster UUID and a UUID carried as
// bytes each have their own numbering.
const (
	kindGPU       = "gpu"
	kindCluster   = "cluster"
	kindUUIDBytes = "uuid-bytes"
	kindGUID      = "guid"
	kindSerial    = "serial"
	kindChassis   = "chassis"
	kindPDI       = "pdi"
)

// JSON redacts a dump (or any JSON document of getter results) and returns it
// compact. Numbers are kept exactly, including those past 2^53.
func (r *Redactor) JSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	out, err := json.Marshal(r.walk("", "", doc))
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Canonical re-encodes a document the way JSON does, so two documents can be
// compared for content rather than layout.
func Canonical(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

// walk visits a decoded value. key is the field it sits under and getter the
// nearest enclosing call, such as GetPdi.
func (r *Redactor) walk(key, getter string, v any) any {
	switch t := v.(type) {
	case map[string]any:
		return r.walkObject(getter, t)
	case []any:
		if isByteArray(t) {
			return r.bytes(key, getter, t)
		}
		for i, child := range t {
			t[i] = r.walk(key, getter, child)
		}
		return t
	case json.Number:
		return r.number64(key, getter, t)
	case string:
		return r.text(key, getter, t)
	}
	return v
}

// walkObject visits an object's fields in sorted order, so the numbering does
// not depend on Go's random map order. A field that holds a call entry (it has a
// "ret") names the getter for everything beneath it.
func (r *Redactor) walkObject(getter string, obj map[string]any) any {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := getter
		if m, ok := obj[k].(map[string]any); ok {
			if _, isCall := m["ret"]; isCall {
				g = k
			}
		}
		obj[k] = r.walk(k, g, obj[k])
	}
	return obj
}

// keyRules decide by a field's name, first match wins; valueRules decide by the
// getter's name for a scalar result, which an NVML dump stores under "value".
var (
	keyRules = []struct {
		all  []string
		kind string
	}{
		{[]string{"chassis", "serial"}, kindChassis},
		{[]string{"serial"}, kindSerial},
		{[]string{"cluster", "uuid"}, kindCluster},
		{[]string{"uuid"}, kindUUIDBytes},
		{[]string{"guid"}, kindGUID},
		{[]string{"pdi"}, kindPDI},
	}
	valueRules = []struct{ getter, kind string }{
		{"pdi", kindPDI}, {"serial", kindSerial}, {"uuid", kindGPU},
	}
)

// kindOf says what kind of identifier a field holds, or "" if it holds none. The
// tests list what each rule covers.
func kindOf(key, getter string) string {
	k, g := strings.ToLower(key), strings.ToLower(getter)
	for _, r := range keyRules {
		if containsAll(k, r.all) {
			return r.kind
		}
	}
	if k == "value" {
		for _, r := range valueRules {
			if strings.Contains(g, r.getter) {
				return r.kind
			}
		}
	}
	return ""
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func isByteArray(a []any) bool {
	if len(a) == 0 {
		return false
	}
	for _, e := range a {
		n, ok := e.(json.Number)
		if !ok {
			return false
		}
		i, err := n.Int64()
		if err != nil || i < 0 || i > 255 {
			return false
		}
	}
	return true
}

func byteValues(a []any) []byte {
	b := make([]byte, len(a))
	for i, e := range a {
		n, _ := e.(json.Number).Int64()
		b[i] = byte(n)
	}
	return b
}

// bytes redacts a C byte array: a serial is text, a UUID or GUID is binary.
func (r *Redactor) bytes(key, getter string, a []any) any {
	kind := kindOf(key, getter)
	if kind == "" {
		return a
	}
	b := byteValues(a)
	if nonZero(b) <= 1 { // all zeros, or already a placeholder
		return a
	}
	var out []byte
	if kind == kindChassis || kind == kindSerial {
		out = r.serialBytes(kind, b)
	} else {
		out = r.binaryBytes(kind, b)
	}
	if out == nil {
		return a // a serial that is already a placeholder
	}
	res := make([]any, len(out))
	for i, c := range out {
		res[i] = json.Number(strconv.Itoa(int(c)))
	}
	return res
}

// serialBytes returns the placeholder text for a serial, as bytes of the same
// length, or nil if it is one already.
func (r *Redactor) serialBytes(kind string, b []byte) []byte {
	text := strings.TrimRight(string(b), "\x00")
	if serialPrefixes.MatchString(text) {
		return nil
	}
	prefix := "1650000000"
	if kind == kindChassis {
		prefix = "1820000000"
	}
	out := make([]byte, len(b))
	copy(out, fmt.Sprintf("%s%03d", prefix, r.number(kind, text)))
	return out
}

// binaryBytes returns an array of the same length with one non-zero byte, the
// placeholder's number: at the end for a UUID, at byte 7 for a GUID, which is 8
// bytes long inside a 16-byte field.
func (r *Redactor) binaryBytes(kind string, b []byte) []byte {
	pos := len(b) - 1
	if kind == kindGUID && pos > 7 {
		pos = 7
	}
	out := make([]byte, len(b))
	out[pos] = byte(r.number(kind, string(b)))
	return out
}

func nonZero(b []byte) int {
	n := 0
	for _, c := range b {
		if c != 0 {
			n++
		}
	}
	return n
}

// number64 redacts an identifier carried as an integer, such as a PDI.
func (r *Redactor) number64(key, getter string, n json.Number) any {
	if kindOf(key, getter) != kindPDI {
		return n
	}
	s := n.String()
	if len(s) < 6 { // zero or already a placeholder
		return n
	}
	return json.Number(strconv.Itoa(r.number("pdi", s)))
}

var (
	serialPrefixes    = regexp.MustCompile(`^(1650000000|1820000000)`)
	uuidPlaceholder   = regexp.MustCompile(`^(GPU|MIG)-0{6}[0-9a-f]{2}-0000-0000-0000-0{10}[0-9a-f]{2}$`)
	serialPlaceholder = regexp.MustCompile(`^(1650000000|1820000000)\d{3}$`)
)

// text redacts an identifier carried as a string.
func (r *Redactor) text(key, getter, s string) any {
	kind := kindOf(key, getter)
	if kind == "" || s == "" || strings.EqualFold(s, "N/A") {
		return s
	}
	switch kind {
	case kindSerial, kindChassis:
		if serialPlaceholder.MatchString(s) {
			return s
		}
		prefix := "1650000000"
		if kind == kindChassis {
			prefix = "1820000000"
		}
		return fmt.Sprintf("%s%03d", prefix, r.number(kind, s))
	case kindGPU:
		if uuidPlaceholder.MatchString(s) {
			return s
		}
		n := r.number(kind, s)
		prefix := "GPU"
		if strings.HasPrefix(s, "MIG-") {
			prefix = "MIG"
		}
		return fmt.Sprintf("%s-%08x-0000-0000-0000-%012x", prefix, n, n)
	}
	return s
}
