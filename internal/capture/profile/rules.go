// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package profile

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// This file is the mapping from capture to profile. Each line names a profile
// key and the capture value it is read from, in the order the profile lists
// them, so reviewing a generated profile means reading this file beside it.
//
// The policy is in the verb:
//
//	prop        a property of the board: written once when every GPU agrees,
//	            once per device under devices[] when they differ (an error
//	            for a key the engine reads only from device_defaults), and
//	            left out with a comment when the driver does not answer.
//	propOrNA    prop for keys that spell "not supported" as "N/A".
//	reading     moves with load or time: the capture's idle reading (the
//	            lower median when GPUs differ), never -base's.
//	remainder   a reading that is the rest of a total, such as free memory:
//	            the total less the parts written before it, so they add up,
//	            per GPU; a used reading a GPU has no room for is lowered.
//	derive      computed per GPU from several values; the comment shows each
//	            GPU's working and the lower median is written.
//	placeholder an identifier the capture scrubbed: -base's, else the
//	            capture's placeholder.
//	preferBase  describes the mock's behaviour more than the board: -base's
//	            value, else the capture's.
//	fromBase    not in a capture at all: copied from -base, else noted.
//	ident, perDevice  name one device, so always under devices[].
//
// getter(call("GetClockInfo", nvml.CLOCK_SM)) reads the dump entry
// "GetClockInfo(1)", the call with that argument; getter("GetMemoryInfo_v2",
// "Total") reads a field of an answer and getter("GetEccMode", 1) an element.

func (g *generator) systemRules(s *section) {
	s.prop("driver_version", sysGetter("SystemGetDriverVersion"))
	s.prop("nvml_version", sysGetter("SystemGetNVMLVersion"))
	s.prop("cuda_version", sysGetter("SystemGetCudaDriverVersion").as(cudaVersion))
	s.prop("cuda_version_major", sysGetter("SystemGetCudaDriverVersion").as(cudaMajor))
	s.prop("cuda_version_minor", sysGetter("SystemGetCudaDriverVersion").as(cudaMinor))
	s.prop("product_name", hostValue(dmiFile, "product_name"))
	// The profile holds only what the capture answers, so a feature it leaves
	// out is one the node does not support.
	s.value("absent_features", quote("not_supported"),
		"a feature this profile leaves out answers NOT_SUPPORTED, as on the captured node", ruleGenerated)
}

// deviceRules sets what names each device: these keys always sit under
// devices[], ahead of any property that turns out to differ per GPU.
func (g *generator) deviceRules() {
	g.ident("uuid", nil, getter("GetUUID"))
	g.ident("serial", nil, getter("GetSerial"))
	g.perDevice("bus_id", []string{"pci"}, getter("GetPciInfo", "BusIdLegacy").as(cText))
	g.perDevice("minor_number", nil, getter("GetMinorNumber"))
	if g.c2c() {
		// With the CPU attached over NVLink-C2C, the GPU's NUMA node is the
		// CPU it pairs with.
		g.perDevice("grace_cpu_pair", nil, gpuNUMANode())
	}
	g.perDevice("module_id", []string{"platform"}, getter("GetPlatformInfo", "ModuleId"))
}

