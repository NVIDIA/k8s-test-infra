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
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
)

// invalidDevice wraps mock.Device with all methods returning ERROR_INVALID_ARGUMENT.
// Used as a null-object pattern when LookupDevice fails to find a valid device.
// This eliminates nil checks throughout the bridge layer.
type invalidDevice struct {
	mock.Device
}

// Ensure invalidDevice implements nvml.Device
var _ nvml.Device = (*invalidDevice)(nil)

// InvalidDeviceInstance is the singleton returned for failed device lookups
var InvalidDeviceInstance nvml.Device = newInvalidDevice()

func newInvalidDevice() *invalidDevice {
	d := &invalidDevice{}

	d.ClearAccountingPidsFunc = func() nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ClearCpuAffinityFunc = func() nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ClearEccErrorCountsFunc = func(_ nvml.EccCounterType) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ClearFieldValuesFunc = func(_ []nvml.FieldValue) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.CreateGpuInstanceFunc = func(_ *nvml.GpuInstanceProfileInfo) (nvml.GpuInstance, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.CreateGpuInstanceWithPlacementFunc = func(_ *nvml.GpuInstanceProfileInfo, _ *nvml.GpuInstancePlacement) (nvml.GpuInstance, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.FreezeNvLinkUtilizationCounterFunc = func(_ int, _ int, _ nvml.EnableState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAPIRestrictionFunc = func(_ nvml.RestrictedAPI) (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAccountingBufferSizeFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAccountingModeFunc = func() (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAccountingPidsFunc = func() ([]int, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAccountingStatsFunc = func(_ uint32) (nvml.AccountingStats, nvml.Return) {
		return nvml.AccountingStats{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetActiveVgpusFunc = func() ([]nvml.VgpuInstance, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAdaptiveClockInfoStatusFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAddressingModeFunc = func() (nvml.DeviceAddressingMode, nvml.Return) {
		return nvml.DeviceAddressingMode{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetApplicationsClockFunc = func(_ nvml.ClockType) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetArchitectureFunc = func() (nvml.DeviceArchitecture, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAttributesFunc = func() (nvml.DeviceAttributes, nvml.Return) {
		return nvml.DeviceAttributes{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetAutoBoostedClocksEnabledFunc = func() (nvml.EnableState, nvml.EnableState, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetBAR1MemoryInfoFunc = func() (nvml.BAR1Memory, nvml.Return) {
		return nvml.BAR1Memory{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetBoardIdFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetBoardPartNumberFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetBrandFunc = func() (nvml.BrandType, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetBridgeChipInfoFunc = func() (nvml.BridgeChipHierarchy, nvml.Return) {
		return nvml.BridgeChipHierarchy{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetBusTypeFunc = func() (nvml.BusType, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetC2cModeInfoVFunc = func() nvml.C2cModeInfoHandler {
		return nvml.C2cModeInfoHandler{}
	}
	d.GetCapabilitiesFunc = func() (nvml.DeviceCapabilities, nvml.Return) {
		return nvml.DeviceCapabilities{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetClkMonStatusFunc = func() (nvml.ClkMonStatus, nvml.Return) {
		return nvml.ClkMonStatus{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetClockFunc = func(_ nvml.ClockType, _ nvml.ClockId) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetClockInfoFunc = func(_ nvml.ClockType) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetClockOffsetsFunc = func() (nvml.ClockOffset, nvml.Return) {
		return nvml.ClockOffset{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetComputeInstanceIdFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetComputeModeFunc = func() (nvml.ComputeMode, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetConfComputeGpuAttestationReportFunc = func(_ *nvml.ConfComputeGpuAttestationReport) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetConfComputeGpuCertificateFunc = func() (nvml.ConfComputeGpuCertificate, nvml.Return) {
		return nvml.ConfComputeGpuCertificate{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetConfComputeMemSizeInfoFunc = func() (nvml.ConfComputeMemSizeInfo, nvml.Return) {
		return nvml.ConfComputeMemSizeInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetConfComputeProtectedMemoryUsageFunc = func() (nvml.Memory, nvml.Return) {
		return nvml.Memory{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCoolerInfoFunc = func() (nvml.CoolerInfo, nvml.Return) {
		return nvml.CoolerInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCpuAffinityFunc = func(_ int) ([]uint, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCpuAffinityWithinScopeFunc = func(_ int, _ nvml.AffinityScope) ([]uint, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCreatableVgpusFunc = func() ([]nvml.VgpuTypeId, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCudaComputeCapabilityFunc = func() (int, int, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCurrPcieLinkGenerationFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCurrPcieLinkWidthFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCurrentClockFreqsFunc = func() (nvml.DeviceCurrentClockFreqs, nvml.Return) {
		return nvml.DeviceCurrentClockFreqs{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCurrentClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetCurrentClocksThrottleReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDecoderUtilizationFunc = func() (uint32, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDefaultApplicationsClockFunc = func(_ nvml.ClockType) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDefaultEccModeFunc = func() (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDetailedEccErrorsFunc = func(_ nvml.MemoryErrorType, _ nvml.EccCounterType) (nvml.EccErrorCounts, nvml.Return) {
		return nvml.EccErrorCounts{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDeviceHandleFromMigDeviceHandleFunc = func() (nvml.Device, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDisplayActiveFunc = func() (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDisplayModeFunc = func() (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDramEncryptionModeFunc = func() (nvml.DramEncryptionInfo, nvml.DramEncryptionInfo, nvml.Return) {
		return nvml.DramEncryptionInfo{}, nvml.DramEncryptionInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDriverModelFunc = func() (nvml.DriverModel, nvml.DriverModel, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDriverModel_v2Func = func() (nvml.DriverModel, nvml.DriverModel, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetDynamicPstatesInfoFunc = func() (nvml.GpuDynamicPstatesInfo, nvml.Return) {
		return nvml.GpuDynamicPstatesInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetEccModeFunc = func() (nvml.EnableState, nvml.EnableState, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetEncoderCapacityFunc = func(_ nvml.EncoderType) (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetEncoderSessionsFunc = func() ([]nvml.EncoderSessionInfo, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetEncoderStatsFunc = func() (int, uint32, uint32, nvml.Return) {
		return 0, 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetEncoderUtilizationFunc = func() (uint32, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetEnforcedPowerLimitFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetFBCSessionsFunc = func() ([]nvml.FBCSessionInfo, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetFBCStatsFunc = func() (nvml.FBCStats, nvml.Return) {
		return nvml.FBCStats{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetFanControlPolicy_v2Func = func(_ int) (nvml.FanControlPolicy, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetFanSpeedFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetFanSpeedRPMFunc = func() (nvml.FanSpeedInfo, nvml.Return) {
		return nvml.FanSpeedInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetFanSpeed_v2Func = func(_ int) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetFieldValuesFunc = func(_ []nvml.FieldValue) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpcClkMinMaxVfOffsetFunc = func() (int, int, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpcClkVfOffsetFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuFabricInfoFunc = func() (nvml.GpuFabricInfo, nvml.Return) {
		return nvml.GpuFabricInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuFabricInfoVFunc = func() nvml.GpuFabricInfoHandler {
		return nvml.GpuFabricInfoHandler{}
	}
	d.GetGpuInstanceByIdFunc = func(_ int) (nvml.GpuInstance, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuInstanceIdFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuInstancePossiblePlacementsFunc = func(_ *nvml.GpuInstanceProfileInfo) ([]nvml.GpuInstancePlacement, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuInstanceProfileInfoFunc = func(_ int) (nvml.GpuInstanceProfileInfo, nvml.Return) {
		return nvml.GpuInstanceProfileInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuInstanceProfileInfoByIdVFunc = func(_ int) nvml.GpuInstanceProfileInfoByIdHandler {
		return nvml.GpuInstanceProfileInfoByIdHandler{}
	}
	d.GetGpuInstanceProfileInfoVFunc = func(_ int) nvml.GpuInstanceProfileInfoHandler {
		return nvml.GpuInstanceProfileInfoHandler{}
	}
	d.GetGpuInstanceRemainingCapacityFunc = func(_ *nvml.GpuInstanceProfileInfo) (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuInstancesFunc = func(_ *nvml.GpuInstanceProfileInfo) ([]nvml.GpuInstance, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuMaxPcieLinkGenerationFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGpuOperationModeFunc = func() (nvml.GpuOperationMode, nvml.GpuOperationMode, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGraphicsRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGridLicensableFeaturesFunc = func() (nvml.GridLicensableFeatures, nvml.Return) {
		return nvml.GridLicensableFeatures{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGspFirmwareModeFunc = func() (bool, bool, nvml.Return) {
		return false, false, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetGspFirmwareVersionFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetHostVgpuModeFunc = func() (nvml.HostVgpuMode, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetIndexFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetInforomConfigurationChecksumFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetInforomImageVersionFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetInforomVersionFunc = func(_ nvml.InforomObject) (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetIrqNumFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetJpgUtilizationFunc = func() (uint32, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetLastBBXFlushTimeFunc = func() (uint64, uint, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMPSComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMarginTemperatureFunc = func() (nvml.MarginTemperature, nvml.Return) {
		return nvml.MarginTemperature{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMaxClockInfoFunc = func(_ nvml.ClockType) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMaxCustomerBoostClockFunc = func(_ nvml.ClockType) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMaxMigDeviceCountFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMaxPcieLinkGenerationFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMaxPcieLinkWidthFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMemClkMinMaxVfOffsetFunc = func() (int, int, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMemClkVfOffsetFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMemoryAffinityFunc = func(_ int, _ nvml.AffinityScope) ([]uint, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMemoryBusWidthFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMemoryErrorCounterFunc = func(_ nvml.MemoryErrorType, _ nvml.EccCounterType, _ nvml.MemoryLocation) (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMemoryInfoFunc = func() (nvml.Memory, nvml.Return) {
		return nvml.Memory{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMemoryInfo_v2Func = func() (nvml.Memory_v2, nvml.Return) {
		return nvml.Memory_v2{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMigDeviceHandleByIndexFunc = func(_ int) (nvml.Device, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMigModeFunc = func() (int, int, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMinMaxClockOfPStateFunc = func(_ nvml.ClockType, _ nvml.Pstates) (uint32, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMinMaxFanSpeedFunc = func() (int, int, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMinorNumberFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetModuleIdFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetMultiGpuBoardFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNameFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNumFansFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNumGpuCoresFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNumaNodeIdFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkCapabilityFunc = func(_ int, _ nvml.NvLinkCapability) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkErrorCounterFunc = func(_ int, _ nvml.NvLinkErrorCounter) (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkInfoFunc = func() nvml.NvLinkInfoHandler {
		return nvml.NvLinkInfoHandler{}
	}
	d.GetNvLinkRemoteDeviceTypeFunc = func(_ int) (nvml.IntNvLinkDeviceType, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkRemotePciInfoFunc = func(_ int) (nvml.PciInfo, nvml.Return) {
		return nvml.PciInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkStateFunc = func(_ int) (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkUtilizationControlFunc = func(_ int, _ int) (nvml.NvLinkUtilizationControl, nvml.Return) {
		return nvml.NvLinkUtilizationControl{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkUtilizationCounterFunc = func(_ int, _ int) (uint64, uint64, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvLinkVersionFunc = func(_ int) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvlinkBwModeFunc = func() (nvml.NvlinkGetBwMode, nvml.Return) {
		return nvml.NvlinkGetBwMode{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetNvlinkSupportedBwModesFunc = func() (nvml.NvlinkSupportedBwModes, nvml.Return) {
		return nvml.NvlinkSupportedBwModes{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetOfaUtilizationFunc = func() (uint32, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetP2PStatusFunc = func(_ nvml.Device, _ nvml.GpuP2PCapsIndex) (nvml.GpuP2PStatus, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPciInfoFunc = func() (nvml.PciInfo, nvml.Return) {
		return nvml.PciInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPciInfoExtFunc = func() (nvml.PciInfoExt, nvml.Return) {
		return nvml.PciInfoExt{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPcieLinkMaxSpeedFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPcieReplayCounterFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPcieSpeedFunc = func() (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPcieThroughputFunc = func(_ nvml.PcieUtilCounter) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPdiFunc = func() (nvml.Pdi, nvml.Return) {
		return nvml.Pdi{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPerformanceModesFunc = func() (nvml.DevicePerfModes, nvml.Return) {
		return nvml.DevicePerfModes{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPerformanceStateFunc = func() (nvml.Pstates, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPersistenceModeFunc = func() (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPgpuMetadataStringFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPlatformInfoFunc = func() (nvml.PlatformInfo, nvml.Return) {
		return nvml.PlatformInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerManagementDefaultLimitFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerManagementLimitFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerManagementLimitConstraintsFunc = func() (uint32, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerManagementModeFunc = func() (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerMizerMode_v1Func = func() (nvml.DevicePowerMizerModes_v1, nvml.Return) {
		return nvml.DevicePowerMizerModes_v1{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerSourceFunc = func() (nvml.PowerSource, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerStateFunc = func() (nvml.Pstates, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetPowerUsageFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetProcessUtilizationFunc = func(_ uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetProcessesUtilizationInfoFunc = func() (nvml.ProcessesUtilizationInfo, nvml.Return) {
		return nvml.ProcessesUtilizationInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetRemappedRowsFunc = func() (int, int, bool, bool, nvml.Return) {
		return 0, 0, false, false, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetRepairStatusFunc = func() (nvml.RepairStatus, nvml.Return) {
		return nvml.RepairStatus{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetRetiredPagesFunc = func(_ nvml.PageRetirementCause) ([]uint64, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetRetiredPagesPendingStatusFunc = func() (nvml.EnableState, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetRetiredPages_v2Func = func(_ nvml.PageRetirementCause) ([]uint64, []uint64, nvml.Return) {
		return nil, nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetRowRemapperHistogramFunc = func() (nvml.RowRemapperHistogramValues, nvml.Return) {
		return nvml.RowRemapperHistogramValues{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetRunningProcessDetailListFunc = func() (nvml.ProcessDetailList, nvml.Return) {
		return nvml.ProcessDetailList{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSamplesFunc = func(_ nvml.SamplingType, _ uint64) (nvml.ValueType, []nvml.Sample, nvml.Return) {
		return 0, nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSerialFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSramEccErrorStatusFunc = func() (nvml.EccSramErrorStatus, nvml.Return) {
		return nvml.EccSramErrorStatus{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSramUniqueUncorrectedEccErrorCountsFunc = func(_ *nvml.EccSramUniqueUncorrectedErrorCounts) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSupportedClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSupportedClocksThrottleReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSupportedEventTypesFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSupportedGraphicsClocksFunc = func(_ int) (int, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSupportedMemoryClocksFunc = func() (int, uint32, nvml.Return) {
		return 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSupportedPerformanceStatesFunc = func() ([]nvml.Pstates, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetSupportedVgpusFunc = func() ([]nvml.VgpuTypeId, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTargetFanSpeedFunc = func(_ int) (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTemperatureFunc = func(_ nvml.TemperatureSensors) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTemperatureThresholdFunc = func(_ nvml.TemperatureThresholds) (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTemperatureVFunc = func() nvml.TemperatureHandler {
		return nvml.TemperatureHandler{}
	}
	d.GetThermalSettingsFunc = func(_ uint32) (nvml.GpuThermalSettings, nvml.Return) {
		return nvml.GpuThermalSettings{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTopologyCommonAncestorFunc = func(_ nvml.Device) (nvml.GpuTopologyLevel, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTopologyNearestGpusFunc = func(_ nvml.GpuTopologyLevel) ([]nvml.Device, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTotalEccErrorsFunc = func(_ nvml.MemoryErrorType, _ nvml.EccCounterType) (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetTotalEnergyConsumptionFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetUUIDFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetUtilizationRatesFunc = func() (nvml.Utilization, nvml.Return) {
		return nvml.Utilization{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVbiosVersionFunc = func() (string, nvml.Return) {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuCapabilitiesFunc = func(_ nvml.DeviceVgpuCapability) (bool, nvml.Return) {
		return false, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuHeterogeneousModeFunc = func() (nvml.VgpuHeterogeneousMode, nvml.Return) {
		return nvml.VgpuHeterogeneousMode{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuInstancesUtilizationInfoFunc = func() (nvml.VgpuInstancesUtilizationInfo, nvml.Return) {
		return nvml.VgpuInstancesUtilizationInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuMetadataFunc = func() (nvml.VgpuPgpuMetadata, nvml.Return) {
		return nvml.VgpuPgpuMetadata{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuProcessUtilizationFunc = func(_ uint64) ([]nvml.VgpuProcessUtilizationSample, nvml.Return) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuProcessesUtilizationInfoFunc = func() (nvml.VgpuProcessesUtilizationInfo, nvml.Return) {
		return nvml.VgpuProcessesUtilizationInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuSchedulerCapabilitiesFunc = func() (nvml.VgpuSchedulerCapabilities, nvml.Return) {
		return nvml.VgpuSchedulerCapabilities{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuSchedulerLogFunc = func() (nvml.VgpuSchedulerLog, nvml.Return) {
		return nvml.VgpuSchedulerLog{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuSchedulerStateFunc = func() (nvml.VgpuSchedulerGetState, nvml.Return) {
		return nvml.VgpuSchedulerGetState{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuTypeCreatablePlacementsFunc = func(_ nvml.VgpuTypeId) (nvml.VgpuPlacementList, nvml.Return) {
		return nvml.VgpuPlacementList{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuTypeSupportedPlacementsFunc = func(_ nvml.VgpuTypeId) (nvml.VgpuPlacementList, nvml.Return) {
		return nvml.VgpuPlacementList{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVgpuUtilizationFunc = func(_ uint64) (nvml.ValueType, []nvml.VgpuInstanceUtilizationSample, nvml.Return) {
		return 0, nil, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetViolationStatusFunc = func(_ nvml.PerfPolicyType) (nvml.ViolationTime, nvml.Return) {
		return nvml.ViolationTime{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GetVirtualizationModeFunc = func() (nvml.GpuVirtualizationMode, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GpmMigSampleGetFunc = func(_ int, _ nvml.GpmSample) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.GpmQueryDeviceSupportFunc = func() (nvml.GpmSupport, nvml.Return) {
		return nvml.GpmSupport{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GpmQueryDeviceSupportVFunc = func() nvml.GpmSupportV {
		return nvml.GpmSupportV{}
	}
	d.GpmQueryIfStreamingEnabledFunc = func() (uint32, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.GpmSampleGetFunc = func(_ nvml.GpmSample) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.GpmSetStreamingEnabledFunc = func(_ uint32) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.IsMigDeviceHandleFunc = func() (bool, nvml.Return) {
		return false, nvml.ERROR_INVALID_ARGUMENT
	}
	d.OnSameBoardFunc = func(_ nvml.Device) (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.PowerSmoothingActivatePresetProfileFunc = func(_ *nvml.PowerSmoothingProfile) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.PowerSmoothingSetStateFunc = func(_ *nvml.PowerSmoothingState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.PowerSmoothingUpdatePresetProfileParamFunc = func(_ *nvml.PowerSmoothingProfile) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ReadWritePRM_v1Func = func(_ *nvml.PRMTLV_v1) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.RegisterEventsFunc = func(_ uint64, _ nvml.EventSet) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ResetApplicationsClocksFunc = func() nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ResetGpuLockedClocksFunc = func() nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ResetMemoryLockedClocksFunc = func() nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ResetNvLinkErrorCountersFunc = func(_ int) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ResetNvLinkUtilizationCounterFunc = func(_ int, _ int) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetAPIRestrictionFunc = func(_ nvml.RestrictedAPI, _ nvml.EnableState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetAccountingModeFunc = func(_ nvml.EnableState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetApplicationsClocksFunc = func(_ uint32, _ uint32) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetAutoBoostedClocksEnabledFunc = func(_ nvml.EnableState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetClockOffsetsFunc = func(_ nvml.ClockOffset) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetComputeModeFunc = func(_ nvml.ComputeMode) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetConfComputeUnprotectedMemSizeFunc = func(_ uint64) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetCpuAffinityFunc = func() nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetDefaultAutoBoostedClocksEnabledFunc = func(_ nvml.EnableState, _ uint32) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetDefaultFanSpeed_v2Func = func(_ int) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetDramEncryptionModeFunc = func(_ *nvml.DramEncryptionInfo) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetDriverModelFunc = func(_ nvml.DriverModel, _ uint32) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetEccModeFunc = func(_ nvml.EnableState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetFanControlPolicyFunc = func(_ int, _ nvml.FanControlPolicy) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetFanSpeed_v2Func = func(_ int, _ int) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetGpcClkVfOffsetFunc = func(_ int) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetGpuLockedClocksFunc = func(_ uint32, _ uint32) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetGpuOperationModeFunc = func(_ nvml.GpuOperationMode) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetMemClkVfOffsetFunc = func(_ int) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetMemoryLockedClocksFunc = func(_ uint32, _ uint32) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetMigModeFunc = func(_ int) (nvml.Return, nvml.Return) {
		return nvml.ERROR_INVALID_ARGUMENT, nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetNvLinkDeviceLowPowerThresholdFunc = func(_ *nvml.NvLinkPowerThres) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetNvLinkUtilizationControlFunc = func(_ int, _ int, _ *nvml.NvLinkUtilizationControl, _ bool) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetNvlinkBwModeFunc = func(_ *nvml.NvlinkSetBwMode) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetPersistenceModeFunc = func(_ nvml.EnableState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetPowerManagementLimitFunc = func(_ uint32) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetPowerManagementLimit_v2Func = func(_ *nvml.PowerValue_v2) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetTemperatureThresholdFunc = func(_ nvml.TemperatureThresholds, _ int) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetVgpuCapabilitiesFunc = func(_ nvml.DeviceVgpuCapability, _ nvml.EnableState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetVgpuHeterogeneousModeFunc = func(_ nvml.VgpuHeterogeneousMode) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetVgpuSchedulerStateFunc = func(_ *nvml.VgpuSchedulerSetState) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.SetVirtualizationModeFunc = func(_ nvml.GpuVirtualizationMode) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.ValidateInforomFunc = func() nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.VgpuTypeGetMaxInstancesFunc = func(_ nvml.VgpuTypeId) (int, nvml.Return) {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	d.WorkloadPowerProfileClearRequestedProfilesFunc = func(_ *nvml.WorkloadPowerProfileRequestedProfiles) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.WorkloadPowerProfileGetCurrentProfilesFunc = func() (nvml.WorkloadPowerProfileCurrentProfiles, nvml.Return) {
		return nvml.WorkloadPowerProfileCurrentProfiles{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.WorkloadPowerProfileGetProfilesInfoFunc = func() (nvml.WorkloadPowerProfileProfilesInfo, nvml.Return) {
		return nvml.WorkloadPowerProfileProfilesInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d.WorkloadPowerProfileSetRequestedProfilesFunc = func(_ *nvml.WorkloadPowerProfileRequestedProfiles) nvml.Return {
		return nvml.ERROR_INVALID_ARGUMENT
	}

	return d
}
