/*
 * NVML Type Definitions for Mock Library
 *
 * This header provides ABI-compatible type definitions extracted from nvml.h
 * (go-nvml's pkg/nvml/nvml.h, version 13.0.39).
 *
 * We include types only, not function prototypes, to avoid conflicts with
 * CGo-generated function declarations.
 *
 * IMPORTANT: These types must match the real NVML exactly for ABI compatibility.
 * When updating, compare against go-nvml's nvml.h.
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

/*
 * Temperature info (v1) — used by the versioned nvmlDeviceGetTemperatureV API.
 * sensorType is an input (which sensor to read); temperature is the output.
 * Defined after nvmlTemperatureSensors_t above so the field type is complete.
 */
typedef struct nvmlTemperature_st
{
    unsigned int             version;      //!< IN: NVML_STRUCT_VERSION(Temperature, 1)
    nvmlTemperatureSensors_t sensorType;   //!< IN: sensor to query (e.g. NVML_TEMPERATURE_GPU)
    int                      temperature;  //!< OUT: temperature in degrees C
} nvmlTemperature_t;

/* --- Opaque handle types (passed by value as pointers) --- */
typedef struct nvmlComputeInstance_st* nvmlComputeInstance_t;
typedef struct nvmlEventSet_st*       nvmlEventSet_t;
typedef struct nvmlGpuInstance_st*     nvmlGpuInstance_t;
typedef struct nvmlUnit_st*           nvmlUnit_t;
typedef struct nvmlVgpuInstance_st*    nvmlVgpuInstance_t;

/* Upstream wraps the opaque GPM sample pointer in a one-field struct. */
typedef struct {
    struct nvmlGpmSample_st* handle;
} nvmlGpmSample_t;

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
/**
 * C2C Mode information for a device. A single field with no version tag:
 * unlike nvmlGpuFabricInfoV_t the caller passes no version, so
 * nvmlDeviceGetC2cModeInfoV needs no version dispatch.
 */
typedef struct nvmlC2cModeInfo_v1_st
{
    unsigned int isC2cEnabled;
} nvmlC2cModeInfo_v1_t;
/* Callers allocate this buffer from go-nvml's C2cModeInfo_v1, so any field
 * added here would make the bridge write past the caller's allocation. A struct
 * that grows to carry the version field the V suffix implies also means the
 * bridge's lack of version dispatch needs revisiting. */
_Static_assert(sizeof(nvmlC2cModeInfo_v1_t) == 4,
               "nvmlC2cModeInfo_v1_t must stay a single unsigned int to match the go-nvml ABI");
typedef struct nvmlClkMonStatus_st                          nvmlClkMonStatus_t;
typedef struct nvmlClockOffset_st                           nvmlClockOffset_t;
typedef struct nvmlComputeInstanceInfo_st                   nvmlComputeInstanceInfo_t;
typedef struct nvmlComputeInstancePlacement_st              nvmlComputeInstancePlacement_t;
typedef struct nvmlComputeInstanceProfileInfo_st            nvmlComputeInstanceProfileInfo_t;
typedef struct nvmlComputeInstanceProfileInfo_v2_st         nvmlComputeInstanceProfileInfo_v2_t;
typedef struct nvmlConfComputeGetKeyRotationThresholdInfo_st nvmlConfComputeGetKeyRotationThresholdInfo_t;
typedef struct nvmlConfComputeGpuAttestationReport_st       nvmlConfComputeGpuAttestationReport_t;
typedef struct nvmlConfComputeGpuCertificate_st             nvmlConfComputeGpuCertificate_t;
/* How device memory splits across a Confidential Compute partition. Full
 * definition needed by bridge so conf_compute.go can fill the caller's buffer. */
typedef struct nvmlConfComputeMemSizeInfo_st
{
    unsigned long long protectedMemSizeKib;
    unsigned long long unprotectedMemSizeKib;
} nvmlConfComputeMemSizeInfo_t;
/* Callers allocate this buffer from go-nvml's ConfComputeMemSizeInfo, so a
 * field added here would make the bridge write past the caller's allocation. */
_Static_assert(sizeof(nvmlConfComputeMemSizeInfo_t) == 16,
               "nvmlConfComputeMemSizeInfo_t must stay two unsigned long longs to match the go-nvml ABI");
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
/* SRAM ECC error status — full definition needed by the bridge so
 * nvmlDeviceGetSramEccErrorStatus can populate the caller's buffer (issue
 * #641). version is an input the caller stamps with the NVML_STRUCT_VERSION
 * macro; the rest are outputs. Layout matches
 * go-nvml's pkg/nvml/nvml.h (v1), pinned by
 * ecc_layout_test.go. */
