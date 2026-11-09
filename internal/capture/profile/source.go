// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package profile

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// source is where one profile value is read from. read returns a GPU's raw
// answer, or "" and the reason the capture has none (usually the NVML return
// code); format turns the raw answer into YAML, or reports that the profile
// has no spelling for it.
type source struct {
	from   string
	read   func(c *capture, gpu int) (any, string)
	format func(raw any) (string, bool)
}

// getter reads a device getter from the NVML dump. path selects a struct field
// (string) or list element (int) of the answer, so getter("GetMemoryInfo_v2",
// "Total") is the Total field and getter("GetEccMode", 1) the pending mode.
// A getter that takes arguments is named with call.
func getter(key string, path ...any) source {
	return source{from: key + steps(path), read: func(c *capture, gpu int) (any, string) {
		return recorded(c, gpu, key, path)
	}, format: plain}
}

// call names the dump entry of a getter called with arguments, as the dump
// records it: call("GetClockInfo", nvml.CLOCK_SM) is "GetClockInfo(1)".
func call(name string, args ...any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprintf("%d", a)
	}
	return name + "(" + strings.Join(parts, ",") + ")"
}

// sysGetter reads a process-wide getter, the same for every GPU.
func sysGetter(key string) source {
	return source{from: key, read: func(c *capture, _ int) (any, string) {
		return recorded(c, -1, key, nil)
	}, format: plain}
}

// answered is true when the getter succeeds, for keys that only say whether a
// feature is supported.
func answered(key string) source {
	return source{from: key + " answers", read: func(c *capture, gpu int) (any, string) {
		r, ok := c.call(gpu, key)
		if !ok {
			return nil, "not recorded"
		}
		return r.Ret == "SUCCESS", ""
	}, format: plain}
}

// smi reads an element of the GPU's block in smi-q.xml. N/A counts as no
// answer.
func smi(path string) source {
	return source{from: smiXMLFile + " " + path, read: func(c *capture, gpu int) (any, string) {
		if gpu >= len(c.gpus) {
			return nil, smiXMLFile + " not captured"
		}
		n := c.gpus[gpu].find(path)
		if n == nil {
			return nil, "no " + path
		}
		if n.text() == notSupportedNA {
			return nil, notSupportedNA
		}
		return n.text(), ""
	}, format: plain}
}

// field reads one NVML field value, decoded by the value type the driver
// reported. Field IDs below the block a newer 580 driver renumbered are the
// same in every driver branch.
func field(id int) source {
	key := fmt.Sprintf("GetFieldValues(id=%d)", id)
	return source{from: key, read: func(c *capture, gpu int) (any, string) {
		v, why := recorded(c, gpu, key, nil)
		if why != "" {
			return nil, why
		}
		typ, okT := at(v, "valueType")
		hexValue, okV := at(v, "value")
		t, okN := asInt(typ)
		h, okS := hexValue.(string)
		if !okT || !okV || !okN || !okS {
			return nil, "unreadable field value"
		}
		n, ok := decodeField(t, h)
		if !ok {
			return nil, "unreadable field value"
		}
		return n, ""
	}, format: plain}
}

// decodeField decodes the little-endian 8-byte value of an nvmlFieldValue_t.
func decodeField(valueType int64, hexValue string) (json.Number, bool) {
	b, err := hex.DecodeString(hexValue)
	if err != nil || len(b) != 8 {
		return "", false
	}
	u := binary.LittleEndian.Uint64(b)
	switch nvml.ValueType(valueType) {
	case nvml.VALUE_TYPE_DOUBLE:
		return json.Number(strconv.FormatFloat(math.Float64frombits(u), 'g', -1, 64)), true
	case nvml.VALUE_TYPE_UNSIGNED_INT:
		return json.Number(strconv.FormatUint(uint64(uint32(u)), 10)), true
	case nvml.VALUE_TYPE_SIGNED_INT:
		return json.Number(strconv.FormatInt(int64(int32(u)), 10)), true
	case nvml.VALUE_TYPE_UNSIGNED_SHORT:
		return json.Number(strconv.FormatUint(uint64(uint16(u)), 10)), true
	case nvml.VALUE_TYPE_SIGNED_LONG_LONG:
		return json.Number(strconv.FormatInt(int64(u), 10)), true
	case nvml.VALUE_TYPE_UNSIGNED_LONG, nvml.VALUE_TYPE_UNSIGNED_LONG_LONG:
		return json.Number(strconv.FormatUint(u, 10)), true
	case nvml.VALUE_TYPE_COUNT:
		// Not a value type: the size of the enum.
	}
	return "", false
}

