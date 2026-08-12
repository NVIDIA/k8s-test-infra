// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file carries no build tag on purpose — same rationale as gfd_labels.go.
// DiffTemperatureQuery is pure string checking so it unit-tests without a
// cluster; the kubectl exec wrapper lives in nvidiasmi.go under //go:build e2e.

var tempQueryRowRE = regexp.MustCompile(`(?m)^\s*(GPU .+? Temp)\s*:\s*(-?\d+)\s*C\s*$`)

// EncoderFBCStats are the non-default values issue #636 expects nvidia-smi to
// surface. They intentionally stay next to the other pure nvidia-smi parsers
// so parser tests do not require the e2e build tag.
type EncoderFBCStats struct {
	SessionCount     int
	AverageFPS       int
	AverageLatencyUS int
}

type nvidiaSMILog struct {
	GPUs []nvidiaSMIGPU `xml:"gpu"`
}

type nvidiaSMIGPU struct {
	ID                       string              `xml:"id,attr"`
	AccountingModeBufferSize string              `xml:"accounting_mode_buffer_size"`
	EncoderStats             nvidiaSMIStatsBlock `xml:"encoder_stats"`
	FBCStats                 nvidiaSMIStatsBlock `xml:"fbc_stats"`
}

type nvidiaSMIStatsBlock struct {
	SessionCount   string `xml:"session_count"`
	AverageFPS     string `xml:"average_fps"`
	AverageLatency string `xml:"average_latency"`
}

// ValidateNvidiaSMIEncoderFBCXML decodes nvidia-smi -q -x output into the
// subset of its XML schema relevant to issue #636 and validates every GPU.
func ValidateNvidiaSMIEncoderFBCXML(out string, encoder, fbc EncoderFBCStats, accountingBufferSize int) error {
	var log nvidiaSMILog
	if err := xml.Unmarshal([]byte(out), &log); err != nil {
		return fmt.Errorf("parse nvidia-smi XML: %w", err)
	}
	if len(log.GPUs) == 0 {
		return errors.New("nvidia-smi XML contains no GPUs")
	}
	for i, gpu := range log.GPUs {
		name := gpu.ID
		if name == "" {
			name = fmt.Sprintf("GPU %d", i)
		}
		if err := validateStatsBlock(name+" Encoder Stats", gpu.EncoderStats, encoder); err != nil {
			return err
		}
		if err := validateStatsBlock(name+" FBC Stats", gpu.FBCStats, fbc); err != nil {
			return err
		}
		if err := validateNvidiaSMIInteger(name+" Accounting Mode Buffer Size", gpu.AccountingModeBufferSize, accountingBufferSize); err != nil {
			return err
		}
	}
	return nil
}

func validateStatsBlock(name string, got nvidiaSMIStatsBlock, want EncoderFBCStats) error {
	if err := validateNvidiaSMIInteger(name+" session_count", got.SessionCount, want.SessionCount); err != nil {
		return err
	}
	if err := validateNvidiaSMIInteger(name+" average_fps", got.AverageFPS, want.AverageFPS); err != nil {
		return err
	}
	return validateNvidiaSMIInteger(name+" average_latency", got.AverageLatency, want.AverageLatencyUS)
}

func validateNvidiaSMIInteger(name, raw string, want int) error {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return fmt.Errorf("%s is empty", name)
	}
	value, err := strconv.Atoi(fields[0])
	if err != nil {
		return fmt.Errorf("%s = %q: expected integer: %w", name, raw, err)
	}
	if value != want {
		return fmt.Errorf("%s = %d, want %d", name, value, want)
	}
	return nil
}

