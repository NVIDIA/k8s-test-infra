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

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
)

// MaxDevices is the maximum number of devices supported by the mock server.
const MaxDevices = 8

// DefaultBAR1SizeMB is the default simulated BAR1 aperture size in megabytes.
const DefaultBAR1SizeMB = 256

// ConfigurableDevice wraps dgxa100.Device and adds YAML-based configuration support
type ConfigurableDevice struct {
	*dgxa100.Device
	config      *DeviceConfig
	index       int
	minorNumber int

	// Cached computed values
	bar1Memory nvml.BAR1Memory
	pciInfo    nvml.PciInfo
}

// NewConfigurableDevice creates a device with YAML configuration
func NewConfigurableDevice(index int, baseDevice *dgxa100.Device, config *DeviceConfig, uuid string, pciBusID string, minorNumber int) *ConfigurableDevice {
	dev := &ConfigurableDevice{
		Device:      baseDevice,
		config:      config,
		index:       index,
		minorNumber: minorNumber,
	}

	// Override base device properties from config
	if config != nil {
		if config.Name != "" {
			dev.Name = config.Name
		}
		if config.Architecture != "" {
			dev.Architecture = parseArchitecture(config.Architecture)
		}
		if config.Brand != "" {
			dev.Brand = parseBrand(config.Brand)
		}
		if config.ComputeCapability != nil {
			dev.CudaComputeCapability = dgxa100.CudaComputeCapability{
				Major: config.ComputeCapability.Major,
				Minor: config.ComputeCapability.Minor,
			}
		}
		if config.Memory != nil {
			dev.MemoryInfo = nvml.Memory{
				Total: config.Memory.TotalBytes,
				Free:  config.Memory.FreeBytes,
				Used:  config.Memory.UsedBytes,
			}
		}
	}

	// Override UUID if provided
	if uuid != "" {
		dev.UUID = uuid
	}

	// Override PCI bus ID if provided
	if pciBusID != "" {
		dev.PciBusID = pciBusID
	}

	// Set minor number
	dev.Minor = minorNumber

	// Initialize cached values
	dev.initBAR1Memory()
	dev.initPciInfo()

	debugLog("[DEVICE %d] Created: name=%s uuid=%s pci=%s\n", index, dev.Name, dev.UUID, dev.PciBusID)

	return dev
}

func (d *ConfigurableDevice) initBAR1Memory() {
	if d.config != nil && d.config.BAR1Memory != nil {
		d.bar1Memory = nvml.BAR1Memory{
			Bar1Total: d.config.BAR1Memory.TotalBytes,
			Bar1Free:  d.config.BAR1Memory.FreeBytes,
			Bar1Used:  d.config.BAR1Memory.UsedBytes,
		}
	} else {
		// Default: 256 MiB (will be 64 GiB for A100 from YAML)
		bar1Bytes := uint64(DefaultBAR1SizeMB * 1024 * 1024)
		d.bar1Memory = nvml.BAR1Memory{
			Bar1Total: bar1Bytes,
			Bar1Free:  bar1Bytes,
			Bar1Used:  0,
		}
	}
}

// ParsePCIBusID parses a PCI bus ID string into its components.
// Accepts formats: "DDDD:BB:DD.F" or "BB:DD.F" where D=domain, B=bus, D=device, F=function.
// Returns error if format is invalid.
func ParsePCIBusID(busID string) (domain, bus, device, function uint32, err error) {
	if busID == "" {
		return 0, 0, 0, 0, fmt.Errorf("empty PCI bus ID")
	}

	// Try standard format: DDDD:BB:DD.F (domain:bus:device.function)
	n, _ := fmt.Sscanf(busID, "%x:%x:%x.%x", &domain, &bus, &device, &function)
	if n == 4 {
		return domain, bus, device, function, nil
	}

	// Try short format: BB:DD.F (bus:device.function, domain=0)
	domain = 0
	n, _ = fmt.Sscanf(busID, "%x:%x.%x", &bus, &device, &function)
	if n == 3 {
		return domain, bus, device, function, nil
	}

	return 0, 0, 0, 0, fmt.Errorf("invalid PCI bus ID format: %q (expected DDDD:BB:DD.F or BB:DD.F)", busID)
}

func (d *ConfigurableDevice) initPciInfo() {
	var domain, bus, device, function uint32

	if d.PciBusID != "" {
		var err error
		domain, bus, device, function, err = ParsePCIBusID(d.PciBusID)
		if err != nil {
			debugLog("[DEVICE %d] Warning: %v, using defaults\n", d.index, err)
			// Use default values (zeros) on parse failure
		}
		_ = function // unused in pciInfo struct
	}

	d.pciInfo = nvml.PciInfo{
		Domain: domain,
		Bus:    bus,
		Device: device,
	}

	// Set PCI device ID from config or default
	if d.config != nil && d.config.PCI != nil {
		if d.config.PCI.DeviceID != 0 {
			d.pciInfo.PciDeviceId = d.config.PCI.DeviceID
		}
		if d.config.PCI.SubsystemID != 0 {
			d.pciInfo.PciSubSystemId = d.config.PCI.SubsystemID
		}
	} else {
		// Default A100 IDs
		d.pciInfo.PciDeviceId = 0x20B010DE
		d.pciInfo.PciSubSystemId = 0x134710DE
	}

	// Copy bus ID string
	for i := 0; i < len(d.PciBusID) && i < 32; i++ {
		d.pciInfo.BusId[i] = uint8(d.PciBusID[i])
	}
}

