// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvidiasmi asserts what `nvidia-smi -q -x` reports. The XML is the
// machine-readable form of -q, so checks match DTD element names
// (nvsmi_device_v13.dtd) instead of the column layout of the human table.
//
// The package is layered, one concern per file:
//
//   - schema.go decodes the document. It is the only place element names
//     appear, so a driver bump that renames one is a single-file change.
//   - snapshot.go exposes the decoded readings as Snapshot and GPU.
//   - diff.go and temperature.go compare those readings against what the mock
//     was configured with, returning the problems they found.
//   - exec.go runs nvidia-smi in a pod and asserts through Gomega. It is the
//     only file under //go:build e2e, so everything else unit-tests without a
//     cluster against the captured documents in testdata/.
package nvidiasmi

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Several element names repeat under different parents (sm_clock under both
// clocks and max_clocks; average_power_draw under gpu_power_readings,
// gpu_memory_power_readings and module_power_readings), so nesting matters:
// every block that shares child names with a sibling says so.

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

// document is <nvidia_smi_log>, the whole -q -x output.
type document struct {
	DriverVersion reading      `xml:"driver_version"`
	AttachedGPUs  reading      `xml:"attached_gpus"`
	GPUs          []gpuElement `xml:"gpu"`
}

// gpuElement holds the elements assertions read; add fields as more are needed.
// Every body is a reading rather than a number for the reasons on that type.
type gpuElement struct {
	ID                       string            `xml:"id,attr"`
	ProductName              reading           `xml:"product_name"`
	ProductArchitecture      reading           `xml:"product_architecture"`
	UUID                     reading           `xml:"uuid"`
	BoardID                  reading           `xml:"board_id"`
	C2CMode                  reading           `xml:"c2c_mode"`
	PlatformInfo             platformInfo      `xml:"platformInfo"`
	PCI                      pciInfo           `xml:"pci"`
	FanSpeed                 reading           `xml:"fan_speed"`
	PerformanceState         reading           `xml:"performance_state"`
	FBMemoryUsage            memoryUsage       `xml:"fb_memory_usage"`
	AccountingModeBufferSize reading           `xml:"accounting_mode_buffer_size"`
	EncoderStats             statsBlock        `xml:"encoder_stats"`
	FBCStats                 statsBlock        `xml:"fbc_stats"`
	Utilization              utilization       `xml:"utilization"`
	Virtualization           gpuVirtualization `xml:"gpu_virtualization_mode"`
	Temperature              temperature       `xml:"temperature"`
	PowerReadings            powerReadings     `xml:"gpu_power_readings"`
	Clocks                   clocks            `xml:"clocks"`
	ECCMode                  eccMode           `xml:"ecc_mode"`
	ECCErrors                eccErrors         `xml:"ecc_errors"`
	RemappedRows             remappedRows      `xml:"remapped_rows"`
	ClocksEventReasons       eventReasons      `xml:"clocks_event_reasons"`
	ClocksEventCounters      eventCounters     `xml:"clocks_event_reasons_counters"`
	Fabric                   fabricBlock       `xml:"fabric"`
	Processes                struct {
		Infos []processInfo `xml:"process_info"`
	} `xml:"processes"`
}

// memoryUsage is <fb_memory_usage>: the framebuffer. The sibling
// <bar1_memory_usage> and <cc_protected_memory_usage> blocks repeat the same
// child names and are deliberately not decoded.
type memoryUsage struct {
	Total    reading `xml:"total"`
	Reserved reading `xml:"reserved"`
	Used     reading `xml:"used"`
	Free     reading `xml:"free"`
}

// platformInfo is <platformInfo> — the one camelCase container in the document.
// It answers where the board sits in a rack: the chassis, the slot and tray
// within it, the node inside that tray, and the GPU's module within the node.
// gpu_fabric_guid shares the block but comes from a field of the same NVML
// struct that the mock does not model, so it is deliberately not decoded.
type platformInfo struct {
	ChassisSerialNumber reading `xml:"chassis_serial_number"`
	SlotNumber          reading `xml:"slot_number"`
	TrayIndex           reading `xml:"tray_index"`
	HostID              reading `xml:"host_id"`
	PeerType            reading `xml:"peer_type"`
	ModuleID            reading `xml:"module_id"`
}

// pciInfo is <pci>. Only the link-generation subtree is decoded; the sibling
// <pci_bridge_chip> and the flat address elements are not read.
type pciInfo struct {
	GPULinkInfo gpuLinkInfo `xml:"pci_gpu_link_info"`
}

// gpuLinkInfo is <pci_gpu_link_info>. Its other child, <link_widths>, names its
// readings max_link_width and current_link_width, so the generation and width
// elements cannot be confused for one another.
type gpuLinkInfo struct {
	PCIeGen pcieGen `xml:"pcie_gen"`
}

// pcieGen is <pcie_gen>. The three maxima reach nvidia-smi by different routes:
// max_link_gen and max_device_link_gen come from public NVML getters, while
// max_host_link_gen comes from a slot of the internal export table. The current
// readings are deliberately not decoded — no assertion reads them yet.
type pcieGen struct {
	Max       reading `xml:"max_link_gen"`
	DeviceMax reading `xml:"max_device_link_gen"`
	HostMax   reading `xml:"max_host_link_gen"`
}

type statsBlock struct {
	SessionCount   reading `xml:"session_count"`
	AverageFPS     reading `xml:"average_fps"`
	AverageLatency reading `xml:"average_latency"`
}

type utilization struct {
	GPU     reading `xml:"gpu_util"`
	Memory  reading `xml:"memory_util"`
	Encoder reading `xml:"encoder_util"`
	Decoder reading `xml:"decoder_util"`
	JPEG    reading `xml:"jpeg_util"`
	OFA     reading `xml:"ofa_util"`
}

// gpuVirtualization is <gpu_virtualization_mode>. Only the first element is a
// bare-metal answer the mock can give; the vGPU pair is unsupported hardware
// state, so N/A is correct for them and asserted as such.
type gpuVirtualization struct {
	Mode              reading `xml:"virtualization_mode"`
	HostVGPUMode      reading `xml:"host_vgpu_mode"`
	HeterogeneousMode reading `xml:"vgpu_heterogeneous_mode"`
}

// temperature carries both threshold presentations. nvidia-smi emits the
// absolute elements on pre-Ada and the *_tlimit_threshold elements on Ada and
// later; the unused set is absent from the document, not N/A. That absence is
// what the architecture gate (#635) asserts on.
type temperature struct {
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

type powerReadings struct {
	PowerState        reading `xml:"power_state"`
	AveragePowerDraw  reading `xml:"average_power_draw"`
	InstantPowerDraw  reading `xml:"instant_power_draw"`
	CurrentPowerLimit reading `xml:"current_power_limit"`
	DefaultPowerLimit reading `xml:"default_power_limit"`
	MinPowerLimit     reading `xml:"min_power_limit"`
	MaxPowerLimit     reading `xml:"max_power_limit"`
}

// clocks is the <clocks> block: the clocks in effect now. The sibling
// <max_clocks> and <applications_clocks> blocks reuse the same child names and
// are deliberately not decoded — clocks.sm reads the current value.
type clocks struct {
	GraphicsClock reading `xml:"graphics_clock"`
	SMClock       reading `xml:"sm_clock"`
	MemClock      reading `xml:"mem_clock"`
	VideoClock    reading `xml:"video_clock"`
}

type eccErrors struct {
	Volatile  eccCounters `xml:"volatile"`
	Aggregate eccCounters `xml:"aggregate"`
	// SRAMSources is emitted as a sibling of the two scope blocks, not inside
	// <aggregate>, even though it breaks down the aggregate uncorrectable count.
	SRAMSources eccSRAMSources `xml:"aggregate_uncorrectable_sram_sources"`
}

type eccCounters struct {
	SRAMCorrectable reading `xml:"sram_correctable"`
	// The uncorrectable SRAM count comes in one of two shapes, and which one
	// nvidia-smi emits depends on the GPU's architecture: Ampere and later split
	// it into the parity and SEC-DED pair, pre-Ampere reports the single
	// combined element. Both are decoded so a check can tell an absent element
	// from a wrong value in either rendering.
	SRAMUncorrectableParity reading `xml:"sram_uncorrectable_parity"`
	SRAMUncorrectableSECDED reading `xml:"sram_uncorrectable_secded"`
	SRAMUncorrectable       reading `xml:"sram_uncorrectable"`
	DRAMCorrectable         reading `xml:"dram_correctable"`
	DRAMUncorrectable       reading `xml:"dram_uncorrectable"`
	// SRAMThresholdExceeded is emitted under <aggregate> only; it is absent from
	// <volatile>, which is a threshold-free scope, and from pre-Ampere output.
	SRAMThresholdExceeded reading `xml:"sram_threshold_exceeded"`
}

// eccSRAMSources is <aggregate_uncorrectable_sram_sources>: which unit reported
// the aggregate uncorrectable SRAM errors. Ampere and later only.
type eccSRAMSources struct {
	L2              reading `xml:"sram_l2"`
	SM              reading `xml:"sram_sm"`
	Microcontroller reading `xml:"sram_microcontroller"`
	PCIe            reading `xml:"sram_pcie"`
	Other           reading `xml:"sram_other"`
}

// eccMode is <ecc_mode>. SRAM and DRAM counters only exist while ECC is on, so
// specs asserting on them gate against this first.
type eccMode struct {
	Current reading `xml:"current_ecc"`
	Pending reading `xml:"pending_ecc"`
}

// remappedRows is <remapped_rows>: rows the GPU has retired plus how much spare
// remap capacity is left.
type remappedRows struct {
	Correctable   reading `xml:"remapped_row_corr"`
	Uncorrectable reading `xml:"remapped_row_unc"`
	Pending       reading `xml:"remapped_row_pending"`
	Failure       reading `xml:"remapped_row_failure"`
	// Histogram is "N/A" when the GPU reports no bank availability and a nested
	// block of per-bucket bank counts when it does. The raw body is kept rather
	// than decoded: the child element names belong to the driver's DTD, and an
	// assertion only needs to tell a populated histogram from an absent one.
	Histogram struct {
		Body string `xml:",innerxml"`
	} `xml:"row_remapper_histogram"`
}

type eventReasons struct {
	GPUIdle           reading `xml:"clocks_event_reason_gpu_idle"`
	SWPowerCap        reading `xml:"clocks_event_reason_sw_power_cap"`
	HWSlowdown        reading `xml:"clocks_event_reason_hw_slowdown"`
	HWThermalSlowdown reading `xml:"clocks_event_reason_hw_thermal_slowdown"`
	SWThermalSlowdown reading `xml:"clocks_event_reason_sw_thermal_slowdown"`
}

// eventCounters is <clocks_event_reasons_counters>: how long each cause has
// held the GPU below its requested clocks, as microsecond totals. It is the
// after-the-fact companion to the instantaneous flags in eventReasons above —
// a workload that ran slow is diagnosed from these, not from catching a flag
// mid-sample. Every element read N/A until the mock answered the field ids
// behind them (#678). The element names are the NVML field names, which is why
// they abbreviate "thermal" where the flags above spell it out.
type eventCounters struct {
	SWPowerCap        reading `xml:"clocks_event_reasons_counters_sw_power_cap"`
	SyncBoost         reading `xml:"clocks_event_reasons_counters_sync_boost"`
	SWThermalSlowdown reading `xml:"clocks_event_reasons_counters_sw_therm_slowdown"`
	HWThermalSlowdown reading `xml:"clocks_event_reasons_counters_hw_therm_slowdown"`
	HWPowerBrake      reading `xml:"clocks_event_reasons_counters_hw_power_brake"`
}

// fabricBlock is <fabric>: the GPU's NVLink fabric attachment. Its <status>
// child is the NVML return code embedded in GpuFabricInfo, not a health
// verdict — the health verdict is the <health> sub-block, whose every element
// read N/A until the mock reported a health summary (#677).
//
// The reference GB300 tray also renders a Partition Assigned row, but no
// element for it exists in the documents this package is pinned against, so
// none is decoded: nvidia-smi 580.65.06 does not know that row at all.
type fabricBlock struct {
	State       reading           `xml:"state"`
	Status      reading           `xml:"status"`
	CliqueID    reading           `xml:"cliqueId"`
	ClusterUUID reading           `xml:"clusterUuid"`
	Health      fabricHealthBlock `xml:"health"`
}

// fabricHealthBlock is <fabric><health>. Each element carries one field of the
// NVML fabric health mask, except <summary>, which carries the overall
// healthSummary the mock has to report before nvidia-smi decodes any of the
// others.
type fabricHealthBlock struct {
	Summary                reading `xml:"summary"`
	Bandwidth              reading `xml:"bandwidth"`
	RouteRecovery          reading `xml:"route_recovery_in_progress"`
	RouteUnhealthy         reading `xml:"route_unhealthy"`
	AccessTimeoutRecovery  reading `xml:"access_timeout_recovery"`
	IncorrectConfiguration reading `xml:"incorrect_configuration"`
}

type processInfo struct {
	PID        int     `xml:"pid"`
	Name       string  `xml:"process_name"`
	UsedMemory reading `xml:"used_memory"`
}

// parse decodes `nvidia-smi -q -x` output. A document with no GPUs is an error
// rather than an empty result: nvidia-smi can die mid-run and leave a partial
// tree on stdout, and every caller would otherwise pass by having nothing to
// check.
func parse(out string) (document, error) {
	var doc document
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		return doc, fmt.Errorf("parse nvidia-smi XML: %w", err)
	}
	if len(doc.GPUs) == 0 {
		return doc, errors.New("nvidia-smi XML contains no GPUs")
	}
	return doc, nil
}

// label names a GPU in assertion output, falling back to its position when
// nvidia-smi emitted no id attribute.
func (g gpuElement) label(index int) string {
	if g.ID == "" {
		return fmt.Sprintf("GPU %d", index)
	}
	return g.ID
}
