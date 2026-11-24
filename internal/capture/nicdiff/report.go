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

package nicdiff

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// surfaceOfKind names the surface each kind of record belongs to, so the
// report counts checks the way a reader thinks of a NIC.
var surfaceOfKind = map[string]string{
	"pci":      "PCI functions",
	"bridge":   "PCIe switch",
	"gpu":      "GPUs",
	"rdma":     "RDMA devices",
	"port":     "RDMA ports",
	"uverbs":   "Device files",
	"mad":      "Device files",
	"devnode":  "Device files",
	"net":      "Network interfaces",
	"module":   "Kernel and modules",
	"node":     "Kernel and modules",
	"rel":      "Relations",
	"ibv":      "ibv_devinfo",
	"ibstat":   "ibstat",
	"rdmalink": "rdma",
	"devlink":  "devlink",
	"fw":       "mlxconfig and flint",
	"cable":    "mlxlink",
	"topo":     "nvidia-smi topo -m",
}

// surfaceOrder is the order the report lists surfaces in: the hardware, then
// what the kernel shows of it, then what tools report.
var surfaceOrder = []string{
	"PCI functions", "PCIe switch", "GPUs", "RDMA devices", "RDMA ports", "Device files",
	"Network interfaces", "Kernel and modules", "Relations", "ibv_devinfo", "ibstat", "rdma",
	"devlink", "mlxconfig and flint", "mlxlink", "nvidia-smi topo -m",
}

func surfaceOf(rule string) string {
	if rule == "node.dev_infiniband" {
		return "Device files"
	}
	kind, _, _ := strings.Cut(rule, ".")
	if s, ok := surfaceOfKind[kind]; ok {
		return s
	}
	return kind
}

// group is the checks that share a status and a cause.
type group struct {
	cause, note string
	rules       map[string]bool
	n           int
}

// groupsOf groups the checks with status by cause and note, largest first.
func groupsOf(checks []check, status string) []group {
	byKey := map[[2]string]*group{}
	for _, c := range checks {
		if c.Status != status {
			continue
		}
		key := [2]string{c.Cause, c.Note}
		g := byKey[key]
		if g == nil {
			g = &group{cause: c.Cause, note: c.Note, rules: map[string]bool{}}
			byKey[key] = g
		}
		g.n++
		g.rules[c.Rule] = true
	}
	out := make([]group, 0, len(byKey))
	for _, g := range byKey {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].cause+out[i].note < out[j].cause+out[j].note
	})
	return out
}

// cell makes text safe for a Markdown table cell.
func cell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", `\|`)
}

func ruleList(rules map[string]bool) string {
	names := make([]string, 0, len(rules))
	for r := range rules {
		names = append(names, "`"+r+"`")
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// writeReport writes the comparison as a Markdown page a reviewer reads in a
// minute: checks by surface, then the failures and the checks not made, each
// grouped by cause, then what passes.
func writeReport(w io.Writer, checks []check, s summary, realDir, mockDir string) {
	failed := 0
	for _, n := range s.Failed {
		failed += n
	}
	skipped := 0
	for _, n := range s.NotCompared {
		skipped += n
	}
	_, _ = fmt.Fprintf(w, "# NIC surfaces: real node against Mokka node\n\n")
	_, _ = fmt.Fprintf(w, "- Real node: `%s`\n- Mokka node: `%s`\n", realDir, mockDir)
	_, _ = fmt.Fprintf(w, "- %d checks: %d pass, %d fail. %d not compared.\n", s.Checks, s.Passed, failed, skipped)

	writeSurfaces(w, checks)
	writeGroups(w, "Gaps", "Every failure, by its cause.", "Cause", groupsOf(checks, fail))
	writeGroups(w, "Not compared", "Checks not made, by reason.", "Reason", groupsOf(checks, notCompared))

	passing := map[string]int{}
	for _, c := range checks {
		if c.Status == pass {
			passing[c.Rule]++
		}
	}
	rules := make([]string, 0, len(passing))
	for r := range passing {
		rules = append(rules, r)
	}
	sort.Strings(rules)
	_, _ = fmt.Fprintf(w, "\n## Passing\n\n| Rule | Checks |\n| --- | ---: |\n")
	for _, r := range rules {
		_, _ = fmt.Fprintf(w, "| `%s` | %d |\n", r, passing[r])
	}
}

func writeSurfaces(w io.Writer, checks []check) {
	counts := map[string]map[string]int{}
	for _, c := range checks {
		sf := surfaceOf(c.Rule)
		if counts[sf] == nil {
			counts[sf] = map[string]int{}
		}
		counts[sf][c.Status]++
	}
	_, _ = fmt.Fprintf(w, "\n## By surface\n\n| Surface | Pass | Fail | Not compared |\n| --- | ---: | ---: | ---: |\n")
	order := append([]string{}, surfaceOrder...)
	for sf := range counts {
		if !slices.Contains(surfaceOrder, sf) {
			order = append(order, sf)
		}
	}
	for _, sf := range order {
		if n, ok := counts[sf]; ok {
			_, _ = fmt.Fprintf(w, "| %s | %d | %d | %d |\n", sf, n[pass], n[fail], n[notCompared])
		}
	}
}

func writeGroups(w io.Writer, title, intro, column string, groups []group) {
	_, _ = fmt.Fprintf(w, "\n## %s\n\n", title)
	if len(groups) == 0 {
		_, _ = fmt.Fprintf(w, "None.\n")
		return
	}
	_, _ = fmt.Fprintf(w, "%s\n\n| Checks | %s | Rules |\n| ---: | --- | --- |\n", intro, column)
	for _, g := range groups {
		why := g.cause
		if g.note != "" {
			why += ": " + g.note
		}
		_, _ = fmt.Fprintf(w, "| %d | %s | %s |\n", g.n, cell(why), ruleList(g.rules))
	}
}
