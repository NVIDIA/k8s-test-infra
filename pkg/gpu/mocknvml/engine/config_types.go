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
}

// SystemConfig contains system-level NVML settings
type SystemConfig struct {
	DriverVersion    string `json:"driver_version"`
	NVMLVersion      string `json:"nvml_version"`
	CUDAVersion      string `json:"cuda_version"`
	CUDAVersionMajor int    `json:"cuda_version_major"`
	CUDAVersionMinor int    `json:"cuda_version_minor"`
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
	NumGPUCores       int                      `json:"num_gpu_cores,omitempty"`

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

	// Grace Superchip (GB200)
	GraceSuperchip *GraceSuperchipConfig `json:"grace_superchip,omitempty"`

	// Processes
	Processes []ProcessConfig `json:"processes,omitempty"`
}

// DeviceOverride contains per-device settings that override defaults
type DeviceOverride struct {
	Index        int    `json:"index"`
	UUID         string `json:"uuid,omitempty"`
	MinorNumber  int    `json:"minor_number,omitempty"`
	GraceCPUPair int    `json:"grace_cpu_pair,omitempty"`
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
	TotalBytes    uint64 `json:"total_bytes"`
	ReservedBytes uint64 `json:"reserved_bytes,omitempty"`
	FreeBytes     uint64 `json:"free_bytes,omitempty"`
	UsedBytes     uint64 `json:"used_bytes,omitempty"`
}

// BAR1MemoryConfig defines BAR1 aperture settings
type BAR1MemoryConfig struct {
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes,omitempty"`
	UsedBytes  uint64 `json:"used_bytes,omitempty"`
}

// PCIConfig defines PCI device information
type PCIConfig struct {
	DeviceID    uint32 `json:"device_id,omitempty"`
	SubsystemID uint32 `json:"subsystem_id,omitempty"`
	BusID       string `json:"bus_id,omitempty"`
}

// PCIeConfig defines PCIe link information
type PCIeConfig struct {
	MaxLinkGen       int    `json:"max_link_gen,omitempty"`
	CurrentLinkGen   int    `json:"current_link_gen,omitempty"`
	MaxLinkWidth     int    `json:"max_link_width,omitempty"`
	CurrentLinkWidth int    `json:"current_link_width,omitempty"`
	ReplayCounter    uint64 `json:"replay_counter,omitempty"`
	TxThroughputKBPS uint64 `json:"tx_throughput_kbps,omitempty"`
	RxThroughputKBPS uint64 `json:"rx_throughput_kbps,omitempty"`
}

// PowerConfig defines power management settings
type PowerConfig struct {
	ManagementSupported bool   `json:"management_supported,omitempty"`
	ManagementMode      string `json:"management_mode,omitempty"`
	DefaultLimitMW      uint32 `json:"default_limit_mw,omitempty"`
	EnforcedLimitMW     uint32 `json:"enforced_limit_mw,omitempty"`
	MinLimitMW          uint32 `json:"min_limit_mw,omitempty"`
	MaxLimitMW          uint32 `json:"max_limit_mw,omitempty"`
	CurrentDrawMW       uint32 `json:"current_draw_mw,omitempty"`
	PowerState          string `json:"power_state,omitempty"`
}

// ThermalConfig defines thermal settings
type ThermalConfig struct {
	TemperatureGPU_C    int `json:"temperature_gpu_c,omitempty"`
	TemperatureMemory_C int `json:"temperature_memory_c,omitempty"`
	ShutdownThreshold_C int `json:"shutdown_threshold_c,omitempty"`
	SlowdownThreshold_C int `json:"slowdown_threshold_c,omitempty"`
	MaxOperating_C      int `json:"max_operating_c,omitempty"`
	TargetTemperature_C int `json:"target_temperature_c,omitempty"`
}

// FanConfig defines fan settings
type FanConfig struct {
	Count              int    `json:"count,omitempty"`
	SpeedPercent       string `json:"speed_percent,omitempty"`
	TargetSpeedPercent string `json:"target_speed_percent,omitempty"`
}

// ClocksConfig defines clock speed settings
type ClocksConfig struct {
	GraphicsCurrent    uint32 `json:"graphics_current,omitempty"`
	GraphicsMax        uint32 `json:"graphics_max,omitempty"`
	GraphicsApp        uint32 `json:"graphics_app,omitempty"`
	GraphicsAppDefault uint32 `json:"graphics_app_default,omitempty"`
	SMCurrent          uint32 `json:"sm_current,omitempty"`
	SMMax              uint32 `json:"sm_max,omitempty"`
	MemoryCurrent      uint32 `json:"memory_current,omitempty"`
	MemoryMax          uint32 `json:"memory_max,omitempty"`
	MemoryApp          uint32 `json:"memory_app,omitempty"`
	MemoryAppDefault   uint32 `json:"memory_app_default,omitempty"`
	VideoCurrent       uint32 `json:"video_current,omitempty"`
	VideoMax           uint32 `json:"video_max,omitempty"`
}

// ClocksThrottleReasonsConfig defines throttle reason flags
type ClocksThrottleReasonsConfig struct {
	GPUIdle                   bool `json:"gpu_idle,omitempty"`
	ApplicationsClocksSetting bool `json:"applications_clocks_setting,omitempty"`
	SWPowerCap                bool `json:"sw_power_cap,omitempty"`
	HWSlowdown                bool `json:"hw_slowdown,omitempty"`
	HWThermalSlowdown         bool `json:"hw_thermal_slowdown,omitempty"`
	HWPowerBrakeSlowdown      bool `json:"hw_power_brake_slowdown,omitempty"`
	SyncBoost                 bool `json:"sync_boost,omitempty"`
	SWThermalSlowdown         bool `json:"sw_thermal_slowdown,omitempty"`
	DisplayClocksSetting      bool `json:"display_clocks_setting,omitempty"`
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
	GPU     uint32 `json:"gpu,omitempty"`
	Memory  uint32 `json:"memory,omitempty"`
	Encoder uint32 `json:"encoder,omitempty"`
	Decoder uint32 `json:"decoder,omitempty"`
	JPEG    uint32 `json:"jpeg,omitempty"`
	OFA     uint32 `json:"ofa,omitempty"`
}

