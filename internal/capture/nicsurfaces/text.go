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

package nicsurfaces

import (
	"regexp"
	"sort"
	"strings"
)

// block is one "== <name>" section of a capture file.
type block struct {
	name  string
	lines []string
}

// blocks splits text into its "== <name>" sections.
func blocks(text string) []block {
	var out []block
	for _, l := range strings.Split(text, "\n") {
		if name, ok := strings.CutPrefix(l, "== "); ok {
			out = append(out, block{name: strings.TrimSpace(name)})
			continue
		}
		if len(out) > 0 && strings.TrimSpace(l) != "" {
			out[len(out)-1].lines = append(out[len(out)-1].lines, l)
		}
	}
	return out
}

// keyValues reads key=value lines; the first of a repeated key wins.
func keyValues(lines []string) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		if k, v, ok := strings.Cut(l, "="); ok {
			if _, seen := out[k]; !seen {
				out[k] = v
			}
		}
	}
	return out
}

// sysfsValue reads what `echo "f=$(cat f 2>&1)"` printed: a file that does not
// exist has no value, and one that exists but cannot be read (a link that is
// down has no speed) reads as empty.
func sysfsValue(v string) (string, bool) {
	if !strings.HasPrefix(v, "cat: ") {
		return v, true
	}
	if strings.Contains(v, "No such file or directory") || strings.Contains(v, "can't open") {
		return "", false
	}
	return "", true
}

// setSysfs sets a record from a key=value line's value when the file existed.
func (n *Node) setSysfs(src, obj, attr string, kv map[string]string, key string) {
	v, ok := kv[key]
	if !ok {
		return
	}
	if v, ok = sysfsValue(v); ok {
		n.set(src, obj, attr, v)
	}
}

var bdfRE = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)

// lastBDF returns the bus ID a sysfs path ends in, or "" when it names no PCI
// function.
func lastBDF(path string) string {
	b := path[strings.LastIndex(path, "/")+1:]
	if bdfRE.MatchString(b) {
		return b
	}
	return ""
}

// rootOf returns the root complex a sysfs device path sits under:
// /sys/devices/pci0000:00/... gives pci0000:00.
func rootOf(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 3 {
		return parts[3]
	}
	return ""
}

// sortedSet joins the distinct non-empty members, sorted, with commas.
func sortedSet(members []string) string {
	set := map[string]bool{}
	for _, m := range members {
		if m != "" {
			set[m] = true
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// colonValue splits "Key   : value" into its trimmed key and value.
func colonValue(line string) (string, string, bool) {
	k, v, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}

// attrName turns a tool's label into an attribute name: lower case, with runs
// of spaces and brackets as one underscore.
func attrName(label string) string {
	return strings.Trim(labelRun.ReplaceAllString(strings.ToLower(strings.TrimSpace(label)), "_"), "_")
}

var labelRun = regexp.MustCompile(`[ ()\[\]]+`)
