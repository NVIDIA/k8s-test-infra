// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// EncoderFBCStats are the non-default encoder / FBC values an e2e profile
// override must surface through nvidia-smi -q (issue #636). Using non-zero
// figures keeps the assertion from accidentally passing against a zeroed
// default that never left the stub path.
type EncoderFBCStats struct {
	SessionCount       int
	AverageFPS         int
	AverageLatencyUS   int
}

// DiffEncoderFBCAccountingQuery checks nvidia-smi -q output for Encoder Stats,
// FBC Stats, and Accounting Mode Buffer Size. Missing blocks, N/A cells, or
// wrong numbers are reported as problems.
func DiffEncoderFBCAccountingQuery(out string, encoder, fbc EncoderFBCStats, accountingBufferSize int) []string {
	var problems []string
	problems = append(problems, diffStatBlock(out, "Encoder Stats", encoder)...)
	problems = append(problems, diffStatBlock(out, "FBC Stats", fbc)...)
	problems = append(problems, diffAccountingBufferSize(out, accountingBufferSize)...)
	return problems
}

var (
	// Body rows are "Label : value"; the next sibling section header (no colon)
	// must not be swallowed into the preceding block.
	statBlockRE = regexp.MustCompile(`(?ms)^([ \t]*)(Encoder Stats|FBC Stats)\s*\n((?:^[ \t]+[^:\n]+:[^\n]*\n)*)`)
	statRowRE   = regexp.MustCompile(`(?m)^\s*(Active Sessions|Average FPS|Average Latency)\s*:\s*(.+?)\s*$`)
	accountingBufferRE = regexp.MustCompile(`(?m)^\s*Accounting Mode Buffer Size\s*:\s*(.+?)\s*$`)
	leadingIntRE = regexp.MustCompile(`^-?\d+`)
)

func diffStatBlock(out, block string, want EncoderFBCStats) []string {
	body, ok := findStatBlock(out, block)
	if !ok {
		return []string{fmt.Sprintf("missing %q block", block)}
	}
	rows := parseStatRows(body)
	wantRows := map[string]int{
		"Active Sessions": want.SessionCount,
		"Average FPS":     want.AverageFPS,
		"Average Latency": want.AverageLatencyUS,
	}
	var problems []string
	for label, wantN := range wantRows {
		raw, present := rows[label]
		if !present {
			problems = append(problems, fmt.Sprintf("%s: missing %q row", block, label))
			continue
		}
		if strings.EqualFold(strings.TrimSpace(raw), "N/A") {
			problems = append(problems, fmt.Sprintf("%s: %s is N/A (stub still answering)", block, label))
			continue
		}
		got, err := parseLeadingInt(raw)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %s = %q, want %d", block, label, raw, wantN))
			continue
		}
		if got != wantN {
			problems = append(problems, fmt.Sprintf("%s: %s = %d, want %d", block, label, got, wantN))
		}
	}
	return problems
}

func findStatBlock(out, block string) (string, bool) {
	for _, m := range statBlockRE.FindAllStringSubmatch(out, -1) {
		if m[2] == block {
			return m[3], true
		}
	}
	return "", false
}

func parseStatRows(body string) map[string]string {
	rows := map[string]string{}
	for _, m := range statRowRE.FindAllStringSubmatch(body, -1) {
		rows[m[1]] = strings.TrimSpace(m[2])
	}
	return rows
}

func diffAccountingBufferSize(out string, want int) []string {
	m := accountingBufferRE.FindStringSubmatch(out)
	if m == nil {
		return []string{`missing "Accounting Mode Buffer Size" row`}
	}
	raw := strings.TrimSpace(m[1])
	if strings.EqualFold(raw, "N/A") {
		return []string{"Accounting Mode Buffer Size is N/A (stub still answering)"}
	}
	got, err := parseLeadingInt(raw)
	if err != nil {
		return []string{fmt.Sprintf("Accounting Mode Buffer Size = %q, want %d", raw, want)}
	}
	if got != want {
		return []string{fmt.Sprintf("Accounting Mode Buffer Size = %d, want %d", got, want)}
	}
	return nil
}

func parseLeadingInt(raw string) (int, error) {
	m := leadingIntRE.FindString(strings.TrimSpace(raw))
	if m == "" {
		return 0, fmt.Errorf("no integer in %q", raw)
	}
	return strconv.Atoi(m)
}
