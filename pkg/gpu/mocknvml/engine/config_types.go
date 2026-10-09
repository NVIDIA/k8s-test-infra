// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

// YAMLConfig represents the top-level YAML configuration for mock NVML.
// Use MOCK_NVML_CONFIG environment variable to specify the config file path.
type YAMLConfig struct {
	Version        string           `json:"version"`
	System         SystemConfig     `json:"system"`
	DeviceDefaults DeviceConfig     `json:"device_defaults"`
	Devices        []DeviceOverride `json:"devices"`
	NVLink         *NVLinkConfig    `json:"nvlink,omitempty"`

	// PCIeTopology describes the root-complex / NUMA layout consumed by the
	// pcibus simulator and the engine for NVLink topology, pairwise PCIe
	// levels, and CPU/NUMA affinity. Optional; absent on PCIe-only profiles.
	PCIeTopology *PCIeTopologyConfig `json:"pcie_topology,omitempty"`
}

// SystemConfig contains system-level NVML settings
type SystemConfig struct {
	DriverVersion    string `json:"driver_version"`
	NVMLVersion      string `json:"nvml_version"`
	CUDAVersion      string `json:"cuda_version"`
	CUDAVersionMajor int    `json:"cuda_version_major"`
	CUDAVersionMinor int    `json:"cuda_version_minor"`
	NumDevices       int    `json:"num_devices,omitzero"`
}

// DeviceConfig represents the full device configuration.
// Used for both defaults and per-device overrides.
type DeviceConfig struct {
	// Basic identification
	Name            string `json:"name,omitempty"`
	Brand           string `json:"brand,omitempty"`
	Serial          string `json:"serial,omitempty"`
	BoardPartNumber string `json:"board_part_number,omitempty"`
	VBIOSVersion    string `json:"vbios_version,omitempty"`

	// Architecture
	Architecture      string                   `json:"architecture,omitempty"`
	ComputeCapability *ComputeCapabilityConfig `json:"compute_capability,omitempty"`
	NumGPUCores       int                      `json:"num_gpu_cores,omitzero"`

	// InfoROM
	InfoROM *InfoROMConfig `json:"inforom,omitempty"`

	// Memory
	Memory     *MemoryConfig     `json:"memory,omitempty"`
	BAR1Memory *BAR1MemoryConfig `json:"bar1_memory,omitempty"`

	// PCI
	PCI  *PCIConfig  `json:"pci,omitempty"`
	PCIe *PCIeConfig `json:"pcie,omitempty"`

	// Power
	Power *PowerConfig `json:"power,omitempty"`

	// Thermal
	Thermal *ThermalConfig `json:"thermal,omitempty"`

	// Fan
	Fan *FanConfig `json:"fan,omitempty"`

	// Clocks
	Clocks                *ClocksConfig                `json:"clocks,omitempty"`
	ClocksThrottleReasons *ClocksThrottleReasonsConfig `json:"clocks_throttle_reasons,omitempty"`
	SupportedClocks       *SupportedClocksConfig       `json:"supported_clocks,omitempty"`

	// Performance
	PerformanceState string `json:"performance_state,omitempty"`

	// Utilization
	Utilization *UtilizationConfig `json:"utilization,omitempty"`

	// Encoder/Decoder
	EncoderStats *EncoderStatsConfig `json:"encoder_stats,omitempty"`
	FBCStats     *FBCStatsConfig     `json:"fbc_stats,omitempty"`

	// ECC
	ECC *ECCConfig `json:"ecc,omitempty"`

	// Retired pages
	RetiredPages *RetiredPagesConfig `json:"retired_pages,omitempty"`

	// Remapped rows
	RemappedRows *RemappedRowsConfig `json:"remapped_rows,omitempty"`

	// Display
	Display *DisplayConfig `json:"display,omitempty"`

	// Modes
	PersistenceMode string `json:"persistence_mode,omitempty"`
	ComputeMode     string `json:"compute_mode,omitempty"`

	// Draining is the drain state nvmlDeviceModifyDrainState sets. A draining
	// GPU drops out of enumeration for every process that initialises NVML
	// afterwards, but stays addressable by PCI location so it can be undrained.
	Draining bool `json:"draining,omitempty"`

	// Removed is set by nvmlDeviceRemoveGpu and cleared by
	// nvmlDeviceDiscoverGpus. A removed GPU is gone from the driver: no process
	// enumerates it and the drain calls no longer resolve it.
	Removed bool `json:"removed,omitempty"`

	// Excluded models a GPU the kernel module was told to skip
	// (NVreg_ExcludedGpus). It is never enumerated, the driver does not manage
	// it, and nvmlGetExcludedDeviceInfoByIndex lists it.
	Excluded bool `json:"excluded,omitempty"`

	// MIG
	MIG *MIGConfig `json:"mig,omitempty"`

	// GPU Operation Mode
	GPUOperationMode *GPUOperationModeConfig `json:"gpu_operation_mode,omitempty"`

	// Driver Model
	DriverModel *DriverModelConfig `json:"driver_model,omitempty"`

	// Accounting
	Accounting *AccountingConfig `json:"accounting,omitempty"`

	// Virtualization
	Virtualization *VirtualizationConfig `json:"virtualization,omitempty"`

	// GSP Firmware
	GSPFirmware *GSPFirmwareConfig `json:"gsp_firmware,omitempty"`

	// Blackwell-specific features (GB200)
	Features *FeaturesConfig `json:"features,omitempty"`

	// Host CPU the board is paired with
	CPU *CPUConfig `json:"cpu,omitempty"`

	// Topology
	Topology *TopologyConfig `json:"topology,omitempty"`

	// Processes
	Processes []ProcessConfig `json:"processes,omitempty"`

	// DynamicMetrics enables time-varying values for temperature, power, and
	// utilization. When nil (default), static values from the other config
	// sections are returned as-is.
	DynamicMetrics *DynamicMetricsConfig `json:"dynamic_metrics,omitempty"`

	// GPM tunes the GPU Performance Monitoring surface DCGM's profiling
	// module reads (DCGM_FI_PROF_*). When nil, GPM support follows the
	// device architecture (Hopper and newer, matching real NVML).
	GPM *GPMConfig `json:"gpm,omitempty"`

	// Failure enables GPU failure injection (lost device, fallen-off-bus,
	// uncorrectable ECC, Xid). When nil (default) the device behaves as
	// healthy hardware.
	Failure *FailureInjectionConfig `json:"failure,omitempty"`

	// Fabric enables the GB200/GB300 NVLink fabric API surface. When nil
	// (default) GetGpuFabricInfo / GetGpuFabricInfoV report
	// ERROR_NOT_SUPPORTED — matching every non-fabric-attached GPU.
	Fabric *FabricConfig `json:"fabric,omitempty"`

	// NVLinkError injects per-link NVLink DL error accrual on this device's
	// links to its NVSwitch. When nil (default) the links report the healthy
	// baseline. See NVLinkErrorInjectionConfig.
	NVLinkError *NVLinkErrorInjectionConfig `json:"nvlink_error,omitempty"`

	// NVLinkBwMode records the NVLink Reduced Bandwidth Mode a runtime setter
	// applied. It is per device because that is what the device-level NVML
	// pair takes; the node-wide pair writes the same field into the `all:`
	// bucket, so a node-wide set moves every device the way the driver does.
	//
	// A pointer so an explicit 0 (FULL) is distinguishable from "never set",
	// which falls back to the profile's nvlink.bw_mode.
	NVLinkBwMode *uint8 `json:"nvlink_bw_mode,omitempty"`

	// NVLinkLowPowerThreshold records the threshold
	// nvmlDeviceSetNvLinkDeviceLowPowerThreshold applied, in the 50us units
	// the low-power field values report. Nil is the driver default, which is
	// also what the reset sentinel restores.
	NVLinkLowPowerThreshold *uint32 `json:"nvlink_low_power_threshold,omitempty"`

	// Platform describes where the board physically sits in a rack. When nil
	// (default) nvmlDeviceGetPlatformInfo and nvmlDeviceGetModuleId report
	// ERROR_NOT_SUPPORTED — matching every board outside a Grace-Blackwell
	// rack, whose platform cannot report a location.
	Platform *PlatformConfig `json:"platform,omitempty"`
}