// GetIndex returns the device index
func (d *ConfigurableDevice) GetIndex() (int, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetIndex -> %d\n", d.index)
	return d.index, nvml.SUCCESS
}

// GetMinorNumber returns the device minor number
func (d *ConfigurableDevice) GetMinorNumber() (int, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetMinorNumber -> %d\n", d.minorNumber)
	return d.minorNumber, nvml.SUCCESS
}

// GetBAR1MemoryInfo returns BAR1 memory information
func (d *ConfigurableDevice) GetBAR1MemoryInfo() (nvml.BAR1Memory, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetBAR1MemoryInfo -> total=%d\n", d.bar1Memory.Bar1Total)
	return d.bar1Memory, nvml.SUCCESS
}

// GetMemoryInfo returns GPU memory information
func (d *ConfigurableDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetMemoryInfo -> total=%d\n", d.MemoryInfo.Total)
	return d.MemoryInfo, nvml.SUCCESS
}

// GetMemoryInfo_v2 returns GPU memory information (v2 API)
func (d *ConfigurableDevice) GetMemoryInfo_v2() (nvml.Memory_v2, nvml.Return) {
	mem := nvml.Memory_v2{
		Version:  2,
		Total:    d.MemoryInfo.Total,
		Reserved: 0,
		Free:     d.MemoryInfo.Free,
		Used:     d.MemoryInfo.Used,
	}
	// If we have config with reserved bytes, use it
	if d.config != nil && d.config.Memory != nil {
		mem.Reserved = d.config.Memory.ReservedBytes
	}
	debugLog("[NVML] nvmlDeviceGetMemoryInfo_v2 -> total=%d reserved=%d\n", mem.Total, mem.Reserved)
	return mem, nvml.SUCCESS
}

// GetPciInfo returns PCI information for the device
func (d *ConfigurableDevice) GetPciInfo() (nvml.PciInfo, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetPciInfo -> busId=%s\n", d.PciBusID)
	return d.pciInfo, nvml.SUCCESS
}

// GetComputeRunningProcesses returns running compute processes
func (d *ConfigurableDevice) GetComputeRunningProcesses() ([]nvml.ProcessInfo, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetComputeRunningProcesses\n")
	if d.config == nil || len(d.config.Processes) == 0 {
		return []nvml.ProcessInfo{}, nvml.SUCCESS
	}

	var procs []nvml.ProcessInfo
	for _, p := range d.config.Processes {
		if p.Type == "C" || p.Type == "" { // Compute process
			procs = append(procs, nvml.ProcessInfo{
				Pid:           p.PID,
				UsedGpuMemory: p.UsedMemoryMiB * 1024 * 1024,
			})
		}
	}
	return procs, nvml.SUCCESS
}

// GetGraphicsRunningProcesses returns running graphics processes
func (d *ConfigurableDevice) GetGraphicsRunningProcesses() ([]nvml.ProcessInfo, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetGraphicsRunningProcesses\n")
	if d.config == nil || len(d.config.Processes) == 0 {
		return []nvml.ProcessInfo{}, nvml.SUCCESS
	}

	var procs []nvml.ProcessInfo
	for _, p := range d.config.Processes {
		if p.Type == "G" { // Graphics process
			procs = append(procs, nvml.ProcessInfo{
				Pid:           p.PID,
				UsedGpuMemory: p.UsedMemoryMiB * 1024 * 1024,
			})
		}
	}
	return procs, nvml.SUCCESS
}