func (g *generator) deviceDefaultRules(s *section) {
	s.prop("name", getter("GetName"))
	s.prop("brand", getter("GetBrand").as(named(brandNames)))
	s.prop("board_part_number", getter("GetBoardPartNumber"))
	s.prop("vbios_version", getter("GetVbiosVersion"))
	s.prop("hostname", getter("GetHostname_v1"))
	s.prop("architecture", getter("GetArchitecture").as(archName))
	cc := s.sub("compute_capability")
	cc.prop("major", getter("GetCudaComputeCapability", 0))
	cc.prop("minor", getter("GetCudaComputeCapability", 1))
	s.prop("num_gpu_cores", getter("GetNumGpuCores"))

	fabric := s.sub("fabric")
	fabric.placeholder("cluster_uuid", getter("GetGpuFabricInfo", "ClusterUuid").as(uuidBytes))
	fabric.prop("clique_id", getter("GetGpuFabricInfo", "CliqueId"))
	// "auto" ties the state to the mock fabric manager's readiness, which a
	// cluster deployment wants; -base supplies it, the capture says completed.
	fabric.preferBase("state", getter("GetGpuFabricInfo", "State").as(named(fabricStates)))

	platform := s.sub("platform")
	platform.placeholder("chassis_serial_number", getter("GetPlatformInfo", "ChassisSerialNumber").as(cText))
	platform.prop("slot_number", getter("GetPlatformInfo", "SlotNumber"))
	platform.prop("tray_index", getter("GetPlatformInfo", "TrayIndex"))
	platform.prop("host_id", getter("GetPlatformInfo", "HostId"))
	platform.prop("peer_type", getter("GetPlatformInfo", "PeerType").as(named(peerTypes)))

	inforom := s.sub("inforom")
	inforom.prop("image_version", getter("GetInforomImageVersion"))
	inforom.prop("oem_object", getter(call("GetInforomVersion", nvml.INFOROM_OEM)))
	inforom.prop("ecc_object", getter(call("GetInforomVersion", nvml.INFOROM_ECC)))
	inforom.prop("pwr_object", getter(call("GetInforomVersion", nvml.INFOROM_POWER)))

	memory := s.sub("memory")
	memory.prop("total_bytes", getter("GetMemoryInfo_v2", "Total"))
	memory.prop("reserved_bytes", getter("GetMemoryInfo_v2", "Reserved"))
	memory.reading("used_bytes", getter("GetMemoryInfo_v2", "Used"))
	memory.remainder("free_bytes", getter("GetMemoryInfo_v2", "Free"), "total_bytes", "reserved_bytes", "used_bytes")
	memory.prop("memory_bus_width", getter("GetMemoryBusWidth"))

	bar1 := s.sub("bar1_memory")
	bar1.prop("total_bytes", getter("GetBAR1MemoryInfo", "Bar1Total"))
	bar1.reading("used_bytes", getter("GetBAR1MemoryInfo", "Bar1Used"))
	bar1.remainder("free_bytes", getter("GetBAR1MemoryInfo", "Bar1Free"), "total_bytes", "used_bytes")

	pci := s.sub("pci")
	pci.prop("device_id", pciDeviceID)
	pci.prop("subsystem_id", getter("GetPciInfo", "PciSubSystemId").as(hex32))

	pcie := s.sub("pcie")
	pcie.prop("max_link_gen", getter("GetMaxPcieLinkGeneration"))
	pcie.prop("current_link_gen", getter("GetCurrPcieLinkGeneration"))
	pcie.prop("max_link_width", getter("GetMaxPcieLinkWidth"))
	pcie.prop("current_link_width", getter("GetCurrPcieLinkWidth"))
	pcie.prop("replay_counter", getter("GetPcieReplayCounter"))
	pcie.reading("tx_throughput_kbps", getter(call("GetPcieThroughput", nvml.PCIE_UTIL_TX_BYTES)))
	pcie.reading("rx_throughput_kbps", getter(call("GetPcieThroughput", nvml.PCIE_UTIL_RX_BYTES)))

	power := s.sub("power")
	power.prop("management_supported", answered("GetPowerManagementMode"))
	power.prop("management_mode", getter("GetPowerManagementMode").as(enabledName))
	power.prop("default_limit_mw", getter("GetPowerManagementDefaultLimit"))
	power.prop("enforced_limit_mw", getter("GetEnforcedPowerLimit"))
	power.prop("min_limit_mw", getter("GetPowerManagementLimitConstraints", 0))
	power.prop("max_limit_mw", getter("GetPowerManagementLimitConstraints", 1))
	power.reading("current_draw_mw", getter("GetPowerUsage"))
	power.reading("power_state", getter("GetPowerState").as(pstate))
	power.reading("total_energy_consumption_mj", getter("GetTotalEnergyConsumption"))
	power.prop("power_mizer_mode", getter("GetPowerMizerMode_v1", "CurrentMode").as(named(powerMizerModes)))
	power.prop("supported_power_mizer_modes", getter("GetPowerMizerMode_v1", "SupportedPowerMizerModes").as(namedBits(powerMizerModes)))

	thermal := s.sub("thermal")
	// GPU 0's, like the T.Limit values the mock's margin adds up with.
	temp := thermal.gpu0("temperature_gpu_c", gpuTemperature, true)
	thermal.reading("temperature_memory_c", field(nvml.FI_DEV_MEMORY_TEMP))
	thermal.prop("target_temperature_c", smi("temperature/gpu_target_temperature").as(celsius))
	// shutdown_threshold_c, slowdown_threshold_c, max_operating_c and the
	// tlimit block, from GPU 0's T.Limit readings: see thermal.go.
	g.thermalLimits(thermal, temp)

	fan := s.sub("fan")
	fan.prop("count", getter("GetNumFans"))
	fan.propOrNA("speed_percent", getter("GetFanSpeed").as(quotedNumber))
	fan.propOrNA("target_speed_percent", getter(call("GetTargetFanSpeed", 0)).as(quotedNumber))

	clocks := s.sub("clocks")
	for _, c := range []struct {
		prefix string
		clock  nvml.ClockType
	}{{"graphics", nvml.CLOCK_GRAPHICS}, {"sm", nvml.CLOCK_SM}, {"memory", nvml.CLOCK_MEM}, {"video", nvml.CLOCK_VIDEO}} {
		clocks.reading(c.prefix+"_current", getter(call("GetClockInfo", c.clock)))
		clocks.prop(c.prefix+"_max", getter(call("GetMaxClockInfo", c.clock)))
		if c.clock == nvml.CLOCK_GRAPHICS || c.clock == nvml.CLOCK_MEM {
			clocks.prop(c.prefix+"_app", getter(call("GetApplicationsClock", c.clock)))
			clocks.prop(c.prefix+"_app_default", getter(call("GetDefaultApplicationsClock", c.clock)))
		}
	}
	offsets := clocks.sub("offsets")
	graphicsOffset := offsets.sub("graphics")
	graphicsOffset.prop("min_mhz", getter("GetGpcClkMinMaxVfOffset", 0))
	graphicsOffset.prop("max_mhz", getter("GetGpcClkMinMaxVfOffset", 1))
	graphicsOffset.prop("offset_mhz", getter("GetGpcClkVfOffset"))
	memoryOffset := offsets.sub("memory")
	memoryOffset.prop("min_mhz", getter("GetMemClkMinMaxVfOffset", 0))
	memoryOffset.prop("max_mhz", getter("GetMemClkMinMaxVfOffset", 1))
	memoryOffset.prop("offset_mhz", getter("GetMemClkVfOffset"))
	clocks.prop("adaptive_clocking", getter("GetAdaptiveClockInfoStatus").as(enabledName))

	reasons := s.sub("clocks_throttle_reasons")
	for _, r := range []struct {
		key  string
		mask int64
	}{
		{"gpu_idle", nvml.ClocksEventReasonGpuIdle},
		{"applications_clocks_setting", nvml.ClocksEventReasonApplicationsClocksSetting},
		{"sw_power_cap", nvml.ClocksEventReasonSwPowerCap},
		{"hw_slowdown", nvml.ClocksThrottleReasonHwSlowdown},
		{"hw_thermal_slowdown", nvml.ClocksThrottleReasonHwThermalSlowdown},
		{"hw_power_brake_slowdown", nvml.ClocksThrottleReasonHwPowerBrakeSlowdown},
		{"sync_boost", nvml.ClocksEventReasonSyncBoost},
		{"sw_thermal_slowdown", nvml.ClocksEventReasonSwThermalSlowdown},
		{"display_clocks_setting", nvml.ClocksEventReasonDisplayClockSetting},
	} {
		reasons.reading(r.key, getter("GetCurrentClocksEventReasons").as(bit(r.mask)))
	}

	g.supportedClocks(s)
	s.reading("performance_state", getter("GetPerformanceState").as(pstate))

	utilization := s.sub("utilization")
	utilization.reading("gpu", getter("GetUtilizationRates", "Gpu"))
	utilization.reading("memory", getter("GetUtilizationRates", "Memory"))
	utilization.reading("encoder", getter("GetEncoderUtilization", 0))
	utilization.reading("decoder", getter("GetDecoderUtilization", 0))
	utilization.reading("jpeg", getter("GetJpgUtilization", 0))
	utilization.reading("ofa", getter("GetOfaUtilization", 0))
	s.fromBase("dynamic_metrics", "how the mock varies its readings over time")

	encoder := s.sub("encoder_stats")
	encoder.reading("session_count", getter("GetEncoderStats", 0))
	encoder.reading("average_fps", getter("GetEncoderStats", 1))
	encoder.reading("average_latency_us", getter("GetEncoderStats", 2))
	fbc := s.sub("fbc_stats")
	fbc.reading("session_count", getter("GetFBCStats", "SessionsCount"))
	fbc.reading("average_fps", getter("GetFBCStats", "AverageFPS"))
	fbc.reading("average_latency_us", getter("GetFBCStats", "AverageLatency"))

	ecc := s.sub("ecc")
	ecc.prop("mode_current", getter("GetEccMode", 0).as(enabledName))
	ecc.prop("mode_pending", getter("GetEccMode", 1).as(enabledName))
	ecc.prop("default_mode", getter("GetDefaultEccMode").as(enabledName))
	errors := ecc.sub("errors")
	for _, counter := range []struct {
		key string
		typ nvml.EccCounterType
	}{{"volatile", nvml.VOLATILE_ECC}, {"aggregate", nvml.AGGREGATE_ECC}} {
		for _, kind := range []struct {
			key string
			typ nvml.MemoryErrorType
		}{{"single_bit", nvml.MEMORY_ERROR_TYPE_CORRECTED}, {"double_bit", nvml.MEMORY_ERROR_TYPE_UNCORRECTED}} {
			k := errors.sub(counter.key).sub(kind.key)
			k.prop("device_memory", getter(call("GetDetailedEccErrors", kind.typ, counter.typ), "DeviceMemory"))
			k.prop("l1_cache", getter(call("GetDetailedEccErrors", kind.typ, counter.typ), "L1Cache"))
			k.prop("l2_cache", getter(call("GetDetailedEccErrors", kind.typ, counter.typ), "L2Cache"))
			k.prop("register_file", getter(call("GetDetailedEccErrors", kind.typ, counter.typ), "RegisterFile"))
			k.prop("total", getter(call("GetTotalEccErrors", kind.typ, counter.typ)))
		}
	}
	sram := ecc.sub("sram")
	for _, scope := range []struct{ key, field string }{{"volatile", "Volatile"}, {"aggregate", "Aggregate"}} {
		c := sram.sub(scope.key)
		c.prop("correctable", getter("GetSramEccErrorStatus", scope.field+"Cor"))
		c.prop("uncorrectable_parity", getter("GetSramEccErrorStatus", scope.field+"UncParity"))
		c.prop("uncorrectable_secded", getter("GetSramEccErrorStatus", scope.field+"UncSecDed"))
	}
	sources := sram.sub("uncorrectable_sources")
	sources.prop("l2", getter("GetSramEccErrorStatus", "AggregateUncBucketL2"))
	sources.prop("sm", getter("GetSramEccErrorStatus", "AggregateUncBucketSm"))
	sources.prop("microcontroller", getter("GetSramEccErrorStatus", "AggregateUncBucketMcu"))
	sources.prop("pcie", getter("GetSramEccErrorStatus", "AggregateUncBucketPcie"))
	sources.prop("other", getter("GetSramEccErrorStatus", "AggregateUncBucketOther"))
	sram.prop("threshold_exceeded", getter("GetSramEccErrorStatus", "BThresholdExceeded").as(bit(1)))
	dram := ecc.sub("dram_encryption")
	dram.prop("mode_current", getter("GetDramEncryptionMode", 0, "EncryptionState").as(enabledName))
	dram.prop("mode_pending", getter("GetDramEncryptionMode", 1, "EncryptionState").as(enabledName))

	retired := s.sub("retired_pages")
	retired.sub("single_bit_retirement").prop("count", getter(call("GetRetiredPages", nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS)).as(length))
	retired.sub("double_bit_retirement").prop("count", getter(call("GetRetiredPages", nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR)).as(length))
	retired.prop("pending_blacklist", getter("GetRetiredPagesPendingStatus").as(bit(1)))
	retired.prop("pending_retirement", getter("GetRetiredPagesPendingStatus").as(bit(1)))

	remapped := s.sub("remapped_rows")
	remapped.prop("correctable", getter("GetRemappedRows", 0))
	remapped.prop("uncorrectable", getter("GetRemappedRows", 1))
	remapped.prop("pending", getter("GetRemappedRows", 2))
	remapped.prop("failure_occurred", getter("GetRemappedRows", 3))
	histogram := remapped.sub("availability_histogram")
	histogram.prop("max", getter("GetRowRemapperHistogram", "Max"))
	histogram.prop("high", getter("GetRowRemapperHistogram", "High"))
	histogram.prop("partial", getter("GetRowRemapperHistogram", "Partial"))
	histogram.prop("low", getter("GetRowRemapperHistogram", "Low"))
	histogram.prop("none", getter("GetRowRemapperHistogram", "None"))

	display := s.sub("display")
	display.prop("mode", getter("GetDisplayMode").as(enabledName))
	display.prop("active", getter("GetDisplayActive").as(enabledName))
	s.prop("persistence_mode", getter("GetPersistenceMode").as(enabledName))
	s.prop("compute_mode", getter("GetComputeMode").as(named(computeModes)))
	s.prop("addressing_mode", getter("GetAddressingMode", "Value").as(named(addressingModes)))
	s.prop("egm", getter("GetCapabilities", "CapMask").as(bit(nvml.DEV_CAP_EGM)))

	mig := s.sub("mig")
	mig.prop("mode_current", getter("GetMigMode", 0).as(enabledName))
	mig.prop("mode_pending", getter("GetMigMode", 1).as(enabledName))
	mig.derive("max_gpu_instances", "largest InstanceCount of GetGpuInstanceProfileInfo(*)", g.maxGPUInstances)

	gom := s.sub("gpu_operation_mode")
	gom.prop("current", getter("GetGpuOperationMode", 0).as(named(gomNames)))
	gom.prop("pending", getter("GetGpuOperationMode", 1).as(named(gomNames)))
	driverModel := s.sub("driver_model")
	driverModel.propOrNA("current", getter("GetDriverModel_v2", 0).as(named(nil)))
	driverModel.propOrNA("pending", getter("GetDriverModel_v2", 1).as(named(nil)))
	accounting := s.sub("accounting")
	accounting.prop("mode", getter("GetAccountingMode").as(enabledName))
	accounting.prop("buffer_size", getter("GetAccountingBufferSize"))
	virtualization := s.sub("virtualization")
	virtualization.prop("mode", getter("GetVirtualizationMode").as(named(virtualizationModes)))
	virtualization.propOrNA("host_vgpu_mode", getter("GetHostVgpuMode").as(named(hostVGPUModes)))
	gsp := s.sub("gsp_firmware")
	gsp.prop("mode", getter("GetGspFirmwareMode", 0).as(enabledName))
	gsp.prop("version", getter("GetGspFirmwareVersion"))

	s.fromBase("features", "marketing capabilities no getter reports")
	// type, cores, memory_gb and coherent_memory: see cpu in aggregate.go.
	g.cpu(s)
	g.processes(s)
}