// PlatformConfig models the platform identity nvmlDeviceGetPlatformInfo
// exposes, which `nvidia-smi -q` renders as its "Platform Info" block. It is
// what turns a GPU fault into an actionable physical location on NVL72: module
// 2 of the tray in slot 9 of a given chassis.
//
// The fields live on DeviceConfig so a profile can set any of them per device,
// but only ModuleID varies between the GPUs of a shipped profile. NVML defines
// the rest as properties of the node — see PlatformInfo — and a node occupies
// exactly one tray in one slot of one chassis.
type PlatformConfig struct {
	// ChassisSerialNumber identifies the chassis (the rack). Rendered as a
	// string, so real values are the 13-digit serial read from the backplane
	// EEPROM. Truncated to 15 characters plus a terminator.
	ChassisSerialNumber string `json:"chassis_serial_number,omitempty"`
	// SlotNumber is the absolute physical slot in the chassis, counting
	// switch trays as well as compute trays (1-27 on NVL72).
	SlotNumber uint8 `json:"slot_number,omitzero"`
	// TrayIndex is the position of this tray among compute trays only, so it
	// runs below SlotNumber on a rack whose switch trays sit in between
	// (1-18 on NVL72).
	TrayIndex uint8 `json:"tray_index,omitzero"`
	// HostID identifies the OS domain within the tray — this node.
	HostID uint8 `json:"host_id,omitzero"`
	// PeerType is how this GPU reaches its NVLink peers:
	// "switch_connected" through an NVSwitch tray, or "direct_connected".
	// Empty defaults to direct. See PeerType* constants.
	PeerType string `json:"peer_type,omitempty"`
	// ModuleID is the id of this GPU within the node, and the one field a
	// profile is expected to vary per device.
	ModuleID uint8 `json:"module_id,omitzero"`
}

// NVLinkErrorInjectionConfig injects NVLink data-link error accrual on a
// device's links so the GPU's uplinks to its NVSwitch report climbing DL
// errors — the closest an NVML-only mock can get to an NVSwitch-side fault.
//
// It exists because DCGM's NVSwitch entity health (DCGM_HEALTH_WATCH_NVSWITCH_*)
// is sourced from NSCQ, not NVML, so a libnvidia-ml mock cannot drive it. What
// the mock CAN drive is the GPU-side NVLink error surface DCGM's
// DCGM_HEALTH_WATCH_NVLINK reads: the per-link/per-counter direct API
// (nvmlDeviceGetNvLinkErrorCounter) and the DL error field values
// (NVML_FI_DEV_NVLINK_ERROR_DL_{REPLAY,RECOVERY,CRC}, field ids 161-163).
// A rising error rate there surfaces as DCGM_FR_NVLINK_* and is detected and
// remediated by NVSentinel's gpu-health-monitor.
type NVLinkErrorInjectionConfig struct {
	// Rate is the injected error accrual in errors/second, added on top of
	// each affected link's baseline. The counter climbs monotonically off the
	// shared epoch (the same accrual model as NVLinkDefaults.ErrorRate), so
	// DCGM's delta-based NVLink health watch observes a rising error rate
	// rather than a one-shot step it would treat as stale after the first
	// sample. 0 (default) disables injection — the healthy baseline.
	Rate float64 `json:"rate,omitzero"`

	// Links restricts injection to specific link ids. Empty (default) injects
	// on every active link on the device — the "GPU lost its switch uplinks"
	// fault. Ids that don't map to an active link are ignored.
	Links []int `json:"links,omitempty"`
}

// DeviceOverride contains per-device settings that override defaults
type DeviceOverride struct {
	Index int    `json:"index"`
	UUID  string `json:"uuid,omitempty"`
	// MinorNumber is the /dev/nvidia<N> the driver would have created. A
	// pointer because minor 0 is a legal value on a device that is not index
	// 0, so an omitted key has to stay distinguishable from an explicit zero.
	MinorNumber  *int             `json:"minor_number,omitempty"`
	GraceCPUPair int              `json:"grace_cpu_pair,omitzero"`
	DeviceConfig `json:",inline"` // Embed all device config fields
}

// ComputeCapabilityConfig defines CUDA compute capability
type ComputeCapabilityConfig struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// InfoROMConfig defines InfoROM version information
type InfoROMConfig struct {
	ImageVersion string `json:"image_version,omitempty"`
	OEMObject    string `json:"oem_object,omitempty"`
	ECCObject    string `json:"ecc_object,omitempty"`
	PWRObject    string `json:"pwr_object,omitempty"`
}

// MemoryConfig defines GPU memory settings
type MemoryConfig struct {
	TotalBytes     uint64 `json:"total_bytes"`
	ReservedBytes  uint64 `json:"reserved_bytes,omitzero"`
	FreeBytes      uint64 `json:"free_bytes,omitzero"`
	UsedBytes      uint64 `json:"used_bytes,omitzero"`
	MemoryBusWidth uint32 `json:"memory_bus_width,omitzero"` // bits (e.g., 5120 for A100)
}

// BAR1MemoryConfig defines BAR1 aperture settings
type BAR1MemoryConfig struct {
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes,omitzero"`
	UsedBytes  uint64 `json:"used_bytes,omitzero"`
}

// PCIConfig defines PCI device information
type PCIConfig struct {
	DeviceID    uint32 `json:"device_id,omitzero"`
	SubsystemID uint32 `json:"subsystem_id,omitzero"`
	BusID       string `json:"bus_id,omitempty"`
}

// PCIeConfig defines PCIe link information
type PCIeConfig struct {
	MaxLinkGen       int    `json:"max_link_gen,omitzero"`
	CurrentLinkGen   int    `json:"current_link_gen,omitzero"`
	MaxLinkWidth     int    `json:"max_link_width,omitzero"`
	CurrentLinkWidth int    `json:"current_link_width,omitzero"`
	ReplayCounter    uint64 `json:"replay_counter,omitzero"`
	TxThroughputKBPS uint64 `json:"tx_throughput_kbps,omitzero"`
	RxThroughputKBPS uint64 `json:"rx_throughput_kbps,omitzero"`
}

// PowerConfig defines power management settings
type PowerConfig struct {
	ManagementSupported      bool   `json:"management_supported,omitzero"`
	ManagementMode           string `json:"management_mode,omitempty"`
	DefaultLimitMW           uint32 `json:"default_limit_mw,omitzero"`
	EnforcedLimitMW          uint32 `json:"enforced_limit_mw,omitzero"`
	MinLimitMW               uint32 `json:"min_limit_mw,omitzero"`
	MaxLimitMW               uint32 `json:"max_limit_mw,omitzero"`
	CurrentDrawMW            uint32 `json:"current_draw_mw,omitzero"`
	PowerState               string `json:"power_state,omitempty"`
	TotalEnergyConsumptionMJ uint64 `json:"total_energy_consumption_mj,omitzero"` // millijoules since boot

	// WorkloadProfiles opts a device into the Blackwell workload power
	// profile feature. Absent means the device declines it, which is what
	// every pre-Blackwell part does.
	WorkloadProfiles *WorkloadPowerProfilesConfig `json:"workload_power_profiles,omitempty"`
}