typedef struct nvmlEccSramErrorStatus_st
{
    unsigned int       version;                 //!< IN: NVML_STRUCT_VERSION(EccSramErrorStatus, 1)
    unsigned long long aggregateUncParity;
    unsigned long long aggregateUncSecDed;
    unsigned long long aggregateCor;
    unsigned long long volatileUncParity;
    unsigned long long volatileUncSecDed;
    unsigned long long volatileCor;
    unsigned long long aggregateUncBucketL2;
    unsigned long long aggregateUncBucketSm;
    unsigned long long aggregateUncBucketPcie;
    unsigned long long aggregateUncBucketMcu;
    unsigned long long aggregateUncBucketOther;
    unsigned int       bThresholdExceeded;
} nvmlEccSramErrorStatus_t;
typedef struct nvmlEccSramUniqueUncorrectedErrorCounts_st   nvmlEccSramUniqueUncorrectedErrorCounts_t;
typedef struct nvmlEncoderSessionInfo_st                    nvmlEncoderSessionInfo_t;
/* Event data - full definition needed by bridge so the failure-injection
 * Xid event surfaced via nvmlEventSetWait_v1/_v2 can populate the eventType
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
/* FBC session info stays opaque: ConfigurableDevice.GetFBCSessions currently
 * always returns an empty list, so the bridge only ever writes sessionCount. */
typedef struct nvmlFBCSessionInfo_st                        nvmlFBCSessionInfo_t;
/* FBC stats — full definition so nvmlDeviceGetFBCStats can populate the
 * caller's buffer (issue #636). Layout matches the upstream NVML header. */
typedef struct nvmlFBCStats_st
{
    unsigned int sessionsCount;
    unsigned int averageFPS;
    unsigned int averageLatency;
} nvmlFBCStats_t;
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
/* GPM metrics query — full definition needed by the bridge so
 * nvmlGpmMetricsGet (see bridge/gpm.go) can read the requested metricIds and
 * populate value/nvmlReturn/metricInfo per metric. Layout matches the
 * upstream NVML public header (NVML_GPM_METRIC_MAX = 333 for driver 550 /
 * go-nvml 0.13.x). */
#define NVML_GPM_METRIC_MAX 333
#define NVML_GPM_METRICS_GET_VERSION 1
typedef struct {
    char *shortName;
    char *longName;
    char *unit;
} nvmlGpmMetricMetricInfo_t;
typedef struct {
    unsigned int metricId;                 //!< IN: NVML_GPM_METRIC_? id to retrieve
    nvmlReturn_t nvmlReturn;               //!< OUT: status of this metric
    double value;                          //!< OUT: value, valid iff nvmlReturn == SUCCESS
    nvmlGpmMetricMetricInfo_t metricInfo;  //!< OUT: metric name and unit (may be NULL)
} nvmlGpmMetric_t;
typedef struct nvmlGpmMetricsGet_st {
    unsigned int version;                          //!< IN: NVML_GPM_METRICS_GET_VERSION
    unsigned int numMetrics;                       //!< IN: number of entries in metrics[]
    nvmlGpmSample_t sample1;                       //!< IN: first sample buffer
    nvmlGpmSample_t sample2;                       //!< IN: second sample buffer
    nvmlGpmMetric_t metrics[NVML_GPM_METRIC_MAX];  //!< IN/OUT: metricId in, value out
} nvmlGpmMetricsGet_t;
/* GPM support - full definition needed by bridge */
typedef struct nvmlGpmSupport_st {
    unsigned int version;
    unsigned int isSupportedDevice;
} nvmlGpmSupport_t;
#define NVML_GPM_SUPPORT_VERSION 1
typedef struct nvmlGpuDynamicPstatesInfo_st                 nvmlGpuDynamicPstatesInfo_t;

/* GPU Fabric information — full definitions needed by the bridge so
 * nvmlDeviceGetGpuFabricInfo / nvmlDeviceGetGpuFabricInfoV can populate
 * the caller's struct. Layout matches the upstream NVML public header
 * (versions v1 / v2 / v3); see go-nvml's pkg/nvml/nvml.h. */
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
/**
 * Platform identity — where the board physically sits in a rack. Full
 * definition needed so nvmlDeviceGetPlatformInfo can populate the caller's
 * struct. Layout matches the upstream NVML public header; see
 * go-nvml's pkg/nvml/nvml.h.
 *
 * v1 is deprecated upstream in favour of v2, which renames the same bytes:
 * rackGuid became chassisSerialNumber (on Blackwell the rack is identified by
 * the chassis serial), and the three location bytes gained clearer names. The
 * two layouts are therefore identical in size and offsets, which is what lets
 * the bridge serve either version from one payload.
 */
#define NVML_PLATFORM_CHASSIS_SERIAL_LEN 16
#define NVML_PLATFORM_IB_GUID_LEN        16