// hostValue reads key=value text such as host-dmi.txt; it is the same for
// every GPU.
func hostValue(file, key string) source {
	return source{from: file + " " + key, read: func(c *capture, _ int) (any, string) {
		text, ok := c.files[file]
		if !ok {
			return nil, file + " not captured"
		}
		v, ok := keyValues(text)[key]
		if !ok || v == "" {
			return nil, "no " + key
		}
		return v, ""
	}, format: plain}
}

func recorded(c *capture, gpu int, key string, path []any) (any, string) {
	r, ok := c.call(gpu, key)
	if !ok {
		return nil, "not recorded"
	}
	if r.Ret != "SUCCESS" {
		return nil, r.Ret
	}
	v, ok := at(r.Value, path...)
	if !ok {
		return nil, "no " + strings.TrimPrefix(steps(path), ".") + " in the answer"
	}
	return v, ""
}

func steps(path []any) string {
	var b strings.Builder
	for _, p := range path {
		switch s := p.(type) {
		case string:
			b.WriteString("." + s)
		case int:
			fmt.Fprintf(&b, "[%d]", s)
		}
	}
	return b.String()
}

// as replaces the source's formatting.
func (s source) as(format func(any) (string, bool)) source {
	s.format = format
	return s
}

// show renders a raw answer for comments and -explain.
func show(raw any) string {
	switch v := raw.(type) {
	case json.Number:
		return v.String()
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case []any:
		if s, ok := cString(v); ok && printable(s) {
			return s
		}
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = show(x)
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	raw2, _ := json.Marshal(raw)
	return string(raw2)
}

func printable(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

// Formats. Each turns one raw answer into the YAML the profile key takes.

func plain(raw any) (string, bool) {
	switch v := raw.(type) {
	case json.Number:
		return v.String(), true
	case string:
		return strconv.Quote(v), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

// hex32 writes a PCI ID pair the way the profiles do: 0x304110DE.
func hex32(raw any) (string, bool) {
	n, ok := asInt(raw)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("0x%08X", uint32(n)), true
}

// named maps an NVML enum value onto the profile's name for it.
func named(names map[int64]string) func(any) (string, bool) {
	return func(raw any) (string, bool) {
		n, ok := asInt(raw)
		if !ok {
			return "", false
		}
		name, ok := names[n]
		if !ok {
			return "", false
		}
		return strconv.Quote(name), true
	}
}

// namedBits lists the names of the enum values set in an NVML mask, where
// value v is bit 1<<v, in value order. A mask with no bits, or with a bit the
// profile has no name for, has no profile value.
func namedBits(names map[int64]string) func(any) (string, bool) {
	return func(raw any) (string, bool) {
		mask, ok := asInt(raw)
		if !ok || mask <= 0 {
			return "", false
		}
		var list []string
		for v := int64(0); mask>>v != 0; v++ {
			if mask&(1<<v) == 0 {
				continue
			}
			name, ok := names[v]
			if !ok {
				return "", false
			}
			list = append(list, strconv.Quote(name))
		}
		return "[" + strings.Join(list, ", ") + "]", true
	}
}

// enabledName is NVML's nvmlEnableState_t (and the 0/1 modes that share it).
var enabledName = named(map[int64]string{0: "disabled", 1: "enabled"})

// oneOf maps a text answer (from smi-q.xml) onto the profile's YAML.
func oneOf(values map[string]string) func(any) (string, bool) {
	return func(raw any) (string, bool) {
		s, ok := raw.(string)
		if !ok {
			return "", false
		}
		v, ok := values[s]
		return v, ok
	}
}

// equals is true when the answer is want.
func equals(want int64) func(any) (string, bool) {
	return func(raw any) (string, bool) {
		n, ok := asInt(raw)
		return strconv.FormatBool(n == want), ok
	}
}

// quotedNumber writes a number as a string key such as fan.speed_percent.
func quotedNumber(raw any) (string, bool) {
	n, ok := asInt(raw)
	return strconv.Quote(strconv.FormatInt(n, 10)), ok
}

// celsius reads nvidia-smi's "85 C".
func celsius(raw any) (string, bool) {
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	v, ok := strings.CutSuffix(s, " C")
	if _, err := strconv.Atoi(v); !ok || err != nil {
		return "", false
	}
	return v, true
}

// length counts a list answer, such as the retired page addresses.
func length(raw any) (string, bool) {
	if raw == nil {
		return "0", true
	}
	l, ok := raw.([]any)
	return strconv.Itoa(len(l)), ok
}

// pstate writes an NVML performance state as P0..P15.
func pstate(raw any) (string, bool) {
	n, ok := asInt(raw)
	if !ok || n < 0 || n > 15 {
		return "", false
	}
	return strconv.Quote("P" + strconv.FormatInt(n, 10)), true
}

// bit is true when the answer has mask set.
func bit(mask int64) func(any) (string, bool) {
	return func(raw any) (string, bool) {
		n, ok := asInt(raw)
		if !ok {
			return "", false
		}
		return strconv.FormatBool(n&mask != 0), true
	}
}

func cText(raw any) (string, bool) {
	s, ok := cString(raw)
	if !ok {
		return "", false
	}
	return strconv.Quote(s), true
}

// uuidBytes writes a 16-byte UUID array in its canonical text form.
func uuidBytes(raw any) (string, bool) {
	l, ok := raw.([]any)
	if !ok || len(l) != 16 {
		return "", false
	}
	b := make([]byte, 16)
	for i, x := range l {
		n, ok := asInt(x)
		if !ok {
			return "", false
		}
		b[i] = byte(n)
	}
	return strconv.Quote(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])), true
}

func archName(raw any) (string, bool) {
	n, ok := asInt(raw)
	if !ok || !gpuarch.Arch(n).Known() {
		return "", false
	}
	name := gpuarch.Arch(n).String()
	if _, known := gpuarch.Parse(name); !known {
		return "", false
	}
	return strconv.Quote(name), true
}

// cudaVersion splits NVML's CUDA driver version (13040) into the profile's
// "13.4", major 13 and minor 4.
func cudaVersion(raw any) (string, bool) {
	n, ok := asInt(raw)
	if !ok {
		return "", false
	}
	return strconv.Quote(fmt.Sprintf("%d.%d", n/1000, n%1000/10)), true
}

func cudaMajor(raw any) (string, bool) {
	n, ok := asInt(raw)
	return strconv.FormatInt(n/1000, 10), ok
}

func cudaMinor(raw any) (string, bool) {
	n, ok := asInt(raw)
	return strconv.FormatInt(n%1000/10, 10), ok
}

// nvlinkGeneration maps NVML's nvmlNvlinkVersion_t onto the NVLink generation
// the profile's nvlink.version holds: NVML numbers point releases too, so
// NVML_NVLINK_VERSION_6_0 is 8.
func nvlinkGeneration(raw any) (string, bool) {
	gen := map[int64]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 3, 6: 4, 7: 5, 8: 6}
	n, ok := asInt(raw)
	if !ok {
		return "", false
	}
	g, ok := gen[n]
	return strconv.Itoa(g), ok
}