// WorkloadPowerProfilesConfig models the pre-tuned performance/power recipes
// Blackwell exposes through `nvidia-smi power-profiles`.
type WorkloadPowerProfilesConfig struct {
	// Supported lists the profiles the device advertises. An empty list is
	// not the same as an absent WorkloadProfiles: it models a device that
	// supports the feature but exposes no profile.
	Supported []WorkloadPowerProfileConfig `json:"supported,omitempty"`

	// Requested holds the profile ids asked for. Real hardware with nothing
	// requested reports N/A for both the requested and the enforced mask, so
	// this defaults to empty rather than preselecting a profile.
	Requested []uint32 `json:"requested,omitempty"`
}

// WorkloadPowerProfileConfig is one advertised profile.
type WorkloadPowerProfileConfig struct {
	// ID is an NVML_POWER_PROFILE_* index (0-254), which is both the
	// semantic name nvidia-smi renders and the profile's bit position.
	ID uint32 `json:"id"`

	// Priority arbitrates between conflicting requested profiles. Lower
	// wins, matching NVML's "the lower the value, the higher the priority".
	Priority uint32 `json:"priority,omitzero"`

	// Conflicts lists profile ids that cannot be enforced alongside this
	// one.
	Conflicts []uint32 `json:"conflicts,omitempty"`
}

// ThermalConfig defines thermal settings
type ThermalConfig struct {
	TemperatureGPU_C    int `json:"temperature_gpu_c,omitzero"`
	TemperatureMemory_C int `json:"temperature_memory_c,omitzero"`
	ShutdownThreshold_C int `json:"shutdown_threshold_c,omitzero"`
	SlowdownThreshold_C int `json:"slowdown_threshold_c,omitzero"`
	MaxOperating_C      int `json:"max_operating_c,omitzero"`
	TargetTemperature_C int `json:"target_temperature_c,omitzero"`
}

// FanConfig defines fan settings
type FanConfig struct {
	Count              int    `json:"count,omitzero"`
	SpeedPercent       string `json:"speed_percent,omitempty"`
	TargetSpeedPercent string `json:"target_speed_percent,omitempty"`
}

// ClocksConfig defines clock speed settings
type ClocksConfig struct {
	GraphicsCurrent    uint32 `json:"graphics_current,omitzero"`
	GraphicsMax        uint32 `json:"graphics_max,omitzero"`
	GraphicsApp        uint32 `json:"graphics_app,omitzero"`
	GraphicsAppDefault uint32 `json:"graphics_app_default,omitzero"`
	SMCurrent          uint32 `json:"sm_current,omitzero"`
	SMMax              uint32 `json:"sm_max,omitzero"`
	MemoryCurrent      uint32 `json:"memory_current,omitzero"`
	MemoryMax          uint32 `json:"memory_max,omitzero"`
	MemoryApp          uint32 `json:"memory_app,omitzero"`
	MemoryAppDefault   uint32 `json:"memory_app_default,omitzero"`
	VideoCurrent       uint32 `json:"video_current,omitzero"`
	VideoMax           uint32 `json:"video_max,omitzero"`
}

// ClocksThrottleReasonsConfig defines throttle reason flags
type ClocksThrottleReasonsConfig struct {
	GPUIdle                   bool `json:"gpu_idle,omitzero"`
	ApplicationsClocksSetting bool `json:"applications_clocks_setting,omitzero"`
	SWPowerCap                bool `json:"sw_power_cap,omitzero"`
	HWSlowdown                bool `json:"hw_slowdown,omitzero"`
	HWThermalSlowdown         bool `json:"hw_thermal_slowdown,omitzero"`
	HWPowerBrakeSlowdown      bool `json:"hw_power_brake_slowdown,omitzero"`
	SyncBoost                 bool `json:"sync_boost,omitzero"`
	SWThermalSlowdown         bool `json:"sw_thermal_slowdown,omitzero"`
	DisplayClocksSetting      bool `json:"display_clocks_setting,omitzero"`

	// Counters seeds the cumulative time each cause has already cost the GPU.
	// The flags above answer "is it throttled now"; the counters answer "how
	// long has it been", which is what a slow workload is diagnosed from.
	Counters *ThrottleCountersConfig `json:"counters,omitempty"`
}

// ThrottleCountersConfig is the starting value of each clocks-event counter, in
// microseconds — the unit nvidia-smi prints them in and the unit a reading off
// a real tray is copied from. A device accrues further time on top of these
// while the matching flag above is set, but only within one process, so a
// throttle state entered at runtime carries no history of its own: the baseline
// here is what makes an Active flag read as a duration rather than as the age of
// the calling process. Omitting the block means a GPU that has never been
// throttled, which reports 0 rather than N/A.
type ThrottleCountersConfig struct {
	SWPowerCapUS           uint64 `json:"sw_power_cap_us,omitzero"`
	SyncBoostUS            uint64 `json:"sync_boost_us,omitzero"`
	SWThermalSlowdownUS    uint64 `json:"sw_thermal_slowdown_us,omitzero"`
	HWThermalSlowdownUS    uint64 `json:"hw_thermal_slowdown_us,omitzero"`
	HWPowerBrakeSlowdownUS uint64 `json:"hw_power_brake_slowdown_us,omitzero"`
}

// SupportedClocksConfig defines supported clock frequencies
type SupportedClocksConfig struct {
	MemoryClocks []MemoryClockConfig `json:"memory_clocks,omitempty"`
}

// MemoryClockConfig defines a memory clock with associated graphics clocks
type MemoryClockConfig struct {
	FreqMHz        uint32   `json:"freq_mhz"`
	GraphicsClocks []uint32 `json:"graphics_clocks"`
}

// UtilizationConfig defines utilization percentages
type UtilizationConfig struct {
	GPU     uint32 `json:"gpu,omitzero"`
	Memory  uint32 `json:"memory,omitzero"`
	Encoder uint32 `json:"encoder,omitzero"`
	Decoder uint32 `json:"decoder,omitzero"`
	JPEG    uint32 `json:"jpeg,omitzero"`
	OFA     uint32 `json:"ofa,omitzero"`
}

// EncoderStatsConfig defines encoder statistics
type EncoderStatsConfig struct {
	SessionCount     uint32 `json:"session_count,omitzero"`
	AverageFPS       uint32 `json:"average_fps,omitzero"`
	AverageLatencyUS uint32 `json:"average_latency_us,omitzero"`
}

// FBCStatsConfig defines frame buffer capture statistics
type FBCStatsConfig struct {
	SessionCount     uint32 `json:"session_count,omitzero"`
	AverageFPS       uint32 `json:"average_fps,omitzero"`
	AverageLatencyUS uint32 `json:"average_latency_us,omitzero"`
}

// ECCConfig defines ECC memory configuration
type ECCConfig struct {
	ModeCurrent string           `json:"mode_current,omitempty"`
	ModePending string           `json:"mode_pending,omitempty"`
	DefaultMode string           `json:"default_mode,omitempty"`
	Errors      *ECCErrorsConfig `json:"errors,omitempty"`
	SRAM        *ECCSramConfig   `json:"sram,omitempty"`
}