// EncoderStatsConfig defines encoder statistics
type EncoderStatsConfig struct {
	SessionCount     uint32 `json:"session_count,omitempty"`
	AverageFPS       uint32 `json:"average_fps,omitempty"`
	AverageLatencyUS uint32 `json:"average_latency_us,omitempty"`
}

// FBCStatsConfig defines frame buffer capture statistics
type FBCStatsConfig struct {
	SessionCount     uint32 `json:"session_count,omitempty"`
	AverageFPS       uint32 `json:"average_fps,omitempty"`
	AverageLatencyUS uint32 `json:"average_latency_us,omitempty"`
}

// ECCConfig defines ECC memory configuration
type ECCConfig struct {
	ModeCurrent string           `json:"mode_current,omitempty"`
	ModePending string           `json:"mode_pending,omitempty"`
	DefaultMode string           `json:"default_mode,omitempty"`
	Errors      *ECCErrorsConfig `json:"errors,omitempty"`
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
	DeviceMemory  uint64 `json:"device_memory,omitempty"`
	L1Cache       uint64 `json:"l1_cache,omitempty"`
	L2Cache       uint64 `json:"l2_cache,omitempty"`
	RegisterFile  uint64 `json:"register_file,omitempty"`
	TextureMemory uint64 `json:"texture_memory,omitempty"`
	Total         uint64 `json:"total,omitempty"`
}

// RetiredPagesConfig defines retired pages information
type RetiredPagesConfig struct {
	SingleBitRetirement *RetirementInfoConfig `json:"single_bit_retirement,omitempty"`
	DoubleBitRetirement *RetirementInfoConfig `json:"double_bit_retirement,omitempty"`
	PendingBlacklist    bool                  `json:"pending_blacklist,omitempty"`
	PendingRetirement   bool                  `json:"pending_retirement,omitempty"`
}

// RetirementInfoConfig defines retirement count and addresses
type RetirementInfoConfig struct {
	Count     int      `json:"count,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
}

// RemappedRowsConfig defines remapped rows information
type RemappedRowsConfig struct {
	Correctable     int  `json:"correctable,omitempty"`
	Uncorrectable   int  `json:"uncorrectable,omitempty"`
	Pending         bool `json:"pending,omitempty"`
	FailureOccurred bool `json:"failure_occurred,omitempty"`
}

// DisplayConfig defines display output settings
type DisplayConfig struct {
	Mode   string `json:"mode,omitempty"`
	Active string `json:"active,omitempty"`
}

// MIGConfig defines MIG configuration
type MIGConfig struct {
	ModeCurrent     string `json:"mode_current,omitempty"`
	ModePending     string `json:"mode_pending,omitempty"`
	MaxGPUInstances int    `json:"max_gpu_instances,omitempty"`
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
	BufferSize int    `json:"buffer_size,omitempty"`
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

// FeaturesConfig defines GPU-specific features (like Blackwell features)
type FeaturesConfig struct {
	TransformerEngine    bool `json:"transformer_engine,omitempty"`
	FP4Support           bool `json:"fp4_support,omitempty"`
	FP8Support           bool `json:"fp8_support,omitempty"`
	ConfidentialCompute  bool `json:"confidential_compute,omitempty"`
	NVLinkC2C            bool `json:"nvlink_c2c,omitempty"`
	DecompressionEngine  bool `json:"decompression_engine,omitempty"`
	FifthGenTensorCores  bool `json:"fifth_gen_tensor_cores,omitempty"`
}

// GraceSuperchipConfig defines Grace CPU pairing for GB200
type GraceSuperchipConfig struct {
	Enabled        bool `json:"enabled,omitempty"`
	CPUCores       int  `json:"cpu_cores,omitempty"`
	CPUMemoryGB    int  `json:"cpu_memory_gb,omitempty"`
	CoherentMemory bool `json:"coherent_memory,omitempty"`
}

// ProcessConfig defines a running process
type ProcessConfig struct {
	PID           uint32 `json:"pid"`
	Type          string `json:"type,omitempty"` // "C" for compute, "G" for graphics
	Name          string `json:"name,omitempty"`
	UsedMemoryMiB uint64 `json:"used_memory_mib,omitempty"`
}

// NVLinkConfig defines NVLink topology
type NVLinkConfig struct {
	Version              int                `json:"version,omitempty"`
	LinksPerGPU          int                `json:"links_per_gpu,omitempty"`
	BandwidthPerLinkGBPS int                `json:"bandwidth_per_link_gbps,omitempty"`
	SwitchSupport        bool               `json:"switch_support,omitempty"`
	SwitchCount          int                `json:"switch_count,omitempty"`
	C2CEnabled           bool               `json:"c2c_enabled,omitempty"`
	Links                []NVLinkLinkConfig `json:"links,omitempty"`
}

// NVLinkLinkConfig defines a single NVLink connection
type NVLinkLinkConfig struct {
	Link             int    `json:"link"`
	State            string `json:"state,omitempty"`
	RemoteDeviceType string `json:"remote_device_type,omitempty"`
	RemotePCIBusID   string `json:"remote_pci_bus_id,omitempty"`
}
