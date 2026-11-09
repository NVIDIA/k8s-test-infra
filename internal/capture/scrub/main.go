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

// Package scrub copies a directory of real-node captures, such as the one
// `mokka-capture collect` writes, replacing everything that names a particular
// machine with a placeholder, so the copy can be committed. It backs
// `mokka-capture scrub`.
//
//	mokka-capture scrub -out scrubbed -replace worker-7=node-a raw/worker-7 raw/cluster
//	mokka-capture scrub -check smi-q.xml
//
// It rewrites, in every text file:
//
//   - GPU and MIG UUIDs, other UUIDs (fabric, Kubernetes object UIDs) and 32-digit
//     machine IDs;
//   - serial numbers, in nvidia-smi XML (<serial>, <chassis_serial_number>) and
//     in nvidia-smi text output;
//   - PDIs and fabric GUIDs, 16-digit hex GUIDs, colon-separated GUIDs and IPv6
//     addresses such as RDMA GIDs; a 64-bit ID gets one placeholder whichever
//     notation it is written in (0xcc30890300cbbe0e, cc30:8903:00cb:be0e);
//   - MAC addresses, and interface names derived from them (enx<mac>);
//   - IPv4 addresses;
//   - any literal given with -replace, for host and cluster names.
//
// One value always gets one placeholder, across every file and directory in the
// run, so a GPU keeps the same placeholder in nvidia-smi, the NVML dump and the
// CDI specs. -seed names a file whose GPU UUIDs are numbered first, in file
// order, so placeholders follow the GPU index.
//
// A serial number found in a context that names it (an XML element, a "Serial
// Number" line, a VPD field) is also replaced wherever else the same value
// appears in the run.
//
// Values that report nothing (empty, N/A, all zeros) are left alone, and so are
// 16-digit hex values below 2^32 or all ones in their low bits, such as clock
// event and timestamp masks: random IDs and vendor-prefixed GUIDs are neither. Binary files
// and files matching -exclude are skipped and named on standard error.
//
// The result is a best effort, not a guarantee: read it, and grep it for
// anything specific to the machine before committing. -report writes a table of
// every substitution made (original and placeholder) for that audit; the table
// contains the originals, so never commit it.
package scrub

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/redact"
)

// kinds of value the scrubber replaces. Each draws its own placeholder numbers.
const (
	kindGPU     = "gpu-uuid"
	kindMIG     = "mig-uuid"
	kindUUID    = "uuid"
	kindMachine = "machine-id"
	kindSerial  = "serial"
	kindChassis = "chassis-serial"
	kindGUID    = "guid"
	kindIPv6    = "ipv6"
	kindMAC     = "mac"
	kindIPv4    = "ipv4"
)

// scrubber holds the numbering shared by every file in one run.
type scrubber struct {
	replacements [][2]string
	seen         map[string]map[string]string
	next         map[string]int
	learned      map[string]string // serial value -> kind, found by learn
	log          []substitution
	counts       map[string]int
}

type substitution struct{ kind, from, to string }

func newScrubber(replacements [][2]string) *scrubber {
	// Longest first, so a name that contains another is replaced whole.
	sort.SliceStable(replacements, func(i, j int) bool {
		return len(replacements[i][0]) > len(replacements[j][0])
	})
	return &scrubber{
		replacements: replacements,
		seen:         map[string]map[string]string{},
		next:         map[string]int{},
		counts:       map[string]int{},
		learned:      map[string]string{},
	}
}

// first is the number given to the first distinct value of a kind. GPU UUIDs
// count from 0, as the nvidia-smi fixtures do; the rest count from 1, because
// zero reads as "absent".
func first(kind string) int {
	if kind == kindGPU || kind == kindMIG {
		return 0
	}
	return 1
}

func (s *scrubber) number(kind, value string) int {
	if s.seen[kind] == nil {
		s.seen[kind] = map[string]string{}
		s.next[kind] = first(kind)
	}
	key := strings.ToLower(value)
	if kind == kindGUID {
		key = guidKey(value)
	}
	if _, ok := s.seen[kind][key]; !ok {
		s.seen[kind][key] = strconv.Itoa(s.next[kind])
		s.next[kind]++
	}
	n, _ := strconv.Atoi(s.seen[kind][key])
	return n
}

