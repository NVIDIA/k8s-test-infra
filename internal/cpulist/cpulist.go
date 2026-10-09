// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cpulist reads and writes the kernel's cpulist format ("0-3,8,10-11"),
// used by sysfs files such as /sys/devices/system/cpu/online and a PCI
// device's local_cpulist.
package cpulist

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse returns the CPUs s lists, in the order written. Whitespace around the
// list, its entries and range bounds is ignored: the kernel ends the list with
// a newline, and a profile's cpu_affinity may be spaced.
func Parse(s string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		start, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil || start < 0 {
			return nil, fmt.Errorf("cpulist %q: bad CPU %q", s, lo)
		}
		end := start
		if isRange {
			if end, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil || end < start {
				return nil, fmt.Errorf("cpulist %q: bad range %q", s, part)
			}
		}
		for c := start; c <= end; c++ {
			out = append(out, c)
		}
	}
	return out, nil
}

// Format writes cpus, which must be ascending, collapsing consecutive runs
// into ranges as the kernel does.
func Format(cpus []int) string {
	var b strings.Builder
	for i := 0; i < len(cpus); {
		j := i
		for j+1 < len(cpus) && cpus[j+1] == cpus[j]+1 {
			j++
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(cpus[i]))
		if j > i {
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(cpus[j]))
		}
		i = j + 1
	}
	return b.String()
}