// ECCSramConfig defines the on-die SRAM ECC error state. Hardware counts SRAM
// errors separately from the DRAM counters in Errors and reports them through
// their own API (nvmlDeviceGetSramEccErrorStatus), which is why they are a
// sibling block rather than another memory location under errors.
type ECCSramConfig struct {
	Volatile  *ECCSramCountsConfig `json:"volatile,omitempty"`
	Aggregate *ECCSramCountsConfig `json:"aggregate,omitempty"`
	// UncorrectableSources attributes the aggregate uncorrectable errors to the
	// unit that reported them; nvidia-smi renders it as "Aggregate
	// Uncorrectable SRAM Sources".
	UncorrectableSources *ECCSramSourcesConfig `json:"uncorrectable_sources,omitempty"`
	// ThresholdExceeded reports whether the accumulated SRAM errors have passed
	// the driver's threshold — the signal that the GPU needs servicing rather
	// than just a count that went up.
	ThresholdExceeded bool `json:"threshold_exceeded,omitzero"`
}

// ECCSramCountsConfig defines the SRAM error counts for one scope (volatile,
// reset at driver reload, or aggregate, persisted in the InfoROM).
type ECCSramCountsConfig struct {
	Correctable         uint64 `json:"correctable,omitzero"`
	UncorrectableParity uint64 `json:"uncorrectable_parity,omitzero"`
	UncorrectableSECDED uint64 `json:"uncorrectable_secded,omitzero"`
}

// ECCSramSourcesConfig defines the per-unit breakdown of aggregate
// uncorrectable SRAM errors.
type ECCSramSourcesConfig struct {
	L2              uint64 `json:"l2,omitzero"`
	SM              uint64 `json:"sm,omitzero"`
	Microcontroller uint64 `json:"microcontroller,omitzero"`
	PCIe            uint64 `json:"pcie,omitzero"`
	Other           uint64 `json:"other,omitzero"`
}

// ECCErrorsConfig defines ECC error counts
type ECCErrorsConfig struct {
	Volatile  *ECCErrorCountsConfig `json:"volatile,omitempty"`
	Aggregate *ECCErrorCountsConfig `json:"aggregate,omitempty"`
}

// ECCErrorCountsConfig defines single/double bit error counts
type ECCErrorCountsConfig struct {
	SingleBit *ECCMemoryErrorsConfig `json:"single_bit,omitempty"`
	DoubleBit *ECCMemoryErrorsConfig `json:"double_bit,omitempty"`
}

// ECCMemoryErrorsConfig defines per-memory-location error counts
type ECCMemoryErrorsConfig struct {
	DeviceMemory  uint64 `json:"device_memory,omitzero"`
	L1Cache       uint64 `json:"l1_cache,omitzero"`
	L2Cache       uint64 `json:"l2_cache,omitzero"`
	RegisterFile  uint64 `json:"register_file,omitzero"`
	TextureMemory uint64 `json:"texture_memory,omitzero"`
	Total         uint64 `json:"total,omitzero"`
}

// RetiredPagesConfig defines retired pages information
type RetiredPagesConfig struct {
	SingleBitRetirement *RetirementInfoConfig `json:"single_bit_retirement,omitempty"`
	DoubleBitRetirement *RetirementInfoConfig `json:"double_bit_retirement,omitempty"`
	PendingBlacklist    bool                  `json:"pending_blacklist,omitzero"`
	PendingRetirement   bool                  `json:"pending_retirement,omitzero"`
}

// RetirementInfoConfig defines retirement count and addresses
type RetirementInfoConfig struct {
	Count     int      `json:"count,omitzero"`
	Addresses []string `json:"addresses,omitempty"`
}

// RemappedRowsConfig defines remapped rows information
type RemappedRowsConfig struct {
	Correctable           int                      `json:"correctable,omitzero"`
	Uncorrectable         int                      `json:"uncorrectable,omitzero"`
	Pending               bool                     `json:"pending,omitzero"`
	FailureOccurred       bool                     `json:"failure_occurred,omitzero"`
	AvailabilityHistogram *RowRemapHistogramConfig `json:"availability_histogram,omitempty"`
}

// RowRemapHistogramConfig defines how many memory banks fall into each
// row-remap availability bucket, i.e. how much spare capacity is left to
// remap future failures. Row remapping is Ampere and later, so leaving this
// unset makes the mock report the feature as unsupported.
type RowRemapHistogramConfig struct {
	Max     uint32 `json:"max,omitzero"`
	High    uint32 `json:"high,omitzero"`
	Partial uint32 `json:"partial,omitzero"`
	Low     uint32 `json:"low,omitzero"`
	None    uint32 `json:"none,omitzero"`
}

// DisplayConfig defines display output settings
type DisplayConfig struct {
	Mode   string `json:"mode,omitempty"`
	Active string `json:"active,omitempty"`
}

// MIGConfig defines MIG configuration.
//
// GPUInstances declares the partitioning the device boots with. It is only
// honoured while mode_current is "enabled", so a profile can carry the layout
// its board would normally be partitioned into and leave MIG off. The declared
// layout seeds in-memory state that NVML callers can then add to and destroy,
// the same way nvidia-mig-parted would on real hardware.
type MIGConfig struct {
	ModeCurrent     string `json:"mode_current,omitempty"`
	ModePending     string `json:"mode_pending,omitempty"`
	MaxGPUInstances int    `json:"max_gpu_instances,omitzero"`
	// SupportedProfiles is the board's MIG profile table: the rows
	// `nvidia-smi mig -lgip` prints. It is declared here rather than derived
	// in Go so that teaching the mock a new board is a YAML edit. A board
	// declaring none is not MIG-capable, which is how l40s and t4 report
	// ERROR_NOT_SUPPORTED.
	SupportedProfiles []MIGProfileSpec       `json:"supported_profiles,omitempty"`
	GPUInstances      []MIGGPUInstanceConfig `json:"gpu_instances,omitempty"`
	// Instances is the explicit layout: exactly which GPU instances exist,
	// with the IDs and placements they were created under. It is what a
	// runtime mutation through NVML records, because a count cannot express
	// a layout with a hole in it — delete instance 1 of three and the
	// survivors are 0 and 2, which "count: 2" would reload as 0 and 1.
	//
	// A pointer because absent and present-but-empty differ: an empty list is
	// a MIG-enabled board with every instance deleted, and must not fall back
	// to GPUInstances.
	Instances *[]MIGGPUInstanceRecord `json:"instances,omitempty"`
}

// MIGGPUInstanceConfig declares one or more identical GPU instances.
//
// The profile is named the way the cluster names it — "1g.10gb", "2g.20gb",
// "1g.5gb+me" — so that what a profile declares reads the same as the
// nvidia.com/mig-<profile> resource the device plugin ends up publishing.
// ProfileID is the escape hatch for a raw NVML profile ID; exactly one of the
// two must be set.
type MIGGPUInstanceConfig struct {
	Profile   string `json:"profile,omitempty"`
	ProfileID *int   `json:"profile_id,omitempty"`
	Count     int    `json:"count,omitzero"`
	// ComputeInstances defaults to a single instance spanning the whole GPU
	// instance, which is the only partitioning most consumers ask for and
	// what nvidia-mig-parted creates when a profile names no compute slices.
	ComputeInstances []MIGComputeInstanceConfig `json:"compute_instances,omitempty"`
}

// MIGComputeInstanceConfig declares one or more identical compute instances
// inside a GPU instance. The profile is the compute-slice spelling NVML uses,
// e.g. "1c" for a single slice; ProfileID takes a raw NVML profile ID.
type MIGComputeInstanceConfig struct {
	Profile   string `json:"profile,omitempty"`
	ProfileID *int   `json:"profile_id,omitempty"`
	Count     int    `json:"count,omitzero"`
}

