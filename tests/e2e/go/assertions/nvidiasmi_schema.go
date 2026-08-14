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
// XML is the machine-readable form of `-q`, so checks match DTD element names
// (nvsmi_device_v13.dtd) instead of the column layout of the human table.
//
// This file carries no build tag on purpose — same rationale as gfd_labels.go:
// decoding and comparing is pure, so it unit-tests without a cluster, while the
// kubectl exec wrappers live in nvidiasmi.go under //go:build e2e.
//
// Captured reference documents live in testdata/. This is the only place
// element names appear, so a driver bump that renames one is a single-file
// change. Several names repeat under different parents (sm_clock under both
// clocks and max_clocks; average_power_draw under gpu_power_readings,
// gpu_memory_power_readings and module_power_readings), so nesting matters.

// reading is one element body. nvidia-smi renders four distinct things in the
// same element, and collapsing them loses the signal assertions need: an
// architecture that does not emit the element at all, a supported-but-N/A
// query, a failed device, and a value. encoding/xml cannot tell an absent
// element from an empty body — both decode to "" — but nvidia-smi never emits
// an empty body, so "" means absent.
type reading string

// nvmlErrorBodies are the NVML failure strings nvidia-smi substitutes for a
// value. "N/A" is deliberately absent: an unsupported query is not a failure,
// and a healthy passively-cooled GPU reports fan_speed as N/A.
var nvmlErrorBodies = []string{
	"GPU is lost",
	"Unknown Error",
	"GPU is inaccessible",
	"Insufficient Permissions",
}

func (r reading) present() bool { return strings.TrimSpace(string(r)) != "" }

func (r reading) unsupported() bool { return strings.TrimSpace(string(r)) == "N/A" }

func (r reading) failed() bool {
	body := strings.TrimSpace(string(r))
	for _, e := range nvmlErrorBodies {
		if strings.EqualFold(body, e) {
			return true
		}
	}
	return false
}

// intValue reads the leading integer of a body, which is how nvidia-smi renders
// every quantity: "2", "35 %", "6000 MiB", "1500 us", "-5 C". An absent body,
// "N/A" and a failure string all report false.
func (r reading) intValue() (int, bool) {
	fields := strings.Fields(string(r))
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false
	}
	return value, true
}