typedef struct nvmlPlatformInfo_v1_st {
    unsigned int  version;
    unsigned char ibGuid[NVML_PLATFORM_IB_GUID_LEN];
    unsigned char rackGuid[NVML_PLATFORM_CHASSIS_SERIAL_LEN];
    unsigned char chassisPhysicalSlotNumber;
    unsigned char computeSlotIndex;
    unsigned char nodeIndex;
    unsigned char peerType;
    unsigned char moduleId;
} nvmlPlatformInfo_v1_t;

typedef struct nvmlPlatformInfo_v2_st {
    unsigned int  version;
    unsigned char ibGuid[NVML_PLATFORM_IB_GUID_LEN];
    unsigned char chassisSerialNumber[NVML_PLATFORM_CHASSIS_SERIAL_LEN];
    unsigned char slotNumber;
    unsigned char trayIndex;
    unsigned char hostId;
    unsigned char peerType;
    unsigned char moduleId;
} nvmlPlatformInfo_v2_t;

typedef nvmlPlatformInfo_v2_t nvmlPlatformInfo_t;
/* Callers allocate this buffer from go-nvml's PlatformInfo, so a field added
 * here would make the bridge write past the caller's allocation. The equal
 * sizes are also what the version dispatch relies on: NVML_STRUCT_VERSION
 * encodes sizeof in its low bits, so v1 and v2 tags differ only in the version
 * byte, and a payload written for one fits the other exactly. */
_Static_assert(sizeof(nvmlPlatformInfo_v1_t) == sizeof(nvmlPlatformInfo_v2_t),
               "nvmlPlatformInfo v1 and v2 must stay the same size: the bridge serves both from one payload");
_Static_assert(sizeof(nvmlPlatformInfo_v2_t) == 44,
               "nvmlPlatformInfo_v2_t must stay 44 bytes to match the go-nvml ABI");
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
/* Row-remap availability histogram — full definition needed by the bridge so
 * nvmlDeviceGetRowRemapperHistogram can populate the caller's buffer (issue
 * #641). Each field counts the memory banks with that much remap capacity
 * left. Layout matches the upstream NVML header. */
typedef struct nvmlRowRemapperHistogramValues_st
{
    unsigned int max;
    unsigned int high;
    unsigned int partial;
    unsigned int low;
    unsigned int none;
} nvmlRowRemapperHistogramValues_t;
typedef struct nvmlSample_st                                nvmlSample_t;
typedef struct nvmlSystemConfComputeSettings_st             nvmlSystemConfComputeSettings_t;
typedef struct nvmlSystemDriverBranchInfo_st                nvmlSystemDriverBranchInfo_t;
typedef struct nvmlSystemEventSetCreateRequest_st           nvmlSystemEventSetCreateRequest_t;
typedef struct nvmlSystemEventSetFreeRequest_st             nvmlSystemEventSetFreeRequest_t;
typedef struct nvmlSystemEventSetWaitRequest_st             nvmlSystemEventSetWaitRequest_t;
typedef struct nvmlSystemRegisterEventRequest_st            nvmlSystemRegisterEventRequest_t;
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
/* Violation time — full definition so nvmlDeviceGetViolationStatus can
 * populate the caller's buffer (issue #636). Layout matches upstream. */
typedef struct nvmlViolationTime_st
{
    unsigned long long referenceTime;
    unsigned long long violationTime;
} nvmlViolationTime_t;
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
 * Completed struct bodies for the versioned getters implemented by hand
 * (nvmlDeviceGetPciInfoExt, nvmlDeviceGetMarginTemperature). The opaque
 * forward declarations above only name the typedefs; these complete the struct
 * tags so the bridge can read/write their fields. Layouts mirror
 * go-nvml's pkg/nvml/nvml.h (v1). version is an input
 * the caller stamps with the NVML_STRUCT_VERSION macro.
 */
struct nvmlPciInfoExt_st
{
    unsigned int version;         //!< IN: NVML_STRUCT_VERSION(PciInfoExt, 1)
    unsigned int domain;          //!< OUT: PCI domain
    unsigned int bus;             //!< OUT: PCI bus
    unsigned int device;          //!< OUT: PCI device
    unsigned int pciDeviceId;     //!< OUT: combined 16-bit device + 16-bit vendor id
    unsigned int pciSubSystemId;  //!< OUT: 32-bit subsystem device id
    unsigned int baseClass;       //!< OUT: 8-bit PCI base class code
    unsigned int subClass;        //!< OUT: 8-bit PCI sub class code
    char busId[NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE]; //!< OUT: domain:bus:device.function
};

struct nvmlMarginTemperature_st
{
    unsigned int version;            //!< IN: NVML_STRUCT_VERSION(MarginTemperature, 1)
    int          marginTemperature;  //!< OUT: margin to the thermal limit, degrees C
};

