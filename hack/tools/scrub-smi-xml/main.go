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

// scrub-smi-xml replaces the identifiers in an `nvidia-smi -q -x` capture with
// the placeholders that tests/e2e/go/assertions/nvidiasmi/testdata/hardware/README.md
// documents, so a capture can be committed.
//
//	scrub-smi-xml raw.xml > qx-<node>.xml
//	scrub-smi-xml -check qx-<node>.xml
//
// Six elements are rewritten: serial, chassis_serial_number, uuid,
// clusterUuid, pdi and gpu_fabric_guid. One placeholder is used per distinct
// value, so GPUs that shared an identifier upstream still share one. Elements
// that report nothing (empty, N/A, all zeros) are left alone, so a scrubbed
// file does not claim hardware the node lacks.
//
// -check reads a file and exits 1 if any of the six elements still carries a
// value that is not a placeholder. It applies the same patterns as
// TestHardwareCapturesAreScrubbed.
//
// The tool rewrites the six elements only. Reading the output for anything
// else that traces back to the machine remains the reviewer's job.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// rule describes one element: how its placeholder is built, whether its
// numbering is shared with another element, and which values mean "absent".
type rule struct {
	element string
	// group names the numbering the element draws from. pdi and gpu_fabric_guid
	// share one: on Grace-Blackwell they are the same value, and scrubbing them
	// apart would invent a difference the node did not report.
	group string
	// first is the number given to the first distinct value. clusterUuid and
	// the serials count from 1 because zero is how nvidia-smi says "not in a
	// fabric cluster"; GPU UUIDs count from 0, as the existing captures do.
	first       int
	placeholder func(n int) string
	pattern     *regexp.Regexp
}

var rules = []rule{
	{
		element: "serial", group: "serial", first: 1,
		placeholder: func(n int) string { return fmt.Sprintf("1650000000%03d", n) },
		pattern:     regexp.MustCompile(`^1650000000\d{3}$`),
	},
	{
		element: "chassis_serial_number", group: "chassis", first: 1,
		placeholder: func(n int) string { return fmt.Sprintf("1820000000%03d", n) },
		pattern:     regexp.MustCompile(`^1820000000\d{3}$`),
	},
	{
		element: "uuid", group: "uuid", first: 0,
		placeholder: func(n int) string { return fmt.Sprintf("GPU-%08x-0000-0000-0000-%012x", n, n) },
		pattern:     regexp.MustCompile(`^GPU-000000[0-9a-f]{2}-0000-0000-0000-0000000000[0-9a-f]{2}$`),
	},
	{
		element: "clusterUuid", group: "cluster", first: 1,
		placeholder: func(n int) string { return fmt.Sprintf("%08x-0000-0000-0000-%012x", n, n) },
		pattern:     regexp.MustCompile(`^0{7}[0-9a-f]-0000-0000-0000-0{11}[0-9a-f]$`),
	},
	{
		element: "pdi", group: "die", first: 1,
		placeholder: func(n int) string { return fmt.Sprintf("0x%016x", n) },
		pattern:     regexp.MustCompile(`^0x0{14}[0-9a-f]{2}$`),
	},
	{
		element: "gpu_fabric_guid", group: "die", first: 1,
		placeholder: func(n int) string { return fmt.Sprintf("0x%016x", n) },
		pattern:     regexp.MustCompile(`^0x0{14}[0-9a-f]{2}$`),
	},
}

var allZero = regexp.MustCompile(`^(0x)?[0-]*$`)

// absent reports whether a reported value says "the board has no such thing".
func absent(v string) bool {
	return v == "" || strings.EqualFold(v, "N/A") || allZero.MatchString(v)
}

// numbering hands out one placeholder number per distinct value in a group.
type numbering struct {
	next map[string]int
	seen map[string]map[string]int
}

func newNumbering() *numbering {
	return &numbering{next: map[string]int{}, seen: map[string]map[string]int{}}
}

func (n *numbering) number(group, value string, first int) int {
	if n.seen[group] == nil {
		n.seen[group] = map[string]int{}
		n.next[group] = first
	}
	if got, ok := n.seen[group][value]; ok {
		return got
	}
	got := n.next[group]
	n.next[group]++
	n.seen[group][value] = got
	return got
}

// scrub rewrites src and reports how many distinct values each element had.
func scrub(src string) (string, map[string]int) {
	nums := newNumbering()
	counts := map[string]int{}
	out := src

	for _, r := range rules {
		re := regexp.MustCompile(`(<` + r.element + `>)([^<]*)(</` + r.element + `>)`)
		distinct := map[string]bool{}
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			parts := re.FindStringSubmatch(m)
			value := strings.TrimSpace(parts[2])
			if absent(value) || r.pattern.MatchString(value) {
				return m
			}
			distinct[value] = true
			return parts[1] + r.placeholder(nums.number(r.group, value, r.first)) + parts[3]
		})
		counts[r.element] = len(distinct)
	}
	return out, counts
}

// check returns one message per element whose value is neither absent nor a
// placeholder.
func check(src string) []string {
	var problems []string
	for _, r := range rules {
		re := regexp.MustCompile(`<` + r.element + `>([^<]*)</` + r.element + `>`)
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			value := strings.TrimSpace(m[1])
			if absent(value) || r.pattern.MatchString(value) {
				continue
			}
			problems = append(problems, fmt.Sprintf("<%s> is not a placeholder", r.element))
		}
	}
	return problems
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrub-smi-xml", flag.ContinueOnError)
	fs.SetOutput(stderr)
	checkOnly := fs.Bool("check", false, "report unscrubbed identifiers instead of rewriting; exit 1 if any")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: scrub-smi-xml [-check] capture.xml")
		return 2
	}

	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	if *checkOnly {
		problems := check(string(raw))
		for _, p := range problems {
			_, _ = fmt.Fprintln(stderr, p)
		}
		if len(problems) > 0 {
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "ok: every identifier is a placeholder or absent")
		return 0
	}

	out, counts := scrub(string(raw))
	if _, err := io.WriteString(stdout, out); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	for _, r := range rules {
		_, _ = fmt.Fprintf(stderr, "%-22s %d distinct value(s) replaced\n", r.element, counts[r.element])
	}
	return 0
}