//nolint:cyclop // one branch per kind of identifier; splitting it would hide the order the rules apply in
func (s *scrubber) placeholder(kind, value string) string {
	n := s.number(kind, value)
	var out string
	switch kind {
	case kindGPU:
		out = fmt.Sprintf("GPU-%08x-0000-0000-0000-%012x", n, n)
	case kindMIG:
		out = fmt.Sprintf("MIG-%08x-0000-0000-0000-%012x", n, n)
	case kindUUID:
		out = fmt.Sprintf("%08x-0000-0000-0000-%012x", n, n)
	case kindMachine:
		out = fmt.Sprintf("%032x", n)
	case kindSerial:
		out = fmt.Sprintf("1650000000%03d", n)
	case kindChassis:
		out = fmt.Sprintf("1820000000%03d", n)
	case kindGUID:
		out = fmt.Sprintf("0x%016x", n)
	case kindIPv6:
		out = fmt.Sprintf("fe80:0000:0000:0000:0000:0000:0000:%04x", n)
	case kindMAC:
		out = fmt.Sprintf("02:00:00:00:%02x:%02x", n>>8, n&0xff)
	case kindIPv4:
		out = fmt.Sprintf("192.0.2.%d", n)
		if n > 254 {
			out = fmt.Sprintf("198.51.100.%d", n-254)
		}
	}
	s.log = append(s.log, substitution{kind, value, out})
	s.counts[kind]++
	return out
}

// guidPlaceholder keeps the notation of the original: an RDMA node GUID written
// as cc30:8903:00cb:be0e stays four groups of four digits.
func (s *scrubber) guidPlaceholder(value string) string {
	n := s.number(kindGUID, value)
	s.counts[kindGUID]++
	out := fmt.Sprintf("0000:0000:0000:%04x", n)
	s.log = append(s.log, substitution{kindGUID, value, out})
	return out
}

var (
	uuidPat = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

	// Alternatives are tried in order at each position, so a GPU UUID is taken
	// whole before its UUID part could be matched on its own, and an 8-group
	// IPv6 address before its first four groups could look like a GUID.
	tokenRE = regexp.MustCompile(strings.Join([]string{
		`(?P<gpu>\bGPU-` + uuidPat + `)`,
		`(?P<mig>\bMIG-` + uuidPat + `)`,
		`(?P<uuid>\b` + uuidPat + `\b)`,
		`(?P<ipv6>\b[0-9a-fA-F]{4}(?::[0-9a-fA-F]{4}){7}\b)`,
		`(?P<enx>\benx[0-9a-f]{12}\b)`,
		`(?P<mac>\b[0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){5}\b)`,
		`(?P<hex16>\b0x[0-9a-fA-F]{16}\b)`,
		`(?P<guid>\b[0-9a-fA-F]{4}(?::[0-9a-fA-F]{4}){3}\b)`,
		// Any other IPv6 notation (fd00::1, ::ffff:10.0.0.1); replaceToken
		// keeps only what parses as an address, so times like 04:50:48 stay.
		`(?P<ipv6c>[0-9a-fA-F]{0,4}(?::[0-9a-fA-F]{0,4}){2,7}(?:\.\d{1,3}){0,3})`,
		`(?P<machine>\b[0-9a-f]{32}\b)`,
		`(?P<ipv4>\b\d{1,3}(?:\.\d{1,3}){3}\b)`,
	}, "|"))

	groupNames = tokenRE.SubexpNames()

	vpdRE = regexp.MustCompile(`(\[(?:VU|V3)\] Vendor specific: )([0-9a-fA-F]{16,32})(\S*)`)

	xmlElementRE = regexp.MustCompile(
		`(<(serial|chassis_serial_number|pdi|gpu_fabric_guid)>)([^<]*)(</(?:serial|chassis_serial_number|pdi|gpu_fabric_guid)>)`)

	// nvidia-smi text output: "Serial Number    : 123".
	serialLineRE = regexp.MustCompile(
		`(?m)^(\s*(?:Board )?(Serial Number|Chassis Serial Number)\s*:\s*)(\S+)[ \t]*$`)
)