/*
 * powerScope is unsigned char upstream, so the trailing powerValueMw sits at
 * offset 8 behind three bytes of padding. Spelling the type out (rather than
 * widening it to unsigned int) is what keeps that offset where a caller built
 * against the real header expects to write.
 */
struct nvmlPowerValue_v2_st
{
    unsigned int  version;       //!< IN: NVML_STRUCT_VERSION(PowerValue, 2)
    unsigned char powerScope;    //!< IN: NVML_POWER_SCOPE_* target
    unsigned int  powerValueMw;  //!< IN: power limit to set, milliwatts
};

/*
 * Workload power profiles (Blackwell+). The bridge writes profilesInfo and
 * currentProfiles into the caller's allocation, so these need exact layouts
 * rather than the opaque forward declarations above: perfProfile is a
 * 255-entry array and a wrong element stride would walk off the end of a
 * caller built against the real header.
 */
#define NVML_255_MASK_NUM_ELEMS          8
#define NVML_WORKLOAD_POWER_MAX_PROFILES 255

typedef struct
{
    unsigned int mask[NVML_255_MASK_NUM_ELEMS];  //!< 255 bits, one per profile index
} nvmlMask255_t;

typedef enum
{
    NVML_POWER_PROFILE_OPERATION_CLEAR             = 0,  //!< Remove the named profiles from the request
    NVML_POWER_PROFILE_OPERATION_SET               = 1,  //!< Add the named profiles to the request
    NVML_POWER_PROFILE_OPERATION_SET_AND_OVERWRITE = 2,  //!< Replace the request with the named profiles

    NVML_POWER_PROFILE_OPERATION_MAX               = 3,
} nvmlPowerProfileOperation_t;

typedef struct
{
    unsigned int  version;          //!< OUT: NVML_STRUCT_VERSION(WorkloadPowerProfileInfo, 1)
    unsigned int  profileId;        //!< OUT: NVML_POWER_PROFILE_* semantic id
    unsigned int  priority;         //!< OUT: lower value wins arbitration
    nvmlMask255_t conflictingMask;  //!< OUT: profiles that cannot be combined with this one
} nvmlWorkloadPowerProfileInfo_t;

struct nvmlWorkloadPowerProfileProfilesInfo_st
{
    unsigned int                   version;           //!< IN: NVML_STRUCT_VERSION(WorkloadPowerProfileProfilesInfo, 1)
    nvmlMask255_t                  perfProfilesMask;  //!< OUT: bit set per supported profile
    nvmlWorkloadPowerProfileInfo_t perfProfile[NVML_WORKLOAD_POWER_MAX_PROFILES]; //!< OUT: metadata per supported profile
};

struct nvmlWorkloadPowerProfileCurrentProfiles_st
{
    unsigned int  version;                //!< IN: NVML_STRUCT_VERSION(WorkloadPowerProfileCurrentProfiles, 1)
    nvmlMask255_t perfProfilesMask;       //!< OUT: bit set per supported profile
    nvmlMask255_t requestedProfilesMask;  //!< OUT: profiles asked for
    nvmlMask255_t enforcedProfilesMask;   //!< OUT: profiles in effect after arbitration
};

struct nvmlWorkloadPowerProfileRequestedProfiles_st
{
    unsigned int  version;                //!< IN: NVML_STRUCT_VERSION(WorkloadPowerProfileRequestedProfiles, 1)
    nvmlMask255_t requestedProfilesMask;  //!< IN: profiles to add or remove
};

/*
 * Unlike its siblings this struct carries no version member, even though
 * upstream defines a version macro for it — so the bridge has no tag to check
 * and the operation is the only input that can be validated.
 */
struct nvmlWorkloadPowerProfileUpdateProfiles_v1_st
{
    nvmlPowerProfileOperation_t operation;          //!< IN: NVML_POWER_PROFILE_OPERATION_*
    nvmlMask255_t               updateProfilesMask; //!< IN: profiles the operation applies to
};

/*
 * NVML additions (go-nvml v0.13.2-0, #410). Remapped rows v2 is written by the
 * bridge, so keep this layout in sync with go-nvml's nvml.h.
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
 * nvmlSystemGetCPER_v1 in go-nvml's pkg/nvml/nvml.go)
 * fail to compile against an opaque C.nvmlXxx_t with "could not determine
 * what C.nvmlXxx_t refers to" unless the full struct body is visible.
 * Definitions extracted verbatim from
 * go-nvml's pkg/nvml/nvml.h.
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

/*
 * NVML additions (go-nvml v0.13.4-0). The generated stubs for the new entry
 * points take these types by pointer. Per the repo rule for go-nvml bumps they
 * carry full field layouts, not opaque forward declarations. Definitions and
 * their dependencies are extracted verbatim from go-nvml v0.13.4-0
 * pkg/nvml/nvml.h, in the same field order and widths.
 */
