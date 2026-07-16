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
	"slices"
	"time"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
)

// nvmlStructVersion computes NVML_STRUCT_VERSION(size, ver) = size | (ver << 24).
// This matches the NVML C macro encoding used for versioned structs.
func nvmlStructVersion(structSize uintptr, version uint32) uint32 {
	return uint32(structSize) | (version << 24)
}

// MaxDevices is the maximum number of devices supported by the mock server.
const MaxDevices = 8

// DefaultBAR1SizeMB is the default simulated BAR1 aperture size in megabytes.
const DefaultBAR1SizeMB = 256

// ConfigurableDevice wraps dgxa100.Device and adds YAML-based configuration support
type ConfigurableDevice struct {
	*dgxa100.Device
	config      *DeviceConfig
	fabric      *NodeFabric
	index       int
	minorNumber int

	// Cached computed values
	bar1Memory nvml.BAR1Memory
	pciInfo    nvml.PciInfo

	// Mutable in-memory state (not persisted across restarts)
	persistenceModeOverride *nvml.EnableState

	// dynamicMetrics is non-nil when DeviceConfig.DynamicMetrics is set.
	// When nil, the device returns static values from config unchanged.
	dynamicMetrics *dynamicMetricsSimulator

	// failure is non-nil when DeviceConfig.Failure selects a non-healthy
	// mode. When nil, the device behaves as healthy hardware. Methods
	// that should respect failure injection call checkFailure / tickLost.
	failure *failureInjector
}

// NewConfigurableDevice creates a device with YAML configuration. The
// shared, immutable *NodeFabric carries the node-level NVLink / PCIe /
// affinity topology; this device only needs its own index to derive its
// per-device view. fabric may be nil (legacy/default mode) in which case
// the NVLink and topology getters fall back to per-device defaults.
func NewConfigurableDevice(index int, baseDevice *mockserver.Device, config *DeviceConfig, uuid string, pciBusID string, minorNumber int, fabric *NodeFabric) *ConfigurableDevice {
	dev := &ConfigurableDevice{
		Device:      baseDevice,
		config:      config,
		fabric:      fabric,
		index:       index,
		minorNumber: minorNumber,
	}

	// Override base device properties from config
	if config != nil {
		if config.Name != "" {
			dev.Config.Name = config.Name
		}
		if config.Architecture != "" {
			dev.Config.Architecture = parseArchitecture(config.Architecture)
		}
		if config.Brand != "" {
			dev.Config.Brand = parseBrand(config.Brand)
		}
		if config.ComputeCapability != nil {
			dev.Config.CudaMajor = config.ComputeCapability.Major
			dev.Config.CudaMinor = config.ComputeCapability.Minor
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

	// Enable dynamic metric simulation if the YAML opts in. Leaving
	// config.DynamicMetrics nil preserves the historical static behavior.
	if config != nil && config.DynamicMetrics != nil {
		dev.dynamicMetrics = newDynamicMetricsSimulator(config.DynamicMetrics)
	}

	// Enable failure injection (lost/fallen_off_bus/ecc_uncorrectable)
	// when the YAML opts in. newFailureInjector returns nil for the
	// default healthy mode, so the per-call hot path stays a single
	// nil check.
	if config != nil && config.Failure != nil {
		dev.failure = newFailureInjector(config.Failure)
	}

	debugLog("[DEVICE %d] Created: name=%s uuid=%s pci=%s\n", index, dev.Config.Name, dev.UUID, dev.PciBusID)

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
		d.pciInfo.BusId[i] = int8(d.PciBusID[i])
	}
}

// GetConfig returns the device configuration
func (d *ConfigurableDevice) GetConfig() *DeviceConfig {
	return d.config
}

// GetIndex returns the device index
func (d *ConfigurableDevice) GetIndex() (int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	debugLog("[NVML] nvmlDeviceGetIndex -> %d\n", d.index)
	return d.index, nvml.SUCCESS
}

// GetUUID returns the device UUID. Overrides the embedded dgxa100.Device
// implementation so the lost / fallen_off_bus failure modes can surface
// ERROR_GPU_IS_LOST from this getter — real NVML returns the same error
// code from every device method once the kernel driver has marked the
// GPU gone, including identity queries that don't touch hardware.
func (d *ConfigurableDevice) GetUUID() (string, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return "", ret
	}
	debugLog("[NVML] nvmlDeviceGetUUID -> %s\n", d.UUID)
	return d.UUID, nvml.SUCCESS
}

// GetName returns the device name. Overrides the embedded dgxa100.Device
// implementation for the same reason as GetUUID — lost GPUs must
// propagate ERROR_GPU_IS_LOST from every device method.
func (d *ConfigurableDevice) GetName() (string, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return "", ret
	}
	debugLog("[NVML] nvmlDeviceGetName -> %s\n", d.Config.Name)
	return d.Config.Name, nvml.SUCCESS
}

// GetMinorNumber returns the device minor number
func (d *ConfigurableDevice) GetMinorNumber() (int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
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
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.Memory{}, ret
	}
	debugLog("[NVML] nvmlDeviceGetMemoryInfo -> total=%d\n", d.MemoryInfo.Total)
	return d.MemoryInfo, nvml.SUCCESS
}

// GetMemoryInfo_v2 returns GPU memory information (v2 API)
func (d *ConfigurableDevice) GetMemoryInfo_v2() (nvml.Memory_v2, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.Memory_v2{}, ret
	}
	mem := nvml.Memory_v2{
		Version:  nvmlStructVersion(unsafe.Sizeof(nvml.Memory_v2{}), 2),
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
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.PciInfo{}, ret
	}
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

