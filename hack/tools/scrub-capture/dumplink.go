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
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strconv"
	"strings"
)

// nvml-dump redacts identifiers as it writes, numbering each node's GPUs from 0,
// while the text captures are numbered across every node in one run. linkDump
// renames a dump's placeholders to the ones the same node's text files get, so a
// GPU keeps one UUID, serial, PDI and fabric GUID in every file. It pairs the
// dump's devices with the GPUs in the node's raw smi-q.xml by PCI bus ID.

type smiGPU struct {
	ID      string `xml:"id,attr"`
	UUID    string `xml:"uuid"`
	Serial  string `xml:"serial"`
	Chassis string `xml:"platformInfo>chassis_serial_number"`
	PDI     string `xml:"pdi"`
	Fabric  string `xml:"platformInfo>gpu_fabric_guid"`
}

func parseSMIGPUs(raw []byte) map[string]smiGPU {
	var log struct {
		GPUs []smiGPU `xml:"gpu"`
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	if err := dec.Decode(&log); err != nil {
		return nil
	}
	out := make(map[string]smiGPU, len(log.GPUs))
	for _, g := range log.GPUs {
		out[normalizeBusID(g.ID)] = g
	}
	return out
}

// normalizeBusID makes 0000:81:00.0 and 00000000:81:00.0 compare equal.
func normalizeBusID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	domain, rest, ok := strings.Cut(id, ":")
	if !ok {
		return id
	}
	n, err := strconv.ParseUint(domain, 16, 32)
	if err != nil {
		return id
	}
	return strconv.FormatUint(n, 16) + ":" + rest
}

// textPlaceholder is the placeholder the text files give a value, or "" if the
// value reports nothing.
func (s *scrubber) textPlaceholder(kind, v string) string {
	v = strings.TrimSpace(v)
	switch {
	case isAbsent(v):
		return ""
	case kind == kindGPU && isPlaceholder("gpu", v), kind == kindGUID && notAnID(v),
		kind != kindGPU && kind != kindGUID && placeholderSerialRE.MatchString(v):
		return v
	}
	return s.placeholder(kind, v)
}

// linkDump returns a dump (already redacted) with its placeholders renamed after
// the node's smi-q.xml. Without a usable smi-q.xml it returns the dump unchanged.
func (s *scrubber) linkDump(dump, smiXML []byte) ([]byte, error) {
	gpus := parseSMIGPUs(smiXML)
	if len(gpus) == 0 {
		return dump, nil
	}
	dec := json.NewDecoder(bytes.NewReader(dump))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	renames := map[string]string{}
	matched := false
	devices, _ := doc["devices"].([]any)
	for _, d := range devices {
		calls, _ := d.(map[string]any)["calls"].(map[string]any)
		if g, ok := gpus[normalizeBusID(textOf(field(value(calls, "GetPciInfo"), "BusId")))]; ok {
			s.deviceRenames(calls, g, renames)
			s.linkNumbers(calls, g)
			matched = true
		}
	}
	if !matched {
		return dump, nil
	}
	out, err := json.Marshal(rename(doc, renames))
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// deviceRenames maps one device's dump placeholders to its text placeholders.
func (s *scrubber) deviceRenames(calls map[string]any, g smiGPU, renames map[string]string) {
	pairs := [][3]string{
		{kindGPU, textOf(value(calls, "GetUUID")), g.UUID},
		{kindSerial, textOf(value(calls, "GetSerial")), g.Serial},
		{kindChassis, textOf(field(value(calls, "GetPlatformInfo"), "ChassisSerialNumber")), g.Chassis},
	}
	for _, p := range pairs {
		if p[1] == "" {
			continue
		}
		if to := s.textPlaceholder(p[0], p[2]); to != "" && to != p[1] {
			renames[p[1]] = to
		}
	}
}

// linkNumbers sets the PDI and fabric GUID, which the dump holds as an integer
// and as the first 8 bytes (little-endian) of IbGuid, to the text placeholders.
func (s *scrubber) linkNumbers(calls map[string]any, g smiGPU) {
	if pdi, ok := s.guidNumber(g.PDI); ok {
		if m, _ := value(calls, "GetPdi").(map[string]any); m != nil {
			m["Value"] = json.Number(strconv.FormatUint(pdi, 10))
		}
	}
	fabric, ok := s.guidNumber(g.Fabric)
	if !ok {
		return
	}
	for _, getter := range []string{"GetPlatformInfo", "GetPlatformInfo_v1"} {
		m, _ := value(calls, getter).(map[string]any)
		ib, _ := m["IbGuid"].([]any)
		if len(ib) < 8 {
			continue
		}
		for i := range 8 {
			ib[i] = json.Number(strconv.FormatUint(fabric>>(8*i)&0xff, 10))
		}
	}
}

// guidNumber is the number of the text placeholder for a 64-bit ID.
func (s *scrubber) guidNumber(v string) (uint64, bool) {
	ph := s.textPlaceholder(kindGUID, v)
	if ph == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(guidKey(ph), 16, 64)
	return n, err == nil
}

func value(calls map[string]any, getter string) any {
	c, _ := calls[getter].(map[string]any)
	if c == nil || c["ret"] != "SUCCESS" {
		return nil
	}
	return c["value"]
}

func field(v any, name string) any {
	m, _ := v.(map[string]any)
	return m[name]
}

// textOf reads a string, or a C char array as text.
func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		b := make([]byte, 0, len(t))
		for _, e := range t {
			n, ok := e.(json.Number)
			if !ok {
				return ""
			}
			i, err := n.Int64()
			if err != nil || i < 0 || i > 255 {
				return ""
			}
			if i == 0 {
				break
			}
			b = append(b, byte(i))
		}
		return string(b)
	}
	return ""
}

// rename replaces every string, and every char array holding text, that names a
// key of renames. All renames apply at once, so one never feeds another.
func rename(v any, renames map[string]string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = rename(child, renames)
		}
	case []any:
		if to, ok := renames[textOf(t)]; ok && len(to) <= len(t) {
			return chars(to, len(t))
		}
		for i, child := range t {
			t[i] = rename(child, renames)
		}
	case string:
		if to, ok := renames[t]; ok {
			return to
		}
	}
	return v
}

// chars writes text as a zero-padded C char array of length n.
func chars(text string, n int) []any {
	out := make([]any, n)
	for i := range out {
		c := 0
		if i < len(text) {
			c = int(text[i])
		}
		out[i] = json.Number(strconv.Itoa(c))
	}
	return out
}