func (g *generator) nvlinkRules(s *section) {
	g.nvlinkVersion(s)
	g.nvlinkLinks(s)
	g.nvlinkBandwidth(s)
	s.prop("c2c_enabled", smi("c2c_mode").as(oneOf(map[string]string{"Enabled": "true", "Disabled": "false"})))
	// GPUs that reach their peers through NVSwitch trays sit in an NVLink
	// domain wider than the node, which is what IMEX and ComputeDomains need.
	s.prop("multi_node", getter("GetPlatformInfo", "PeerType").as(equals(peerTypeSwitchConnected)))
	g.nvlinkBwMode(s)
	s.fromBase("switches", "NVSwitches sit in switch trays outside the captured node")
}

func (g *generator) infinibandRules(s *section) {
	all := rdmaDevices(g.c.files[ibSysfsFile], g.c.files[ibstatFile])
	if len(all) == 0 {
		s.m.note("left out: the capture lists no RDMA devices (" + ibSysfsFile + ")")
		return
	}
	// A virtual function is a slice of a NIC already counted; counted itself,
	// its own hca_type can outnumber the NICs'.
	devs, vfs, why := g.physicalFunctions(all)
	if why != "" {
		s.m.note("left out: " + why)
		return
	}
	if len(devs) == 0 {
		s.m.note(fmt.Sprintf("left out: all %s in %s are virtual functions (physfn in %s), so the capture shows no NIC",
			countOf(vfs, "RDMA device"), ibSysfsFile, nicFunctions))
		return
	}
	enabled := countOf(len(devs), "RDMA device") + " in " + ibSysfsFile + "; no virtual functions in " + nicFunctions
	if vfs > 0 {
		enabled = fmt.Sprintf("%s in %s, not counting %s (physfn in %s)",
			countOf(len(devs), "RDMA device"), ibSysfsFile, countOf(vfs, "virtual function"), nicFunctions)
	}
	s.value("enabled", boolean(true), enabled, ruleSame)
	// A profile models one NIC type: the one most devices are.
	hca, n, others, err := majority(devs, func(d rdmaDevice) string { return d.fields["hca_type"] })
	if err != nil {
		g.errs = append(g.errs, fmt.Errorf("infiniband.hca_type: %w", err))
		return
	}
	s.value("hca_type", quote(hca), ibSysfsFile+" hca_type of "+countOf(n, "device")+" of "+itoa(len(devs))+others, ruleSame)
	g.nicValues(s, hca, filter(devs, func(d rdmaDevice) bool { return d.fields["hca_type"] == hca }))
	if gpus := g.c.gpuCount(); len(devs)%gpus == 0 {
		s.value("hcas_per_gpu", integer(len(devs)/gpus), itoa(len(devs))+" RDMA devices / "+itoa(gpus)+" GPUs", ruleSame)
	} else {
		s.value("hca_count", integer(len(devs)), countOf(len(devs), "RDMA device"), ruleSame)
	}
	s.fromBase("guid_prefix", "the capture scrubs GUIDs")
	s.fromBase("node_desc_template", "the mock's node description naming")
}

