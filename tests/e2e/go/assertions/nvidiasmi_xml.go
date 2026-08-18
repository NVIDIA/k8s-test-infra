// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The `nvidia-smi -q -x` schema, shared by every assertion that reads it. The
// XML is the machine-readable form of `-q`, so these checks match DTD element
// names instead of the column layout of the human-readable table.
//
// This file carries no build tag on purpose — same rationale as gfd_labels.go:
// decoding and comparing is pure, so it unit-tests without a cluster, while the
// kubectl exec wrappers live in nvidiasmi.go under //go:build e2e.

type nvidiaSMILog struct {
	GPUs []nvidiaSMIGPU `xml:"gpu"`
}

// nvidiaSMIGPU holds the elements assertions read so far; add fields as more
// are needed. Every reading is a string because nvidia-smi renders unsupported
// queries as "N/A" in the same element that otherwise carries a number, and a
// caller has to be able to tell those apart rather than see a zero.
type nvidiaSMIGPU struct {
	ID                       string               `xml:"id,attr"`
	AccountingModeBufferSize string               `xml:"accounting_mode_buffer_size"`
	EncoderStats             nvidiaSMIStatsBlock  `xml:"encoder_stats"`
	FBCStats                 nvidiaSMIStatsBlock  `xml:"fbc_stats"`
	Utilization              nvidiaSMIUtilization `xml:"utilization"`
	Processes                struct {
		Infos []nvidiaSMIProcessInfo `xml:"process_info"`
	} `xml:"processes"`
}

type nvidiaSMIStatsBlock struct {
	SessionCount   string `xml:"session_count"`
	AverageFPS     string `xml:"average_fps"`
	AverageLatency string `xml:"average_latency"`
}

type nvidiaSMIUtilization struct {
	GPU     string `xml:"gpu_util"`
	Memory  string `xml:"memory_util"`
	Encoder string `xml:"encoder_util"`
	Decoder string `xml:"decoder_util"`
	JPEG    string `xml:"jpeg_util"`
	OFA     string `xml:"ofa_util"`
}

type nvidiaSMIProcessInfo struct {
	PID        int    `xml:"pid"`
	Name       string `xml:"process_name"`
	UsedMemory string `xml:"used_memory"`
}

// parseNvidiaSMIXML decodes `nvidia-smi -q -x` output. A document with no GPUs
// is an error rather than an empty result: nvidia-smi can die mid-run and leave
// a partial tree on stdout, and every caller would otherwise pass by having
// nothing to check.
func parseNvidiaSMIXML(out string) (nvidiaSMILog, error) {
	var log nvidiaSMILog
	if err := xml.Unmarshal([]byte(out), &log); err != nil {
		return log, fmt.Errorf("parse nvidia-smi XML: %w", err)
	}
	if len(log.GPUs) == 0 {
		return log, errors.New("nvidia-smi XML contains no GPUs")
	}
	return log, nil
}

// label names a GPU in assertion output, falling back to its position when
// nvidia-smi emitted no id attribute.
func (g nvidiaSMIGPU) label(index int) string {
	if g.ID == "" {
		return fmt.Sprintf("GPU %d", index)
	}
	return g.ID
}

// nvidiaSMIInteger reads the leading integer of an element body, which is how
// nvidia-smi renders every quantity: "2", "35 %", "6000 MiB", "1500 us". "N/A"
// and an empty body (absent element) both fail.
func nvidiaSMIInteger(raw string) (int, bool) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false
	}
	return value, true
}

// EncoderFBCStats are the non-default values issue #636 expects nvidia-smi to
// surface.
type EncoderFBCStats struct {
	SessionCount     int
	AverageFPS       int
	AverageLatencyUS int
}

// ValidateNvidiaSMIEncoderFBCXML checks the encoder_stats, fbc_stats and
// accounting_mode_buffer_size elements of every GPU. All three read N/A while
// the NVML exports were generated stubs (#636).
func ValidateNvidiaSMIEncoderFBCXML(out string, encoder, fbc EncoderFBCStats, accountingBufferSize int) error {
	log, err := parseNvidiaSMIXML(out)
	if err != nil {
		return err
	}
	for i, gpu := range log.GPUs {
		name := gpu.label(i)
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
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s is empty", name)
	}
	value, ok := nvidiaSMIInteger(raw)
	if !ok {
		return fmt.Errorf("%s = %q: expected integer", name, raw)
	}
	if value != want {
		return fmt.Errorf("%s = %d, want %d", name, value, want)
	}
	return nil
}

// DiffJpgOfaUtilizationXML checks the jpeg_util and ofa_util elements against
// the configured utilization.jpeg and utilization.ofa percentages, for every GPU
// in the output. A GPU-scoped getter that answers for only one device is
// therefore caught.
//
// An "N/A" reading is the defect this exists to catch (#637): both values were
// parsed from the config and then dropped, because the NVML entry points were
// generated stubs returning NOT_SUPPORTED.
func DiffJpgOfaUtilizationXML(out string, wantJPEG, wantOFA int) []string {
	log, err := parseNvidiaSMIXML(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range log.GPUs {
		readings := []struct {
			element string
			raw     string
			want    int
		}{
			{"jpeg_util", gpu.Utilization.JPEG, wantJPEG},
			{"ofa_util", gpu.Utilization.OFA, wantOFA},
		}
		for _, reading := range readings {
			pct, ok := nvidiaSMIInteger(reading.raw)
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf(
					"%s %s = %q, want %d %%; a non-numeric reading means the NVML getter is missing or unimplemented",
					gpu.label(i), reading.element, reading.raw, reading.want))
			case pct != reading.want:
				problems = append(problems, fmt.Sprintf("%s %s = %d %%, want %d %%",
					gpu.label(i), reading.element, pct, reading.want))
			}
		}
	}
	return problems
}

// SMIProcess is one decoded <process_info> entry.
type SMIProcess struct {
	PID       int
	Name      string
	MemoryMiB int
}

// ProcessesXML decodes the <processes> block of the GPU at index gpuIndex, in
// the order nvidia-smi emits GPUs. Callers use it to check the whole-system XML
// view, which walks every GPU's process list in one run.
func ProcessesXML(out string, gpuIndex int) ([]SMIProcess, error) {
	log, err := parseNvidiaSMIXML(out)
	if err != nil {
		return nil, err
	}
	if gpuIndex < 0 || gpuIndex >= len(log.GPUs) {
		return nil, fmt.Errorf("nvidia-smi XML reported %d GPUs, want an entry at index %d",
			len(log.GPUs), gpuIndex)
	}

	infos := log.GPUs[gpuIndex].Processes.Infos
	processes := make([]SMIProcess, 0, len(infos))
	for _, info := range infos {
		mib, ok := nvidiaSMIInteger(info.UsedMemory)
		if !ok {
			return nil, fmt.Errorf("%s: pid %d used_memory = %q, want a MiB reading",
				log.GPUs[gpuIndex].label(gpuIndex), info.PID, info.UsedMemory)
		}
		processes = append(processes, SMIProcess{PID: info.PID, Name: info.Name, MemoryMiB: mib})
	}
	return processes, nil
}