// floatValue is intValue for the readings nvidia-smi renders with decimals,
// such as "618.35 W".
func (r reading) floatValue() (float64, bool) {
	fields := strings.Fields(string(r))
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

type nvidiaSMILog struct {
	DriverVersion reading        `xml:"driver_version"`
	AttachedGPUs  reading        `xml:"attached_gpus"`
	GPUs          []nvidiaSMIGPU `xml:"gpu"`
}

// nvidiaSMIGPU holds the elements assertions read; add fields as more are
// needed. Every body is a reading rather than a number for the reasons on the
// reading type.
type nvidiaSMIGPU struct {
	ID                       string                 `xml:"id,attr"`
	ProductName              reading                `xml:"product_name"`
	ProductArchitecture      reading                `xml:"product_architecture"`
	UUID                     reading                `xml:"uuid"`
	FanSpeed                 reading                `xml:"fan_speed"`
	PerformanceState         reading                `xml:"performance_state"`
	FBMemoryUsage            nvidiaSMIMemoryUsage   `xml:"fb_memory_usage"`
	AccountingModeBufferSize reading                `xml:"accounting_mode_buffer_size"`
	EncoderStats             nvidiaSMIStatsBlock    `xml:"encoder_stats"`
	FBCStats                 nvidiaSMIStatsBlock    `xml:"fbc_stats"`
	Utilization              nvidiaSMIUtilization   `xml:"utilization"`
	Temperature              nvidiaSMITemperature   `xml:"temperature"`
	PowerReadings            nvidiaSMIPowerReadings `xml:"gpu_power_readings"`
	Clocks                   nvidiaSMIClocks        `xml:"clocks"`
	ECCErrors                nvidiaSMIECCErrors     `xml:"ecc_errors"`
	ClocksEventReasons       nvidiaSMIEventReasons  `xml:"clocks_event_reasons"`
	Processes                struct {
		Infos []nvidiaSMIProcessInfo `xml:"process_info"`
	} `xml:"processes"`
}

// nvidiaSMIMemoryUsage is <fb_memory_usage>: the framebuffer. The sibling
// <bar1_memory_usage> and <cc_protected_memory_usage> blocks repeat the same
// child names and are deliberately not decoded.
type nvidiaSMIMemoryUsage struct {
	Total    reading `xml:"total"`
	Reserved reading `xml:"reserved"`
	Used     reading `xml:"used"`
	Free     reading `xml:"free"`
}

type nvidiaSMIStatsBlock struct {
	SessionCount   reading `xml:"session_count"`
	AverageFPS     reading `xml:"average_fps"`
	AverageLatency reading `xml:"average_latency"`
}

type nvidiaSMIUtilization struct {
	GPU     reading `xml:"gpu_util"`
	Memory  reading `xml:"memory_util"`
	Encoder reading `xml:"encoder_util"`
	Decoder reading `xml:"decoder_util"`
	JPEG    reading `xml:"jpeg_util"`
	OFA     reading `xml:"ofa_util"`
}

// nvidiaSMITemperature carries both threshold presentations. nvidia-smi emits
// the absolute elements on pre-Ada and the *_tlimit_threshold elements on Ada
// and later; the unused set is absent from the document, not N/A. That absence
// is what the architecture gate (#635) asserts on.
type nvidiaSMITemperature struct {
	GPUTemp               reading `xml:"gpu_temp"`
	TLimit                reading `xml:"gpu_temp_tlimit"`
	MaxThreshold          reading `xml:"gpu_temp_max_threshold"`
	SlowThreshold         reading `xml:"gpu_temp_slow_threshold"`
	MaxGPUThreshold       reading `xml:"gpu_temp_max_gpu_threshold"`
	MaxTLimitThreshold    reading `xml:"gpu_temp_max_tlimit_threshold"`
	SlowTLimitThreshold   reading `xml:"gpu_temp_slow_tlimit_threshold"`
	MaxGPUTLimitThreshold reading `xml:"gpu_temp_max_gpu_tlimit_threshold"`
	TargetTemperature     reading `xml:"gpu_target_temperature"`
	MemoryTemp            reading `xml:"memory_temp"`
}

type nvidiaSMIPowerReadings struct {
	PowerState        reading `xml:"power_state"`
	AveragePowerDraw  reading `xml:"average_power_draw"`
	InstantPowerDraw  reading `xml:"instant_power_draw"`
	CurrentPowerLimit reading `xml:"current_power_limit"`
	DefaultPowerLimit reading `xml:"default_power_limit"`
	MinPowerLimit     reading `xml:"min_power_limit"`
	MaxPowerLimit     reading `xml:"max_power_limit"`
}

// nvidiaSMIClocks is the <clocks> block: the clocks in effect now. The sibling
// <max_clocks> and <applications_clocks> blocks reuse the same child names and
// are deliberately not decoded — clocks.sm reads the current value.
type nvidiaSMIClocks struct {
	GraphicsClock reading `xml:"graphics_clock"`
	SMClock       reading `xml:"sm_clock"`
	MemClock      reading `xml:"mem_clock"`
	VideoClock    reading `xml:"video_clock"`
}

type nvidiaSMIECCErrors struct {
	Volatile  nvidiaSMIECCCounters `xml:"volatile"`
	Aggregate nvidiaSMIECCCounters `xml:"aggregate"`
}

type nvidiaSMIECCCounters struct {
	SRAMCorrectable         reading `xml:"sram_correctable"`
	SRAMUncorrectableParity reading `xml:"sram_uncorrectable_parity"`
	SRAMUncorrectableSECDED reading `xml:"sram_uncorrectable_secded"`
	DRAMCorrectable         reading `xml:"dram_correctable"`
	DRAMUncorrectable       reading `xml:"dram_uncorrectable"`
}

type nvidiaSMIEventReasons struct {
	GPUIdle           reading `xml:"clocks_event_reason_gpu_idle"`
	SWPowerCap        reading `xml:"clocks_event_reason_sw_power_cap"`
	HWSlowdown        reading `xml:"clocks_event_reason_hw_slowdown"`
	HWThermalSlowdown reading `xml:"clocks_event_reason_hw_thermal_slowdown"`
	SWThermalSlowdown reading `xml:"clocks_event_reason_sw_thermal_slowdown"`
}

type nvidiaSMIProcessInfo struct {
	PID        int     `xml:"pid"`
	Name       string  `xml:"process_name"`
	UsedMemory reading `xml:"used_memory"`
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
