// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ConfComputeReport is the "NVSMI CONF-COMPUTE LOG" block `nvidia-smi
// conf-compute -q` prints, one field per row, in nvidia-smi's own wording.
type ConfComputeReport struct {
	State        string
	MultiGPUMode string
	CPUCaps      string
	GPUCaps      string
	ReadyState   string
}

// confComputeLogHeader opens the -q report. Its absence means nvidia-smi never
// got as far as the report, as on a node with no CC-capable GPU.
const confComputeLogHeader = "NVSMI CONF-COMPUTE LOG"

// ConfComputeQueryProblems compares `nvidia-smi conf-compute -q` output with
// want, row by row. nvidia-smi stops printing at the first getter that
// declines, so a missing row is reported as missing rather than as a mismatch:
// it says which NVML call failed.
func ConfComputeQueryProblems(out string, want ConfComputeReport) []string {
	if !strings.Contains(out, confComputeLogHeader) {
		return []string{fmt.Sprintf("conf-compute -q printed no %s: %q", confComputeLogHeader, strings.TrimSpace(out))}
	}
	rows := colonRows(out)
	var problems []string
	for _, row := range []struct{ label, want string }{
		{"CC State", want.State},
		{"Multi-GPU Mode", want.MultiGPUMode},
		{"CPU CC Capabilities", want.CPUCaps},
		{"GPU CC Capabilities", want.GPUCaps},
		{"CC GPUs Ready State", want.ReadyState},
	} {
		got, ok := rows[row.label]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("conf-compute -q: %q row missing, want %q", row.label, row.want))
		case got != row.want:
			problems = append(problems, fmt.Sprintf("conf-compute -q: %s = %q, want %q", row.label, got, row.want))
		}
	}
	return problems
}

// colonRows keys on the label trimmed of the padding nvidia-smi aligns its
// columns with, and splits at the first colon because no conf-compute label
// contains one while a value might.
func colonRows(out string) map[string]string {
	rows := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		label, value, ok := strings.Cut(sc.Text(), ":")
		if ok {
			rows[strings.TrimSpace(label)] = strings.TrimSpace(value)
		}
	}
	return rows
}

// ConfComputeMemSize is one GPU's protected/unprotected split, as
// `nvidia-smi conf-compute -gm` prints it.
type ConfComputeMemSize struct {
	ProtectedKiB   uint64
	UnprotectedKiB uint64
}

var (
	ccGPULine         = regexp.MustCompile(`^GPU (\d+): `)
	ccProtectedLine   = regexp.MustCompile(`^Protected memory size:\s+(\d+) KiB$`)
	ccUnprotectedLine = regexp.MustCompile(`^Unprotected memory size:\s+(\d+) KiB$`)
	ccKeyRotationLine = regexp.MustCompile(`Key Rotation Threshold Attacker Advantage:\s+(\d+)`)
)

// ParseConfComputeMemSizes reads `nvidia-smi conf-compute -gm` output, keyed by
// GPU index.
func ParseConfComputeMemSizes(out string) (map[int]ConfComputeMemSize, error) {
	sizes := map[int]ConfComputeMemSize{}
	gpu := -1
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := ccGPULine.FindStringSubmatch(line); m != nil {
			// The pattern only matches digits.
			gpu, _ = strconv.Atoi(m[1])
			sizes[gpu] = ConfComputeMemSize{}
			continue
		}
		if gpu < 0 {
			continue
		}
		size := sizes[gpu]
		if m := ccProtectedLine.FindStringSubmatch(line); m != nil {
			v, err := strconv.ParseUint(m[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("GPU %d protected size %q: %w", gpu, m[1], err)
			}
			size.ProtectedKiB = v
		}
		if m := ccUnprotectedLine.FindStringSubmatch(line); m != nil {
			v, err := strconv.ParseUint(m[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("GPU %d unprotected size %q: %w", gpu, m[1], err)
			}
			size.UnprotectedKiB = v
		}
		sizes[gpu] = size
	}
	if len(sizes) == 0 {
		return nil, fmt.Errorf("conf-compute -gm printed no GPU rows: %q", strings.TrimSpace(out))
	}
	return sizes, nil
}

// ParseConfComputeKeyRotation reads the attacker advantage from
// `nvidia-smi conf-compute -gkr` output.
func ParseConfComputeKeyRotation(out string) (uint64, error) {
	m := ccKeyRotationLine.FindStringSubmatch(out)
	if m == nil {
		return 0, errors.New("conf-compute -gkr printed no attacker advantage: " + strings.TrimSpace(out))
	}
	return strconv.ParseUint(m[1], 10, 64)
}
