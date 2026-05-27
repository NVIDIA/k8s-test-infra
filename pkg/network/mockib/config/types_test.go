// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func ptrTrue() *bool  { b := true; return &b }
func ptrFalse() *bool { b := false; return &b }

func TestCounters_DefaultsAbsent(t *testing.T) {
	c := Counters{}.Defaults()
	if !c.EnabledOrDefault() {
		t.Fatalf("counters must default to enabled")
	}
	if c.TickSeconds != 5 {
		t.Fatalf("tick_seconds default = %d, want 5", c.TickSeconds)
	}
}

func TestCounters_ExplicitFalsePreserved(t *testing.T) {
	c := Counters{Enabled: ptrFalse(), TickSeconds: 10}.Defaults()
	if c.EnabledOrDefault() {
		t.Fatalf("explicit false must be preserved through Defaults()")
	}
	if c.TickSeconds != 10 {
		t.Fatalf("explicit tick_seconds = %d, want 10", c.TickSeconds)
	}
}

func TestCounters_ExplicitTruePreserved(t *testing.T) {
	c := Counters{Enabled: ptrTrue(), TickSeconds: 7}.Defaults()
	if !c.EnabledOrDefault() {
		t.Fatalf("explicit true must remain true through Defaults()")
	}
	if c.Enabled == nil || *c.Enabled != true {
		t.Fatalf("explicit true must remain a non-nil &true through Defaults(), got %v", c.Enabled)
	}
	if c.TickSeconds != 7 {
		t.Fatalf("explicit tick_seconds = %d, want 7", c.TickSeconds)
	}
}

func TestInfiniband_DefaultsAppliesCounters(t *testing.T) {
	ib := Infiniband{Enabled: true}.Defaults()
	if !ib.Counters.EnabledOrDefault() {
		t.Fatalf("Infiniband.Defaults() must apply Counters.Defaults()")
	}
	if ib.Counters.TickSeconds != 5 {
		t.Fatalf("Counters.TickSeconds = %d, want 5", ib.Counters.TickSeconds)
	}
}
