// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// The unit query, `nvidia-smi -q -u -x`. It is a different document from the
// device query the rest of this package decodes — DTD nvsmi_unit_v13.dtd, not
// nvsmi_device_v13.dtd — so it gets its own schema rather than growing
// schema.go with elements that never appear alongside a <gpu> block.
//
// This is the only nvidia-smi surface that renders nvmlSystemGetHicVersion.
// Reaching it also depends on nvmlUnitGetCount: nvidia-smi reads the unit
// count first and bails with "Unable to determine number of available units"
// if that call fails, printing neither the HIC block nor the units.

// unitDocument is the <nvidia_smi_log> of the unit query.
type unitDocument struct {
	XMLName       xml.Name   `xml:"nvidia_smi_log"`
	DriverVersion reading    `xml:"driver_version"`
	HICs          []hicEntry `xml:"hic_info>hic"`
	AttachedUnits reading    `xml:"attached_units"`
}

// hicEntry is one <hic id="N"> block. The id is an attribute, not a body, so
// a card's identity survives even when its firmware element is missing.
type hicEntry struct {
	ID       string  `xml:"id,attr"`
	Firmware reading `xml:"firmware"`
}

// HIC is one host interface card as nvidia-smi renders it. Firmware is kept as
// text: what an operator reads is exactly this string, and the mock's job is to
// put the configured value in front of them unchanged.
type HIC struct {
	ID       string
	Firmware string
}

// ParseUnits decodes a `nvidia-smi -q -u -x` document into the HIC entries it
// reports.
// It accepts combined stdout+stderr: nvidia-smi writes its unit-count failure
// to stderr and still exits 0, so callers have to keep stderr to detect that,
// and any such preamble is skipped before decoding.
func ParseUnits(out string) ([]HIC, error) {
	if i := strings.Index(out, "<?xml"); i > 0 {
		out = out[i:]
	}

	var doc unitDocument
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("parse nvidia-smi unit XML: %w", err)
	}

	cards := make([]HIC, 0, len(doc.HICs))
	for _, hic := range doc.HICs {
		cards = append(cards, HIC{
			ID:       strings.TrimSpace(hic.ID),
			Firmware: strings.TrimSpace(string(hic.Firmware)),
		})
	}
	return cards, nil
}

// HICProblems checks the cards nvidia-smi reports against want, in order.
// An empty want asserts the node reports no HIC at all, which is what every
// modern DGX/HGX/NVL system does — HICs belong to the retired S-class
// enclosures — and is therefore the case worth pinning for the default
// profiles.
func HICProblems(out string, want []HIC) []string {
	got, err := ParseUnits(out)
	if err != nil {
		return []string{err.Error()}
	}

	if len(got) != len(want) {
		return []string{fmt.Sprintf("nvidia-smi -q -u reports %d HIC(s) %v, want %d %v",
			len(got), got, len(want), want)}
	}

	var problems []string
	for i, w := range want {
		if got[i].ID != w.ID {
			problems = append(problems, fmt.Sprintf("hic[%d] id = %q, want %q", i, got[i].ID, w.ID))
		}
		if got[i].Firmware != w.Firmware {
			problems = append(problems, fmt.Sprintf("hic %q firmware = %q, want %q",
				w.ID, got[i].Firmware, w.Firmware))
		}
	}
	return problems
}

// UnitQueryProblems reports whether the unit query answered at all. nvidia-smi
// prints its error to stderr and still exits 0, so a spec that only checks the
// exit status would pass against a driver whose nvmlUnitGetCount is a stub.
func UnitQueryProblems(out string) []string {
	if strings.Contains(out, "Unable to determine number of available units") {
		return []string{"nvidia-smi -q -u could not determine the unit count: " +
			"nvmlUnitGetCount failed, so neither the HIC block nor the units were printed"}
	}
	if _, err := ParseUnits(out); err != nil {
		return []string{err.Error()}
	}
	return nil
}