func isAbsent(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "N/A") {
		return true
	}
	return strings.Trim(strings.TrimPrefix(v, "0x"), "0-:") == ""
}

// ipv4Keep reports whether an address-shaped string should be left alone: a
// version number (a leading zero, or a part above 255), the unspecified,
// loopback and broadcast addresses, and netmasks (ones, then only zeros).
func ipv4Keep(v string) bool {
	var n uint32
	for _, p := range strings.Split(v, ".") {
		if len(p) > 1 && p[0] == '0' {
			return true
		}
		o, _ := strconv.Atoi(p)
		if o > 255 {
			return true
		}
		n = n<<8 | uint32(o)
	}
	inv := ^n
	return inv&(inv+1) == 0 || n>>24 == 127
}

var (
	placeholderSerialRE = regexp.MustCompile(`^(1650000000|1820000000)\d{3}$`)

	// Each pattern captures a serial number in a context that names it as one.
	// The same value then gets scrubbed wherever else it appears, such as in an
	// NVML dump or a kernel log line, where nothing around it says "serial".
	learnPatterns = []struct {
		kind string
		re   *regexp.Regexp
		list bool // the capture is a comma-separated list
	}{
		{kindSerial, regexp.MustCompile(`<serial>([^<]+)</serial>`), false},
		{kindChassis, regexp.MustCompile(`<chassis_serial_number>([^<]+)</chassis_serial_number>`), false},
		{kindChassis, regexp.MustCompile(`(?m)Chassis Serial Number\s*:\s*(\S+)`), false},
		{kindSerial, regexp.MustCompile(`(?m)^\s*(?:Board )?Serial Number\s*:\s*(\S+)`), false},
		{kindSerial, regexp.MustCompile(`Board Serial Number:\s*(\S+)`), false},
		{kindSerial, regexp.MustCompile(`(?m)^serial: (\S+)$`), true},
		{kindChassis, regexp.MustCompile(`(?m)^platform\.chassis_serial_number: (\S+)$`), true},
		{kindSerial, regexp.MustCompile(`"GetSerial":\{"ret":"SUCCESS","value":"([^"]+)"`), false},
		{kindSerial, regexp.MustCompile(`\[SN\] Serial number: (\S+)`), false},
	}
)

// learn records the serial numbers in text, so scrub can find them elsewhere.
func (s *scrubber) learn(text string) {
	for _, p := range learnPatterns {
		for _, m := range p.re.FindAllStringSubmatch(text, -1) {
			values := []string{m[1]}
			if p.list {
				values = strings.Split(m[1], ",")
			}
			for _, v := range values {
				if len(v) >= 6 && !isAbsent(v) && !placeholderSerialRE.MatchString(v) {
					s.learned[v] = p.kind
				}
			}
		}
	}
}