// nicValues writes the values of the NIC type the profile models, each the
// one most of its devices report.
func (g *generator) nicValues(s *section, hca string, devs []rdmaDevice) {
	for _, k := range []struct {
		key, from string
		of        func(rdmaDevice) string
		number    bool
	}{
		{"fw_version", ibSysfsFile + " fw_ver", func(d rdmaDevice) string { return d.fields["fw_ver"] }, false},
		{"hw_rev", ibstatFile + " Hardware version", func(d rdmaDevice) string { return d.hwRev }, false},
		{"board_id", ibSysfsFile + " board_id", func(d rdmaDevice) string { return d.fields["board_id"] }, false},
		{"link_layer", ibSysfsFile + " port layer", func(d rdmaDevice) string { return d.linkLayer }, false},
		{"rate_gbps", ibSysfsFile + " port rate", func(d rdmaDevice) string { return itoa(d.rateGbps) }, true},
		{"port_state", ibSysfsFile + " port state", func(d rdmaDevice) string { return d.portState }, false},
		{"phys_state", ibSysfsFile + " port phys", func(d rdmaDevice) string { return d.physState }, false},
	} {
		v, _, _, err := majority(devs, k.of)
		switch {
		case err != nil:
			g.errs = append(g.errs, fmt.Errorf("infiniband.%s of the %s devices: %w", k.key, hca, err))
		case v == "":
			s.m.note(k.key + ": left out, " + k.from + " has no value for the " + hca + " devices")
		case k.number:
			s.value(k.key, scalar{v}, k.from+" of the "+hca+" devices", ruleSame)
		default:
			s.value(k.key, quote(v), k.from+" of the "+hca+" devices", ruleSame)
		}
	}
}
