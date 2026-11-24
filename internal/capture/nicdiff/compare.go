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
	"regexp"
	"sort"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
)

const (
	pass = "PASS"
	fail = "FAIL"
	// notCompared is a check that was not made: the value is not comparable by
	// design, or one side could not capture it.
	notCompared = "SKIP"
)

// The causes a difference or a skipped check is counted under.
const (
	causeSchemaGap    = "schema gap"
	causeRuleBug      = "rule bug"
	causeBlindSpot    = "capture blind spot"
	causeByDesign     = "by design"
	causeUnclassified = "unclassified"
)

// check is one expectation: that the simulated node shows the real node's
// surface the same way.
type check struct {
	Status string `json:"status"`
	Rule   string `json:"rule"`
	Object string `json:"object"`
	Real   string `json:"real,omitempty"`
	Sim    string `json:"sim,omitempty"`
	Detail string `json:"detail,omitempty"`
	Cause  string `json:"cause,omitempty"`
	Note   string `json:"note,omitempty"`
}

// compare makes one check per attribute either side has, in a stable order.
// An object only one side has fails once per attribute it holds, as it would
// once both sides have it and the attributes differ, so the totals of two runs
// stay comparable when Mokka starts rendering an object.
func compare(realNode, mockNode *nicsurfaces.Node) []check {
	mockNode.BindUnbound(realNode)
	realNode.BindUnbound(mockNode)
	hw, sim := node{realNode}, node{mockNode}
	hw.addRelations()
	sim.addRelations()
	var out []check
	for _, o := range union(hw.Records, sim.Records) {
		r, s := hw.Records[o], sim.Records[o]
		for _, a := range unionKeys(r, s) {
			out = append(out, compareAttr(o, a, r, s, hw, sim))
		}
	}
	return out
}

func compareAttr(o, a string, r, s map[string]string, hw, sim node) check {
	name := ruleName(o, a)
	c := check{Rule: name, Object: o}
	rv, inReal := r[a]
	sv, inSim := s[a]
	c.Real, c.Sim = rv, sv
	if why := blindSpot(o, a, hw, sim); why != "" {
		c.Status, c.Cause, c.Note = notCompared, causeBlindSpot, why
		return c
	}
	switch {
	case !inSim:
		c.Status, c.Detail = fail, "only on the real node"
		return c
	case !inReal:
		c.Status, c.Detail = fail, "only on the simulated node"
		return c
	}
	rl := ruleFor(name)
	if rl.mode == skip {
		c.Status, c.Cause, c.Note = notCompared, causeByDesign, rl.why
		return c
	}
	if ok, detail := equal(rl.mode, rv, sv); !ok {
		c.Status, c.Detail = fail, detail
		return c
	}
	c.Status = pass
	return c
}

// blindSpot says why a record cannot be compared because one side could not
// capture it, or "" when both could: the capture file it comes from is missing
// on a side, or a side's capturing pod could not see /dev/infiniband.
func blindSpot(o, a string, hw, sim node) string {
	src := hw.Source(o, a)
	if src == "" {
		src = sim.Source(o, a)
	}
	if why := notCaptured(src, "", hw, sim); why != "" {
		return why
	}
	// Without rdma-sysfs.txt a side cannot key what is keyed by RDMA device:
	// its records stay under device names and pair with nothing.
	if keyedByRDMADevice(o) {
		if why := notCaptured(srcRDMA, ", which keys the RDMA devices,", hw, sim); why != "" {
			return why
		}
	}
	if needsDeviceNodes(o, a) || nicsurfaces.Kind(o) == "devnode" {
		if !seesDeviceNodes(hw) || !seesDeviceNodes(sim) {
			return "a capturing pod cannot see /dev/infiniband"
		}
	}
	return ""
}

// notCaptured says which side lacks the capture file src, and why, or "".
func notCaptured(src, what string, hw, sim node) string {
	for _, side := range []struct {
		name string
		n    node
	}{{"real", hw}, {"simulated", sim}} {
		if why, ok := side.n.Missing[src]; ok {
			return src + what + " was not captured on the " + side.name + " node: " + why
		}
	}
	return ""
}

// keyedByRDMADevice says whether an object's key comes from an RDMA device's
// key in rdma-sysfs.txt, as the tools' records and the device files' do.
func keyedByRDMADevice(o string) bool {
	switch nicsurfaces.Kind(o) {
	case "rdma", "port", "uverbs", "mad", "ibv", "ibstat", "rdmalink", "topo":
		return true
	}
	return strings.HasPrefix(o, "rel/devnode/")
}

// seesDeviceNodes says whether the capturing pod saw /dev/infiniband.
func seesDeviceNodes(n node) bool {
	v, _ := n.get("node", "dev_infiniband")
	return v != ""
}

func equal(m mode, a, b string) (bool, string) {
	switch m {
	case asSet:
		return setDiff(split(a), split(b))
	case nameKinds:
		return setDiff(kinds(split(a)), kinds(split(b)))
	case auxNames:
		return setDiff(dropAuxNumbers(split(a)), dropAuxNumbers(split(b)))
	case count:
		na, nb := len(split(a)), len(split(b))
		return na == nb, fmt.Sprintf("real has %d, simulated %d", na, nb)
	case devNumber:
		return dropMinor(a) == dropMinor(b), ""
	case vuMarker:
		return vuKind(a) == vuKind(b), "marker real=" + vuKind(a) + " simulated=" + vuKind(b)
	default:
		return a == b, ""
	}
}

func split(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// setDiff compares two lists as sets and names what each side lacks.
func setDiff(a, b []string) (bool, string) {
	missing, extra := lacking(b, a), lacking(a, b)
	if len(missing) == 0 && len(extra) == 0 {
		return true, ""
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "not simulated: "+strings.Join(missing, ","))
	}
	if len(extra) > 0 {
		parts = append(parts, "only simulated: "+strings.Join(extra, ","))
	}
	return false, strings.Join(parts, "; ")
}

// lacking returns the members of want that have does not hold, sorted and
// without repeats.
func lacking(have, want []string) []string {
	in := map[string]bool{}
	for _, x := range have {
		in[x] = true
	}
	var out []string
	for _, x := range want {
		if !in[x] {
			in[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

var trailingDigits = regexp.MustCompile(`[0-9]+$`)

// auxNumber is the probe-order number of an auxiliary device such as
// mlx5_core.rdma.0.
var auxNumber = regexp.MustCompile(`^([a-z0-9_]+\.[a-z_]+)\.[0-9]+$`)

func dropAuxNumbers(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, auxNumber.ReplaceAllString(n, "$1"))
	}
	return out
}

func kinds(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, trailingDigits.ReplaceAllString(n, ""))
	}
	return out
}

// dropMinor turns "231:192 666" into "231 666".
func dropMinor(v string) string {
	number, rest, _ := strings.Cut(v, " ")
	major, _, _ := strings.Cut(number, ":")
	return strings.TrimSpace(major + " " + rest)
}

func union(a, b map[string]map[string]string) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	return sortedKeys(set)
}

func unionKeys(a, b map[string]string) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
