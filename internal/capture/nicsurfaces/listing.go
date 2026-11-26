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
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// New returns a Node without records, for surfaces that come from somewhere
// other than a capture directory, such as a declared node's resolved state.
func New() *Node { return newNode() }

// readOrder is the order read parses the sources in. A listing doesn't say
// which source first gave an object a record, so ReadListing takes the
// earliest of its records' sources in this order.
var readOrder = []string{
	pciNICSysfs, pciVPD, lspciNIC, pciAll, netSysfs, rdmaSysfs, rdmaChar, sysModules, rdmaSystem, rdmaLink,
	netECNQoS, netEthtoolI, netEthtool, netEthtoolM, ibvDevinfo, ibstat,
	devlinkInfo, devlinkPort, devlinkESW, devlinkHealth, fwMLXConfig, fwFlint, cableMLXLink, smiTopo,
}

var notCapturedEntry = regexp.MustCompile(`([^\s,()]+) \(([^)]*)\)`)

// ReadListing reads a node's NIC surfaces as Write prints them: the comment
// naming the sources not captured, then one record per line as object,
// attribute, value and source files, joined by + for a derived record.
func ReadListing(r io.Reader) (*Node, error) {
	n := newNode()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if rest, ok := strings.CutPrefix(text, "# not captured: "); ok {
			for _, m := range notCapturedEntry.FindAllStringSubmatch(rest, -1) {
				n.Missing[m[1]] = m[2]
			}
			continue
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Split(text, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d: want object, attribute, value and source separated by tabs, got %d fields", line, len(f))
		}
		n.setFrom(strings.Split(f[3], "+"), f[0], f[1], f[2])
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for obj := range n.Records {
		n.first[obj] = n.earliestSource(obj)
	}
	return n, nil
}

// earliestSource is the source of the object's records that read parses
// first; a record derived from no capture file comes last.
func (n *Node) earliestSource(obj string) string {
	best, bestAt := "", len(readOrder)+1
	for a := range n.Records[obj] {
		src := n.Source(obj, a)
		at := slices.Index(readOrder, src)
		if at < 0 {
			at = len(readOrder)
		}
		if at < bestAt || (at == bestAt && src < best) {
			best, bestAt = src, at
		}
	}
	return best
}

// readListingFile reads a listing Write printed to path.
func readListingFile(path string) (*Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	n, err := ReadListing(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}