//nolint:cyclop // one branch per kind of identifier; splitting it would hide the order the rules apply in
func (s *scrubber) scrub(text string) string {
	for _, r := range s.replacements {
		text = strings.ReplaceAll(text, r[0], r[1])
	}

	values := make([]string, 0, len(s.learned))
	for v := range s.learned {
		values = append(values, v)
	}
	// Longest first, so a value inside a longer one is not replaced in part; then
	// in order of appearance, so the numbering is the same on every run.
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		if a, b := strings.Index(text, values[i]), strings.Index(text, values[j]); a != b {
			return uint(a) < uint(b) // -1, not in the text, sorts last
		}
		return values[i] < values[j]
	})
	for _, v := range values {
		if strings.Contains(text, v) {
			text = strings.ReplaceAll(text, v, s.placeholder(s.learned[v], v))
		}
	}

	// lspci prints a device's vital product data. The unit-unique fields name
	// the unit; the part numbers describe the model and stay.
	text = vpdRE.ReplaceAllStringFunc(text, func(m string) string {
		p := vpdRE.FindStringSubmatch(m)
		if isAbsent(p[2]) {
			return m
		}
		return p[1] + s.placeholder(kindMachine, p[2]) + p[3]
	})

	text = xmlElementRE.ReplaceAllStringFunc(text, func(m string) string {
		p := xmlElementRE.FindStringSubmatch(m)
		id := p[2] == "pdi" || p[2] == "gpu_fabric_guid"
		if isAbsent(p[3]) || id && notAnID(p[3]) || !id && placeholderSerialRE.MatchString(strings.TrimSpace(p[3])) {
			return m
		}
		switch p[2] {
		case "serial":
			return p[1] + s.placeholder(kindSerial, p[3]) + p[4]
		case "chassis_serial_number":
			return p[1] + s.placeholder(kindChassis, p[3]) + p[4]
		default:
			return p[1] + s.placeholder(kindGUID, p[3]) + p[4]
		}
	})

	text = serialLineRE.ReplaceAllStringFunc(text, func(m string) string {
		p := serialLineRE.FindStringSubmatch(m)
		if isAbsent(p[3]) || placeholderSerialRE.MatchString(p[3]) {
			return m
		}
		kind := kindSerial
		if strings.Contains(p[2], "Chassis") {
			kind = kindChassis
		}
		return p[1] + s.placeholder(kind, p[3])
	})

	var out strings.Builder
	last := 0
	for _, loc := range tokenRE.FindAllStringSubmatchIndex(text, -1) {
		match := text[loc[0]:loc[1]]
		group := ""
		for i := 1; i < len(groupNames); i++ {
			if loc[2*i] >= 0 {
				group = groupNames[i]
				break
			}
		}

		replacement, ok := s.replaceToken(group, match, text, loc[0], loc[1])
		if !ok {
			continue
		}
		out.WriteString(text[last:loc[0]])
		out.WriteString(replacement)
		last = loc[1]
	}
	out.WriteString(text[last:])
	return out.String()
}

//nolint:cyclop // one branch per kind of identifier; splitting it would hide the order the rules apply in
func (s *scrubber) replaceToken(group, match, text string, start, end int) (string, bool) {
	// An address inside a longer dotted number is a version, not an address.
	if group == "ipv4" {
		if start > 0 && text[start-1] == '.' || end < len(text) && text[end] == '.' && end+1 < len(text) && text[end+1] >= '0' && text[end+1] <= '9' {
			return "", false
		}
		if ipv4Keep(match) {
			return "", false
		}
	}
	if isAbsent(match) || strings.EqualFold(match, "ff:ff:ff:ff:ff:ff") || isPlaceholder(group, match) {
		return "", false
	}

	switch group {
	case "gpu":
		return s.placeholder(kindGPU, match), true
	case "mig":
		return s.placeholder(kindMIG, match), true
	case "uuid":
		return s.placeholder(kindUUID, match), true
	case "ipv6", "ipv6c":
		return s.ipv6Placeholder(match, text, start, end)
	case "enx":
		mac := match[3:]
		colon := fmt.Sprintf("%s:%s:%s:%s:%s:%s", mac[0:2], mac[2:4], mac[4:6], mac[6:8], mac[8:10], mac[10:12])
		return "enx" + strings.ReplaceAll(s.placeholder(kindMAC, colon), ":", ""), true
	case "mac":
		return s.placeholder(kindMAC, match), true
	case "hex16":
		// Kernel virtual addresses (module load addresses) look like GUIDs.
		if strings.HasPrefix(strings.ToLower(match), "0xffff") {
			return "", false
		}
		return s.placeholder(kindGUID, match), true
	case "guid":
		// The subnet prefix of a link-local address is not an identifier.
		if strings.HasPrefix(strings.ToLower(match), "fe80:0000:0000:0000") {
			return "", false
		}
		return s.guidPlaceholder(match), true
	case "machine":
		return s.placeholder(kindMachine, match), true
	case "ipv4":
		return s.placeholder(kindIPv4, match), true
	}
	return "", false
}