#define NVML_PERF_METRICS_PWR_MODEL_DLPPM_1X_MAX_CORE_RAILS                                2  //!< Maximum number of core rails for DLPPM 1x power model
typedef struct
{
    unsigned int freqkHz;                                   //!< Frequency in kilohertz
    unsigned long long utilPct;                             //!< Utilization percentage (fixed-point)
} nvmlRailMetrics_t;

typedef struct
{
    nvmlRailMetrics_t rails[NVML_PERF_METRICS_PWR_MODEL_DLPPM_1X_MAX_CORE_RAILS];  //!< Array of core rail metrics
} nvmlCoreRailMetrics_t;

typedef struct
{
    unsigned int pwrmW;                                     //!< Power consumption in milliwatts
} nvmlPmgrPwrTuple_t;

typedef struct
{
    unsigned int perfms;                                    //!< Performance metric in milliseconds
} nvmlPwrModelMetricsDlppm1xPerf_t;

#define NVML_PERF_METRICS_NNE_DESC_INFERENCE_LOOPS_MAX                                     8  //!< Maximum number of NNE descriptor inference loops
#define NVML_PERF_METRICS_PWR_MODEL_SCALE_METRICS_INPUT_MAX                               16  //!< Maximum number of power model scale metrics inputs
typedef struct
{
    unsigned char bValid;                                   //!< Validity flag: non-zero if metrics are valid
    nvmlCoreRailMetrics_t coreRail;                         //!< Core rail metrics
    nvmlRailMetrics_t fbRail;                               //!< Fb rail metrics
    nvmlPmgrPwrTuple_t tgpPwrTuple;                         //!< Total Graphics Power (TGP) in milliwatts
    nvmlPwrModelMetricsDlppm1xPerf_t perfMetrics;           //!< Performance metrics
} nvmlPwrModelMetricsDlppm1x_t;

#define NVML_PERF_METRICS_PWR_MODEL_METRICS_DLPPM_1X_OBESRVED_INTIAL_DRAMCLK_ESTIMATES_MAX 3  //!< Maximum number of initial DRAMCLK estimates for DLPPM 1x observed metrics
#define NVML_PERF_METRICS_PWR_MODEL_SCALE_LOOPS_MAX_PFPP_1X                               32  //!< Maximum number of power model scale loops for PFPP 1x
typedef struct
{
    nvmlPwrModelMetricsDlppm1x_t estimatedMetrics[NVML_PERF_METRICS_NNE_DESC_INFERENCE_LOOPS_MAX];  //!< Array of estimated metrics for each inference loop
    unsigned char numEstimatedMetrics;                                                              //!< Number of valid entries in estimatedMetrics array
} nvmlPwrModelMetricsDlppm1xDramclkEstimates_t;

typedef struct
{
    unsigned int freqkHz[NVML_PERF_METRICS_PWR_MODEL_SCALE_METRICS_INPUT_MAX];  //!< Array of input frequencies in kilohertz for each domain
    unsigned int estTgpPwrmW;                                                   //!< Estimated Total Graphics Power in milliwatts
} nvmlPwrModelMetricsSamplePfpp1x_t;

typedef struct
{
    unsigned int freqkHz;                                   //!< Operating frequency in kilohertz
    unsigned int pwrmW;                                     //!< Power consumption at this frequency in milliwatts
} nvmlPwrModelOperatingPointPfpp1x_t;

typedef struct
{
    nvmlPwrModelMetricsDlppm1xDramclkEstimates_t initialDramclkEst[NVML_PERF_METRICS_PWR_MODEL_METRICS_DLPPM_1X_OBESRVED_INTIAL_DRAMCLK_ESTIMATES_MAX];  //!< Initial DRAMCLK estimates for different scenarios
    unsigned char bValid;                                                                                                                                //!< Validity flag: non-zero if observed metrics are valid
    nvmlCoreRailMetrics_t coreRail;                                                                                                                      //!< Observed core rail metrics
    nvmlRailMetrics_t fbRail;                                                                                                                            //!< Observed fb rail metrics
    nvmlPmgrPwrTuple_t tgpPwrTuple;                                                                                                                      //!< Observed Total Graphics Power (TGP) in milliwatts
    nvmlPwrModelMetricsDlppm1xPerf_t perfMetrics;                                                                                                        //!< Observed performance metrics
} nvmlObservedMetrics_t;