// GetProcessUtilization returns one sample per configured process, compute and
// graphics/video alike (real NVML reports both; GetComputeRunningProcesses is the
// compute-only call), so it does not filter by type. lastSeenTimestamp is not used
// as a filter: each sample is stamped with time.Now() (always newer than the
// caller's lastSeen) so the common lastSeen=0 polling pattern keeps observing the
// configured values.
func (d *ConfigurableDevice) GetProcessUtilization(lastSeenTimestamp uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetProcessUtilization(lastSeen=%d)\n", lastSeenTimestamp)
	if d.config == nil || len(d.config.Processes) == 0 {
		return nil, nvml.SUCCESS
	}
	ts := uint64(time.Now().UnixMicro())
	var out []nvml.ProcessUtilizationSample
	for _, p := range d.config.Processes {
		out = append(out, nvml.ProcessUtilizationSample{
			Pid:       p.PID,
			TimeStamp: ts,
			SmUtil:    p.SmUtil,
			MemUtil:   p.MemUtil,
			EncUtil:   p.EncUtil,
			DecUtil:   p.DecUtil,
		})
	}
	return out, nvml.SUCCESS
}

// GetCurrentClocksEventReasons returns clock event reasons bitmask (newer API name for throttle reasons)
func (d *ConfigurableDevice) GetCurrentClocksEventReasons() (uint64, nvml.Return) {
	return d.GetCurrentClocksThrottleReasons()
}

// GetGspFirmwareMode returns GSP firmware mode (isEnabled, defaultMode)
func (d *ConfigurableDevice) GetGspFirmwareMode() (bool, bool, nvml.Return) {
	isEnabled := false
	defaultMode := false
	if d.config != nil && d.config.GSPFirmware != nil {
		isEnabled = d.config.GSPFirmware.Mode == "enabled"
		defaultMode = isEnabled // default mode follows mode unless overridden
	}
	debugLog("[NVML] nvmlDeviceGetGspFirmwareMode -> enabled=%v default=%v\n", isEnabled, defaultMode)
	return isEnabled, defaultMode, nvml.SUCCESS
}

// GetSerial returns the device serial number
func (d *ConfigurableDevice) GetSerial() (string, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return "", ret
	}
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
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return "", ret
	}
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