// MIGGPUInstanceRecord is one GPU instance that exists, as opposed to
// MIGGPUInstanceConfig which declares how many of a shape to create.
//
// PlacementStart pins the instance to a slice offset. It is optional: an
// omitted placement lets the engine choose the first free slot, which is what
// a layout hand-written for a test usually wants.
type MIGGPUInstanceRecord struct {
	ID             uint32 `json:"id"`
	Profile        string `json:"profile,omitempty"`
	ProfileID      *int   `json:"profile_id,omitempty"`
	PlacementStart *int   `json:"placement_start,omitempty"`
	// ComputeInstances is a pointer for the same reason MIGConfig.Instances
	// is: a GPU instance with no compute instances is a state hardware has —
	// `nvidia-smi mig -cgi` without -C creates one, and deleting the last
	// compute instance leaves one — so an empty list must not read as
	// unspecified and be handed the spanning default back.
	ComputeInstances *[]MIGComputeInstanceRecord `json:"compute_instances,omitempty"`
}

// MIGComputeInstanceRecord is one compute instance that exists inside a GPU
// instance.
type MIGComputeInstanceRecord struct {
	ID        uint32 `json:"id"`
	Profile   string `json:"profile,omitempty"`
	ProfileID *int   `json:"profile_id,omitempty"`
}

// GPUOperationModeConfig defines GOM settings
type GPUOperationModeConfig struct {
	Current string `json:"current,omitempty"`
	Pending string `json:"pending,omitempty"`
}

// DriverModelConfig defines driver model (Windows)
type DriverModelConfig struct {
	Current string `json:"current,omitempty"`
	Pending string `json:"pending,omitempty"`
}

// AccountingConfig defines accounting mode settings
type AccountingConfig struct {
	Mode       string `json:"mode,omitempty"`
	BufferSize int    `json:"buffer_size,omitzero"`
}

// VirtualizationConfig defines virtualization settings
type VirtualizationConfig struct {
	Mode         string `json:"mode,omitempty"`
	HostVGPUMode string `json:"host_vgpu_mode,omitempty"`
}

// GSPFirmwareConfig defines GSP firmware settings
type GSPFirmwareConfig struct {
	Mode    string `json:"mode,omitempty"`
	Version string `json:"version,omitempty"`
}

// FeaturesConfig defines GPU-specific features (like Blackwell features).
//
// Every field here is descriptive metadata that no getter reads. A key only
// belongs in this block while nothing depends on it: confidential_compute was
// removed once the Conf Compute memory getters landed, because real NVML answers
// those on any part and gating them on a capability claim would have made a T4
// report N/A where a real one reports 0 MiB. See issue #711.
type FeaturesConfig struct {
	TransformerEngine   bool `json:"transformer_engine,omitzero"`
	FP4Support          bool `json:"fp4_support,omitzero"`
	FP8Support          bool `json:"fp8_support,omitzero"`
	NVLinkC2C           bool `json:"nvlink_c2c,omitzero"`
	DecompressionEngine bool `json:"decompression_engine,omitzero"`
	FifthGenTensorCores bool `json:"fifth_gen_tensor_cores,omitzero"`
}

// CPUConfig describes the host CPU the board is attached to. Type is free-form
// and names the CPU family — "grace" on the superchip profiles. An absent block
// means the profile makes no claim about its host, which is the honest reading
// for a PCIe or SXM part that can ship behind any CPU.
//
// Descriptive metadata that no getter reads, on the same terms as FeaturesConfig.
type CPUConfig struct {
	Type           string `json:"type,omitempty"`
	Cores          int    `json:"cores,omitzero"`
	MemoryGB       int    `json:"memory_gb,omitzero"`
	CoherentMemory bool   `json:"coherent_memory,omitzero"`
}

// ProcessConfig defines a running process
type ProcessConfig struct {
	PID           uint32 `json:"pid"`
	Type          string `json:"type,omitempty"` // "C" for compute, "G" for graphics
	Name          string `json:"name,omitempty"`
	UsedMemoryMiB uint64 `json:"used_memory_mib,omitzero"`
	SmUtil        uint32 `json:"sm_util,omitzero"`  // SM utilization %
	MemUtil       uint32 `json:"mem_util,omitzero"` // memory-bandwidth utilization %
	EncUtil       uint32 `json:"enc_util,omitzero"` // encoder utilization %
	DecUtil       uint32 `json:"dec_util,omitzero"` // decoder utilization %
}

// TopologyConfig defines GPU topology settings
type TopologyConfig struct {
	DefaultLevel string `json:"default_level,omitempty"` // internal, single, multiple, hostbridge, node, system
}

// DynamicMetricsConfig enables opt-in time-varying simulation for GPU
// metrics that, on real hardware, change between calls (temperature, power,
// utilization). When this config is absent, static values from the Thermal,
// Power, and Utilization sections are returned unchanged.
type DynamicMetricsConfig struct {
	// Seed seeds the per-device PRNG. Zero means use a time-based seed so
	// each process sees different but repeatable-within-run values.
	Seed int64 `json:"seed,omitzero"`

	// Temperature, Power, Utilization are each independently opt-in. Any
	// sub-config left nil keeps the corresponding metric static.
	Temperature *DynamicTemperatureConfig `json:"temperature,omitempty"`
	Power       *DynamicPowerConfig       `json:"power,omitempty"`
	Utilization *DynamicUtilizationConfig `json:"utilization,omitempty"`
}

// DynamicTemperatureConfig produces GPU temperatures that fluctuate over time.
//
// Returned value (clamped to the thermal shutdown threshold if known):
//
//	base_c + ramp_offset(t) + noise
//
// where ramp_offset oscillates in [0, ramp_c] with period ramp_period_sec
// (a sine wave shifted to be non-negative), and noise is uniform in
// [-variance_c, +variance_c].
type DynamicTemperatureConfig struct {
	BaseC         int `json:"base_c"`
	VarianceC     int `json:"variance_c,omitzero"`
	RampC         int `json:"ramp_c,omitzero"`
	RampPeriodSec int `json:"ramp_period_sec,omitzero"`
}

// DynamicPowerConfig produces power draw values (milliwatts) that fluctuate
// around base_mw by at most variance_mw. The result is clamped to
// [min_limit_mw, max_limit_mw] from PowerConfig when those bounds are set.
type DynamicPowerConfig struct {
	BaseMW     uint32 `json:"base_mw"`
	VarianceMW uint32 `json:"variance_mw,omitzero"`
}

// DynamicUtilizationConfig drives GPU / memory utilization percentages.
//
// Pattern semantics (all values clamped to 0..100):
//   - "idle":   random in [gpu_min, gpu_min + (gpu_max-gpu_min)/4]
//   - "busy":   random in [gpu_max - (gpu_max-gpu_min)/4, gpu_max]
//   - "burst":  alternates "idle" and "busy" phases every burst_period_sec
//   - "steady" or empty: random in [gpu_min, gpu_max]
//
// Memory utilization follows the same rule using memory_min / memory_max.
type DynamicUtilizationConfig struct {
	Pattern        string `json:"pattern,omitempty"`
	GPUMin         uint32 `json:"gpu_min,omitzero"`
	GPUMax         uint32 `json:"gpu_max,omitzero"`
	MemoryMin      uint32 `json:"memory_min,omitzero"`
	MemoryMax      uint32 `json:"memory_max,omitzero"`
	BurstPeriodSec int    `json:"burst_period_sec,omitzero"`
}