typedef struct
{
    unsigned char numVfPoints;                                                                                //!< Number of valid vf points
    nvmlPwrModelMetricsSamplePfpp1x_t estimatedMetrics[NVML_PERF_METRICS_PWR_MODEL_SCALE_LOOPS_MAX_PFPP_1X];  //!< Array of estimated metrics for different operating points
    unsigned char bValid;                                                                                     //!< Validity flag: non-zero if metrics are valid
    nvmlPwrModelOperatingPointPfpp1x_t maxPerfPerWattPoint;                                                   //!< Operating point with maximum performance per watt
    nvmlPwrModelOperatingPointPfpp1x_t fmaxAtVmaxPoint;                                                       //!< Operating point at maximum frequency and voltage
    unsigned int tgpHeadroommW;                                                                               //!< TGP headroom in milliwatts
} nvmlPwrModelMetricsPfpp1x_t;

typedef struct
{
    nvmlObservedMetrics_t observedMetrics;                  //!< Observed metrics from the DLPPC 2x controller
} nvmlPerfMetricsDlppc2xSample_t;

typedef struct
{
    nvmlPwrModelMetricsPfpp1x_t estimatedMetrics;           //!< Estimated metrics from the PFPP 1x controller
} nvmlPerfMetricsPfpp1xSample_t;

#define NVML_PERF_METRICS_CONTROLLER_SAMPLE_CONTROLLER_MAX_NUM                             4  //!< Maximum number of controllers that can be sampled
typedef struct
{
    unsigned int controllerType;                            //!< Controller type: NVML_PERF_METRICS_CONTROLLER_TYPE_DLPPC_2X or NVML_PERF_METRICS_CONTROLLER_TYPE_PFPP_1X
    union{
        nvmlPerfMetricsDlppc2xSample_t dlppc2x;             //!< DLPPC 2x controller sample data
        nvmlPerfMetricsPfpp1xSample_t  pfpp1x;              //!< PFPP 1x controller sample data
    } data;                                                 //!< Union containing controller-specific data
} nvmlPerfMetricControllerSample_t;

#define NVML_DEVICE_UUID_V2_BUFFER_SIZE               96
#define NVML_GPU_FABRIC_CLIQUE_MAX       64
#define NVML_PERF_METRICS_SAMPLE_COUNT                                                    13  //!< Total number of performance metrics samples that can be collected
typedef struct
{
    unsigned int maxSpareGroupCount;    //!< Number of groups that have maximum spare.
    unsigned int noSpareGroupCount;     //!< Number of groups that have not spare.
} nvmlEccBankRemapperHistogram_v1_t;

typedef struct
{
    unsigned char type;  //!< Clique type. See NVML_GPU_FABRIC_CLIQUE_TYPE_*
    unsigned int  id;    //!< Clique ID assigned by the Fabric Manager
} nvmlGpuFabricClique_v1_t;

typedef struct
{
    unsigned int linkId;         //!<[in] LinkId
    unsigned int sampleType;     //!<[in] Type of telemetry to sample, specified by `nvmlNvlinkTelemetrySampleType_t`
    unsigned int sampleCount;    //!<[in,out]: Number of samples users need to allocate. If set to 0, will return max
                                 //! supported count of samples without touching the `samples` pointer.
    unsigned long long *samples; //!<[in,out]: Array of samples allocated by the user. Can be set to NULL when getting count
    nvmlReturn_t nvmlReturn;     //!<[out]: Return code for retrieving this sample. This must be checked by the client
                                 //! before looking at any output values, as they are invalid if `nvmlReturn != NVML_SUCCESS`.
} nvmlNvlinkTelemetrySample_v1_t;

typedef struct
{
    unsigned char numControllerData;                                                                          //!< Number of valid controller samples in this sample
    nvmlPerfMetricControllerSample_t controllerData[NVML_PERF_METRICS_CONTROLLER_SAMPLE_CONTROLLER_MAX_NUM];  //!< Array of controller samples
} nvmlPerfMetricsSample_t;


typedef struct
{
    const char* nameSpace;         //!<[in] Full path to sysfs cgroup file name
    unsigned long long softLimit;  //!<[in] Soft memory limit in Bytes.
    unsigned long long hardLimit;  //!<[in] Hard memory limit in Bytes.
} nvmlSetMemoryLimits_v1_t;

typedef struct
{
    const char* nameSpace;           //!<[in]  Full path to sysfs cgroup file name
    unsigned long long softLimit;    //!<[out] Currently set soft memory limit in Bytes.
    unsigned long long hardLimit;    //!<[out] Currently set hard memory limit in Bytes.
    unsigned long long currentUsed;  //!<[out] Currently used memory in Bytes.
} nvmlGetMemoryLimits_v1_t;

typedef struct
{
    unsigned int numSamples;                                          //!< Number of samples in the samples array
    nvmlPerfMetricsSample_t samples[NVML_PERF_METRICS_SAMPLE_COUNT];  //!< Array of performance metrics samples
} nvmlPerfMetricsSamples_v1_t;

