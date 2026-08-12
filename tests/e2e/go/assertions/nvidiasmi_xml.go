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

// Decoding of `nvidia-smi -q -x`, the machine-readable form of `-q`. Matching
// DTD element names keeps these checks independent of the column layout of the
// human-readable table.
//
// This file carries no build tag on purpose — same rationale as
// nvidiasmi_temperature.go: pure parsing unit-tests without a cluster, while
// the kubectl exec wrappers live in nvidiasmi.go under //go:build e2e.

// smiLog is the subset of nvidia-smi's XML these assertions read. Readings are
// strings because nvidia-smi renders both "35 %" and "N/A" in the same element.
type smiLog struct {
	GPUs []smiGPU `xml:"gpu"`
}

type smiGPU struct {
	ID          string `xml:"id,attr"`
	Utilization struct {
		JPEG string `xml:"jpeg_util"`
		OFA  string `xml:"ofa_util"`
	} `xml:"utilization"`
	Processes struct {
		Infos []struct {
			PID        int    `xml:"pid"`
			Name       string `xml:"process_name"`
			UsedMemory string `xml:"used_memory"`
		} `xml:"process_info"`
	} `xml:"processes"`
}

func parseSMILog(out string) (smiLog, error) {
	var log smiLog
	if err := xml.Unmarshal([]byte(out), &log); err != nil {
		return log, fmt.Errorf("parse nvidia-smi -q -x output: %w", err)
	}
	if len(log.GPUs) == 0 {
		return log, errors.New("nvidia-smi -q -x reported no GPUs")
	}
	return log, nil
}

// DiffJpgOfaUtilizationXML checks the jpeg_util and ofa_util elements against
// the configured utilization.jpeg and utilization.ofa percentages, for every
// GPU in the output. A GPU-scoped getter that answers for only one device is
// therefore caught.
//
// An "N/A" reading is the defect this exists to catch (#637): both values were
// parsed from the config and then dropped, because the NVML entry points were
// generated stubs returning NOT_SUPPORTED.
func DiffJpgOfaUtilizationXML(out string, wantJPEG, wantOFA int) []string {
	log, err := parseSMILog(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for _, gpu := range log.GPUs {
		rows := []struct {
			element string
			reading string
			want    int
		}{
			{"jpeg_util", gpu.Utilization.JPEG, wantJPEG},
			{"ofa_util", gpu.Utilization.OFA, wantOFA},
		}
		for _, row := range rows {
			pct, ok := parseSMIQuantity(row.reading, "%")
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"GPU %s %s = %q, want %d %%; a non-numeric reading means the NVML getter is missing or unimplemented",
					gpu.ID, row.element, row.reading, row.want))
				continue
			}
			if pct != row.want {
				problems = append(problems, fmt.Sprintf("GPU %s %s = %d %%, want %d %%",
					gpu.ID, row.element, pct, row.want))
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
// view, which walks every GPU's process list.
func ProcessesXML(out string, gpuIndex int) ([]SMIProcess, error) {
	log, err := parseSMILog(out)
	if err != nil {
		return nil, err
	}
	if gpuIndex < 0 || gpuIndex >= len(log.GPUs) {
		return nil, fmt.Errorf("nvidia-smi -q -x reported %d GPUs, want an entry at index %d",
			len(log.GPUs), gpuIndex)
	}

	infos := log.GPUs[gpuIndex].Processes.Infos
	processes := make([]SMIProcess, 0, len(infos))
	for _, info := range infos {
		mib, ok := parseSMIQuantity(info.UsedMemory, "MiB")
		if !ok {
			return nil, fmt.Errorf("GPU at index %d: pid %d used_memory = %q, want a MiB reading",
				gpuIndex, info.PID, info.UsedMemory)
		}
		processes = append(processes, SMIProcess{PID: info.PID, Name: info.Name, MemoryMiB: mib})
	}
	return processes, nil
}

// parseSMIQuantity reads a "35 %" or "6000 MiB" style element body. An empty
// body (element absent) and "N/A" both fail, which callers report rather than
// silently reading as zero.
func parseSMIQuantity(reading, unit string) (int, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(reading), unit))
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, false
	}
	return value, true
}
