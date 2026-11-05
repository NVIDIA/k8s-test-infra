// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// This file carries no build tag, for the same reason as ibfabric.go: checking
// what NICSysfs read needs only the standard library, so its tests run in the
// regular `go test ./...` job. The cluster-facing half is in nic.go.

// nicRow is what NICSysfs read for one HCA. An empty field is a file that was
// not there.
type nicRow struct {
	name, vendor, numa, slot, xmitWait, rnrNakRetry string
}

// hcaFiles are the per-HCA attributes NICSysfs reads, relative to
// infiniband/<hca>/, each with the nicRow field a line of it fills.
var hcaFiles = []struct {
	path string
	set  func(n *nicRow, line string)
}{
	{"device/vendor", func(n *nicRow, v string) { n.vendor = v }},
	{"device/numa_node", func(n *nicRow, v string) { n.numa = v }},
	{"device/uevent", func(n *nicRow, v string) {
		if slot, ok := strings.CutPrefix(v, "PCI_SLOT_NAME="); ok {
			n.slot = slot
		}
	}},
	{"ports/1/counters/port_xmit_wait", func(n *nicRow, v string) { n.xmitWait = v }},
	{"ports/1/hw_counters/rnr_nak_retry_err", func(n *nicRow, v string) { n.rnrNakRetry = v }},
}

// parseHCAFiles groups `grep -H` output over infiniband/<hca>/<file> by HCA,
// sorted by name. Paths are relative to the class directory and so carry no
// ':', which makes the first one on each line the end of the path.
func parseHCAFiles(out string) []nicRow {
	byName := map[string]*nicRow{}
	for line := range strings.SplitSeq(out, "\n") {
		path, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		parts := strings.SplitN(path, "/", 3)
		if len(parts) != 3 || parts[0] != "infiniband" {
			continue
		}
		n, seen := byName[parts[1]]
		if !seen {
			n = &nicRow{name: parts[1]}
			byName[parts[1]] = n
		}
		for _, f := range hcaFiles {
			if f.path == parts[2] {
				f.set(n, value)
			}
		}
	}

	nics := make([]nicRow, 0, len(byName))
	for _, n := range byName {
		nics = append(nics, *n)
	}
	slices.SortFunc(nics, func(a, b nicRow) int { return strings.Compare(a.name, b.name) })
	return nics
}

// nicSysfsProblems lists every way the HCAs fall short of what a sysfs-based
// NIC health monitor needs to discover, place and watch them, given the NUMA
// nodes the profile's GPUs sit on and the PCI addresses it already declares.
func nicSysfsProblems(nics []nicRow, expectedHCAs int, gpuNUMA []int, declared []string) []string {
	var problems []string
	if len(nics) != expectedHCAs {
		problems = append(problems, fmt.Sprintf("%d HCAs rendered, expected %d", len(nics), expectedHCAs))
	}

	slotOwner := map[string]string{}
	withNIC := map[int]bool{}
	for _, n := range nics {
		problems = append(problems, hcaProblems(n, gpuNUMA, declared)...)

		if owner, dup := slotOwner[n.slot]; dup && n.slot != "" {
			problems = append(problems, fmt.Sprintf("%s and %s share PCI_SLOT_NAME %s", owner, n.name, n.slot))
		}
		slotOwner[n.slot] = n.name

		if numa, err := strconv.Atoi(n.numa); err == nil {
			withNIC[numa] = true
		}
	}

	for _, numa := range gpuNUMA {
		if !withNIC[numa] {
			problems = append(problems, fmt.Sprintf("NUMA node %d has GPUs but no NIC", numa))
		}
	}
	return problems
}

// hcaProblems checks one HCA's attributes on their own.
func hcaProblems(n nicRow, gpuNUMA []int, declared []string) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, n.name+": "+fmt.Sprintf(format, args...))
	}

	if n.vendor != "0x15b3" {
		add("device/vendor is %q, want 0x15b3", n.vendor)
	}

	switch {
	case n.slot == "":
		add("device/uevent has no PCI_SLOT_NAME")
	case slices.Contains(declared, n.slot):
		add("PCI_SLOT_NAME %s is already a GPU's or NVSwitch's", n.slot)
	}

	if numa, err := strconv.Atoi(n.numa); err != nil || !slices.Contains(gpuNUMA, numa) {
		add("device/numa_node is %q, want one of the GPUs' NUMA nodes %v", n.numa, gpuNUMA)
	}

	for _, c := range []struct{ file, value string }{
		{"counters/port_xmit_wait", n.xmitWait},
		{"hw_counters/rnr_nak_retry_err", n.rnrNakRetry},
	} {
		if _, err := strconv.ParseUint(c.value, 10, 64); err != nil {
			add("%s is %q, want a counter", c.file, c.value)
		}
	}
	return problems
}