typedef struct
{
    nvmlEnableState_t inBandEnableRequest;    //!< [out] In-band enable requested (NVML_FEATURE_ENABLED) or not requested (NVML_FEATURE_DISABLED)
    nvmlEnableState_t featureAllowedByAdmin;  //!< [out] Feature allowed by out-of-band/admin (NVML_FEATURE_ENABLED) or not allowed (NVML_FEATURE_DISABLED)
    nvmlEnableState_t adminOverrideEnabled;   //!< [out] Out-of-band/admin override active (NVML_FEATURE_ENABLED) or inactive (NVML_FEATURE_DISABLED)
    nvmlEnableState_t enablementStatus;       //!< [out] Enablement after arbitration: active (NVML_FEATURE_ENABLED) or inactive (NVML_FEATURE_DISABLED)
    unsigned int adjustedLimitMw;             //!< [out] Adjusted TGP limit in milliwatts (valid only when feature is enabled)
} nvmlAdaptiveTgpModeInfo_v1_t;

typedef struct
{
    unsigned int activeRemappings;                      //!< Number of active remappings
    unsigned int inactiveRemappings;                    //!< Number of inactive remappings
    unsigned int bPending;                              //!< Whether there exists any pending bank remapping. 0 for no pending remapping, 1 for pending remapping.
    nvmlEccBankRemapperHistogram_v1_t histogram;        //!< Bank remapper histogram
} nvmlEccBankRemapperStatus_v1_t;

typedef struct
{
    unsigned int count; //!< [out] Number of context records associated with the most recent event.
} nvmlEventSetGetContextCount_v1_t;

typedef struct
{
    unsigned int   index;                                //!< [in] Zero-based context index.
    unsigned int   nvmlGpuOperationalEventContextType;  //!< [out] \ref nvmlGpuOperationalEventContextType_t value describing the NVML public interpretation of the context payload.
    unsigned int   sourceEventContextType;              //!< [out] Source-defined context payload type identifier carried by the event.
    unsigned int   dataSize;                            //!< [out] Context payload size in bytes, excluding alignment padding.
    unsigned short dataFormatVersion;                   //!< [out] Payload format version for \c sourceEventContextType.
} nvmlEventSetGetContextInfo_v1_t;

typedef struct
{
    void         *data;     //!< [in] Optional caller-owned buffer that receives the raw context payload.
    unsigned int index;     //!< [in] Zero-based context index.
    unsigned int dataSize;  //!< [in,out] Size of \c data on input; actual or required size on output.
} nvmlEventSetGetContextData_v1_t;

typedef struct
{
    unsigned int index;    //!< [in] Zero-based context index.
    unsigned int xidCode;  //!< [out] Legacy Xid code carried in a GPU Operational Event context.
} nvmlEventSetGetGpuOperationalEventContextLegacyXid_v1_t;

typedef struct
{
    unsigned char            clusterUuid[NVML_GPU_FABRIC_UUID_LEN]; //!< Uuid of the cluster to which this GPU belongs
    nvmlReturn_t             status;                               //!< Probe Error status, if any. Must be checked only if state returns "complete".
    nvmlGpuFabricClique_v1_t cliques[NVML_GPU_FABRIC_CLIQUE_MAX];  //!< Clique entries, sorted by ascending type then ascending id
    unsigned int             numCliques;                           //!< Number of valid entries in \a cliques[]
    nvmlGpuFabricState_t     state;                                //!< Current Probe State. See NVML_GPU_FABRIC_STATE_*
    unsigned int             healthMask;                           //!< GPU Fabric health Status Mask. See NVML_GPU_FABRIC_HEALTH_MASK_*
    unsigned char            healthSummary;                        //!< GPU Fabric health summary. See NVML_GPU_FABRIC_HEALTH_SUMMARY_*
} nvmlGpuFabricInfo_v4_t;

typedef struct nvmlGpuOperationalEventConfig_v1_st
{
    char                                uuid[NVML_DEVICE_UUID_V2_BUFFER_SIZE]; //!< [in] Target GPU UUID string. Must be a NULL-terminated "GPU-..." UUID.
    unsigned int                        minLogLevel;                           //!< [in] \ref nvmlGpuOperationalEventLogLevel_t value for the minimum GPU Operational Event log level. \c NVML_GPU_OPERATIONAL_EVENT_LOG_LEVEL_ALL means no filter.
    unsigned int                        minSeverity;                           //!< [in] \ref nvmlOperationalEventSeverity_t value for the minimum Operational Event severity threshold. \c NVML_OPERATIONAL_EVENT_SEVERITY_ALL means no filter.
} nvmlGpuOperationalEventConfig_v1_t;