// ipv6Placeholder replaces an IPv6 address in any notation. One address gets one
// placeholder however it is written. Text that is not an address on its own (a
// time, a C++ name like std::vector, a::b with no digit) and addresses that name no host
// (unspecified, loopback, multicast) are left alone.
func (s *scrubber) ipv6Placeholder(match, text string, start, end int) (string, bool) {
	lead, core, trail := trimAddrPunct(match)
	addr, ok := hostAddress(core, text, start+len(lead), end-len(trail))
	if !ok {
		return "", false
	}
	expanded := addr.StringExpanded()
	if isPlaceholder("ipv6", expanded) {
		return "", false
	}
	return lead + s.placeholder(kindIPv6, expanded) + trail, true
}

// trimAddrPunct splits off punctuation the pattern takes in but an address does
// not end or start with: the colon of "address:fd00::1", and the full stop or
// colon after one at the end of a sentence or a log prefix.
func trimAddrPunct(match string) (lead, core, trail string) {
	core = match
	if strings.HasPrefix(core, ":") && !strings.HasPrefix(core, "::") {
		lead, core = ":", core[1:]
	}
	for n := len(core); n > 0; n = len(core) {
		c := core[n-1]
		if c != '.' && (c != ':' || strings.HasSuffix(core, "::")) {
			break
		}
		core, trail = core[:n-1], core[n-1:]+trail
	}
	return lead, core, trail
}

// hostAddress parses a match as an IPv6 address that names a host.
func hostAddress(match, text string, start, end int) (netip.Addr, bool) {
	if !standsAlone(match, text, start, end) {
		return netip.Addr{}, false
	}
	host, _, _ := strings.Cut(match, "%")
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Is6() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() {
		return netip.Addr{}, false
	}
	return addr, true
}

// standsAlone reports whether a match could be an address on its own: not part
// of a longer word, and with at least two groups and a digit (fd00, fe80, 2001)
// or at least three groups. a::b or "1::" in a log line is text, and fe80:: alone
// is a prefix.
func standsAlone(match, text string, start, end int) bool {
	if start > 0 && isWordChar(text[start-1]) || end < len(text) && isWordChar(text[end]) {
		return false
	}
	groups := hexGroups(match)
	return groups >= 3 || groups == 2 && strings.ContainsAny(match, "0123456789")
}

func hexGroups(v string) int {
	n := 0
	for _, g := range strings.FieldsFunc(v, func(r rune) bool { return r == ':' }) {
		if g != "" {
			n++
		}
	}
	return n
}

func isWordChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// scrubPath scrubs each component of a relative path, so a directory or file
// named after a machine is renamed like the text that mentions it.
func (s *scrubber) scrubPath(rel string) string {
	parts := strings.Split(rel, string(filepath.Separator))
	for i, p := range parts {
		if p != "." && p != ".." {
			parts[i] = s.scrub(p)
		}
	}
	return filepath.Join(parts...)
}

// isPlaceholder reports whether a value is already one of this tool's
// placeholders, so running the tool over its own output changes nothing.
//
//nolint:cyclop // one branch per kind of identifier; splitting it would hide the order the rules apply in
func isPlaceholder(group, v string) bool {
	switch group {
	case "gpu", "mig", "uuid":
		i := strings.Index(v, "-")
		rest := v
		if group != "uuid" {
			rest = v[i+1:]
		}
		parts := strings.Split(rest, "-")
		return len(parts) == 5 && parts[1] == "0000" && parts[2] == "0000" && parts[3] == "0000" &&
			strings.TrimLeft(parts[0], "0") == strings.TrimLeft(parts[4], "0") && strings.HasPrefix(parts[4], "0000")
	case "mac":
		return strings.HasPrefix(v, "02:00:00:00:")
	case "enx":
		return strings.HasPrefix(v, "enx02000000")
	case "hex16", "guid":
		return notAnID(v)
	case "ipv6":
		return strings.HasPrefix(strings.ToLower(v), "fe80:0000:0000:0000:0000:0000:0000:")
	case "machine":
		return strings.HasPrefix(v, "0000000000000000000000000000")
	case "ipv4":
		return strings.HasPrefix(v, "192.0.2.") || strings.HasPrefix(v, "198.51.100.")
	}
	return false
}

