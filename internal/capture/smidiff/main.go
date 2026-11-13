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

// Package smidiff compares two `nvidia-smi -q -x` documents element by element.
// It backs `mokka-capture diff smi`.
//
//	mokka-capture diff smi [-ignore regexp] [-max n] real.xml mock.xml
//
// Each document is flattened to path=value pairs, where a repeated element
// carries its position (gpu[1]/clocks[0]/graphics_clock[0]). The report says how many
// paths exist on both sides, how many of those carry the same value, and lists
// the paths only one side has and the values that differ. A value that is not a
// property of the board (a reading that moves with load or time, or an
// identifier) is not compared while both sides read one; -ignore changes that
// set. An answer that is not a reading, such as N/A, is still compared, so a
// mock that reads a value where the real GPU has none counts as a difference.
package smidiff

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
)

// loadOrIdentity names the elements whose value is not a property of the board,
// so a mock of a captured node cannot be expected to reproduce it. An element
// name is matched whole, as one path segment: "clocks" (the clocks in use) does
// not hide "max_clocks", and "used" does not hide "used_gpu_memory_ratio". A
// name that holds a subtree hides everything under it.
var loadOrIdentity = []string{
	// Readings that move with load. Temperature thresholds and T.Limit offsets
	// describe the board and are compared; only the readings are listed.
	"gpu_temp", "gpu_temp_tlimit", "memory_temp", "utilization", "power_draw",
	"average_power_draw", "instant_power_draw", "power_state", "performance_state",
	"clocks", "dla_cur_engine_clock", "graphics_volt", "fan_speed", "used", "free",
	"clocks_event_reasons", "clocks_throttle_reasons", "clocks_event_reasons_counters",
	"tx_util", "rx_util", "processes", "process_info", "pid",
	// Readings that move with time: when the document was taken, how long the
	// GPU has run, and its last black-box flush.
	"timestamp", "time_run", "inforom_bbx_flush",
	// Identifiers of the unit and of where the host placed it.
	"uuid", "serial", "pdi", "gpu_fabric_guid", "clusterUuid", "minor_number",
	"pci_bus", "pci_device", "pci_domain", "bus_id",
}

// defaultIgnore matches a path in which any segment is one of those elements.
var defaultIgnore = `(?:^|/)(?:` + strings.Join(loadOrIdentity, "|") + `)\[[0-9]+\](?:/|$)`

// notAReading lists what nvidia-smi prints in place of a value it could not
// read. Such an answer is a property of the board and driver (the GPU does not
// support the query, or the caller may not make it), so it is compared even on
// a path whose readings are not.
var notAReading = map[string]bool{
	"N/A":                      true,
	"Not Supported":            true,
	"Insufficient Permissions": true,
	"Requested functionality has been deprecated": true,
	"Unknown Error": true,
}

// Command is `mokka-capture diff smi`.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "smi",
		Usage: "compare two nvidia-smi -q -x documents",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "ignore",
				Value:       defaultIgnore,
				DefaultText: "readings and identifiers, as docs/tools/mokka-capture.md lists",
				Usage:       "regexp of paths whose reading is not compared (presence, and an answer such as N/A, still are)",
			},
			&cli.IntFlag{Name: "max", Value: 40, Usage: "maximum entries to list per kind"},
		},
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "real.xml", Required: true},
			&cli.StringArg{Name: "mock.xml", Required: true},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if err := cmdline.RejectExtraArgs(cmd); err != nil {
				return err
			}
			ignore, err := regexp.Compile(cmd.String("ignore"))
			if err != nil {
				return cmdline.Usage(cmd, fmt.Sprintf("-ignore: %v", err))
			}
			return cmdline.Exit(run(ignore, cmd.Int("max"), cmd.StringArg("real.xml"), cmd.StringArg("mock.xml"),
				cmd.Root().Writer, cmd.Root().ErrWriter))
		},
	}
}

func run(ignore *regexp.Regexp, limit int, realPath, mockPath string, stdout, stderr io.Writer) int {
	hw, err := flattenFile(realPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	mock, err := flattenFile(mockPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	report(stdout, compare(hw, mock, ignore), limit)
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
	both, same         int
	onlyReal, onlyMock []string
	differs            []string
	// notCompared lists the paths whose value is a reading or an identifier,
	// with both values, so a score says what it leaves out.
	notCompared []string
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
		case ignore.MatchString(path) && !notAReading[rv] && !notAReading[mv]:
			r.notCompared = append(r.notCompared, pair(path, rv, mv))
		case rv == mv:
			r.same++
		default:
			r.differs = append(r.differs, pair(path, rv, mv))
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
	sort.Strings(r.notCompared)
	return r
}

func pair(path, rv, mv string) string {
	return fmt.Sprintf("%-60s real=%q mock=%q", path, rv, mv)
}

func report(w io.Writer, r result, limit int) {
	total := len(r.onlyReal) + r.both
	_, _ = fmt.Fprintf(w, "paths in the real document: %d\n", total)
	_, _ = fmt.Fprintf(w, "present in the mock:        %s\n", ratio(r.both, total))
	compared := r.both - len(r.notCompared)
	_, _ = fmt.Fprintf(w, "same value:                 %s (excluding %d paths whose value is a reading or an identifier)\n",
		ratio(r.same, compared), len(r.notCompared))
	_, _ = fmt.Fprintf(w, "only in the mock:           %d\n", len(r.onlyMock))
	list(w, "only in the real document", r.onlyReal, limit)
	list(w, "only in the mock document", r.onlyMock, limit)
	list(w, "value differs", r.differs, limit)
	list(w, "not compared, a reading or an identifier", r.notCompared, limit)
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
