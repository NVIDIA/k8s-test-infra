// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"strings"
)

// Assertions over the `nvidia-smi -q -x` document. The schema and the reading
// accessors live in nvidiasmi_schema.go.

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
			raw     reading
			want    int
		}{
			{"jpeg_util", gpu.Utilization.JPEG, wantJPEG},
			{"ofa_util", gpu.Utilization.OFA, wantOFA},
		}
		for _, r := range readings {
			pct, ok := r.raw.intValue()
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf(
					"%s %s = %q, want %d %%; a non-numeric reading means the NVML getter is missing or unimplemented",
					gpu.label(i), r.element, string(r.raw), r.want))
			case pct != r.want:
				problems = append(problems, fmt.Sprintf("%s %s = %d %%, want %d %%",
					gpu.label(i), r.element, pct, r.want))
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
