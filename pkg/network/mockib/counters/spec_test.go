// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package counters

import "testing"

func TestCatalog_NoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalog {
		if seen[e.Name] {
			t.Errorf("duplicate counter name in Catalog: %q", e.Name)
		}
		seen[e.Name] = true
	}
}

func TestCatalog_ValidWidthsAndSurfaces(t *testing.T) {
	for _, e := range Catalog {
		if e.Width != 32 && e.Width != 64 {
			t.Errorf("%s: invalid width %d", e.Name, e.Width)
		}
		if e.Surface != SurfaceCounters && e.Surface != SurfaceHWCounters {
			t.Errorf("%s: invalid surface %d", e.Name, e.Surface)
		}
	}
}

// Every counter currently rendered as zero by render.go must appear in
// the catalog so the writer can update it. Update this list if render.go
// gains or drops counters.
func TestCatalog_CoversLegacyRenderSet(t *testing.T) {
	legacy := []string{
		"port_xmit_data", "port_rcv_data", "port_xmit_packets", "port_rcv_packets",
		"port_xmit_discards", "port_rcv_errors", "symbol_error", "link_error_recovery",
		"link_downed", "port_rcv_remote_physical_errors", "port_rcv_switch_relay_errors",
		"local_link_integrity_errors", "excessive_buffer_overrun_errors",
		"VL15_dropped", "port_xmit_constraint_errors", "port_rcv_constraint_errors",
	}
	have := map[string]bool{}
	for _, e := range Catalog {
		have[e.Name] = true
	}
	for _, n := range legacy {
		if !have[n] {
			t.Errorf("legacy render counter %q missing from Catalog", n)
		}
	}
}