// GPMConfig tunes the GPM (GPU Performance Monitoring) profiling surface
// DCGM's profiling module reads (DCGM_FI_PROF_*, dcgmi dmon -e 1001..).
type GPMConfig struct {
	// Supported overrides architecture-based GPM support detection
	// (default: supported on Hopper and newer, matching real NVML).
	Supported *bool `json:"supported,omitempty"`
	// Full-utilization PCIe rates for the PCIE_TX/RX_PER_SEC metrics, in
	// MiB/s. The reported rate is scaled by current GPU utilization.
	// Default: 2048 MiB/s each direction.
	PcieTxMiBPerSec uint64 `json:"pcie_tx_mib_per_sec,omitzero"`
	PcieRxMiBPerSec uint64 `json:"pcie_rx_mib_per_sec,omitzero"`
}

// Failure mode constants used by FailureInjectionConfig.Mode. Anything else
// (including the empty string) is treated as "healthy" — i.e. failure
// injection disabled for the device.
const (
	FailureModeHealthy          = "healthy"
	FailureModeLost             = "lost"
	FailureModeFallenOffBus     = "fallen_off_bus"
	FailureModeECCUncorrectable = "ecc_uncorrectable"
)

// FailureInjectionConfig models GPU failure injection for a device. It is
// disabled by default. When Mode is one of FailureMode* constants the
// device starts (or transitions to) the failed state subject to the
// stochastic and deterministic triggers below.
//
// Trigger semantics:
//   - When AfterCalls > 0 the failure activates as soon as the device has
//     received that many guarded NVML calls. This makes failure timing
//     reproducible in CI without depending on wall-clock time.
//   - When Probability > 0 every guarded call rolls a uniform sample in
//     [0, 1); if it lands below Probability the failure activates.
//   - When neither AfterCalls nor Probability are set, the failure is
//     active immediately (so consumers can pin a "permanently lost"
//     device just by setting Mode).
//
// Once a device has tripped it stays tripped — real lost / fallen-off-bus
// GPUs do not recover until the box is rebooted, and ECC errors only
// accumulate. Use the Xid sub-config to pair the failure with a Xid code
// returned via Device.GetViolationStatus.
type FailureInjectionConfig struct {
	// Mode selects the failure flavour. See FailureMode* constants.
	Mode string `json:"mode,omitempty"`

	// Probability of activating per guarded call, in [0, 1].
	Probability float64 `json:"probability,omitzero"`

	// AfterCalls activates the failure once this many guarded calls have
	// been observed (deterministic). Combine with Probability if you want
	// "may fail before, will fail by".
	AfterCalls int64 `json:"after_calls,omitzero"`

	// Seed seeds the per-device PRNG used for Probability rolls. Zero
	// means "derive from time" (similar to DynamicMetricsConfig.Seed).
	Seed int64 `json:"seed,omitzero"`

	// Xid optionally injects a Xid error code visible via
	// Device.GetViolationStatus once the failure has tripped.
	Xid *XidErrorConfig `json:"xid,omitempty"`
}

// XidErrorConfig models a Xid critical error to surface when a failure
// trips. Code matches the kernel-driver Xid number (e.g. 79 = "GPU has
// fallen off the bus", 64 = "ECC double-bit error").
type XidErrorConfig struct {
	Code uint64 `json:"code,omitzero"`
}

// FabricConfig models the per-GPU NVLink fabric attributes that
// nvmlDeviceGetGpuFabricInfo / nvmlDeviceGetGpuFabricInfoV expose for
// fabric-attached GPUs (Hopper+ / GB200 / GB300). All GPUs on the same
// physical node share the same fabric config — the NVLink domain is a
// node-level property.
//
// State strings map to the NVML_GPU_FABRIC_STATE_* enum (see fabric.go
// FabricState* constants and parseFabricState):
//   - "not_supported" -> 0
//   - "not_started"   -> 1
//   - "in_progress"   -> 2
//   - "completed"     -> 3
//   - "auto"          -> coupled to the fabricmanager readiness marker
//     (resolves to in_progress/completed; see resolveFabricState)
//
// ClusterUUID is parsed as RFC-4122-style hex; non-hex characters are
// dropped and the buffer is zero-padded to 16 bytes so the YAML can
// carry either bare hex or dashed UUID form.
type FabricConfig struct {
	// ClusterUUID identifies the NVLink fabric (16-byte UUID). Accepts
	// dashed form ("00000000-0000-0000-0000-000000000001") or bare hex.
	ClusterUUID string `json:"cluster_uuid,omitempty"`
	// CliqueID is the clique within the cluster this GPU belongs to.
	CliqueID uint32 `json:"clique_id,omitzero"`
	// State is the GPU registration state with the fabric manager.
	// Defaults to "completed" (the healthy steady-state value). The
	// special value "auto" couples the state to the fake fabricmanager's
	// readiness marker when fabricmanager is enabled, and resolves to
	// "completed" when it is disabled (see engine/fabric_readiness.go).
	State string `json:"state,omitempty"`
	// HealthSummary pins the overall fabric health nvidia-smi reports as
	// Fabric.Health.Summary: "healthy", "unhealthy", "limited_capacity"
	// or "not_supported". Empty (or "auto") derives it from the conditions
	// in Health, which is what makes an injected fault move the summary.
	HealthSummary string `json:"health_summary,omitempty"`
	// Health carries the individual fabric health conditions. Absent means
	// an all-clear fabric.
	Health *FabricHealthConfig `json:"health,omitempty"`
	// HealthMask is an escape hatch for the raw v2/v3 health bitmask,
	// for encodings Health cannot express. When set it replaces the mask
	// derived from Health wholesale, and the summary is derived from it.
	// A raw 0 means "the driver reported no health at all", which
	// nvidia-smi renders as N/A for the whole Health block.
	HealthMask *uint32 `json:"health_mask,omitempty"`
}

// FabricHealthConfig models the individual NVLink fabric health conditions
// nvidia-smi renders under Fabric.Health, each mapping to one slot of the NVML
// health bitmask (see engine/fabric_health.go). Every condition defaults to
// its healthy value, so an absent block describes a healthy fabric rather than
// an unknown one.
//
// PartitionAssigned is deliberately absent: real hardware leaves that field
// unanswered (nvidia-smi renders it as N/A even on a healthy rack), so the
// mock reports the same. Use HealthMask to encode it.
type FabricHealthConfig struct {
	// DegradedBandwidth reports the fabric attachment as running below
	// full bandwidth (nvidia-smi Bandwidth: Degraded rather than Full).
	// On its own it summarises as limited capacity, not unhealthy.
	DegradedBandwidth bool `json:"degraded_bandwidth,omitzero"`
	// RouteRecovery reports a route recovery in progress.
	RouteRecovery bool `json:"route_recovery,omitzero"`
	// RouteUnhealthy reports the GPU's fabric route as unhealthy.
	RouteUnhealthy bool `json:"route_unhealthy,omitzero"`
	// AccessTimeoutRecovery reports an access-timeout recovery in progress.
	AccessTimeoutRecovery bool `json:"access_timeout_recovery,omitzero"`
	// IncorrectConfiguration names a detected fabric misconfiguration
	// ("no_partition", "insufficient_nvlinks", ...; see
	// FabricIncorrectConfigNames). Empty means none detected.
	IncorrectConfiguration string `json:"incorrect_configuration,omitempty"`
}

// TopologyDocument is the cluster-level ConfigMap that maps individual
// nodes (by Kubernetes node name) to fabric clusters and cliques. It is
// the single source of truth for ComputeDomain topology in the mock.
//
// At LoadConfig() time the engine looks up the current node (via
// NODE_NAME) and, if found in the topology, overrides the per-device
// FabricConfig.ClusterUUID / CliqueID. Nodes absent from the topology
// keep their default fabric config (or report NOT_SUPPORTED when no
// FabricConfig is present).
type TopologyDocument struct {
	Version int              `json:"version"`
	Domains []TopologyDomain `json:"domains"`
}

