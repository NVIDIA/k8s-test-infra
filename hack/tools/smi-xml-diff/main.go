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

// smi-xml-diff compares two `nvidia-smi -q -x` documents element by element.
//
//	smi-xml-diff [-ignore regexp] [-max n] real.xml mock.xml
//
// Each document is flattened to path=value pairs, where a repeated element
// carries its position (gpu[1]/clocks[0]/graphics_clock[0]). The report says how many
// paths exist on both sides, how many of those carry the same value, and lists
// the paths only one side has and the values that differ. Paths that move with
// load or name a machine (temperatures, utilisation, power draw, identifiers)
// are matched on presence only; -ignore changes that set.
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// loadOrIdentity names the elements whose value is not a property of the board:
// readings that move with load or time, and identifiers. They are matched on
// presence only. An element name is matched whole, as one path segment, so
// "used" does not hide "used_gpu_memory_ratio".
var loadOrIdentity = []string{
	"temperature", "utilization", "power_draw", "power_state", "performance_state",
	"fan_speed", "used", "free", "clocks_event_reasons", "clocks_throttle_reasons",
	"uuid", "serial", "pdi", "gpu_fabric_guid", "clusterUuid", "timestamp", "pid",
	"processes", "process_info", "minor_number", "pci_bus", "pci_device", "pci_domain",
	"bus_id", "tx_util", "rx_util", "value",
}

// defaultIgnore matches a path in which any segment is one of those elements.
var defaultIgnore = `(?:^|/)(?:` + strings.Join(loadOrIdentity, "|") + `)\[[0-9]+\](?:/|$)`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("smi-xml-diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ignore := fs.String("ignore", defaultIgnore, "regexp of paths whose value is not compared (presence still is)")
	limit := fs.Int("max", 40, "maximum entries to list per kind")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: smi-xml-diff [-ignore regexp] [-max n] real.xml mock.xml")
		return 2
	}
	re, err := regexp.Compile(*ignore)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "-ignore: %v\n", err)
		return 2
	}
	hw, err := flattenFile(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	mock, err := flattenFile(fs.Arg(1))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	report(stdout, compare(hw, mock, re), *limit)
	return 0
}

func flattenFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	m, err := flatten(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// node is one element of the parsed document.
type node struct {
	name     string
	text     string
	children []*node
}

func parse(r io.Reader) (*node, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	root := &node{}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		cur := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local}
			cur.children = append(cur.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			cur.text += string(t)
		}
	}
	return root, nil
}

// flatten turns a document into path=value pairs for its leaf elements. Each
// element is indexed among its same-named siblings in document order, so the
// first gpu is gpu[0].
func flatten(r io.Reader) (map[string]string, error) {
	root, err := parse(r)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	walk(root, "", out)
	return out, nil
}

func walk(n *node, prefix string, out map[string]string) {
	seen := map[string]int{}
	for _, c := range n.children {
		// Every element is indexed, so a sibling that occurs once on one side and
		// twice on the other still pairs up its first occurrence.
		name := fmt.Sprintf("%s[%d]", c.name, seen[c.name])
		seen[c.name]++
		path := name
		if prefix != "" {
			path = prefix + "/" + name
		}
		if len(c.children) == 0 {
			out[path] = strings.TrimSpace(c.text)
			continue
		}
		walk(c, path, out)
	}
}

type result struct {
	both, same, ignored int
	onlyReal, onlyMock  []string
	differs             []string
}

func compare(hw, mock map[string]string, ignore *regexp.Regexp) result {
	var r result
	for path, rv := range hw {
		mv, ok := mock[path]
		if !ok {
			r.onlyReal = append(r.onlyReal, path)
			continue
		}
		r.both++
		switch {
		case ignore.MatchString(path):
			r.ignored++
		case rv == mv:
			r.same++
		default:
			r.differs = append(r.differs, fmt.Sprintf("%-60s real=%q mock=%q", path, rv, mv))
		}
	}
	for path := range mock {
		if _, ok := hw[path]; !ok {
			r.onlyMock = append(r.onlyMock, path)
		}
	}
	sort.Strings(r.onlyReal)
	sort.Strings(r.onlyMock)
	sort.Strings(r.differs)
	return r
}

func report(w io.Writer, r result, limit int) {
	total := len(r.onlyReal) + r.both
	_, _ = fmt.Fprintf(w, "paths in the real document: %d\n", total)
	_, _ = fmt.Fprintf(w, "present in the mock:        %s\n", ratio(r.both, total))
	compared := r.both - r.ignored
	_, _ = fmt.Fprintf(w, "same value:                 %s (excluding %d paths whose value is load or identity)\n",
		ratio(r.same, compared), r.ignored)
	_, _ = fmt.Fprintf(w, "only in the mock:           %d\n", len(r.onlyMock))
	list(w, "only in the real document", r.onlyReal, limit)
	list(w, "only in the mock document", r.onlyMock, limit)
	list(w, "value differs", r.differs, limit)
}

func ratio(n, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d of %d (%.1f%%)", n, d, 100*float64(n)/float64(d))
}

func list(w io.Writer, title string, items []string, limit int) {
	if len(items) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\n%s (%d):\n", title, len(items))
	for i, it := range items {
		if i == limit {
			_, _ = fmt.Fprintf(w, "  ... %d more\n", len(items)-limit)
			return
		}
		_, _ = fmt.Fprintf(w, "  %s\n", it)
	}
}