// guidKey is the 64-bit value a GUID names, the same in 0x and colon notation.
func guidKey(v string) string {
	n, err := strconv.ParseUint(strings.ReplaceAll(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "0x"), ":", ""), 16, 64)
	if err != nil {
		return strings.ToLower(v)
	}
	return fmt.Sprintf("%016x", n)
}

// notAnID reports whether a 16-digit hex value or colon GUID is below 2^32 or
// all ones in its low bits (0x7fffffffffffffff). Such a value is a mask, a count
// or one of this tool's placeholders, never a device identifier.
func notAnID(v string) bool {
	n, err := strconv.ParseUint(guidKey(v), 16, 64)
	return err == nil && (n < 1<<32 || n&(n+1) == 0)
}

// seedGPUs numbers the GPU UUIDs of a file in the order they appear, so a GPU's
// placeholder follows its index (nvidia-smi -L lists GPUs in index order).
func (s *scrubber) seedGPUs(text string) {
	for _, m := range regexp.MustCompile(`GPU-`+uuidPat).FindAllString(text, -1) {
		s.number(kindGPU, m)
	}
}

// isDump reports whether a file is an NVML dump, plain or gzipped.
func isDump(name string) bool {
	return name == "nvml-dump.json" || name == "nvml-dump.json.gz"
}

// scrubDump redacts a recording's identity fields, then scrubs the rest as text
// (host names and the like). A gzipped recording stays gzipped.
func scrubDump(s *scrubber, name string, raw, smiXML []byte) ([]byte, error) {
	zipped := strings.HasSuffix(name, ".gz")
	if zipped {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if raw, err = io.ReadAll(zr); err != nil {
			return nil, err
		}
	}
	red, err := redact.New().JSON(raw)
	if err != nil {
		return nil, err
	}
	if red, err = s.linkDump(red, smiXML); err != nil {
		return nil, err
	}
	out := []byte(s.scrub(string(red)))
	if !zipped {
		return out, nil
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(out); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isBinary(b []byte) bool { return bytes.IndexByte(b, 0) >= 0 }

func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// destinations is the output directory of each source: its scrubbed base name
// under out. Two sources that would write to the same directory are an error,
// because one would overwrite the other.
func destinations(s *scrubber, out string, srcs []string) ([]string, error) {
	dsts := make([]string, len(srcs))
	seen := map[string]string{}
	for i, src := range srcs {
		dsts[i] = filepath.Join(out, s.scrub(filepath.Base(filepath.Clean(src))))
		if prev, ok := seen[dsts[i]]; ok {
			return nil, fmt.Errorf("%s and %s would both be written to %s", prev, src, dsts[i])
		}
		seen[dsts[i]] = src
	}
	return dsts, nil
}

// sourceHolding returns the source directory that out is, or lies inside, or ""
// if there is none.
func sourceHolding(out string, srcs []string) string {
	o, err := filepath.Abs(out)
	if err != nil {
		return ""
	}
	for _, src := range srcs {
		a, err := filepath.Abs(src)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(a, o); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return src
		}
	}
	return ""
}

// options are the flags of `mokka-capture scrub`.
type options struct {
	out, report               string
	replaces, excludes, seeds []string
	check                     bool
}

// Command is `mokka-capture scrub`.
func Command() *cli.Command {
	var opts options
	return &cli.Command{
		Name:  "scrub",
		Usage: "replace identifiers in a capture, or -check that none are left",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "out",
				Usage:       "directory to write the scrubbed copy to (required, must not exist or be empty)",
				Destination: &opts.out,
			},
			&cli.StringFlag{
				Name:        "report",
				Usage:       "write the table of substitutions here (contains originals: never commit it)",
				Destination: &opts.report,
			},
			&cli.StringSliceFlag{
				Name:        "replace",
				Usage:       "old=new literal replacement, for host and cluster names",
				Destination: &opts.replaces,
			},
			&cli.StringSliceFlag{
				Name:        "exclude",
				Usage:       "file name pattern to skip",
				DefaultText: "errors.log and *.so*",
				Destination: &opts.excludes,
			},
			&cli.StringSliceFlag{
				Name:        "seed",
				Usage:       "file whose GPU UUIDs are numbered first, in file order",
				Destination: &opts.seeds,
			},
			&cli.BoolFlag{
				Name:        "check",
				Usage:       "check the nvidia-smi -q -x files given in place of SRC_DIR for identifiers left unscrubbed instead; exit 1 if any",
				Destination: &opts.check,
			},
		},
		// Each -replace, -exclude or -seed value is one value, comma or not.
		DisableSliceFlagSeparator: true,
		Arguments: []cli.Argument{
			&cli.StringArgs{Name: "SRC_DIR", UsageText: "SRC_DIR...", Min: 1, Max: -1},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			srcs, stdout, stderr := cmd.StringArgs("SRC_DIR"), cmd.Root().Writer, cmd.Root().ErrWriter
			if opts.check {
				return cmdline.Exit(runCheck(srcs, stdout, stderr))
			}
			if opts.out == "" {
				return cmdline.Usage(cmd, "-out is required unless -check is given")
			}
			return cmdline.Exit(run(opts, srcs, stderr))
		},
	}
}