// GetTemperature returns the GPU temperature. Either a static `thermal:` block
// or a `dynamic_metrics.temperature` block is sufficient; only when neither is
// configured do we report ERROR_NOT_SUPPORTED. When both are present the
// dynamic simulator overrides the static value.
func (d *ConfigurableDevice) GetTemperature(sensor nvml.TemperatureSensors) (uint32, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.config == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	hasStatic := d.config.Thermal != nil
	hasDynamic := d.config.DynamicMetrics != nil && d.config.DynamicMetrics.Temperature != nil
	if !hasStatic && !hasDynamic {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	var temp uint32
	var shutdownC int
	if hasStatic {
		switch sensor {
		case nvml.TEMPERATURE_GPU:
			temp = uint32(d.config.Thermal.TemperatureGPU_C)
		default:
			temp = uint32(d.config.Thermal.TemperatureGPU_C)
		}
		shutdownC = d.config.Thermal.ShutdownThreshold_C
	}
	if d.dynamicMetrics != nil {
		temp = d.dynamicMetrics.Temperature(temp, shutdownC)
	}
	debugLog("[NVML] nvmlDeviceGetTemperature(sensor=%d) -> %d\n", sensor, temp)
	return temp, nvml.SUCCESS
}

// GetTemperatureThreshold returns temperature thresholds
func (d *ConfigurableDevice) GetTemperatureThreshold(thresholdType nvml.TemperatureThresholds) (uint32, nvml.Return) {
	if d.config == nil || d.config.Thermal == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	var temp uint32
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
	debugLog("[NVML] nvmlDeviceGetTemperatureThreshold(type=%d) -> %d\n", thresholdType, temp)
	return temp, nvml.SUCCESS
}

// GetPowerUsage returns current power draw in milliwatts. Either a static
// `power:` block or a `dynamic_metrics.power` block is sufficient; only when
// neither is configured do we report ERROR_NOT_SUPPORTED. When both are
// present the dynamic simulator overrides the static value.
func (d *ConfigurableDevice) GetPowerUsage() (uint32, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.config == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	hasStatic := d.config.Power != nil
	hasDynamic := d.config.DynamicMetrics != nil && d.config.DynamicMetrics.Power != nil
	if !hasStatic && !hasDynamic {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	var power, minMW, maxMW uint32
	if hasStatic {
		power = d.config.Power.CurrentDrawMW
		minMW = d.config.Power.MinLimitMW
		maxMW = d.config.Power.MaxLimitMW
	}
	if d.dynamicMetrics != nil {
		power = d.dynamicMetrics.Power(power, minMW, maxMW)
	}
	debugLog("[NVML] nvmlDeviceGetPowerUsage -> %d mW\n", power)
	return power, nvml.SUCCESS
}

// GetPowerManagementLimit returns the power management limit in milliwatts
func (d *ConfigurableDevice) GetPowerManagementLimit() (uint32, nvml.Return) {
	if d.config == nil || d.config.Power == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	limit := d.config.Power.EnforcedLimitMW
	debugLog("[NVML] nvmlDeviceGetPowerManagementLimit -> %d mW\n", limit)
	return limit, nvml.SUCCESS
}

// GetPowerManagementDefaultLimit returns the default power limit
func (d *ConfigurableDevice) GetPowerManagementDefaultLimit() (uint32, nvml.Return) {
	if d.config == nil || d.config.Power == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	limit := d.config.Power.DefaultLimitMW
	debugLog("[NVML] nvmlDeviceGetPowerManagementDefaultLimit -> %d mW\n", limit)
	return limit, nvml.SUCCESS
}

// GetEnforcedPowerLimit returns the enforced power limit
func (d *ConfigurableDevice) GetEnforcedPowerLimit() (uint32, nvml.Return) {
	if d.config == nil || d.config.Power == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	limit := d.config.Power.EnforcedLimitMW
	debugLog("[NVML] nvmlDeviceGetEnforcedPowerLimit -> %d mW\n", limit)
	return limit, nvml.SUCCESS
}

// GetPowerManagementLimitConstraints returns min/max power limits
func (d *ConfigurableDevice) GetPowerManagementLimitConstraints() (uint32, uint32, nvml.Return) {
	if d.config == nil || d.config.Power == nil {
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}
	minLimit := d.config.Power.MinLimitMW
	maxLimit := d.config.Power.MaxLimitMW
	debugLog("[NVML] nvmlDeviceGetPowerManagementLimitConstraints -> min=%d max=%d mW\n", minLimit, maxLimit)
	return minLimit, maxLimit, nvml.SUCCESS
}

// GetClockInfo returns current clock frequencies
func (d *ConfigurableDevice) GetClockInfo(clockType nvml.ClockType) (uint32, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.config == nil || d.config.Clocks == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	var clock uint32
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
	debugLog("[NVML] nvmlDeviceGetClockInfo(type=%d) -> %d MHz\n", clockType, clock)
	return clock, nvml.SUCCESS
}

// GetMaxClockInfo returns maximum clock frequencies
func (d *ConfigurableDevice) GetMaxClockInfo(clockType nvml.ClockType) (uint32, nvml.Return) {
	if d.config == nil || d.config.Clocks == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	var clock uint32
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
	debugLog("[NVML] nvmlDeviceGetMaxClockInfo(type=%d) -> %d MHz\n", clockType, clock)
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

// GetUtilizationRates returns GPU utilization. When dynamic_metrics.utilization
// is configured the returned values vary according to the selected pattern
// (idle / busy / burst / steady); otherwise the static UtilizationConfig
// values are returned unchanged.
func (d *ConfigurableDevice) GetUtilizationRates() (nvml.Utilization, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.Utilization{}, ret
	}
	util := nvml.Utilization{}
	if d.config != nil && d.config.Utilization != nil {
		util.Gpu = d.config.Utilization.GPU
		util.Memory = d.config.Utilization.Memory
	}
	if d.dynamicMetrics != nil {
		util.Gpu, util.Memory = d.dynamicMetrics.Utilization(util.Gpu, util.Memory)
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
	// Check in-memory override first (set by SetPersistenceMode)
	if d.persistenceModeOverride != nil {
		debugLog("[NVML] nvmlDeviceGetPersistenceMode -> %d (override)\n", *d.persistenceModeOverride)
		return *d.persistenceModeOverride, nvml.SUCCESS
	}
	enabled := nvml.FEATURE_DISABLED
	if d.config != nil && d.config.PersistenceMode == "enabled" {
		enabled = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetPersistenceMode -> %d\n", enabled)
	return enabled, nvml.SUCCESS
}

// SetPersistenceMode sets persistence mode in-memory (not persisted across restarts)
func (d *ConfigurableDevice) SetPersistenceMode(mode nvml.EnableState) nvml.Return {
	debugLog("[NVML] nvmlDeviceSetPersistenceMode(%d)\n", mode)
	d.persistenceModeOverride = &mode
	return nvml.SUCCESS
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

// GetMemoryBusWidth returns the memory bus width in bits.
func (d *ConfigurableDevice) GetMemoryBusWidth() (uint32, nvml.Return) {
	width := uint32(0)
	if d.config != nil && d.config.Memory != nil {
		width = d.config.Memory.MemoryBusWidth
	}
	if width == 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlDeviceGetMemoryBusWidth -> %d bits\n", width)
	return width, nvml.SUCCESS
}

// GetDefaultEccMode returns the default ECC mode.
func (d *ConfigurableDevice) GetDefaultEccMode() (nvml.EnableState, nvml.Return) {
	if d.config == nil || d.config.ECC == nil {
		return nvml.FEATURE_DISABLED, nvml.SUCCESS
	}
	mode := nvml.FEATURE_DISABLED
	if d.config.ECC.DefaultMode == "enabled" {
		mode = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetDefaultEccMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// GetSupportedClocksThrottleReasons returns bitmask of all supported throttle reasons.
func (d *ConfigurableDevice) GetSupportedClocksThrottleReasons() (uint64, nvml.Return) {
	reasons := uint64(nvml.ClocksThrottleReasonAll)
	debugLog("[NVML] nvmlDeviceGetSupportedClocksThrottleReasons -> 0x%x\n", reasons)
	return reasons, nvml.SUCCESS
}

// GetAutoBoostedClocksEnabled returns auto-boost status.
// Datacenter GPUs (A100, H100, etc.) don't support auto-boost.
func (d *ConfigurableDevice) GetAutoBoostedClocksEnabled() (nvml.EnableState, nvml.EnableState, nvml.Return) {
	return nvml.FEATURE_DISABLED, nvml.FEATURE_DISABLED, nvml.ERROR_NOT_SUPPORTED
}

// GetGspFirmwareVersion returns the GSP firmware version string.
func (d *ConfigurableDevice) GetGspFirmwareVersion() (string, nvml.Return) {
	if d.config == nil || d.config.GSPFirmware == nil || d.config.GSPFirmware.Version == "" {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlDeviceGetGspFirmwareVersion -> %s\n", d.config.GSPFirmware.Version)
	return d.config.GSPFirmware.Version, nvml.SUCCESS
}

// GetTotalEnergyConsumption returns cumulative energy in millijoules.
func (d *ConfigurableDevice) GetTotalEnergyConsumption() (uint64, nvml.Return) {
	if d.config == nil || d.config.Power == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	energy := d.config.Power.TotalEnergyConsumptionMJ
	debugLog("[NVML] nvmlDeviceGetTotalEnergyConsumption -> %d mJ\n", energy)
	return energy, nvml.SUCCESS
}

// GetDetailedEccErrors returns per-location ECC error counts.
func (d *ConfigurableDevice) GetDetailedEccErrors(errorType nvml.MemoryErrorType, counterType nvml.EccCounterType) (nvml.EccErrorCounts, nvml.Return) {
	counts := nvml.EccErrorCounts{}
	if d.config == nil || d.config.ECC == nil {
		return counts, nvml.SUCCESS
	}
	debugLog("[NVML] nvmlDeviceGetDetailedEccErrors(errorType=%d, counterType=%d) -> zeros\n", errorType, counterType)
	return counts, nvml.SUCCESS
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

// nvlinkLinkInRange reports whether a link index is within the valid
// per-device NVLink range. Negative or out-of-range indices map to
// ERROR_INVALID_ARGUMENT, matching real NVML.
func nvlinkLinkInRange(link int) bool {
	return link >= 0 && link < nvLinkMaxLinks
}

// GetNvLinkState returns NvLink state for a specific link, derived from
// the node fabric's resolved per-device links.
func (d *ConfigurableDevice) GetNvLinkState(link int) (nvml.EnableState, nvml.Return) {
	if !nvlinkLinkInRange(link) {
		return nvml.FEATURE_DISABLED, nvml.ERROR_INVALID_ARGUMENT
	}
	if d.fabric != nil {
		if l, ok := d.fabric.Link(d.index, link); ok && l.Active {
			debugLog("[NVML] nvmlDeviceGetNvLinkState(link=%d) -> ENABLED\n", link)
			return nvml.FEATURE_ENABLED, nvml.SUCCESS
		}
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkState(link=%d) -> DISABLED\n", link)
	return nvml.FEATURE_DISABLED, nvml.SUCCESS
}

// GetNvLinkVersion returns the NvLink version for a link from the fabric.
func (d *ConfigurableDevice) GetNvLinkVersion(link int) (uint32, nvml.Return) {
	if !nvlinkLinkInRange(link) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	version := uint32(0)
	if d.fabric != nil {
		if l, ok := d.fabric.Link(d.index, link); ok {
			version = l.Version
		} else {
			version = d.fabric.version
		}
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkVersion(link=%d) -> %d\n", link, version)
	return version, nvml.SUCCESS
}

// GetNvLinkCapability returns whether a capability bit is set for a link.
func (d *ConfigurableDevice) GetNvLinkCapability(link int, capability nvml.NvLinkCapability) (uint32, nvml.Return) {
	if !nvlinkLinkInRange(link) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	val := uint32(0)
	if d.fabric != nil {
		if l, ok := d.fabric.Link(d.index, link); ok {
			val = (l.Caps >> uint(capability)) & 1
		}
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkCapability(link=%d, cap=%d) -> %d\n", link, capability, val)
	return val, nvml.SUCCESS
}

// GetNvLinkRemoteDeviceType returns the kind of device on the far end of a
// link (GPU, SWITCH for NVSwitch endpoints, or IBMNPU/UNKNOWN otherwise).
func (d *ConfigurableDevice) GetNvLinkRemoteDeviceType(link int) (nvml.IntNvLinkDeviceType, nvml.Return) {
	if !nvlinkLinkInRange(link) {
		return nvml.NVLINK_DEVICE_TYPE_UNKNOWN, nvml.ERROR_INVALID_ARGUMENT
	}
	t := nvml.NVLINK_DEVICE_TYPE_UNKNOWN
	if d.fabric != nil {
		if l, ok := d.fabric.Link(d.index, link); ok && l.Active {
			switch l.RemoteKind {
			case RemoteGPU:
				t = nvml.NVLINK_DEVICE_TYPE_GPU
			case RemoteSwitch:
				t = nvml.NVLINK_DEVICE_TYPE_SWITCH
			}
		}
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkRemoteDeviceType(link=%d) -> %d\n", link, t)
	return t, nvml.SUCCESS
}

// GetTopologyCommonAncestor returns the pairwise PCIe topology level
// between this device and another. When the fabric carries pcie_topology
// facts the level is computed pairwise; otherwise it falls back to the
// per-device topology.default_level (preserving legacy behavior).
func (d *ConfigurableDevice) GetTopologyCommonAncestor(other nvml.Device) (nvml.GpuTopologyLevel, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.fabric != nil && d.fabric.HasPCIeTopology() {
		if o, ok := other.(*ConfigurableDevice); ok {
			level := d.fabric.TopoLevel(d.index, o.index)
			debugLog("[NVML] nvmlDeviceGetTopologyCommonAncestor(%d,%d) -> %d\n", d.index, o.index, level)
			return level, nvml.SUCCESS
		}
	}
	level := nvml.TOPOLOGY_SINGLE
	if d.config != nil && d.config.Topology != nil {
		level = parseTopologyLevel(d.config.Topology.DefaultLevel)
	}
	debugLog("[NVML] nvmlDeviceGetTopologyCommonAncestor -> %d (default)\n", level)
	return level, nvml.SUCCESS
}

// GetP2PStatus reports the peer-to-peer status between this device and
// another for a given capability index.
//
// On NVSwitch fabrics the per-link remote PCI is an opaque switch endpoint (a
// real GB200 reports FFFFFFFF:FF:FF.0 for every link), so a pair's NVLink
// connectivity cannot be matched via nvmlDeviceGetNvLinkRemotePciInfo. This
// getter answers whether P2P over NVLink is OK between the pair instead.
//
// NOTE: the 580 nvidia-smi `topo -m` no longer calls this; it derives the NV#
// matrix from NVML_FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT (field 147, see
// nvlink_fields.go). This remains implemented for the older binaries and any
// CUDA/NVML caller that probes P2P capability directly.
//
// P2P is OK when the two devices are NVLink-connected in the immutable
// fabric model (NVLinkCount > 0), which already encodes the switch-fanned
// all-to-all connectivity (a100 -> NV12, h100/gb200/gb300 -> NV18). A device
// is trivially OK with itself. Otherwise the pair is not NVLink P2P capable.
func (d *ConfigurableDevice) GetP2PStatus(other nvml.Device, _ nvml.GpuP2PCapsIndex) (nvml.GpuP2PStatus, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.P2P_STATUS_UNKNOWN, ret
	}
	o, ok := other.(*ConfigurableDevice)
	if !ok {
		return nvml.P2P_STATUS_UNKNOWN, nvml.ERROR_INVALID_ARGUMENT
	}
	if o.index == d.index {
		return nvml.P2P_STATUS_OK, nvml.SUCCESS
	}
	if d.fabric != nil && d.fabric.NVLinkCount(d.index, o.index) > 0 {
		debugLog("[NVML] nvmlDeviceGetP2PStatus(%d,%d) -> OK (nvlink)\n", d.index, o.index)
		return nvml.P2P_STATUS_OK, nvml.SUCCESS
	}
	debugLog("[NVML] nvmlDeviceGetP2PStatus(%d,%d) -> NOT_SUPPORTED\n", d.index, o.index)
	return nvml.P2P_STATUS_NOT_SUPPORTED, nvml.SUCCESS
}

// GetNvLinkErrorCounter returns the deterministic error counter for a
// link. Healthy links accrue at rate 0 (always 0); a configured error
// rate accrues monotonically against the shared epoch.
func (d *ConfigurableDevice) GetNvLinkErrorCounter(link int, counter nvml.NvLinkErrorCounter) (uint64, nvml.Return) {
	if !nvlinkLinkInRange(link) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	val := uint64(0)
	if d.fabric != nil {
		val = d.fabric.NvLinkErrorCount(d.index, link, d.fabric.now())
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkErrorCounter(link=%d, counter=%d) -> %d\n", link, counter, val)
	return val, nvml.SUCCESS
}

// GetNvLinkUtilizationCounter returns the deterministic (rx, tx)
// utilization counters for a link. The values grow monotonically with
// wall-clock time and across separate processes (shared epoch).
func (d *ConfigurableDevice) GetNvLinkUtilizationCounter(link, counter int) (uint64, uint64, nvml.Return) {
	if !nvlinkLinkInRange(link) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	if d.fabric == nil {
		return 0, 0, nvml.SUCCESS
	}
	rx, tx := d.fabric.NvLinkCounters(d.index, link, d.fabric.now())
	debugLog("[NVML] nvmlDeviceGetNvLinkUtilizationCounter(link=%d, counter=%d) -> rx=%d tx=%d\n", link, counter, rx, tx)
	return rx, tx, nvml.SUCCESS
}

// FreezeNvLinkUtilizationCounter is a no-op success: the counters are a
// pure function of time, so there is no mutable state to freeze.
func (d *ConfigurableDevice) FreezeNvLinkUtilizationCounter(link, counter int, freeze nvml.EnableState) nvml.Return {
	if !nvlinkLinkInRange(link) {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	debugLog("[NVML] nvmlDeviceFreezeNvLinkUtilizationCounter(link=%d) -> no-op\n", link)
	return nvml.SUCCESS
}

// ResetNvLinkUtilizationCounter is a no-op success (see Freeze).
func (d *ConfigurableDevice) ResetNvLinkUtilizationCounter(link, counter int) nvml.Return {
	if !nvlinkLinkInRange(link) {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	debugLog("[NVML] nvmlDeviceResetNvLinkUtilizationCounter(link=%d) -> no-op\n", link)
	return nvml.SUCCESS
}

// ResetNvLinkErrorCounters is a no-op success.
func (d *ConfigurableDevice) ResetNvLinkErrorCounters(link int) nvml.Return {
	if !nvlinkLinkInRange(link) {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	debugLog("[NVML] nvmlDeviceResetNvLinkErrorCounters(link=%d) -> no-op\n", link)
	return nvml.SUCCESS
}

// GetNvLinkRemotePciInfo returns PCI info for the remote device connected
// via NVLink, derived from the fabric's resolved per-device links.
func (d *ConfigurableDevice) GetNvLinkRemotePciInfo(link int) (nvml.PciInfo, nvml.Return) {
	pci := nvml.PciInfo{}
	if !nvlinkLinkInRange(link) {
		return pci, nvml.ERROR_INVALID_ARGUMENT
	}
	if d.fabric != nil {
		if l, ok := d.fabric.Link(d.index, link); ok && l.RemoteBDF != "" {
			// NVSwitch-attached links: a real GB200/HGX reports the "invalid"
			// PCI sentinel (FFFFFFFF:FF:FF.0) for switch endpoints — switches
			// are not PCI-enumerable from the GPU, so NVML fills 0xFF fields.
			// Matching this makes `nvlink -p` and `-R` render exactly as on
			// hardware ("Remote Device FFFFFFFF:FF:FF.0: Link 0"); a real-looking
			// BDF instead makes `-R` attempt a device lookup that yields
			// "Not Supported". Direct GPU<->GPU links still return the peer BDF.
			if l.RemoteKind == RemoteSwitch {
				setInvalidRemotePci(&pci)
				debugLog("[NVML] nvmlDeviceGetNvLinkRemotePciInfo(link=%d) -> switch sentinel\n", link)
				return pci, nvml.SUCCESS
			}
			if domain, bus, device, _, err := ParsePCIBusID(l.RemoteBDF); err == nil {
				pci.Domain = domain
				pci.Bus = bus
				pci.Device = device
				for i := 0; i < len(l.RemoteBDF) && i < 32; i++ {
					pci.BusId[i] = int8(l.RemoteBDF[i])
				}
			}
			debugLog("[NVML] nvmlDeviceGetNvLinkRemotePciInfo(link=%d) -> %s\n", link, l.RemoteBDF)
			return pci, nvml.SUCCESS
		}
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkRemotePciInfo(link=%d) -> empty\n", link)
	return pci, nvml.SUCCESS
}

// invalidRemotePciBusID is the sentinel BDF NVML returns for an NVLink remote
// endpoint that is not PCI-enumerable (e.g. an NVSwitch). Real GB200/HGX GPUs
// report this for every switch-attached link.
const invalidRemotePciBusID = "FFFFFFFF:FF:FF.0"

// setInvalidRemotePci fills a PciInfo with the all-0xFF sentinel that NVML uses
// for non-enumerable NVLink remote endpoints (NVSwitch).
func setInvalidRemotePci(pci *nvml.PciInfo) {
	pci.Domain = 0xFFFFFFFF
	pci.Bus = 0xFF
	pci.Device = 0xFF
	for i := 0; i < len(invalidRemotePciBusID) && i < len(pci.BusId); i++ {
		// go-nvml v0.13.1-0 (#400) changed PciInfo.BusId to [32]int8; bus-ID
		// bytes are ASCII so the int8 conversion is lossless.
		pci.BusId[i] = int8(invalidRemotePciBusID[i])
	}
}

// GetNumaNodeId returns the device's NUMA node from the fabric. Reports
// ERROR_NOT_SUPPORTED when no pcie_topology facts are available.
func (d *ConfigurableDevice) GetNumaNodeId() (int, nvml.Return) {
	if d.fabric == nil || !d.fabric.HasPCIeTopology() {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	node := d.fabric.NumaNode(d.index)
	if node < 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlDeviceGetNumaNodeId -> %d\n", node)
	return node, nvml.SUCCESS
}

// GetCpuAffinity returns the device's CPU affinity bitmask packed into
// cpuSetSize machine words. Reports ERROR_NOT_SUPPORTED without topology.
// The signature matches nvml.Device so it overrides the embedded stub.
func (d *ConfigurableDevice) GetCpuAffinity(cpuSetSize int) ([]uint, nvml.Return) {
	if cpuSetSize <= 0 {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	if d.fabric == nil || !d.fabric.HasPCIeTopology() {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	mask := wordsToUint(d.fabric.CPUAffinityMask(d.index, cpuSetSize))
	debugLog("[NVML] nvmlDeviceGetCpuAffinity -> %v\n", mask)
	return mask, nvml.SUCCESS
}

// GetCpuAffinityWithinScope returns the CPU affinity bitmask for a scope.
// The mock does not distinguish socket vs node scope, so both return the
// device's NUMA CPU set.
func (d *ConfigurableDevice) GetCpuAffinityWithinScope(cpuSetSize int, scope nvml.AffinityScope) ([]uint, nvml.Return) {
	return d.GetCpuAffinity(cpuSetSize)
}

// GetMemoryAffinity returns the device's memory (NUMA) affinity bitmask
// packed into nodeSetSize machine words.
func (d *ConfigurableDevice) GetMemoryAffinity(nodeSetSize int, scope nvml.AffinityScope) ([]uint, nvml.Return) {
	if nodeSetSize <= 0 {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	if d.fabric == nil || !d.fabric.HasPCIeTopology() {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	mask := wordsToUint(d.fabric.MemoryAffinityMask(d.index, nodeSetSize))
	debugLog("[NVML] nvmlDeviceGetMemoryAffinity -> %v\n", mask)
	return mask, nvml.SUCCESS
}

// wordsToUint converts a 64-bit-word affinity mask into the []uint shape
// the nvml.Device interface uses.
func wordsToUint(words []uint64) []uint {
	out := make([]uint, len(words))
	for i, w := range words {
		out[i] = uint(w)
	}
	return out
}

// GetThermalSettings returns thermal sensor settings.
func (d *ConfigurableDevice) GetThermalSettings(sensorIndex uint32) (nvml.GpuThermalSettings, nvml.Return) {
	settings := nvml.GpuThermalSettings{}
	if d.config != nil && d.config.Thermal != nil {
		settings.Count = 1
		// Note: Sensor[0] fields are set via the opaque _Ctype_struct___28 type,
		// which we cannot directly populate from Go. The Count field is the
		// primary useful value. Bridge layer handles the C struct population.
	}
	debugLog("[NVML] nvmlDeviceGetThermalSettings(sensor=%d) -> count=%d\n", sensorIndex, settings.Count)
	return settings, nvml.SUCCESS
}

// GetPowerManagementMode returns whether power management is enabled.
func (d *ConfigurableDevice) GetPowerManagementMode() (nvml.EnableState, nvml.Return) {
	mode := nvml.FEATURE_DISABLED
	if d.config != nil && d.config.Power != nil && d.config.Power.ManagementMode == "enabled" {
		mode = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceGetPowerManagementMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// GetPowerState returns power state (same as performance state)
func (d *ConfigurableDevice) GetPowerState() (nvml.Pstates, nvml.Return) {
	return d.GetPerformanceState()
}

// GetMigMode returns MIG mode (current, pending)
func (d *ConfigurableDevice) GetMigMode() (int, int, nvml.Return) {
	current, pending := 0, 0
	if d.config != nil && d.config.MIG != nil {
		if d.config.MIG.ModeCurrent == "enabled" {
			current = 1
		}
		if d.config.MIG.ModePending == "enabled" {
			pending = 1
		}
	}
	debugLog("[NVML] nvmlDeviceGetMigMode -> current=%d pending=%d\n", current, pending)
	return current, pending, nvml.SUCCESS
}

// GetMaxMigDeviceCount returns the maximum number of MIG devices
func (d *ConfigurableDevice) GetMaxMigDeviceCount() (int, nvml.Return) {
	count := 0
	if d.config != nil && d.config.MIG != nil {
		count = d.config.MIG.MaxGPUInstances
	}
	debugLog("[NVML] nvmlDeviceGetMaxMigDeviceCount -> %d\n", count)
	return count, nvml.SUCCESS
}

// GetMigDeviceHandleByIndex returns a MIG device handle by index.
// Returns NOT_FOUND when no MIG devices exist (MIG disabled or no instances).
// NOT_FOUND (vs NOT_SUPPORTED) signals "no device at this index" which callers
// like nvidia-device-plugin treat as end-of-iteration, not as a fatal error.
func (d *ConfigurableDevice) GetMigDeviceHandleByIndex(index int) (nvml.Device, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetMigDeviceHandleByIndex(%d) -> NOT_FOUND (no MIG devices)\n", index)
	return nil, nvml.ERROR_NOT_FOUND
}

// GetGpmSupport returns whether GPM (GPU Performance Monitoring) is supported.
// Like real NVML, GPM is supported on Hopper and newer; DCGM's profiling
// module keys its NVML-GPM path (the only mockable profiling path — pre-Hopper
// profiling goes through driver-internal perfworks) off this answer. The
// architecture default can be overridden via the gpm.supported config knob.
func (d *ConfigurableDevice) GetGpmSupport() (uint32, nvml.Return) {
	supported := d.Config.Architecture >= nvml.DEVICE_ARCH_HOPPER && d.Config.Architecture != nvml.DEVICE_ARCH_UNKNOWN
	if d.config != nil && d.config.GPM != nil && d.config.GPM.Supported != nil {
		supported = *d.config.GPM.Supported
	}
	val := uint32(0)
	if supported {
		val = 1
	}
	debugLog("[NVML] nvmlGpmQueryDeviceSupport -> %d\n", val)
	return val, nvml.SUCCESS
}

// GetArchitecture returns GPU architecture
func (d *ConfigurableDevice) GetArchitecture() (nvml.DeviceArchitecture, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetArchitecture -> %d\n", d.Config.Architecture)
	return d.Config.Architecture, nvml.SUCCESS
}

// GetCudaComputeCapability returns CUDA compute capability
func (d *ConfigurableDevice) GetCudaComputeCapability() (int, int, nvml.Return) {
	major := d.Config.CudaMajor
	minor := d.Config.CudaMinor
	debugLog("[NVML] nvmlDeviceGetCudaComputeCapability -> %d.%d\n", major, minor)
	return major, minor, nvml.SUCCESS
}

// GetBrand returns device brand
func (d *ConfigurableDevice) GetBrand() (nvml.BrandType, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	debugLog("[NVML] nvmlDeviceGetBrand -> %d\n", d.Config.Brand)
	return d.Config.Brand, nvml.SUCCESS
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

// GetTotalEccErrors returns total ECC errors. Healthy devices report
// zero. When failure injection has tripped into ecc_uncorrectable mode
// the running call counter is surfaced as the uncorrectable count so
// each subsequent NVML poll sees a strictly increasing value (matching
// real hardware accumulating ECC events).
func (d *ConfigurableDevice) GetTotalEccErrors(errorType nvml.MemoryErrorType, counterType nvml.EccCounterType) (uint64, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, ret
	}
	count := uint64(0)
	if d.failure != nil && d.failure.IsECCUncorrectable() && errorType == nvml.MEMORY_ERROR_TYPE_UNCORRECTED {
		count = uint64(d.failure.CallCount())
	}
	debugLog("[NVML] nvmlDeviceGetTotalEccErrors(errType=%d) -> %d\n", errorType, count)
	return count, nvml.SUCCESS
}

// GetMemoryErrorCounter returns the per-location memory-error counter.
// Healthy devices report zero. ecc_uncorrectable mode reports the running
// call count for the uncorrected counter on device memory, mirroring the
// total error count so callers correlating the two queries see a
// consistent view.
func (d *ConfigurableDevice) GetMemoryErrorCounter(errorType nvml.MemoryErrorType, counterType nvml.EccCounterType, locationType nvml.MemoryLocation) (uint64, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, ret
	}
	count := uint64(0)
	if d.failure != nil && d.failure.IsECCUncorrectable() &&
		errorType == nvml.MEMORY_ERROR_TYPE_UNCORRECTED &&
		locationType == nvml.MEMORY_LOCATION_DEVICE_MEMORY {
		count = uint64(d.failure.CallCount())
	}
	debugLog("[NVML] nvmlDeviceGetMemoryErrorCounter(errType=%d loc=%d) -> %d\n", errorType, locationType, count)
	return count, nvml.SUCCESS
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
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, 0, false, false, ret
	}
	corrRows, uncRows := 0, 0
	isPending, failureOccurred := false, false
	if d.config != nil && d.config.RemappedRows != nil {
		corrRows = d.config.RemappedRows.Correctable
		uncRows = d.config.RemappedRows.Uncorrectable
		isPending = d.config.RemappedRows.Pending
		failureOccurred = d.config.RemappedRows.FailureOccurred
	}
	if d.failure != nil && d.failure.IsECCUncorrectable() {
		uncRows++
		failureOccurred = true
	}
	debugLog("[NVML] nvmlDeviceGetRemappedRows -> corr=%d unc=%d pending=%v failure=%v\n", corrRows, uncRows, isPending, failureOccurred)
	return corrRows, uncRows, isPending, failureOccurred, nvml.SUCCESS
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
	case "ada", "ada_lovelace":
		return nvml.DEVICE_ARCH_ADA
	case "hopper":
		return nvml.DEVICE_ARCH_HOPPER
	case "blackwell":
		return nvml.DEVICE_ARCH_BLACKWELL
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

func parseTopologyLevel(level string) nvml.GpuTopologyLevel {
	switch level {
	case "internal":
		return nvml.TOPOLOGY_INTERNAL
	case "single":
		return nvml.TOPOLOGY_SINGLE
	case "multiple":
		return nvml.TOPOLOGY_MULTIPLE
	case "hostbridge":
		return nvml.TOPOLOGY_HOSTBRIDGE
	case "node":
		return nvml.TOPOLOGY_NODE
	case "system":
		return nvml.TOPOLOGY_SYSTEM
	default:
		return nvml.TOPOLOGY_SINGLE
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

// tickFailure advances the device's failure injector by one guarded call
// and returns the NVML return code clients should propagate. SUCCESS means
// "carry on with the normal code path"; any other value should be
// propagated immediately. A nil failure injector (the default) is a fast
// no-op so callers can sprinkle this at the top of every guarded getter
// without measurable overhead.
func (d *ConfigurableDevice) tickFailure() nvml.Return {
	if d == nil || d.failure == nil {
		return nvml.SUCCESS
	}
	d.failure.Tick()
	if d.failure.IsLost() {
		return d.failure.ErrorReturn()
	}
	return nvml.SUCCESS
}

// failureLost returns true once the device has tripped into a lost or
// fallen-off-bus state. Use this at handle-lookup boundaries (where we do
// not want to advance the call counter — Tick has already done so for the
// guarded call that prompted the lookup).
func (d *ConfigurableDevice) failureLost() bool {
	return d != nil && d.failure != nil && d.failure.IsLost()
}

// GetViolationStatus returns the active violation time information for
// a performance policy. The returned struct stays semantically faithful
// to the NVML spec — both ReferenceTime and ViolationTime are reported
// in nanoseconds for power/thermal violations. Failure injection does
// NOT overload these fields with the configured Xid code; instead the
// Xid is surfaced via the NVML event set
// (NVML_EVENT_TYPE_XID_CRITICAL_ERROR) so consumers like dcgm-exporter
// or the device plugin's health monitor see it through the API designed
// for it. We still return ERROR_GPU_IS_LOST for tripped lost /
// fallen_off_bus devices, matching every other guarded getter.
func (d *ConfigurableDevice) GetViolationStatus(perfPolicyType nvml.PerfPolicyType) (nvml.ViolationTime, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.ViolationTime{}, ret
	}
	debugLog("[NVML] nvmlDeviceGetViolationStatus(policy=%d) -> no violation\n", perfPolicyType)
	return nvml.ViolationTime{}, nvml.SUCCESS
}

// MockServer wraps dgxa100.Server and uses configurable devices
type MockServer struct {
	*dgxa100.Server
	configurableDevices [MaxDevices]*ConfigurableDevice

	// visibleDevices maps "visible index" (0, 1, ...) to the actual device
	// index in configurableDevices. When a container has only a subset of
	// /dev/nvidia* nodes (via CDI injection), this slice contains only the
	// devices whose device nodes exist, mimicking real NVML's cgroup-based
	// device filtering. If nil, all devices are visible (no filtering).
	visibleDevices []int
}

// DeviceGetHandleByIndex returns a configurable device by visible index.
// When device visibility filtering is active (container has only a subset
// of /dev/nvidia* nodes), the index maps through the visibility table.
//
// If the resolved device has tripped failure injection into a "lost" or
// "fallen_off_bus" mode, the lookup returns ERROR_GPU_IS_LOST instead of
// a usable handle, matching real NVML behaviour for hardware that has
// dropped off the PCI bus.
func (s *MockServer) DeviceGetHandleByIndex(index int) (nvml.Device, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetHandleByIndex(%d)\n", index)

	// Map through visibility filter if active
	if s.visibleDevices != nil {
		if index < 0 || index >= len(s.visibleDevices) {
			return nil, nvml.ERROR_INVALID_ARGUMENT
		}
		actual := s.visibleDevices[index]
		dev := s.configurableDevices[actual]
		if dev == nil {
			return nil, nvml.ERROR_NOT_FOUND
		}
		if ret := dev.handleLookupReturn(); ret != nvml.SUCCESS {
			return nil, ret
		}
		return dev, nvml.SUCCESS
	}

	if index < 0 || index >= len(s.configurableDevices) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	dev := s.configurableDevices[index]
	if dev == nil {
		return nil, nvml.ERROR_NOT_FOUND
	}
	if ret := dev.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	return dev, nvml.SUCCESS
}

// DeviceGetHandleByUUID returns a configurable device by UUID.
// When device visibility filtering is active, only visible devices are returned.
// Lost devices behave the same as in DeviceGetHandleByIndex.
func (s *MockServer) DeviceGetHandleByUUID(uuid string) (nvml.Device, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetHandleByUUID(%s)\n", uuid)
	for i, dev := range s.configurableDevices {
		if dev != nil && dev.UUID == uuid {
			if !s.isDeviceVisible(i) {
				return nil, nvml.ERROR_NOT_FOUND
			}
			if ret := dev.handleLookupReturn(); ret != nvml.SUCCESS {
				return nil, ret
			}
			return dev, nvml.SUCCESS
		}
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// DeviceGetHandleByPciBusId returns a configurable device by PCI bus ID.
// When device visibility filtering is active, only visible devices are returned.
// Lost devices behave the same as in DeviceGetHandleByIndex.
func (s *MockServer) DeviceGetHandleByPciBusId(pciBusId string) (nvml.Device, nvml.Return) {
	debugLog("[NVML] nvmlDeviceGetHandleByPciBusId(%s)\n", pciBusId)
	for i, dev := range s.configurableDevices {
		if dev != nil && dev.PciBusID == pciBusId {
			if !s.isDeviceVisible(i) {
				return nil, nvml.ERROR_NOT_FOUND
			}
			if ret := dev.handleLookupReturn(); ret != nvml.SUCCESS {
				return nil, ret
			}
			return dev, nvml.SUCCESS
		}
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// handleLookupReturn returns the NVML return code that a handle-lookup
// path should propagate when the device has tripped failure injection.
// SUCCESS means the device is still reachable. We deliberately do not
// Tick() the failure injector here because handle lookups are not
// guarded NVML operations on real hardware — they simply observe state
// already produced by the actual API calls below. Otherwise a workload
// that resolves a handle once and reuses it would never see the
// scheduled "after_calls" failure.
func (d *ConfigurableDevice) handleLookupReturn() nvml.Return {
	if d == nil || d.failure == nil {
		return nvml.SUCCESS
	}
	if d.failureLost() {
		return d.failure.ErrorReturn()
	}
	return nvml.SUCCESS
}

// isDeviceVisible returns true if the given device index is in the visible set.
// When visibleDevices is nil (no filtering), all devices are visible.
func (s *MockServer) isDeviceVisible(deviceIndex int) bool {
	if s.visibleDevices == nil {
		return true
	}
	return slices.Contains(s.visibleDevices, deviceIndex)
}