// TopologyDomain represents one NVLink fabric domain (cluster UUID).
type TopologyDomain struct {
	Name    string           `json:"name,omitempty"`
	UUID    string           `json:"uuid"`
	Cliques []TopologyClique `json:"cliques"`
}

// TopologyClique groups the Kubernetes node names that share a clique
// inside a fabric domain.
type TopologyClique struct {
	ID    uint32   `json:"id"`
	Nodes []string `json:"nodes"`
}

// MIGProfilesDocument is a board's MIG profile table, held in its own file
// rather than inside the profile that describes the board.
//
// SupportedProfiles is the same type the profile declares inline, so a table
// decodes and validates identically wherever it was authored. See
// MIGConfig.SupportedProfiles for what the rows mean.
type MIGProfilesDocument struct {
	Version           int              `json:"version"`
	SupportedProfiles []MIGProfileSpec `json:"supported_profiles"`
}

// NVLinkConfig defines NVLink topology
type NVLinkConfig struct {
	Version     int `json:"version,omitzero"`
	LinksPerGPU int `json:"links_per_gpu,omitzero"`
	// BandwidthPerLinkMbps sets the per-link speed in Mbps (what
	// NVML/`nvidia-smi nvlink -s` reports, GB/s = Mbps/1000). Mbps lets
	// non-integer GB/s rates render exactly — e.g. NVLink5 is 53.125 GB/s,
	// i.e. 53125 Mbps.
	BandwidthPerLinkMbps int  `json:"bandwidth_per_link_mbps,omitzero"`
	C2CEnabled           bool `json:"c2c_enabled,omitzero"`

	// NvleEnabled mirrors nvlink.nvle_enabled: NVLink encryption, reported
	// by nvmlDeviceGetNvLinkInfo and the " NVLE:" row of
	// `nvidia-smi nvlink --info`. Node-level for the same reason C2C is —
	// it is a property of the board, not of one GPU.
	NvleEnabled bool `json:"nvle_enabled,omitempty"`

	// BwMode declares the NVLink Reduced Bandwidth Mode surface. Absent means
	// the board has none, and every bandwidth-mode call answers NOT_SUPPORTED.
	BwMode *NVLinkBwModeConfig `json:"bw_mode,omitempty"`

	// Firmware declares the NVLink firmware versions reported alongside
	// NVLE. Absent reports none, which nvidia-smi renders as "N/A".
	Firmware *NVLinkFirmwareConfig `json:"firmware,omitempty"`
	// Links is the legacy flat link list. It is kept for backward
	// compatibility and is mapped to device index 0 when no DeviceLinks
	// entry exists for that device.
	Links []NVLinkLinkConfig `json:"links,omitempty"`

	// Switches lists the NVSwitch remote endpoints on the node. NVSwitches
	// are modeled purely as NVLink remote endpoints (remote device type
	// SWITCH); the nvmlUnit* chassis API stays stubbed, matching real
	// DGX/HGX GPU nodes.
	Switches []NVSwitchConfig `json:"switches,omitempty"`

	// Defaults carries per-link defaults applied to every resolved link
	// (state, counter accrual). Absent fields fall back to built-in
	// defaults.
	Defaults *NVLinkDefaults `json:"defaults,omitempty"`

	// DeviceLinks holds per-device link sets keyed by device index. When a
	// device has no entry here the legacy flat Links list is used for
	// device 0 only.
	DeviceLinks []DeviceLinksConfig `json:"device_links,omitempty"`
}

// NVLinkBwModeScope names which NVML function family answers for the NVLink
// Reduced Bandwidth Mode.
type NVLinkBwModeScope string

const (
	// NVLinkBwModeScopeSystem is the node-wide pair,
	// nvmlSystemGet/SetNvlinkBwMode, which is what `nvidia-smi nvlink
	// -gBwMode` and -sBwMode call. Hopper boards answer on it.
	NVLinkBwModeScopeSystem NVLinkBwModeScope = "system"
	// NVLinkBwModeScopeDevice is the per-device trio,
	// nvmlDeviceGet/SetNvlinkBwMode and nvmlDeviceGetNvlinkSupportedBwModes,
	// which no nvidia-smi flag reaches. Blackwell boards answer on it and
	// decline the node-wide pair.
	NVLinkBwModeScopeDevice NVLinkBwModeScope = "device"
)

// NVLinkBwModeConfig declares the NVLink Reduced Bandwidth Mode surface.
//
// Mode values are opaque driver indices; the bundled nvidia-smi names them
// 0=FULL, 1=OFF, 2=MIN, 3=HALF, 4=3QUARTER, so a value above 4 would index
// past its name table and render as garbage.
type NVLinkBwModeConfig struct {
	// Scope selects the one NVML function family that answers; the other
	// answers NOT_SUPPORTED. A real board answers one or the other, never
	// both, so this is a choice rather than two flags.
	Scope NVLinkBwModeScope `json:"scope,omitempty"`

	// Supported is the list of modes the board accepts, and what
	// nvmlDeviceGetNvlinkSupportedBwModes reports. Empty means the five modes
	// the bundled nvidia-smi can name.
	Supported []uint8 `json:"supported,omitempty"`

	// Mode is the initial current mode. A pointer so that an explicit 0
	// (FULL) is distinguishable from "unset".
	Mode *uint8 `json:"mode,omitempty"`
}

// NVLinkFirmwareConfig declares the NVLink firmware versions
// nvmlDeviceGetNvLinkInfo reports, which `nvidia-smi nvlink --info` renders
// under "Firmware Version:".
//
// Each value is "major:minor:subMinor", the form nvidia-smi prints, so a line
// read off real hardware transfers to a profile unchanged. An omitted
// component is left out of the table rather than reported as zero, because
// the board either carries that microcontroller or it does not.
//
// The fields are named rather than a free list because the label is chosen by
// the ucodeType index, of which nvml.h defines exactly these five, and
// nvidia-smi exits non-zero on an index it cannot name — an open-ended list
// would let a profile break `--info` outright.
type NVLinkFirmwareConfig struct {
	MSE       string `json:"mse,omitempty"`
	NETIR     string `json:"netir,omitempty"`
	NETIRUPHY string `json:"netir_uphy,omitempty"`
	NETIRCLN  string `json:"netir_cln,omitempty"`
	NETIRDLN  string `json:"netir_dln,omitempty"`
}

// NVSwitchConfig describes a single NVSwitch remote endpoint.
type NVSwitchConfig struct {
	BDF  string `json:"bdf,omitempty"`
	UUID string `json:"uuid,omitempty"`

	// DeviceID is the packed PCI identity word, (device<<16)|vendor, as
	// device_defaults.pci.device_id carries for a GPU — 0x22a310de for an
	// H100 NVSwitch. Setting it is what puts the switch in the node's
	// rendered PCI tree, where lspci enumerates it as a bridge next to the
	// GPUs, the way it appears on an HGX baseboard.
	//
	// Leave it unset for a switch the node cannot see over PCIe: on
	// GB200/GB300 NVL the switches are in their own trays, so the compute
	// tray's lspci shows the GPUs and no bridges. Such a switch still serves
	// as an NVLink remote endpoint.
	DeviceID uint32 `json:"device_id,omitzero"`
	// SubsystemID is the packed subsystem word, (subdevice<<16)|subvendor.
	// Only meaningful alongside DeviceID.
	SubsystemID uint32 `json:"subsystem_id,omitzero"`
}