//nolint:cyclop // the walk over the capture, in the order it runs
func run(opts options, srcs []string, stderr io.Writer) int {
	out, excludes := opts.out, opts.excludes
	if len(excludes) == 0 {
		excludes = []string{"errors.log", "*.so", "*.so.*"}
	}

	var reps [][2]string
	for _, r := range opts.replaces {
		i := strings.Index(r, "=")
		if i <= 0 {
			_, _ = fmt.Fprintf(stderr, "-replace %q: want old=new\n", r)
			return 2
		}
		reps = append(reps, [2]string{r[:i], r[i+1:]})
	}

	if src := sourceHolding(out, srcs); src != "" {
		_, _ = fmt.Fprintf(stderr, "%s is inside %s; the walk would copy its own output\n", out, src)
		return 1
	}
	if entries, err := os.ReadDir(out); err == nil && len(entries) > 0 {
		_, _ = fmt.Fprintf(stderr, "%s is not empty\n", out)
		return 1
	}

	s := newScrubber(reps)
	for _, seed := range opts.seeds {
		raw, err := os.ReadFile(seed)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		s.seedGPUs(string(raw))
	}

	for _, src := range srcs {
		_ = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || excluded(d.Name(), excludes) {
				return nil
			}
			if raw, err := os.ReadFile(path); err == nil && !isBinary(raw) && !isDump(d.Name()) {
				s.learn(string(raw))
			}
			return nil
		})
	}

	dsts, err := destinations(s, out, srcs)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	status := 0
	for i, src := range srcs {
		dst := dsts[i]
		err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, path)
			rel = s.scrubPath(rel)
			if d.IsDir() {
				if excluded(d.Name(), excludes) {
					return filepath.SkipDir
				}
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			if excluded(d.Name(), excludes) {
				_, _ = fmt.Fprintf(stderr, "skipped %s (excluded)\n", path)
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if isDump(d.Name()) {
				// An NVML recording keeps identifiers as byte arrays and integers,
				// which no text rule matches; redact it by field name first.
				smiXML, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "smi-q.xml"))
				out, err := scrubDump(s, d.Name(), raw, smiXML)
				if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				return os.WriteFile(filepath.Join(dst, rel), out, 0o644)
			}
			if isBinary(raw) {
				_, _ = fmt.Fprintf(stderr, "skipped %s (binary)\n", path)
				return nil
			}
			return os.WriteFile(filepath.Join(dst, rel), []byte(s.scrub(string(raw))), 0o644)
		})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			status = 1
		}
	}

	kinds := make([]string, 0, len(s.counts))
	for k := range s.counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		_, _ = fmt.Fprintf(stderr, "%-20s %d distinct value(s), %d replacement(s)\n", k, len(s.seen[k]), s.counts[k])
	}

	if opts.report != "" {
		var b strings.Builder
		for _, l := range s.log {
			_, _ = fmt.Fprintf(&b, "%s\t%s\t%s\n", l.kind, l.from, l.to)
		}
		if err := os.WriteFile(opts.report, []byte(b.String()), 0o600); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return status
}