// GetSerial returns the device serial number
func (d *ConfigurableDevice) GetSerial() (string, nvml.Return) {
	serial := ""
	if d.config != nil && d.config.Serial != "" {
		serial = d.config.Serial
	}
	debugLog("[NVML] nvmlDeviceGetSerial -> %s\n", serial)
	if serial == "" {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	return serial, nvml.SUCCESS
}

// GetVbiosVersion returns the VBIOS version
func (d *ConfigurableDevice) GetVbiosVersion() (string, nvml.Return) {
	version := ""
	if d.config != nil && d.config.VBIOSVersion != "" {
		version = d.config.VBIOSVersion
	}
	debugLog("[NVML] nvmlDeviceGetVbiosVersion -> %s\n", version)
	if version == "" {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	return version, nvml.SUCCESS
}

// GetBoardPartNumber returns the board part number
func (d *ConfigurableDevice) GetBoardPartNumber() (string, nvml.Return) {
	partNumber := ""
	if d.config != nil && d.config.BoardPartNumber != "" {
		partNumber = d.config.BoardPartNumber
	}
	debugLog("[NVML] nvmlDeviceGetBoardPartNumber -> %s\n", partNumber)
	if partNumber == "" {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	return partNumber, nvml.SUCCESS
}

// GetTemperature returns the GPU temperature
func (d *ConfigurableDevice) GetTemperature(sensor nvml.TemperatureSensors) (uint32, nvml.Return) {
	temp := uint32(0)
	if d.config != nil && d.config.Thermal != nil {
		switch sensor {
		case nvml.TEMPERATURE_GPU:
			temp = uint32(d.config.Thermal.TemperatureGPU_C)
		default:
			temp = uint32(d.config.Thermal.TemperatureGPU_C)
		}
	}
	debugLog("[NVML] nvmlDeviceGetTemperature(sensor=%d) -> %d\n", sensor, temp)
	if temp == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return temp, nvml.SUCCESS
}

// GetTemperatureThreshold returns temperature thresholds
func (d *ConfigurableDevice) GetTemperatureThreshold(thresholdType nvml.TemperatureThresholds) (uint32, nvml.Return) {
	temp := uint32(0)
	if d.config != nil && d.config.Thermal != nil {
		switch thresholdType {
		case nvml.TEMPERATURE_THRESHOLD_SHUTDOWN:
			temp = uint32(d.config.Thermal.ShutdownThreshold_C)
		case nvml.TEMPERATURE_THRESHOLD_SLOWDOWN:
			temp = uint32(d.config.Thermal.SlowdownThreshold_C)
		case nvml.TEMPERATURE_THRESHOLD_GPU_MAX:
			temp = uint32(d.config.Thermal.MaxOperating_C)
		default:
			temp = uint32(d.config.Thermal.MaxOperating_C)
		}
	}
	debugLog("[NVML] nvmlDeviceGetTemperatureThreshold(type=%d) -> %d\n", thresholdType, temp)
	if temp == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return temp, nvml.SUCCESS
}

// GetPowerUsage returns current power draw in milliwatts
func (d *ConfigurableDevice) GetPowerUsage() (uint32, nvml.Return) {
	power := uint32(0)
	if d.config != nil && d.config.Power != nil {
		power = d.config.Power.CurrentDrawMW
	}
	debugLog("[NVML] nvmlDeviceGetPowerUsage -> %d mW\n", power)
	if power == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return power, nvml.SUCCESS
}

// GetPowerManagementLimit returns the power management limit in milliwatts
func (d *ConfigurableDevice) GetPowerManagementLimit() (uint32, nvml.Return) {
	limit := uint32(0)
	if d.config != nil && d.config.Power != nil {
		limit = d.config.Power.EnforcedLimitMW
	}
	debugLog("[NVML] nvmlDeviceGetPowerManagementLimit -> %d mW\n", limit)
	if limit == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return limit, nvml.SUCCESS
}

// GetPowerManagementDefaultLimit returns the default power limit
func (d *ConfigurableDevice) GetPowerManagementDefaultLimit() (uint32, nvml.Return) {
	limit := uint32(0)
	if d.config != nil && d.config.Power != nil {
		limit = d.config.Power.DefaultLimitMW
	}
	debugLog("[NVML] nvmlDeviceGetPowerManagementDefaultLimit -> %d mW\n", limit)
	if limit == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return limit, nvml.SUCCESS
}

// GetEnforcedPowerLimit returns the enforced power limit
func (d *ConfigurableDevice) GetEnforcedPowerLimit() (uint32, nvml.Return) {
	limit := uint32(0)
	if d.config != nil && d.config.Power != nil {
		limit = d.config.Power.EnforcedLimitMW
	}
	debugLog("[NVML] nvmlDeviceGetEnforcedPowerLimit -> %d mW\n", limit)
	if limit == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return limit, nvml.SUCCESS
}

// GetPowerManagementLimitConstraints returns min/max power limits
func (d *ConfigurableDevice) GetPowerManagementLimitConstraints() (uint32, uint32, nvml.Return) {
	minLimit, maxLimit := uint32(0), uint32(0)
	if d.config != nil && d.config.Power != nil {
		minLimit = d.config.Power.MinLimitMW
		maxLimit = d.config.Power.MaxLimitMW
	}
	debugLog("[NVML] nvmlDeviceGetPowerManagementLimitConstraints -> min=%d max=%d mW\n", minLimit, maxLimit)
	if minLimit == 0 && maxLimit == 0 {
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}
	return minLimit, maxLimit, nvml.SUCCESS
}

// GetClockInfo returns current clock frequencies
func (d *ConfigurableDevice) GetClockInfo(clockType nvml.ClockType) (uint32, nvml.Return) {
	clock := uint32(0)
	if d.config != nil && d.config.Clocks != nil {
		switch clockType {
		case nvml.CLOCK_GRAPHICS:
			clock = d.config.Clocks.GraphicsCurrent
		case nvml.CLOCK_SM:
			clock = d.config.Clocks.SMCurrent
		case nvml.CLOCK_MEM:
			clock = d.config.Clocks.MemoryCurrent
		case nvml.CLOCK_VIDEO:
			clock = d.config.Clocks.VideoCurrent
		}
	}
	debugLog("[NVML] nvmlDeviceGetClockInfo(type=%d) -> %d MHz\n", clockType, clock)
	if clock == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return clock, nvml.SUCCESS
}

// GetMaxClockInfo returns maximum clock frequencies
func (d *ConfigurableDevice) GetMaxClockInfo(clockType nvml.ClockType) (uint32, nvml.Return) {
	clock := uint32(0)
	if d.config != nil && d.config.Clocks != nil {
		switch clockType {
		case nvml.CLOCK_GRAPHICS:
			clock = d.config.Clocks.GraphicsMax
		case nvml.CLOCK_SM:
			clock = d.config.Clocks.SMMax
		case nvml.CLOCK_MEM:
			clock = d.config.Clocks.MemoryMax
		case nvml.CLOCK_VIDEO:
			clock = d.config.Clocks.VideoMax
		}
	}
	debugLog("[NVML] nvmlDeviceGetMaxClockInfo(type=%d) -> %d MHz\n", clockType, clock)
	if clock == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return clock, nvml.SUCCESS
}

// GetApplicationsClock returns application clock settings
func (d *ConfigurableDevice) GetApplicationsClock(clockType nvml.ClockType) (uint32, nvml.Return) {
	clock := uint32(0)
	if d.config != nil && d.config.Clocks != nil {
		switch clockType {
		case nvml.CLOCK_GRAPHICS:
			clock = d.config.Clocks.GraphicsApp
		case nvml.CLOCK_MEM:
			clock = d.config.Clocks.MemoryApp
		}
	}
	debugLog("[NVML] nvmlDeviceGetApplicationsClock(type=%d) -> %d MHz\n", clockType, clock)
	if clock == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return clock, nvml.SUCCESS
}

// GetDefaultApplicationsClock returns default application clock settings
func (d *ConfigurableDevice) GetDefaultApplicationsClock(clockType nvml.ClockType) (uint32, nvml.Return) {
	clock := uint32(0)
	if d.config != nil && d.config.Clocks != nil {
		switch clockType {
		case nvml.CLOCK_GRAPHICS:
			clock = d.config.Clocks.GraphicsAppDefault
		case nvml.CLOCK_MEM:
			clock = d.config.Clocks.MemoryAppDefault
		}
	}
	debugLog("[NVML] nvmlDeviceGetDefaultApplicationsClock(type=%d) -> %d MHz\n", clockType, clock)
	if clock == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return clock, nvml.SUCCESS
}

// GetUtilizationRates returns GPU utilization
func (d *ConfigurableDevice) GetUtilizationRates() (nvml.Utilization, nvml.Return) {
	util := nvml.Utilization{}
	if d.config != nil && d.config.Utilization != nil {
		util.Gpu = d.config.Utilization.GPU
		util.Memory = d.config.Utilization.Memory
	}
	debugLog("[NVML] nvmlDeviceGetUtilizationRates -> gpu=%d%% mem=%d%%\n", util.Gpu, util.Memory)
	return util, nvml.SUCCESS
}

// GetPerformanceState returns the current performance state
func (d *ConfigurableDevice) GetPerformanceState() (nvml.Pstates, nvml.Return) {
	pstate := nvml.PSTATE_0 // Default P0
	if d.config != nil && d.config.PerformanceState != "" {
		pstate = parsePstate(d.config.PerformanceState)
	}
	debugLog("[NVML] nvmlDeviceGetPerformanceState -> %d\n", pstate)
	return pstate, nvml.SUCCESS
}

// GetPersistenceMode returns persistence mode status
func (d *ConfigurableDevice) GetPersistenceMode() (nvml.EnableState, nvml.Return) {
	enabled := nvml.FEATURE_DISABLED
	if d.config != nil && d.config.PersistenceMode == "enabled" {
		enabled = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetPersistenceMode -> %d\n", enabled)
	return enabled, nvml.SUCCESS
}

// GetComputeMode returns compute mode
func (d *ConfigurableDevice) GetComputeMode() (nvml.ComputeMode, nvml.Return) {
	mode := nvml.COMPUTEMODE_DEFAULT
	if d.config != nil && d.config.ComputeMode != "" {
		mode = parseComputeMode(d.config.ComputeMode)
	}
	debugLog("[NVML] nvmlDeviceGetComputeMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// GetEccMode returns ECC mode status
func (d *ConfigurableDevice) GetEccMode() (nvml.EnableState, nvml.EnableState, nvml.Return) {
	current := nvml.FEATURE_DISABLED
	pending := nvml.FEATURE_DISABLED
	if d.config != nil && d.config.ECC != nil {
		if d.config.ECC.ModeCurrent == "enabled" {
			current = nvml.FEATURE_ENABLED
		}
		if d.config.ECC.ModePending == "enabled" {
			pending = nvml.FEATURE_ENABLED
		}
	}
	debugLog("[NVML] nvmlDeviceGetEccMode -> current=%d pending=%d\n", current, pending)
	return current, pending, nvml.SUCCESS
}

// GetFanSpeed returns fan speed percentage
func (d *ConfigurableDevice) GetFanSpeed() (uint32, nvml.Return) {
	speed := uint32(0)
	if d.config != nil && d.config.Fan != nil {
		if d.config.Fan.SpeedPercent != "" && d.config.Fan.SpeedPercent != "N/A" {
			_, _ = fmt.Sscanf(d.config.Fan.SpeedPercent, "%d", &speed)
		}
	}
	debugLog("[NVML] nvmlDeviceGetFanSpeed -> %d%%\n", speed)
	// Fan speed of 0 with count 0 means no fans (liquid cooled)
	if d.config != nil && d.config.Fan != nil && d.config.Fan.Count == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return speed, nvml.SUCCESS
}

// GetNumFans returns the number of fans
func (d *ConfigurableDevice) GetNumFans() (int, nvml.Return) {
	count := 0
	if d.config != nil && d.config.Fan != nil {
		count = d.config.Fan.Count
	}
	debugLog("[NVML] nvmlDeviceGetNumFans -> %d\n", count)
	return count, nvml.SUCCESS
}

// GetCurrPcieLinkGeneration returns current PCIe link generation
func (d *ConfigurableDevice) GetCurrPcieLinkGeneration() (int, nvml.Return) {
	gen := 0
	if d.config != nil && d.config.PCIe != nil {
		gen = d.config.PCIe.CurrentLinkGen
	}
	debugLog("[NVML] nvmlDeviceGetCurrPcieLinkGeneration -> %d\n", gen)
	if gen == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return gen, nvml.SUCCESS
}

// GetMaxPcieLinkGeneration returns max PCIe link generation
func (d *ConfigurableDevice) GetMaxPcieLinkGeneration() (int, nvml.Return) {
	gen := 0
	if d.config != nil && d.config.PCIe != nil {
		gen = d.config.PCIe.MaxLinkGen
	}
	debugLog("[NVML] nvmlDeviceGetMaxPcieLinkGeneration -> %d\n", gen)
	if gen == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return gen, nvml.SUCCESS
}

// GetCurrPcieLinkWidth returns current PCIe link width
func (d *ConfigurableDevice) GetCurrPcieLinkWidth() (int, nvml.Return) {
	width := 0
	if d.config != nil && d.config.PCIe != nil {
		width = d.config.PCIe.CurrentLinkWidth
	}
	debugLog("[NVML] nvmlDeviceGetCurrPcieLinkWidth -> %d\n", width)
	if width == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return width, nvml.SUCCESS
}

// GetMaxPcieLinkWidth returns max PCIe link width
func (d *ConfigurableDevice) GetMaxPcieLinkWidth() (int, nvml.Return) {
	width := 0
	if d.config != nil && d.config.PCIe != nil {
		width = d.config.PCIe.MaxLinkWidth
	}
	debugLog("[NVML] nvmlDeviceGetMaxPcieLinkWidth -> %d\n", width)
	if width == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return width, nvml.SUCCESS
}

// GetInforomVersion returns InfoROM version
func (d *ConfigurableDevice) GetInforomVersion(object nvml.InforomObject) (string, nvml.Return) {
	version := ""
	if d.config != nil && d.config.InfoROM != nil {
		switch object {
		case nvml.INFOROM_OEM:
			version = d.config.InfoROM.OEMObject
		case nvml.INFOROM_ECC:
			version = d.config.InfoROM.ECCObject
		case nvml.INFOROM_POWER:
			version = d.config.InfoROM.PWRObject
		default:
			// For INFOROM_IMG and others, use ImageVersion
			version = d.config.InfoROM.ImageVersion
		}
	}
	debugLog("[NVML] nvmlDeviceGetInforomVersion(object=%d) -> %s\n", object, version)
	if version == "" {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	return version, nvml.SUCCESS
}

// GetInforomImageVersion returns InfoROM image version
func (d *ConfigurableDevice) GetInforomImageVersion() (string, nvml.Return) {
	version := ""
	if d.config != nil && d.config.InfoROM != nil {
		version = d.config.InfoROM.ImageVersion
	}
	debugLog("[NVML] nvmlDeviceGetInforomImageVersion -> %s\n", version)
	if version == "" {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	return version, nvml.SUCCESS
}

// GetCurrentClocksThrottleReasons returns clock throttle reasons bitmask
func (d *ConfigurableDevice) GetCurrentClocksThrottleReasons() (uint64, nvml.Return) {
	reasons := uint64(0)
	if d.config != nil && d.config.ClocksThrottleReasons != nil {
		ctr := d.config.ClocksThrottleReasons
		if ctr.GPUIdle {
			reasons |= nvml.ClocksThrottleReasonGpuIdle
		}
		if ctr.ApplicationsClocksSetting {
			reasons |= nvml.ClocksThrottleReasonApplicationsClocksSetting
		}
		if ctr.SWPowerCap {
			reasons |= nvml.ClocksThrottleReasonSwPowerCap
		}
		if ctr.HWSlowdown {
			reasons |= nvml.ClocksThrottleReasonHwSlowdown
		}
		if ctr.SyncBoost {
			reasons |= nvml.ClocksThrottleReasonSyncBoost
		}
		if ctr.SWThermalSlowdown {
			reasons |= nvml.ClocksThrottleReasonSwThermalSlowdown
		}
		if ctr.HWThermalSlowdown {
			reasons |= nvml.ClocksThrottleReasonHwThermalSlowdown
		}
		if ctr.HWPowerBrakeSlowdown {
			reasons |= nvml.ClocksThrottleReasonHwPowerBrakeSlowdown
		}
		if ctr.DisplayClocksSetting {
			// Display clock setting throttle reason (value 256)
			reasons |= 256
		}
	}
	debugLog("[NVML] nvmlDeviceGetCurrentClocksThrottleReasons -> 0x%x\n", reasons)
	return reasons, nvml.SUCCESS
}

// GetDisplayActive returns display active status
func (d *ConfigurableDevice) GetDisplayActive() (nvml.EnableState, nvml.Return) {
	active := nvml.FEATURE_DISABLED
	if d.config != nil && d.config.Display != nil && d.config.Display.Active == "enabled" {
		active = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetDisplayActive -> %d\n", active)
	return active, nvml.SUCCESS
}

// GetDisplayMode returns display mode
func (d *ConfigurableDevice) GetDisplayMode() (nvml.EnableState, nvml.Return) {
	mode := nvml.FEATURE_DISABLED
	if d.config != nil && d.config.Display != nil && d.config.Display.Mode == "enabled" {
		mode = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetDisplayMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// GetAccountingMode returns accounting mode
func (d *ConfigurableDevice) GetAccountingMode() (nvml.EnableState, nvml.Return) {
	mode := nvml.FEATURE_DISABLED
	if d.config != nil && d.config.Accounting != nil && d.config.Accounting.Mode == "enabled" {
		mode = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetAccountingMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// GetAccountingBufferSize returns accounting buffer size
func (d *ConfigurableDevice) GetAccountingBufferSize() (int, nvml.Return) {
	size := 4000 // Default
	if d.config != nil && d.config.Accounting != nil && d.config.Accounting.BufferSize > 0 {
		size = d.config.Accounting.BufferSize
	}
	debugLog("[NVML] nvmlDeviceGetAccountingBufferSize -> %d\n", size)
	return size, nvml.SUCCESS
}

// GetEncoderStats returns encoder statistics
func (d *ConfigurableDevice) GetEncoderStats() (int, uint32, uint32, nvml.Return) {
	sessionCount, avgFps, avgLatency := 0, uint32(0), uint32(0)
	if d.config != nil && d.config.EncoderStats != nil {
		sessionCount = int(d.config.EncoderStats.SessionCount)
		avgFps = d.config.EncoderStats.AverageFPS
		avgLatency = d.config.EncoderStats.AverageLatencyUS
	}
	debugLog("[NVML] nvmlDeviceGetEncoderStats -> sessions=%d fps=%d latency=%d\n", sessionCount, avgFps, avgLatency)
	return sessionCount, avgFps, avgLatency, nvml.SUCCESS
}

// GetFBCStats returns FBC statistics
func (d *ConfigurableDevice) GetFBCStats() (nvml.FBCStats, nvml.Return) {
	stats := nvml.FBCStats{}
	if d.config != nil && d.config.FBCStats != nil {
		stats.SessionsCount = d.config.FBCStats.SessionCount
		stats.AverageFPS = d.config.FBCStats.AverageFPS
		stats.AverageLatency = d.config.FBCStats.AverageLatencyUS
	}
	debugLog("[NVML] nvmlDeviceGetFBCStats -> sessions=%d\n", stats.SessionsCount)
	return stats, nvml.SUCCESS
}

// GetMultiGpuBoard returns whether the device is on a multi-GPU board
func (d *ConfigurableDevice) GetMultiGpuBoard() (int, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetMultiGpuBoard -> 0\n")
	return 0, nvml.SUCCESS
}

// GetBoardId returns the board ID
func (d *ConfigurableDevice) GetBoardId() (uint32, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetBoardId -> 0\n")
	return 0, nvml.SUCCESS
}

// GetEncoderCapacity returns encoder capacity
func (d *ConfigurableDevice) GetEncoderCapacity(encoderType nvml.EncoderType) (int, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetEncoderCapacity(type=%d) -> 100\n", encoderType)
	return 100, nvml.SUCCESS
}

// GetEncoderSessions returns encoder sessions
func (d *ConfigurableDevice) GetEncoderSessions() ([]nvml.EncoderSessionInfo, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetEncoderSessions -> []\n")
	return []nvml.EncoderSessionInfo{}, nvml.SUCCESS
}

// GetFBCSessions returns FBC sessions
func (d *ConfigurableDevice) GetFBCSessions() ([]nvml.FBCSessionInfo, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetFBCSessions -> []\n")
	return []nvml.FBCSessionInfo{}, nvml.SUCCESS
}

// GetGpuOperationMode returns GPU operation mode
func (d *ConfigurableDevice) GetGpuOperationMode() (nvml.GpuOperationMode, nvml.GpuOperationMode, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetGpuOperationMode -> ALL_ON\n")
	return nvml.GOM_ALL_ON, nvml.GOM_ALL_ON, nvml.SUCCESS
}

// GetNvLinkState returns NvLink state
func (d *ConfigurableDevice) GetNvLinkState(link int) (nvml.EnableState, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetNvLinkState(link=%d) -> DISABLED\n", link)
	return nvml.FEATURE_DISABLED, nvml.SUCCESS
}

// GetNvLinkVersion returns NvLink version
func (d *ConfigurableDevice) GetNvLinkVersion(link int) (uint32, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetNvLinkVersion(link=%d) -> 0\n", link)
	return 0, nvml.SUCCESS
}

// GetNvLinkCapability returns NvLink capability
func (d *ConfigurableDevice) GetNvLinkCapability(link int, capability nvml.NvLinkCapability) (uint32, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetNvLinkCapability(link=%d, cap=%d) -> 0\n", link, capability)
	return 0, nvml.SUCCESS
}

// GetPowerState returns power state (same as performance state)
func (d *ConfigurableDevice) GetPowerState() (nvml.Pstates, nvml.Return) {
	return d.GetPerformanceState()
}

// GetMigMode returns MIG mode
func (d *ConfigurableDevice) GetMigMode() (int, int, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetMigMode -> disabled\n")
	return 0, 0, nvml.SUCCESS // Disabled, Disabled
}

// GetArchitecture returns GPU architecture
func (d *ConfigurableDevice) GetArchitecture() (nvml.DeviceArchitecture, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetArchitecture -> %d\n", d.Architecture)
	return d.Architecture, nvml.SUCCESS
}

// GetCudaComputeCapability returns CUDA compute capability
func (d *ConfigurableDevice) GetCudaComputeCapability() (int, int, nvml.Return) {
	major := d.CudaComputeCapability.Major
	minor := d.CudaComputeCapability.Minor
	debugLog("[NVML] nvmlDeviceGetCudaComputeCapability -> %d.%d\n", major, minor)
	return major, minor, nvml.SUCCESS
}

// GetBrand returns device brand
func (d *ConfigurableDevice) GetBrand() (nvml.BrandType, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetBrand -> %d\n", d.Brand)
	return d.Brand, nvml.SUCCESS
}

// GetEncoderUtilization returns encoder utilization
func (d *ConfigurableDevice) GetEncoderUtilization() (uint32, uint32, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetEncoderUtilization -> 0, 0\n")
	return 0, 0, nvml.SUCCESS // Utilization, sampling period
}

// GetDecoderUtilization returns decoder utilization
func (d *ConfigurableDevice) GetDecoderUtilization() (uint32, uint32, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetDecoderUtilization -> 0, 0\n")
	return 0, 0, nvml.SUCCESS // Utilization, sampling period
}

// GetPcieReplayCounter returns PCIe replay counter
func (d *ConfigurableDevice) GetPcieReplayCounter() (int, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetPcieReplayCounter -> 0\n")
	return 0, nvml.SUCCESS
}

// GetPcieThroughput returns PCIe throughput
func (d *ConfigurableDevice) GetPcieThroughput(counter nvml.PcieUtilCounter) (uint32, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetPcieThroughput(counter=%d) -> 0\n", counter)
	return 0, nvml.SUCCESS
}

// GetTotalEccErrors returns total ECC errors
func (d *ConfigurableDevice) GetTotalEccErrors(errorType nvml.MemoryErrorType, counterType nvml.EccCounterType) (uint64, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetTotalEccErrors -> 0\n")
	return 0, nvml.SUCCESS
}

// GetMemoryErrorCounter returns memory error counter
func (d *ConfigurableDevice) GetMemoryErrorCounter(errorType nvml.MemoryErrorType, counterType nvml.EccCounterType, locationType nvml.MemoryLocation) (uint64, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetMemoryErrorCounter -> 0\n")
	return 0, nvml.SUCCESS
}

// GetRetiredPages returns retired pages
func (d *ConfigurableDevice) GetRetiredPages(cause nvml.PageRetirementCause) ([]uint64, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetRetiredPages -> []\n")
	return []uint64{}, nvml.SUCCESS
}

// GetRetiredPages_v2 returns retired pages with timestamps
func (d *ConfigurableDevice) GetRetiredPages_v2(cause nvml.PageRetirementCause) ([]uint64, []uint64, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetRetiredPages_v2 -> [], []\n")
	return []uint64{}, []uint64{}, nvml.SUCCESS
}

// GetRetiredPagesPendingStatus returns whether there are pending retired pages
func (d *ConfigurableDevice) GetRetiredPagesPendingStatus() (nvml.EnableState, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetRetiredPagesPendingStatus -> NOT_PENDING\n")
	return nvml.FEATURE_DISABLED, nvml.SUCCESS // No pending pages
}

// GetRemappedRows returns remapped row information
func (d *ConfigurableDevice) GetRemappedRows() (int, int, bool, bool, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetRemappedRows -> 0, 0, false, false\n")
	return 0, 0, false, false, nvml.SUCCESS
}

// GetFanSpeed_v2 returns fan speed for a specific fan
func (d *ConfigurableDevice) GetFanSpeed_v2(fan int) (uint32, nvml.Return) {
	speed, ret := d.GetFanSpeed()
	debugLog("[NVML] nvmlDeviceGetFanSpeed_v2(fan=%d) -> %d\n", fan, speed)
	return speed, ret
}

// Helper functions

func parseArchitecture(arch string) nvml.DeviceArchitecture {
	switch arch {
	case "kepler":
		return nvml.DEVICE_ARCH_KEPLER
	case "maxwell":
		return nvml.DEVICE_ARCH_MAXWELL
	case "pascal":
		return nvml.DEVICE_ARCH_PASCAL
	case "volta":
		return nvml.DEVICE_ARCH_VOLTA
	case "turing":
		return nvml.DEVICE_ARCH_TURING
	case "ampere":
		return nvml.DEVICE_ARCH_AMPERE
	case "ada":
		return nvml.DEVICE_ARCH_ADA
	case "hopper":
		return nvml.DEVICE_ARCH_HOPPER
	default:
		return nvml.DEVICE_ARCH_UNKNOWN
	}
}

func parseBrand(brand string) nvml.BrandType {
	switch brand {
	case "nvidia":
		return nvml.BRAND_NVIDIA
	case "tesla":
		return nvml.BRAND_TESLA
	case "quadro":
		return nvml.BRAND_QUADRO
	case "geforce":
		return nvml.BRAND_GEFORCE
	case "titan":
		return nvml.BRAND_TITAN
	case "nvidia_rtx":
		return nvml.BRAND_NVIDIA_RTX
	default:
		return nvml.BRAND_UNKNOWN
	}
}

func parsePstate(state string) nvml.Pstates {
	switch state {
	case "P0":
		return nvml.PSTATE_0
	case "P1":
		return nvml.PSTATE_1
	case "P2":
		return nvml.PSTATE_2
	case "P3":
		return nvml.PSTATE_3
	case "P4":
		return nvml.PSTATE_4
	case "P5":
		return nvml.PSTATE_5
	case "P6":
		return nvml.PSTATE_6
	case "P7":
		return nvml.PSTATE_7
	case "P8":
		return nvml.PSTATE_8
	default:
		return nvml.PSTATE_0
	}
}

func parseComputeMode(mode string) nvml.ComputeMode {
	switch mode {
	case "default":
		return nvml.COMPUTEMODE_DEFAULT
	case "exclusive_thread":
		return nvml.COMPUTEMODE_EXCLUSIVE_THREAD
	case "prohibited":
		return nvml.COMPUTEMODE_PROHIBITED
	case "exclusive_process":
		return nvml.COMPUTEMODE_EXCLUSIVE_PROCESS
	default:
		return nvml.COMPUTEMODE_DEFAULT
	}
}

// MockServer wraps dgxa100.Server and uses configurable devices
type MockServer struct {
	*dgxa100.Server
	configurableDevices [MaxDevices]*ConfigurableDevice
}

// DeviceGetHandleByIndex returns a configurable device by index
func (s *MockServer) DeviceGetHandleByIndex(index int) (nvml.Device, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetHandleByIndex(%d)\n", index)
	if index < 0 || index >= len(s.configurableDevices) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	if s.configurableDevices[index] == nil {
		return nil, nvml.ERROR_NOT_FOUND
	}
	return s.configurableDevices[index], nvml.SUCCESS
}

// DeviceGetHandleByUUID returns a configurable device by UUID
func (s *MockServer) DeviceGetHandleByUUID(uuid string) (nvml.Device, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetHandleByUUID(%s)\n", uuid)
	for _, dev := range s.configurableDevices {
		if dev != nil && dev.UUID == uuid {
			return dev, nvml.SUCCESS
		}
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// DeviceGetHandleByPciBusId returns a configurable device by PCI bus ID
func (s *MockServer) DeviceGetHandleByPciBusId(pciBusId string) (nvml.Device, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetHandleByPciBusId(%s)\n", pciBusId)
	for _, dev := range s.configurableDevices {
		if dev != nil && dev.PciBusID == pciBusId {
			return dev, nvml.SUCCESS
		}
	}
	return nil, nvml.ERROR_NOT_FOUND
}

