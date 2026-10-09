// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// SupportedClocks decodes <supported_clocks> into the profile's shape, so the
// two compare directly. A board without a table renders the section as N/A,
// which decodes as nil; anything else that yields no memory clock, or a
// reading that is not a MHz value, is an error rather than an empty table.
func (g GPU) SupportedClocks() ([]profile.SupportedMemoryClock, error) {
	sc := g.element.SupportedClocks
	body := strings.TrimSpace(sc.Body)
	if len(sc.Memory) == 0 {
		if reading(body).unsupported() {
			return nil, nil
		}
		return nil, fmt.Errorf("supported_clocks = %q, want <supported_mem_clock> entries or N/A", body)
	}
	out := make([]profile.SupportedMemoryClock, 0, len(sc.Memory))
	for _, mem := range sc.Memory {
		memMHz, ok := mem.Value.intValue()
		if !ok {
			return nil, fmt.Errorf("supported_mem_clock/value = %q, want a MHz reading", string(mem.Value))
		}
		gfx := make([]int, 0, len(mem.Graphics))
		for _, r := range mem.Graphics {
			mhz, ok := r.intValue()
			if !ok {
				return nil, fmt.Errorf("supported_graphics_clock under %d MHz = %q, want a MHz reading", memMHz, string(r))
			}
			gfx = append(gfx, mhz)
		}
		out = append(out, profile.SupportedMemoryClock{MemoryMHz: memMHz, GraphicsMHz: gfx})
	}
	return out, nil
}

// SupportedClocksProblems checks every GPU's <supported_clocks> against the
// profile's table, entry for entry and in order.
//
// N/A on a profile that declares a table is the defect this exists to catch:
// both NVML getters behind the section were generated stubs, so the mock said
// the driver could not list a single clock while every profile carried the
// list a real board reports. Order is compared too because nvidia-smi renders
// what NVML returns without sorting, and every capture lists highest first.
// A mismatch names the first differing entry rather than dumping the table,
// which runs to a few hundred clocks on Blackwell.
func SupportedClocksProblems(out string, want []profile.SupportedMemoryClock) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i := range snap.doc.GPUs {
		gpu := snap.gpu(i)
		got, err := gpu.SupportedClocks()
		if err != nil {
			problems = append(problems, gpu.Label()+" "+err.Error())
			continue
		}
		if p := supportedClocksDiff(got, want); p != "" {
			problems = append(problems, gpu.Label()+" "+p)
		}
	}
	return problems
}

func supportedClocksDiff(got, want []profile.SupportedMemoryClock) string {
	if len(got) == 0 && len(want) > 0 {
		return fmt.Sprintf("supported_clocks = N/A, want %d memory clocks; "+
			"nvmlDeviceGetSupportedMemoryClocks is missing or unimplemented", len(want))
	}
	if len(got) != len(want) {
		return fmt.Sprintf("supported_clocks lists %d memory clocks %v, want %d %v",
			len(got), memoryClocks(got), len(want), memoryClocks(want))
	}
	for i := range want {
		if got[i].MemoryMHz != want[i].MemoryMHz {
			return fmt.Sprintf("supported_mem_clock[%d] = %d MHz, want %d MHz", i, got[i].MemoryMHz, want[i].MemoryMHz)
		}
		g, w := got[i].GraphicsMHz, want[i].GraphicsMHz
		if len(g) != len(w) {
			return fmt.Sprintf("memory clock %d MHz lists %d graphics clocks, want %d", want[i].MemoryMHz, len(g), len(w))
		}
		for j := range w {
			if g[j] != w[j] {
				return fmt.Sprintf("memory clock %d MHz graphics clock [%d] = %d MHz, want %d MHz",
					want[i].MemoryMHz, j, g[j], w[j])
			}
		}
	}
	return ""
}

func memoryClocks(table []profile.SupportedMemoryClock) []int {
	out := make([]int, 0, len(table))
	for _, mc := range table {
		out = append(out, mc.MemoryMHz)
	}
	return out
}