typedef struct
{
    unsigned int        timeoutMs;                             //!< [in] Maximum amount of time to wait, in milliseconds.
    unsigned int        dataType;                              //!< [out] \ref nvmlEventDataType_t value indicating which event-data format is populated.
    char                uuid[NVML_DEVICE_UUID_V2_BUFFER_SIZE]; //!< [out] UUID for the GPU where the event occurred. Empty if unavailable.
    char                sourceModule[16];                      //!< [out] Source module signature for structured events. Not guaranteed to be NULL-terminated. Empty for NVML event-bit events.
    unsigned long long  eventType;                             //!< [out] NVML event bit for \ref NVML_EVENT_DATA_TYPE_NVML_EVENT events; \ref nvmlEventTypeNone for structured events.
    unsigned long long  eventData;                             //!< [out] Xid code for \ref nvmlEventTypeXidCriticalError, or 0 when not applicable.
    unsigned long long  groupCursor;                           //!< [out] Structured event group identifier. 0 for NVML event-bit events.
    unsigned long long  instanceId;                            //!< [out] Structured event sequence identifier. 0 for NVML event-bit events.
    unsigned long long  timestampUsec;                         //!< [out] Event timestamp in microseconds. 0 if unavailable.
    unsigned long long  traceId;                               //!< [out] Structured event trace identifier. 0 for NVML event-bit events.
    unsigned int        gpuInstanceId;                         //!< [out] MIG GPU instance ID for NVML event-bit data, or \c NVML_GPU_INSTANCE_ID_ANY when not applicable.
    unsigned int        computeInstanceId;                     //!< [out] MIG compute instance ID for NVML event-bit data, or \c NVML_COMPUTE_INSTANCE_ID_ANY when not applicable.
    unsigned int        severity;                              //!< [out] \ref nvmlOperationalEventSeverity_t value for structured events. May contain newer severity values not named in this header. \c NVML_OPERATIONAL_EVENT_SEVERITY_ALL for NVML event-bit events.
    unsigned int        categoryId;                            //!< [out] Source-defined structured event category identifier. 0 for NVML event-bit events.
    unsigned int        moduleEventCode;                       //!< [out] Source-module-defined event code. Interpret with \c sourceModule. 0 for NVML event-bit events.
    unsigned int        scope;                                 //!< [out] Structured event scope identifier. 0 for NVML event-bit events.
    unsigned int        originator;                            //!< [out] Structured event originator identifier. 0 for NVML event-bit events.
    unsigned int        moduleInstance;                        //!< [out] Structured event module instance identifier. 0 for NVML event-bit events.
    unsigned int        chipletId;                             //!< [out] Structured event chiplet identifier. 0 for NVML event-bit events.
    unsigned int        logLevel;                              //!< [out] \ref nvmlGpuOperationalEventLogLevel_t value for structured GPU Operational Events. May contain newer log-level values not named in this header. \c NVML_GPU_OPERATIONAL_EVENT_LOG_LEVEL_ALL for NVML event-bit events.
    unsigned int        attributes;                            //!< [out] Bitmask of \c NVML_OPERATIONAL_EVENT_ATTR_* values for structured events. May contain newer bits not named in this header. 0 for NVML event-bit events. May include \c NVML_OPERATIONAL_EVENT_ATTR_OVERFLOW if events or associated payloads were dropped.
    unsigned int        groupCperSize;                         //!< [out] Associated CPER record size in bytes. 0 when unavailable.
    unsigned int        groupAttributes;                       //!< [out] Bitmask of \c NVML_OPERATIONAL_EVENT_GROUP_ATTR_* values for structured events. May contain newer bits not named in this header. 0 for NVML event-bit events.
    unsigned char       groupSize;                             //!< [out] Total number of events in the structured event group. 0 for NVML event-bit events.
    unsigned char       groupIndex;                            //!< [out] Zero-based index within the structured event group. 0 for NVML event-bit events.
} nvmlEventSetWait_v3_t;

typedef struct
{
    unsigned int bSetBest;           //!< [in]  - Set to the best available Bandwidth mode
    unsigned int bwMode;             //!< [in]  - Requested Bandwidth mode to set. Values can be found from \ref nvmlDeviceGetNvlinkSupportedBwModes()
    unsigned int asyncPollTimeoutMs; //!< [out] - Time in ms to poll to validate bandwidth setting.
} nvmlNvlinkSetBwModeAsync_v1_t;

typedef struct
{
    unsigned int telemetryCount;                      //!<[in]     Number of valid entries in \a telemetrySamples
    nvmlNvlinkTelemetrySample_v1_t *telemetrySamples; //!<[in,out] Caller-allocated array of \a telemetryCount request slots
} nvmlNvlinkTelemetrySamples_v1_t;


#ifdef __cplusplus
}
#endif

#endif /* MOCK_NVML_TYPES_H */