// NVLinkDefaults carries per-link defaults expanded across all links so
// the NV# matrix can be populated without hand-authoring every entry.
type NVLinkDefaults struct {
	// State is the default link state (active/up/enabled vs anything else).
	State string `json:"state,omitempty"`
	// DutyCycle is the fraction of line rate accrued into the utilization
	// counters (0..1). A small positive value makes counters visibly grow
	// across separate nvidia-smi invocations.
	DutyCycle float64 `json:"duty_cycle,omitzero"`
	// CounterSeed is the baseline value added to every utilization counter.
	CounterSeed uint64 `json:"counter_seed,omitzero"`
	// ErrorRate is the per-second accrual rate for NVLink error counters.
	// Defaults to 0 (healthy links report no errors).
	ErrorRate float64 `json:"error_rate,omitzero"`
}

// DeviceLinksConfig is the per-device NVLink link set.
type DeviceLinksConfig struct {
	Index int                `json:"index"`
	Links []NVLinkLinkConfig `json:"links,omitempty"`
}

// NVLinkLinkConfig defines a single NVLink connection
type NVLinkLinkConfig struct {
	Link             int    `json:"link"`
	State            string `json:"state,omitempty"`
	RemoteDeviceType string `json:"remote_device_type,omitempty"`
	RemotePCIBusID   string `json:"remote_pci_bus_id,omitempty"`
	// RemoteIndex optionally identifies the peer GPU directly by device
	// index. When set it takes precedence over RemotePCIBusID for peer
	// resolution.
	RemoteIndex *int `json:"remote_index,omitempty"`
}

// PCIeTopologyConfig describes the root-complex / NUMA layout consumed by the
// pcibus simulator. CoresPerNUMA synthesizes a CPU affinity set per NUMA node
// when a root complex does not declare an explicit cpu_affinity range.
type PCIeTopologyConfig struct {
	RootComplexes []RootComplexConfig `json:"root_complexes,omitempty"`
	CoresPerNUMA  int                 `json:"cores_per_numa,omitzero"`
}

// RootComplexConfig is one PCI host bridge with its NUMA node, attached
// device BDFs, and an optional explicit CPU affinity range.
type RootComplexConfig struct {
	ID       string   `json:"id,omitempty"`
	NUMANode int      `json:"numa_node,omitzero"`
	Devices  []string `json:"devices,omitempty"`
	// CPUAffinity is an optional inclusive CPU range ("0-71") or comma
	// list ("0,2,4"). When empty the affinity set is synthesized from the
	// NUMA node index and CoresPerNUMA.
	CPUAffinity string `json:"cpu_affinity,omitempty"`
}

// MIGProfileSpec is one row of a board's MIG profile table, transcribed from
// NVIDIA's MIG user guide.
//
// NVMLProfile binds the row to the NVML enum every lookup keys on: the suffix
// of the NVML_GPU_INSTANCE_PROFILE_* constant that
// nvmlDeviceGetGpuInstanceProfileInfo takes as its profile argument. ProfileID
// is the number that call returns, which is why the two are separate keys and
// why neither name is a synonym for the other.
//
// The suffix vocabulary is shared with NVML_COMPUTE_INSTANCE_PROFILE_*, so
// MIGComputeInstanceSpec spells its own binding with the same field name and
// the enclosing block decides which family a value names.
//
// It is named rather than inferred from Name: the +me, +gfx, -me and +me.all
// families make a display name an unreliable key. What
// validateMIGSupportedProfiles refuses is an enum it does not recognise or one
// declared twice; Name stays authoritative for what `nvidia-smi mig -lgip`
// prints and is not reconciled against the enum's span.
//
// A row describes its partition completely: the slices it spans, the id the
// board reports for it, where it may sit, and the compute instances it offers.
// Geometry an algorithm cannot express is geometry the mock cannot mock, so
// none of it is computed from the rest.
type MIGProfileSpec struct {
	Name        string `json:"name"`
	NVMLProfile string `json:"nvml_profile"`
	// Slices is how many compute slices the partition spans. NVMLProfile
	// carries the same fact — gpuInstanceSliceCount answers it for every enum
	// — and validateMIGProfileSpec refuses a row where the two disagree, so
	// neither source is trusted alone. That cross-check is what makes stating
	// the width here a second reading of one fact rather than a second source
	// for it.
	Slices int `json:"slices,omitzero"`
	// ProfileID is the id the board publishes for this profile, which is what
	// nvmlDeviceCreateGpuInstance takes. It is not the NVML profile enum: an
	// A100's 1g.5gb is enum 0 and reports 19.
	//
	// There is no absent state to distinguish, so it is a plain int: 0 is a
	// real reported id — the full-board 7g of an A100 or H100 publishes it —
	// and nothing is derived for a row that omits the key. A row omitting it
	// therefore publishes 0, which validateMIGSupportedProfiles refuses as
	// soon as a second row on the board does the same.
	ProfileID int    `json:"profile_id"`
	Instances int    `json:"instances"`
	MemoryMB  uint64 `json:"memory_mb"`
	// Placements are the slots on the board this profile may occupy.
	Placements []MIGPlacementSpec `json:"placements,omitempty"`
	// ComputeInstances is the profile's `nvidia-smi mig -lcip` listing. A GPU
	// instance does not offer every width that fits inside it — a 7-slice
	// instance offers 3c and 4c and then jumps to 7c — which is why the
	// listing is declared per row rather than enumerated from the width.
	ComputeInstances []MIGComputeInstanceSpec `json:"compute_instances,omitempty"`
	Multiprocessors  int                      `json:"multiprocessors,omitzero"`
	CopyEngines      int                      `json:"copy_engines,omitzero"`
	Decoders         int                      `json:"decoders,omitzero"`
	Encoders         int                      `json:"encoders,omitzero"`
	JPEG             int                      `json:"jpeg,omitzero"`
	OFA              int                      `json:"ofa,omitzero"`
}

// MIGPlacementSpec is one slot a GPU instance profile may occupy on the board.
//
// Start and Size are measured in memory units, not compute slices. A board has
// as many memory units as the next power of two at or above its compute-slice
// count — eight for a 7-slice datacenter board — so a 3g partition holding
// half the memory occupies four of eight units and a 7g partition occupies all
// eight. Reading Size as a slice count is what makes go-nvml's H100 table
// report a 7g placement of size 7.
type MIGPlacementSpec struct {
	Start uint32 `json:"start"`
	Size  uint32 `json:"size"`
}

// MIGComputeInstanceSpec is one row of a GPU instance's compute-instance
// listing.
//
// Multiprocessors is this compute instance's own share of the GPU instance's
// SMs. The fixed-function engines are not divided that way: NVML reports them
// as Shared* because every compute instance inside a GPU instance sees all of
// them, so Decoders through OFA repeat the GPU instance's counts.
type MIGComputeInstanceSpec struct {
	NVMLProfile string `json:"nvml_profile"`
	// Slices is cross-checked against NVMLProfile the same way
	// MIGProfileSpec.Slices is, and additionally bounded by the width of the
	// GPU instance the row sits in.
	Slices            int `json:"slices,omitzero"`
	Instances         int `json:"instances"`
	Multiprocessors   int `json:"multiprocessors,omitzero"`
	SharedCopyEngines int `json:"shared_copy_engines,omitzero"`
	Decoders          int `json:"decoders,omitzero"`
	Encoders          int `json:"encoders,omitzero"`
	JPEG              int `json:"jpeg,omitzero"`
	OFA               int `json:"ofa,omitzero"`
}
