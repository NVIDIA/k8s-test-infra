/*
 * NVML Type Definitions for Mock Library
 *
 * This header provides ABI-compatible type definitions extracted from nvml.h
 * (vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h, version 13.0.39).
 *
 * We include types only, not function prototypes, to avoid conflicts with
 * CGo-generated function declarations.
 *
 * IMPORTANT: These types must match the real NVML exactly for ABI compatibility.
 * When updating, compare against the vendored nvml.h.
 */

#ifndef MOCK_NVML_TYPES_H
#define MOCK_NVML_TYPES_H

#ifdef __cplusplus
extern "C" {
#endif

/*
 * Return type for all NVML functions
 */
typedef enum nvmlReturn_enum
{
    NVML_SUCCESS = 0,                          //!< The operation was successful
    NVML_ERROR_UNINITIALIZED = 1,              //!< NVML was not first initialized with nvmlInit()
    NVML_ERROR_INVALID_ARGUMENT = 2,           //!< A supplied argument is invalid
    NVML_ERROR_NOT_SUPPORTED = 3,              //!< The requested operation is not available on target device
    NVML_ERROR_NO_PERMISSION = 4,              //!< The current user does not have permission
    NVML_ERROR_ALREADY_INITIALIZED = 5,        //!< Deprecated: Multiple initializations are now allowed
    NVML_ERROR_NOT_FOUND = 6,                  //!< A query to find an object was unsuccessful
    NVML_ERROR_INSUFFICIENT_SIZE = 7,          //!< An input argument is not large enough
    NVML_ERROR_INSUFFICIENT_POWER = 8,         //!< A device's external power cables are not properly attached
    NVML_ERROR_DRIVER_NOT_LOADED = 9,          //!< NVIDIA driver is not loaded
    NVML_ERROR_TIMEOUT = 10,                   //!< User provided timeout passed
    NVML_ERROR_IRQ_ISSUE = 11,                 //!< NVIDIA Kernel detected an interrupt issue
    NVML_ERROR_LIBRARY_NOT_FOUND = 12,         //!< NVML Shared Library couldn't be found or loaded
    NVML_ERROR_FUNCTION_NOT_FOUND = 13,        //!< Local version of NVML doesn't implement this function
    NVML_ERROR_CORRUPTED_INFOROM = 14,         //!< infoROM is corrupted
    NVML_ERROR_GPU_IS_LOST = 15,               //!< The GPU has fallen off the bus or has otherwise become inaccessible
    NVML_ERROR_RESET_REQUIRED = 16,            //!< The GPU requires a reset before it can be used again
    NVML_ERROR_OPERATING_SYSTEM = 17,          //!< The GPU control device has been blocked
    NVML_ERROR_LIB_RM_VERSION_MISMATCH = 18,   //!< RM detects a driver/library version mismatch
    NVML_ERROR_IN_USE = 19,                    //!< An operation cannot be performed because the GPU is currently in use
    NVML_ERROR_MEMORY = 20,                    //!< Insufficient memory
    NVML_ERROR_NO_DATA = 21,                   //!< No data
    NVML_ERROR_VGPU_ECC_NOT_SUPPORTED = 22,    //!< The requested vgpu operation is not available on target device
    NVML_ERROR_INSUFFICIENT_RESOURCES = 23,    //!< Ran out of critical resources
    NVML_ERROR_FREQ_NOT_SUPPORTED = 24,        //!< Ran out of critical resources
    NVML_ERROR_ARGUMENT_VERSION_MISMATCH = 25, //!< The provided version is invalid/unsupported
    NVML_ERROR_DEPRECATED = 26,                //!< The requested functionality has been deprecated
    NVML_ERROR_NOT_READY = 27,                 //!< The system is not ready for the request
    NVML_ERROR_GPU_NOT_FOUND = 28,             //!< No GPUs were found
    NVML_ERROR_INVALID_STATE = 29,             //!< Resource not in correct state to perform requested operation
    NVML_ERROR_UNKNOWN = 999                   //!< An internal driver error occurred
} nvmlReturn_t;

/*
 * Device handle - opaque reference to a GPU device
 * This is a struct wrapper around an opaque pointer, matching real NVML ABI.
 */
typedef struct
{
    struct nvmlDevice_st* handle;
} nvmlDevice_t;

/*
 * Brand types
 */
typedef enum nvmlBrandType_enum
{
    NVML_BRAND_UNKNOWN              = 0,
    NVML_BRAND_QUADRO               = 1,
    NVML_BRAND_TESLA                = 2,
    NVML_BRAND_NVS                  = 3,
    NVML_BRAND_GRID                 = 4,   // Different different different
    NVML_BRAND_GEFORCE              = 5,
    NVML_BRAND_TITAN                = 6,
    NVML_BRAND_NVIDIA_VAPPS         = 7,   // NVIDIA Virtual Applications
    NVML_BRAND_NVIDIA_VPC           = 8,   // NVIDIA Virtual PC
    NVML_BRAND_NVIDIA_VCS           = 9,   // NVIDIA Virtual Compute Server
    NVML_BRAND_NVIDIA_VWS           = 10,  // NVIDIA RTX Virtual Workstation
    NVML_BRAND_NVIDIA_CLOUD_GAMING  = 11,  // NVIDIA Cloud Gaming
    NVML_BRAND_NVIDIA_VGAMING       = NVML_BRAND_NVIDIA_CLOUD_GAMING, // Deprecated
    NVML_BRAND_QUADRO_RTX           = 12,
    NVML_BRAND_NVIDIA_RTX           = 13,
    NVML_BRAND_NVIDIA               = 14,
    NVML_BRAND_GEFORCE_RTX          = 15,
    NVML_BRAND_TITAN_RTX            = 16,
    NVML_BRAND_COUNT
} nvmlBrandType_t;

/*
 * PCI information about a GPU device
 */
#define NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE      32
#define NVML_DEVICE_PCI_BUS_ID_LEGACY_FMT_SIZE  16

typedef struct nvmlPciInfo_st
{
    char busIdLegacy[NVML_DEVICE_PCI_BUS_ID_LEGACY_FMT_SIZE]; //!< Legacy PCI bus ID
    unsigned int domain;             //!< The PCI domain
    unsigned int bus;                //!< The PCI bus
    unsigned int device;             //!< The PCI device
    unsigned int pciDeviceId;        //!< The combined device and vendor ID
    unsigned int pciSubSystemId;     //!< The subsystem ID
    char busId[NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE]; //!< Full PCI bus ID
} nvmlPciInfo_t;

/*
 * Memory information (v1)
 */
typedef struct nvmlMemory_st
{
    unsigned long long total;        //!< Total physical memory (in bytes)
    unsigned long long free;         //!< Unallocated memory (in bytes)
    unsigned long long used;         //!< Allocated memory (in bytes)
} nvmlMemory_t;

/*
 * Process information
 */
typedef struct nvmlProcessInfo_st
{
    unsigned int        pid;                //!< Process ID
    unsigned long long  usedGpuMemory;      //!< GPU memory used (in bytes)
    unsigned int        gpuInstanceId;      //!< GPU instance ID (for MIG)
    unsigned int        computeInstanceId;  //!< Compute instance ID (for MIG)
} nvmlProcessInfo_t;

/*
 * Utilization information
 */
typedef struct nvmlUtilization_st
{
    unsigned int gpu;                //!< Percent of time GPU was executing kernels
    unsigned int memory;             //!< Percent of time GPU memory controller was active
} nvmlUtilization_t;

/*
 * Memory information (v2) — adds version field and reserved memory
 */
typedef struct nvmlMemory_v2_st
{
    unsigned int version;            //!< Structure format version (must be 2)
    unsigned long long total;        //!< Total physical device memory (in bytes)
    unsigned long long reserved;     //!< Device memory (in bytes) reserved for system use
    unsigned long long free;         //!< Unallocated device memory (in bytes)
    unsigned long long used;         //!< Allocated device memory (in bytes)
} nvmlMemory_v2_t;

/*
 * Device architecture type
 */
typedef unsigned int nvmlDeviceArchitecture_t;

/* =========================================================================
 * Opaque type definitions for stub ABI compatibility.
 *
 * These types are needed for generated stubs to compile with correct
 * function signatures. Stubs only return NVML_ERROR_NOT_SUPPORTED and
 * never dereference these types, so opaque definitions suffice.
 *
 * Types passed by value in NVML function signatures need correct ABI size:
 *   - Enums: typedef unsigned int
 *   - Handles: typedef void* (opaque pointer)
 * Types only passed by pointer: forward-declared opaque struct is sufficient.
 * ========================================================================= */

/* --- Enums (passed by value as unsigned int) --- */
typedef unsigned int nvmlAffinityScope_t;
typedef unsigned int nvmlClockId_t;
typedef unsigned int nvmlClockType_t;
typedef unsigned int nvmlComputeMode_t;
typedef unsigned int nvmlDetachGpuState_t;
typedef unsigned int nvmlDeviceVgpuCapability_t;
typedef unsigned int nvmlDriverModel_t;
typedef unsigned int nvmlEccCounterType_t;
typedef unsigned int nvmlEnableState_t;
typedef unsigned int nvmlEncoderType_t;
typedef unsigned int nvmlFanControlPolicy_t;
typedef unsigned int nvmlGpuOperationMode_t;
typedef unsigned int nvmlGpuP2PCapsIndex_t;
typedef unsigned int nvmlGpuP2PStatus_t;
typedef unsigned int nvmlGpuTopologyLevel_t;
typedef unsigned int nvmlGpuVirtualizationMode_t;
typedef unsigned int nvmlHostVgpuMode_t;
typedef unsigned int nvmlInforomObject_t;
typedef unsigned int nvmlIntNvLinkDeviceType_t;
typedef unsigned int nvmlLedColor_t;
typedef unsigned int nvmlMemoryErrorType_t;
typedef unsigned int nvmlMemoryLocation_t;
typedef unsigned int nvmlNvLinkCapability_t;
typedef unsigned int nvmlNvLinkErrorCounter_t;
typedef unsigned int nvmlPageRetirementCause_t;
typedef unsigned int nvmlPcieLinkState_t;
typedef unsigned int nvmlPcieUtilCounter_t;
typedef unsigned int nvmlPerfPolicyType_t;
typedef unsigned int nvmlPstates_t;
typedef unsigned int nvmlRestrictedAPI_t;
typedef unsigned int nvmlSamplingType_t;
typedef unsigned int nvmlTemperatureSensors_t;
typedef unsigned int nvmlTemperatureThresholds_t;
typedef unsigned int nvmlValueType_t;
typedef unsigned int nvmlVgpuCapability_t;
typedef unsigned int nvmlVgpuDriverCapability_t;
typedef unsigned int nvmlVgpuTypeId_t;

/* --- Opaque handle types (passed by value as pointers) --- */
typedef struct nvmlComputeInstance_st* nvmlComputeInstance_t;
typedef struct nvmlEventSet_st*       nvmlEventSet_t;
typedef struct nvmlGpmSample_st*      nvmlGpmSample_t;
typedef struct nvmlGpuInstance_st*     nvmlGpuInstance_t;
typedef struct nvmlUnit_st*           nvmlUnit_t;
typedef struct nvmlVgpuInstance_st*    nvmlVgpuInstance_t;

/* --- Opaque struct types (only used via pointer in function signatures) --- */
typedef struct nvmlAccountingStats_st                       nvmlAccountingStats_t;
typedef struct nvmlActiveVgpuInstanceInfo_st                nvmlActiveVgpuInstanceInfo_t;
/* BAR1 Memory - full definition needed by bridge */
typedef struct nvmlBAR1Memory_st
{
    unsigned long long bar1Total;    //!< Total BAR1 Memory (in bytes)
    unsigned long long bar1Free;     //!< Unallocated BAR1 Memory (in bytes)
    unsigned long long bar1Used;     //!< Allocated Used Memory (in bytes)
} nvmlBAR1Memory_t;
typedef struct nvmlBridgeChipHierarchy_st                   nvmlBridgeChipHierarchy_t;
typedef struct nvmlBusType_st                               nvmlBusType_t;
typedef struct nvmlC2cModeInfo_v1_st                        nvmlC2cModeInfo_v1_t;
typedef struct nvmlClkMonStatus_st                          nvmlClkMonStatus_t;
typedef struct nvmlClockOffset_st                           nvmlClockOffset_t;
typedef struct nvmlComputeInstanceInfo_st                   nvmlComputeInstanceInfo_t;
typedef struct nvmlComputeInstancePlacement_st              nvmlComputeInstancePlacement_t;
typedef struct nvmlComputeInstanceProfileInfo_st            nvmlComputeInstanceProfileInfo_t;
typedef struct nvmlComputeInstanceProfileInfo_v2_st         nvmlComputeInstanceProfileInfo_v2_t;
typedef struct nvmlConfComputeGetKeyRotationThresholdInfo_st nvmlConfComputeGetKeyRotationThresholdInfo_t;
typedef struct nvmlConfComputeGpuAttestationReport_st       nvmlConfComputeGpuAttestationReport_t;
typedef struct nvmlConfComputeGpuCertificate_st             nvmlConfComputeGpuCertificate_t;
typedef struct nvmlConfComputeMemSizeInfo_st                nvmlConfComputeMemSizeInfo_t;
typedef struct nvmlConfComputeSetKeyRotationThresholdInfo_st nvmlConfComputeSetKeyRotationThresholdInfo_t;
typedef struct nvmlConfComputeSystemCaps_st                 nvmlConfComputeSystemCaps_t;
typedef struct nvmlConfComputeSystemState_st                nvmlConfComputeSystemState_t;
typedef struct nvmlCoolerInfo_st                            nvmlCoolerInfo_t;
typedef struct nvmlDeviceAddressingMode_st                  nvmlDeviceAddressingMode_t;
typedef struct nvmlDeviceAttributes_st                      nvmlDeviceAttributes_t;
typedef struct nvmlDeviceCapabilities_st                    nvmlDeviceCapabilities_t;
typedef struct nvmlDeviceCurrentClockFreqs_st               nvmlDeviceCurrentClockFreqs_t;
typedef struct nvmlDevicePerfModes_st                       nvmlDevicePerfModes_t;
typedef struct nvmlDevicePowerMizerModes_v1_st              nvmlDevicePowerMizerModes_v1_t;
typedef struct nvmlDramEncryptionInfo_st                    nvmlDramEncryptionInfo_t;
/* ECC error counts - full definition needed by bridge */
typedef struct nvmlEccErrorCounts_st {
    unsigned long long l1Cache;
    unsigned long long l2Cache;
    unsigned long long deviceMemory;
    unsigned long long registerFile;
} nvmlEccErrorCounts_t;
typedef struct nvmlEccSramErrorStatus_st                    nvmlEccSramErrorStatus_t;
typedef struct nvmlEccSramUniqueUncorrectedErrorCounts_st   nvmlEccSramUniqueUncorrectedErrorCounts_t;
typedef struct nvmlEncoderSessionInfo_st                    nvmlEncoderSessionInfo_t;
/* Event data - full definition needed by bridge so the failure-injection
 * Xid event surfaced via nvmlEventSetWait_v2 can populate the eventType
 * and eventData fields (see bridge/events.go). Layout matches the
 * upstream NVML header. */
typedef struct nvmlEventData_st {
    nvmlDevice_t        device;
    unsigned long long  eventType;
    unsigned long long  eventData;
    unsigned int        gpuInstanceId;
    unsigned int        computeInstanceId;
} nvmlEventData_t;

/* Event type bitmask values from the upstream NVML public header. We
 * only consume NVML_EVENT_TYPE_XID_CRITICAL_ERROR but advertise the
 * full bitmask as supported so consumers that AND against
 * arbitrary types still find it. */
#define NVML_EVENT_TYPE_SINGLE_BIT_ECC          0x0000000000000001ULL
#define NVML_EVENT_TYPE_DOUBLE_BIT_ECC          0x0000000000000002ULL
#define NVML_EVENT_TYPE_PSTATE_CHANGE           0x0000000000000004ULL
#define NVML_EVENT_TYPE_XID_CRITICAL_ERROR      0x0000000000000008ULL
#define NVML_EVENT_TYPE_CLOCK_CHANGE            0x0000000000000010ULL
#define NVML_EVENT_TYPE_POWER_SOURCE_CHANGE     0x0000000000000080ULL
#define NVML_EVENT_TYPE_MIG_CONFIG_CHANGE       0x0000000000000100ULL
typedef struct nvmlExcludedDeviceInfo_st                    nvmlExcludedDeviceInfo_t;
typedef struct nvmlFBCSessionInfo_st                        nvmlFBCSessionInfo_t;
typedef struct nvmlFBCStats_st                              nvmlFBCStats_t;
typedef struct nvmlFanSpeedInfo_st                          nvmlFanSpeedInfo_t;
/* Field value query — full definition needed by the bridge so
 * nvmlDeviceGetFieldValues (see bridge/fieldvalues.go) can read the
 * requested fieldId/scopeId and populate value/valueType/nvmlReturn.
 * Layout matches the upstream NVML public header; nvmlValueType_t is
 * already typedef'd (unsigned int) above. */
typedef union nvmlValue_st {
    double             dVal;    //!< If the value is double
    int                siVal;   //!< If the value is signed int
    unsigned int       uiVal;   //!< If the value is unsigned int
    unsigned long      ulVal;   //!< If the value is unsigned long
    unsigned long long ullVal;  //!< If the value is unsigned long long
    signed long long   sllVal;  //!< If the value is signed long long
    unsigned short     usVal;   //!< If the value is unsigned short
} nvmlValue_t;
typedef struct nvmlFieldValue_st {
    unsigned int    fieldId;     //!< ID of the NVML field to retrieve
    unsigned int    scopeId;     //!< Context (e.g. NVLink linkId) for fieldId
    long long       timestamp;   //!< CPU timestamp in usec since 1970
    long long       latencyUsec; //!< How long the field took to update (usec)
    nvmlValueType_t valueType;   //!< Type of the value stored in value
    nvmlReturn_t    nvmlReturn;  //!< Per-field return code; check before value
    nvmlValue_t     value;       //!< Field value (valid iff nvmlReturn == SUCCESS)
} nvmlFieldValue_t;
typedef struct nvmlGpmMetricsGet_st                         nvmlGpmMetricsGet_t;
/* GPM support - full definition needed by bridge */
typedef struct nvmlGpmSupport_st {
    unsigned int version;
    unsigned int isSupportedDevice;
} nvmlGpmSupport_t;
typedef struct nvmlGpuDynamicPstatesInfo_st                 nvmlGpuDynamicPstatesInfo_t;

/* GPU Fabric information — full definitions needed by the bridge so
 * nvmlDeviceGetGpuFabricInfo / nvmlDeviceGetGpuFabricInfoV can populate
 * the caller's struct. Layout matches the upstream NVML public header
 * (versions v1 / v2 / v3); see vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h. */
#define NVML_GPU_FABRIC_UUID_LEN 16

#define NVML_GPU_FABRIC_STATE_NOT_SUPPORTED 0
#define NVML_GPU_FABRIC_STATE_NOT_STARTED   1
#define NVML_GPU_FABRIC_STATE_IN_PROGRESS   2
#define NVML_GPU_FABRIC_STATE_COMPLETED     3

typedef unsigned char nvmlGpuFabricState_t;

typedef struct nvmlGpuFabricInfo_st {
    unsigned char        clusterUuid[NVML_GPU_FABRIC_UUID_LEN];
    nvmlReturn_t         status;
    unsigned int         cliqueId;
    nvmlGpuFabricState_t state;
} nvmlGpuFabricInfo_t;

typedef struct nvmlGpuFabricInfo_v2_st {
    unsigned int         version;
    unsigned char        clusterUuid[NVML_GPU_FABRIC_UUID_LEN];
    nvmlReturn_t         status;
    unsigned int         cliqueId;
    nvmlGpuFabricState_t state;
    unsigned int         healthMask;
} nvmlGpuFabricInfo_v2_t;

typedef struct nvmlGpuFabricInfo_v3_st {
    unsigned int         version;
    unsigned char        clusterUuid[NVML_GPU_FABRIC_UUID_LEN];
    nvmlReturn_t         status;
    unsigned int         cliqueId;
    nvmlGpuFabricState_t state;
    unsigned int         healthMask;
    unsigned char        healthSummary;
} nvmlGpuFabricInfo_v3_t;

typedef nvmlGpuFabricInfo_v3_t nvmlGpuFabricInfoV_t;
typedef struct nvmlGpuInstanceInfo_st                       nvmlGpuInstanceInfo_t;
typedef struct nvmlGpuInstancePlacement_st                  nvmlGpuInstancePlacement_t;
typedef struct nvmlGpuInstanceProfileInfo_st                nvmlGpuInstanceProfileInfo_t;
typedef struct nvmlGpuInstanceProfileInfo_v2_st             nvmlGpuInstanceProfileInfo_v2_t;
/* Thermal sensor settings - full definition needed by bridge */
#define NVML_MAX_THERMAL_SENSORS_PER_GPU 3

typedef enum nvmlThermalTarget_enum {
    NVML_THERMAL_TARGET_NONE          = 0,
    NVML_THERMAL_TARGET_GPU           = 1,
    NVML_THERMAL_TARGET_MEMORY        = 2,
    NVML_THERMAL_TARGET_POWER_SUPPLY  = 4,
    NVML_THERMAL_TARGET_BOARD         = 8,
    NVML_THERMAL_TARGET_ALL           = 15,
    NVML_THERMAL_TARGET_UNKNOWN       = -1
} nvmlThermalTarget_t;

typedef enum nvmlThermalController_enum {
    NVML_THERMAL_CONTROLLER_NONE      = 0,
    NVML_THERMAL_CONTROLLER_GPU_INTERNAL = 1,
    NVML_THERMAL_CONTROLLER_UNKNOWN   = -1
} nvmlThermalController_t;

typedef struct {
    unsigned int count;
    struct {
        nvmlThermalController_t controller;
        int defaultMinTemp;
        int defaultMaxTemp;
        int currentTemp;
        nvmlThermalTarget_t target;
    } sensor[NVML_MAX_THERMAL_SENSORS_PER_GPU];
} nvmlGpuThermalSettings_t;
typedef struct nvmlGridLicensableFeatures_st                nvmlGridLicensableFeatures_t;
typedef struct nvmlHwbcEntry_st                             nvmlHwbcEntry_t;
typedef struct nvmlLedState_st                              nvmlLedState_t;
typedef struct nvmlMarginTemperature_st                     nvmlMarginTemperature_t;
typedef struct nvmlNvLinkInfo_st                            nvmlNvLinkInfo_t;
typedef struct nvmlNvLinkPowerThres_st                      nvmlNvLinkPowerThres_t;
typedef struct nvmlNvLinkUtilizationControl_st              nvmlNvLinkUtilizationControl_t;
typedef struct nvmlNvlinkGetBwMode_st                       nvmlNvlinkGetBwMode_t;
typedef struct nvmlNvlinkSetBwMode_st                       nvmlNvlinkSetBwMode_t;
typedef struct nvmlNvlinkSupportedBwModes_st                nvmlNvlinkSupportedBwModes_t;
typedef struct nvmlPRMTLV_v1_st                             nvmlPRMTLV_v1_t;
typedef struct nvmlPSUInfo_st                               nvmlPSUInfo_t;
typedef struct nvmlPciInfoExt_st                            nvmlPciInfoExt_t;
typedef struct nvmlPdi_st                                   nvmlPdi_t;
typedef struct nvmlPlatformInfo_st                          nvmlPlatformInfo_t;
typedef struct nvmlPowerSmoothingProfile_st                 nvmlPowerSmoothingProfile_t;
typedef struct nvmlPowerSmoothingState_st                   nvmlPowerSmoothingState_t;
typedef struct nvmlPowerSource_st                           nvmlPowerSource_t;
typedef struct nvmlPowerValue_v2_st                         nvmlPowerValue_v2_t;
typedef struct nvmlProcessDetailList_st                     nvmlProcessDetailList_t;
typedef struct nvmlProcessInfo_v1_st                        nvmlProcessInfo_v1_t;
typedef struct nvmlProcessInfo_v2_st                        nvmlProcessInfo_v2_t;

/* Process utilization sample */
typedef struct nvmlProcessUtilizationSample_st
{
    unsigned int        pid;        //!< PID of process
    unsigned long long  timeStamp;  //!< CPU Timestamp in microseconds
    unsigned int        smUtil;     //!< SM (3D/Compute) Util Value
    unsigned int        memUtil;    //!< Frame Buffer Memory Util Value
    unsigned int        encUtil;    //!< Encoder Util Value
    unsigned int        decUtil;    //!< Decoder Util Value
} nvmlProcessUtilizationSample_t;
typedef struct nvmlProcessesUtilizationInfo_st              nvmlProcessesUtilizationInfo_t;
typedef struct nvmlRepairStatus_st                          nvmlRepairStatus_t;
typedef struct nvmlRowRemapperHistogramValues_st            nvmlRowRemapperHistogramValues_t;
typedef struct nvmlSample_st                                nvmlSample_t;
typedef struct nvmlSystemConfComputeSettings_st             nvmlSystemConfComputeSettings_t;
typedef struct nvmlSystemDriverBranchInfo_st                nvmlSystemDriverBranchInfo_t;
typedef struct nvmlSystemEventSetCreateRequest_st           nvmlSystemEventSetCreateRequest_t;
typedef struct nvmlSystemEventSetFreeRequest_st             nvmlSystemEventSetFreeRequest_t;
typedef struct nvmlSystemEventSetWaitRequest_st             nvmlSystemEventSetWaitRequest_t;
typedef struct nvmlSystemRegisterEventRequest_st            nvmlSystemRegisterEventRequest_t;
typedef struct nvmlTemperature_st                           nvmlTemperature_t;
typedef struct nvmlUUID_st                                  nvmlUUID_t;
typedef struct nvmlUnitFanSpeeds_st                         nvmlUnitFanSpeeds_t;
typedef struct nvmlUnitInfo_st                              nvmlUnitInfo_t;
typedef struct nvmlVgpuCreatablePlacementInfo_st            nvmlVgpuCreatablePlacementInfo_t;
typedef struct nvmlVgpuHeterogeneousMode_st                 nvmlVgpuHeterogeneousMode_t;
typedef struct nvmlVgpuInstanceUtilizationSample_st         nvmlVgpuInstanceUtilizationSample_t;
typedef struct nvmlVgpuInstancesUtilizationInfo_st          nvmlVgpuInstancesUtilizationInfo_t;
typedef struct nvmlVgpuLicenseInfo_st                       nvmlVgpuLicenseInfo_t;
typedef struct nvmlVgpuMetadata_st                          nvmlVgpuMetadata_t;
typedef struct nvmlVgpuPgpuCompatibility_st                 nvmlVgpuPgpuCompatibility_t;
typedef struct nvmlVgpuPgpuMetadata_st                      nvmlVgpuPgpuMetadata_t;
typedef struct nvmlVgpuPlacementId_st                       nvmlVgpuPlacementId_t;
typedef struct nvmlVgpuPlacementList_st                     nvmlVgpuPlacementList_t;
typedef struct nvmlVgpuProcessUtilizationSample_st          nvmlVgpuProcessUtilizationSample_t;
typedef struct nvmlVgpuProcessesUtilizationInfo_st          nvmlVgpuProcessesUtilizationInfo_t;
typedef struct nvmlVgpuRuntimeState_st                      nvmlVgpuRuntimeState_t;
typedef struct nvmlVgpuSchedulerCapabilities_st             nvmlVgpuSchedulerCapabilities_t;
typedef struct nvmlVgpuSchedulerGetState_st                 nvmlVgpuSchedulerGetState_t;
typedef struct nvmlVgpuSchedulerLogInfo_st                  nvmlVgpuSchedulerLogInfo_t;
typedef struct nvmlVgpuSchedulerLog_st                      nvmlVgpuSchedulerLog_t;
typedef struct nvmlVgpuSchedulerSetState_st                 nvmlVgpuSchedulerSetState_t;
typedef struct nvmlVgpuSchedulerStateInfo_st                nvmlVgpuSchedulerStateInfo_t;
typedef struct nvmlVgpuSchedulerState_st                    nvmlVgpuSchedulerState_t;
typedef struct nvmlVgpuTypeBar1Info_st                      nvmlVgpuTypeBar1Info_t;
typedef struct nvmlVgpuTypeIdInfo_st                        nvmlVgpuTypeIdInfo_t;
typedef struct nvmlVgpuTypeMaxInstance_st                    nvmlVgpuTypeMaxInstance_t;
typedef struct nvmlVgpuVersion_st                           nvmlVgpuVersion_t;
typedef struct nvmlVgpuVmIdType_st                          nvmlVgpuVmIdType_t;
typedef struct nvmlViolationTime_st                         nvmlViolationTime_t;
typedef struct nvmlWorkloadPowerProfileCurrentProfiles_st   nvmlWorkloadPowerProfileCurrentProfiles_t;
typedef struct nvmlWorkloadPowerProfileProfilesInfo_st      nvmlWorkloadPowerProfileProfilesInfo_t;
typedef struct nvmlWorkloadPowerProfileRequestedProfiles_st nvmlWorkloadPowerProfileRequestedProfiles_t;

/*
 * NVML 13.0 additions (go-nvml v0.13.1-0, #400). Only ever passed by pointer
 * through NOT_SUPPORTED stubs, so opaque forward declarations are sufficient
 * and ABI-safe.
 */
typedef struct nvmlHostname_v1_st                           nvmlHostname_v1_t;
typedef struct nvmlPRMCounterList_v1_st                     nvmlPRMCounterList_v1_t;
typedef struct nvmlRusdSettings_v1_st                       nvmlRusdSettings_v1_t;
typedef struct nvmlUnrepairableMemoryStatus_v1_st           nvmlUnrepairableMemoryStatus_v1_t;
typedef struct nvmlVgpuSchedulerLogInfo_v2_st               nvmlVgpuSchedulerLogInfo_v2_t;
typedef struct nvmlVgpuSchedulerSetState_v2_st              nvmlVgpuSchedulerSetState_v2_t;
typedef struct nvmlVgpuSchedulerState_v2_st                 nvmlVgpuSchedulerState_v2_t;
typedef struct nvmlVgpuSchedulerStateInfo_v2_st             nvmlVgpuSchedulerStateInfo_v2_t;
typedef struct nvmlWorkloadPowerProfileUpdateProfiles_v1_st nvmlWorkloadPowerProfileUpdateProfiles_v1_t;

/*
 * NVML additions (go-nvml v0.13.2-0, #410). Remapped rows v2 is written by the
 * bridge, so keep this layout in sync with go-nvml's vendored nvml.h.
 */
typedef struct nvmlRemappedRowsInfo_v2_t
{
    unsigned int corrActiveRemaps;
    unsigned int corrInactiveRemaps;
    unsigned int uncActiveRemaps;
    unsigned int uncInactiveRemaps;
    unsigned int bPending;
    unsigned int bFailureOccurred;
} nvmlRemappedRowsInfo_v2_t;

/*
 * The vGPU scheduler APIs are only ever passed by pointer through NOT_SUPPORTED
 * stubs, so opaque forward declarations are sufficient and ABI-safe.
 */
typedef struct nvmlVgpuSchedulerLogInfo_v2_st               nvmlVgpuSchedulerLogInfo_v2_t;
typedef struct nvmlVgpuSchedulerStateInfo_v2_st             nvmlVgpuSchedulerStateInfo_v2_t;
typedef struct nvmlVgpuSchedulerState_v2_st                 nvmlVgpuSchedulerState_v2_t;

/*
 * NVML additions (go-nvml v0.13.3-1, deps-consolidated-20260713). Unlike the
 * #400/#410 additions above, these need full ABI-accurate definitions, not
 * opaque forward declarations: go-nvml's own cgo wrapper functions
 * (nvmlDeviceGetAccountingStats_v2, nvmlDeviceGetBBXTimeData_v1,
 * nvmlSystemGetCPER_v1 in vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go)
 * fail to compile against an opaque C.nvmlXxx_t with "could not determine
 * what C.nvmlXxx_t refers to" unless the full struct body is visible.
 * Definitions extracted verbatim from
 * vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h.
 */
typedef struct {
    unsigned int       pid;               //!< Process Id of the target process to query stats for
    unsigned int       isRunning;         //!< Flag to represent if the process is running (1 for running, 0 for terminated)
    unsigned int       gpuUtilization;    //!< Percent of time over the process's lifetime during which one or more kernels was executing on the GPU
    unsigned int       memoryUtilization; //!< Percent of time over the process's lifetime during which global (device) memory was being read or written
    unsigned long long maxMemoryUsage;    //!< Maximum total memory in bytes that was ever allocated by the process
    unsigned int       sampleCount;       //!< The sample counts since the process starts
    unsigned long long sumGpuUtil;        //!< The sum of process's GR engine utilization in unit of pct * 100
    unsigned long long sumFbUtil;         //!< The sum of process's FB bandwidth utilization in unit of pct * 100
    unsigned long long time;              //!< Amount of time in ms during which the compute context was active
    unsigned long long startTime;         //!< CPU Timestamp in usec representing start time for the process
} nvmlAccountingStats_v2_t;

typedef struct {
    unsigned int timeRun; //!< [out] Cumulative number of seconds the GPU has had the driver loaded
} nvmlBBXTimeData_v1_t;

#define NVML_DEVICE_UUID_BUFFER_SIZE 80

typedef unsigned long long nvmlCPERCursorHandle_t; //!< Opaque handle to a CPER read position
#define NVML_CPER_CURSOR_HANDLE_INIT ((nvmlCPERCursorHandle_t) 0)

typedef struct
{
    unsigned int           cperTypeMask; //!< [IN] Bitmask of nvmlCPERType_t values
    char                   uuid[NVML_DEVICE_UUID_BUFFER_SIZE]; //!< [IN] UUID of target to filter records for
    nvmlCPERCursorHandle_t handle;       //!< [IN/OUT] Opaque handle tracking read position
} nvmlCPERCursor_v1_t;

typedef enum
{
    NVML_CPER_ACCESS_TYPE_GPU = (1 << 0) //!< Access GPU CPER records
} nvmlCPERType_t;

typedef struct
{
    nvmlCPERCursor_v1_t cursor;     //!< [IN/OUT] Query parameters and cursor
    unsigned char        *buffer;   //!< [OUT] Buffer to be filled (allocated by client)
    unsigned int          bufferSize; //!< [IN/OUT] Size of buffer
} nvmlGetCPER_v1_t;

#ifdef __cplusplus
}
#endif

#endif /* MOCK_NVML_TYPES_H */