// DiffTemperatureQuery checks nvidia-smi -q -d TEMPERATURE output for the
// architecture-correct threshold presentation:
//
//   - pre-Ada (reportsTLimit=false): absolute "GPU Shutdown/Slowdown/Max
//     Operating Temp" rows equal to the profile thresholds, and no T.Limit row
//     carrying a value.
//   - Ada+ (reportsTLimit=true): the "T.Limit" row labels are present.
//
// Only rows with a numeric reading are considered: nvidia-smi still prints a
// "GPU T.Limit Temp : N/A" line on pre-Ada, and an N/A row is exactly what a
// real unsupported query looks like. A T.Limit row with a NUMBER on pre-Ada is
// the defect. Absolute threshold rows must never be negative or ordered with
// shutdown below slowdown — the impossible rendering the gate fixes.
func DiffTemperatureQuery(out string, reportsTLimit bool, shutdownC, slowdownC, maxOperatingC int) []string {
	rows := parseTemperatureQueryRows(out)
	if reportsTLimit {
		return diffTLimitTemperatureRows(rows)
	}
	return diffAbsoluteTemperatureRows(rows, shutdownC, slowdownC, maxOperatingC)
}

func diffTLimitTemperatureRows(rows map[string]int) []string {
	var problems []string
	for _, label := range []string{
		"GPU Shutdown T.Limit Temp",
		"GPU Slowdown T.Limit Temp",
		"GPU Max Operating T.Limit Temp",
	} {
		if _, ok := rows[label]; !ok {
			problems = append(problems, fmt.Sprintf("missing %q row", label))
		}
	}
	for _, label := range []string{
		"GPU Shutdown Temp",
		"GPU Slowdown Temp",
		"GPU Max Operating Temp",
	} {
		if _, ok := rows[label]; ok {
			problems = append(problems, fmt.Sprintf("unexpected absolute %q row on Ada+ profile", label))
		}
	}
	return problems
}

func diffAbsoluteTemperatureRows(rows map[string]int, shutdownC, slowdownC, maxOperatingC int) []string {
	want := map[string]int{
		"GPU Shutdown Temp":      shutdownC,
		"GPU Slowdown Temp":      slowdownC,
		"GPU Max Operating Temp": maxOperatingC,
	}
	problems := diffExpectedAbsoluteRows(rows, want)
	problems = append(problems, diffUnexpectedTLimitRows(rows)...)
	return append(problems, diffAbsoluteTemperatureOrdering(rows)...)
}

func diffExpectedAbsoluteRows(rows, want map[string]int) []string {
	var problems []string
	for label, wantC := range want {
		got, ok := rows[label]
		if !ok {
			problems = append(problems, fmt.Sprintf("missing absolute %q row", label))
			continue
		}
		if got != wantC {
			problems = append(problems, fmt.Sprintf("%s = %d C, want %d C", label, got, wantC))
		}
	}
	return problems
}

func diffUnexpectedTLimitRows(rows map[string]int) []string {
	var problems []string
	for _, label := range []string{
		"GPU Shutdown T.Limit Temp",
		"GPU Slowdown T.Limit Temp",
		"GPU Max Operating T.Limit Temp",
		"GPU T.Limit Temp",
	} {
		if _, ok := rows[label]; ok {
			problems = append(problems, fmt.Sprintf("unexpected T.Limit %q row on pre-Ada profile", label))
		}
	}
	return problems
}

func diffAbsoluteTemperatureOrdering(rows map[string]int) []string {
	var problems []string
	shutdown, hasShutdown := rows["GPU Shutdown Temp"]
	slowdown, hasSlowdown := rows["GPU Slowdown Temp"]
	if hasShutdown && shutdown < 0 {
		problems = append(problems, fmt.Sprintf("GPU Shutdown Temp is negative (%d C)", shutdown))
	}
	if hasSlowdown && slowdown < 0 {
		problems = append(problems, fmt.Sprintf("GPU Slowdown Temp is negative (%d C)", slowdown))
	}
	if hasShutdown && hasSlowdown && shutdown < slowdown {
		problems = append(problems, fmt.Sprintf(
			"GPU Shutdown Temp (%d C) is below GPU Slowdown Temp (%d C)", shutdown, slowdown))
	}
	return problems
}

func parseTemperatureQueryRows(out string) map[string]int {
	rows := make(map[string]int)
	for _, m := range tempQueryRowRE.FindAllStringSubmatch(out, -1) {
		label := strings.TrimSpace(m[1])
		val, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		rows[label] = val
	}
	return rows
}
