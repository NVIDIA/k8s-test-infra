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

/* Buffer for a device or MIG profile name. Fixed by the NVML ABI: it sizes
 * the trailing name array of the versioned MIG profile-info structs, which
 * callers allocate. */
#define NVML_DEVICE_NAME_V2_BUFFER_SIZE         96

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
/* Upstream NVML hands out vGPU instances as plain IDs, not opaque handles. */
typedef unsigned int nvmlVgpuInstance_t;

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

/* Upstream wraps the opaque GPM sample pointer in a one-field struct. */
typedef struct {
    struct nvmlGpmSample_st* handle;
} nvmlGpmSample_t;

/* --- Opaque struct types (only used via pointer in function signatures) --- */
/* Accounting stats — full definition so nvmlDeviceGetAccountingStats can fill
 * the caller's buffer. Layout matches the upstream NVML header. */
typedef struct nvmlAccountingStats_st
{
    unsigned int       gpuUtilization;     //!< OUT: lifetime GPU utilization, percent
    unsigned int       memoryUtilization;  //!< OUT: lifetime memory utilization, percent
    unsigned long long maxMemoryUsage;     //!< OUT: peak memory allocated, bytes
    unsigned long long time;               //!< OUT: run time in ms; 0 while running
    unsigned long long startTime;          //!< OUT: start time, CPU usec
    unsigned int       isRunning;          //!< OUT: 1 running, 0 terminated
    unsigned int       reserved[5];
} nvmlAccountingStats_t;
_Static_assert(sizeof(nvmlAccountingStats_t) == 56,
               "nvmlAccountingStats_t must stay 56 bytes to match the go-nvml ABI");
typedef struct nvmlActiveVgpuInstanceInfo_st                nvmlActiveVgpuInstanceInfo_t;
/* BAR1 Memory - full definition needed by bridge */
typedef struct nvmlBAR1Memory_st
{
    unsigned long long bar1Total;    //!< Total BAR1 Memory (in bytes)
    unsigned long long bar1Free;     //!< Unallocated BAR1 Memory (in bytes)
    unsigned long long bar1Used;     //!< Allocated Used Memory (in bytes)
} nvmlBAR1Memory_t;
typedef struct nvmlBridgeChipHierarchy_st                   nvmlBridgeChipHierarchy_t;
/* An NVML_BUS_TYPE_* code; nvml.h declares it a plain integer, not a struct. */
typedef unsigned int                                        nvmlBusType_t;
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
/* Clock monitor fault status. Full definition so nvmlDeviceGetClkMonStatus
 * can fill the caller's buffer, which carries no version tag to check: the
 * size below is the whole of the ABI contract. */
#define MAX_CLK_DOMAINS 32
typedef struct nvmlClkMonFaultInfo_struct
{
    unsigned int clkApiDomain;
    unsigned int clkDomainFaultMask;
} nvmlClkMonFaultInfo_t;
typedef struct nvmlClkMonStatus_status
{
    unsigned int          bGlobalStatus;
    unsigned int          clkMonListSize;
    nvmlClkMonFaultInfo_t clkMonList[MAX_CLK_DOMAINS];
} nvmlClkMonStatus_t;
_Static_assert(sizeof(nvmlClkMonStatus_t) == 264,
               "nvmlClkMonStatus_t must stay 264 bytes to match the go-nvml ABI");
/* Clock offset — full definition needed by bridge so clocks.go can read the
 * caller's (type, pstate) and fill in the offset and its range. */
typedef struct nvmlClockOffset_st
{
    unsigned int    version;            //!< IN: NVML_STRUCT_VERSION(ClockOffset, 1)
    nvmlClockType_t type;
    nvmlPstates_t   pstate;
    int             clockOffsetMHz;
    int             minClockOffsetMHz;
    int             maxClockOffsetMHz;
} nvmlClockOffset_t;
/* --- MIG compute instances ---
 * Full definitions needed by bridge so mig.go can fill the caller's buffers.
 * The v2 and v3 profile structs differ from v1 by a leading version field, a
 * trailing name, and (v3) a capabilities field; all three are live because
 * nvmlGpuInstanceGetComputeInstanceProfileInfoV dispatches on the version the
 * caller stamps. See mig_layout_test.go, which pins every size and offset here
 * against go-nvml's Go structs — callers allocate these buffers from those, so
 * a mismatch means the bridge writes past the caller's allocation. */
typedef struct nvmlComputeInstancePlacement_st
{
    unsigned int start;                 //!< Index of first occupied compute slice
    unsigned int size;                  //!< Number of compute slices occupied
} nvmlComputeInstancePlacement_t;

typedef struct nvmlComputeInstanceProfileInfo_st
{
    unsigned int id;                    //!< Unique profile ID within the GPU instance
    unsigned int sliceCount;            //!< GPU Slice count
    unsigned int instanceCount;         //!< Compute instance count
    unsigned int multiprocessorCount;   //!< Streaming Multiprocessor count
    unsigned int sharedCopyEngineCount; //!< Shared Copy Engine count
    unsigned int sharedDecoderCount;    //!< Shared Decoder Engine count
    unsigned int sharedEncoderCount;    //!< Shared Encoder Engine count
    unsigned int sharedJpegCount;       //!< Shared JPEG Engine count
    unsigned int sharedOfaCount;        //!< Shared OFA Engine count
} nvmlComputeInstanceProfileInfo_t;

typedef struct nvmlComputeInstanceProfileInfo_v2_st
{
    unsigned int version;                       //!< IN: NVML_STRUCT_VERSION(ComputeInstanceProfileInfo, 2)
    unsigned int id;
    unsigned int sliceCount;
    unsigned int instanceCount;
    unsigned int multiprocessorCount;
    unsigned int sharedCopyEngineCount;
    unsigned int sharedDecoderCount;
    unsigned int sharedEncoderCount;
    unsigned int sharedJpegCount;
    unsigned int sharedOfaCount;
    char         name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
} nvmlComputeInstanceProfileInfo_v2_t;

typedef struct nvmlComputeInstanceProfileInfo_v3_st
{
    unsigned int version;                       //!< IN: NVML_STRUCT_VERSION(ComputeInstanceProfileInfo, 3)
    unsigned int id;
    unsigned int sliceCount;
    unsigned int instanceCount;
    unsigned int multiprocessorCount;
    unsigned int sharedCopyEngineCount;
    unsigned int sharedDecoderCount;
    unsigned int sharedEncoderCount;
    unsigned int sharedJpegCount;
    unsigned int sharedOfaCount;
    char         name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
    unsigned int capabilities;
} nvmlComputeInstanceProfileInfo_v3_t;

typedef struct nvmlComputeInstanceInfo_st
{
    nvmlDevice_t                   device;      //!< Parent device
    nvmlGpuInstance_t              gpuInstance; //!< Parent GPU instance
    unsigned int                   id;
    unsigned int                   profileId;
    nvmlComputeInstancePlacement_t placement;
} nvmlComputeInstanceInfo_t;
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
/* What one cooler drives and how — full definition so
 * nvmlDeviceGetCoolerInfo can populate the caller's buffer. version and index
 * are inputs: the caller stamps the NVML_STRUCT_VERSION tag and names the
 * cooler it is asking about, which is why the bridge reads index back out
 * rather than being handed it as an argument. Layout matches go-nvml's
 * pkg/nvml/nvml.h (v1), pinned by fans_layout_test.go. */
typedef struct nvmlCoolerInfo_st
{
    unsigned int version;      //!< IN: NVML_STRUCT_VERSION(CoolerInfo, 1)
    unsigned int index;        //!< IN: the cooler index
    unsigned int signalType;   //!< OUT: nvmlCoolerControl_t
    unsigned int target;       //!< OUT: nvmlCoolerTarget_t
} nvmlCoolerInfo_t;
/* Callers allocate this buffer from go-nvml's CoolerInfo, so a field added here
 * would make the bridge write past the caller's allocation. */
_Static_assert(sizeof(nvmlCoolerInfo_t) == 16,
               "nvmlCoolerInfo_t must stay 16 bytes to match the go-nvml ABI");
/* Versioned: the caller stamps NVML_STRUCT_VERSION(DeviceAddressingMode, 1),
 * whose low 24 bits are this size. value is an NVML_DEVICE_ADDRESSING_MODE_*. */
typedef struct nvmlDeviceAddressingMode_v1_st
{
    unsigned int version;
    unsigned int value;
} nvmlDeviceAddressingMode_v1_t;
typedef nvmlDeviceAddressingMode_v1_t nvmlDeviceAddressingMode_t;
_Static_assert(sizeof(nvmlDeviceAddressingMode_v1_t) == 8,
               "nvmlDeviceAddressingMode_v1_t must stay 8 bytes to match the go-nvml ABI");
/* A MIG device's engine and memory allocation. Full definition needed by
 * bridge: this is the query the device plugin compares across devices before
 * it will accept a node with migStrategy=single. */
typedef struct nvmlDeviceAttributes_st
{
    unsigned int       multiprocessorCount;
    unsigned int       sharedCopyEngineCount;
    unsigned int       sharedDecoderCount;
    unsigned int       sharedEncoderCount;
    unsigned int       sharedJpegCount;
    unsigned int       sharedOfaCount;
    unsigned int       gpuInstanceSliceCount;
    unsigned int       computeInstanceSliceCount;
    unsigned long long memorySizeMB;        //!< Device memory size (in MiB)
} nvmlDeviceAttributes_t;
/* Versioned like nvmlDeviceAddressingMode_t. capMask is NVML_DEV_CAP_* bits. */
typedef struct nvmlDeviceCapabilities_v1_st
{
    unsigned int version;
    unsigned int capMask;
} nvmlDeviceCapabilities_v1_t;
typedef nvmlDeviceCapabilities_v1_t nvmlDeviceCapabilities_t;
_Static_assert(sizeof(nvmlDeviceCapabilities_v1_t) == 8,
               "nvmlDeviceCapabilities_v1_t must stay 8 bytes to match the go-nvml ABI");
/* Clock strings — full definitions needed by bridge so clocks.go can copy the
 * rendered string into the caller's fixed buffer. */
#define NVML_PERF_MODES_BUFFER_SIZE 2048
typedef struct nvmlDeviceCurrentClockFreqs_st
{
    unsigned int version;                          //!< IN: NVML_STRUCT_VERSION(DeviceCurrentClockFreqs, 1)
    char         str[NVML_PERF_MODES_BUFFER_SIZE]; //!< OUT: the current clock frequency string
} nvmlDeviceCurrentClockFreqs_t;
typedef struct nvmlDevicePerfModes_st
{
    unsigned int version;                          //!< IN: NVML_STRUCT_VERSION(DevicePerfModes, 1)
    char         str[NVML_PERF_MODES_BUFFER_SIZE]; //!< OUT: the performance modes string
} nvmlDevicePerfModes_t;
/* PowerMizer modes, read and written in place: the caller passes mode IN and
 * the bridge fills currentMode and supportedPowerMizerModes OUT. The struct
 * carries no version member. Layout matches go-nvml's pkg/nvml/nvml.h (v1),
 * pinned by power_policy_layout_test.go. */
typedef struct nvmlDevicePowerMizerModes_v1_st
{
    unsigned int currentMode;              //!< OUT: the current PowerMizer mode
    unsigned int mode;                     //!< IN: the PowerMizer mode to set
    unsigned int supportedPowerMizerModes; //!< OUT: bitmask of supported modes
} nvmlDevicePowerMizerModes_v1_t;
_Static_assert(sizeof(nvmlDevicePowerMizerModes_v1_t) == 12,
               "nvmlDevicePowerMizerModes_v1_t must stay 12 bytes to match the go-nvml ABI");
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
/* Excluded GPU info — full definition so nvmlGetExcludedDeviceInfoByIndex can
 * populate the caller's buffer. Layout matches the upstream NVML header. */
#define NVML_DEVICE_UUID_BUFFER_SIZE            80
typedef struct nvmlExcludedDeviceInfo_st {
    nvmlPciInfo_t pciInfo;                   //!< The PCI information for the excluded GPU
    char uuid[NVML_DEVICE_UUID_BUFFER_SIZE]; //!< The ASCII string UUID for the excluded GPU
} nvmlExcludedDeviceInfo_t;
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
/* One fan's tachometer reading — full definition so
 * nvmlDeviceGetFanSpeedRPM can populate the caller's buffer. As with
 * nvmlCoolerInfo_t the fan index arrives inside the struct rather than as an
 * argument, so the bridge reads it back out to know which fan is being asked
 * about. Layout matches go-nvml's pkg/nvml/nvml.h (v1), pinned by
 * fans_layout_test.go. */
typedef struct nvmlFanSpeedInfo_st
{
    unsigned int version;   //!< IN: NVML_STRUCT_VERSION(FanSpeedInfo, 1)
    unsigned int fan;       //!< IN: the fan index
    unsigned int speed;     //!< OUT: the fan speed in RPM
} nvmlFanSpeedInfo_t;
/* Callers allocate this buffer from go-nvml's FanSpeedInfo, so a field added
 * here would make the bridge write past the caller's allocation. */
_Static_assert(sizeof(nvmlFanSpeedInfo_t) == 12,
               "nvmlFanSpeedInfo_t must stay 12 bytes to match the go-nvml ABI");
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
/* Dynamic P-state utilization — full definition needed by bridge. */
#define NVML_MAX_GPU_UTILIZATIONS 8
typedef struct {
    unsigned int bIsPresent;
    unsigned int percentage;
    unsigned int incThreshold;
    unsigned int decThreshold;
} nvmlGpuDynamicPstatesInfoUtilization_t;
typedef struct nvmlGpuDynamicPstatesInfo_st
{
    unsigned int                           flags; //!< Reserved for future use
    nvmlGpuDynamicPstatesInfoUtilization_t utilization[NVML_MAX_GPU_UTILIZATIONS];
} nvmlGpuDynamicPstatesInfo_t;

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
/* --- MIG GPU instances ---
 * Full definitions needed by bridge so mig.go can fill the caller's buffers.
 * Note v3 drops isP2pSupported and appends capabilities, so it is not a
 * superset of v2; the version the caller stamps decides which layout the
 * bridge writes. Sizes and offsets are pinned in mig_layout_test.go. */
typedef struct nvmlGpuInstancePlacement_st
{
    unsigned int start;               //!< Index of first occupied memory slice
    unsigned int size;                //!< Number of memory slices occupied
} nvmlGpuInstancePlacement_t;

typedef struct nvmlGpuInstanceProfileInfo_st
{
    unsigned int       id;                  //!< Unique profile ID within the device
    unsigned int       isP2pSupported;      //!< Peer-to-Peer support
    unsigned int       sliceCount;          //!< GPU Slice count
    unsigned int       instanceCount;       //!< GPU instance count
    unsigned int       multiprocessorCount; //!< Streaming Multiprocessor count
    unsigned int       copyEngineCount;
    unsigned int       decoderCount;
    unsigned int       encoderCount;
    unsigned int       jpegCount;
    unsigned int       ofaCount;
    unsigned long long memorySizeMB;        //!< Memory size in MBytes
} nvmlGpuInstanceProfileInfo_t;

typedef struct nvmlGpuInstanceProfileInfo_v2_st
{
    unsigned int       version;             //!< IN: NVML_STRUCT_VERSION(GpuInstanceProfileInfo, 2)
    unsigned int       id;
    unsigned int       isP2pSupported;
    unsigned int       sliceCount;
    unsigned int       instanceCount;
    unsigned int       multiprocessorCount;
    unsigned int       copyEngineCount;
    unsigned int       decoderCount;
    unsigned int       encoderCount;
    unsigned int       jpegCount;
    unsigned int       ofaCount;
    unsigned long long memorySizeMB;
    char               name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
} nvmlGpuInstanceProfileInfo_v2_t;

typedef struct nvmlGpuInstanceProfileInfo_v3_st
{
    unsigned int       version;             //!< IN: NVML_STRUCT_VERSION(GpuInstanceProfileInfo, 3)
    unsigned int       id;
    unsigned int       sliceCount;
    unsigned int       instanceCount;
    unsigned int       multiprocessorCount;
    unsigned int       copyEngineCount;
    unsigned int       decoderCount;
    unsigned int       encoderCount;
    unsigned int       jpegCount;
    unsigned int       ofaCount;
    unsigned long long memorySizeMB;
    char               name[NVML_DEVICE_NAME_V2_BUFFER_SIZE];
    unsigned int       capabilities;
} nvmlGpuInstanceProfileInfo_v3_t;

typedef struct nvmlGpuInstanceInfo_st
{
    nvmlDevice_t               device;    //!< Parent device
    unsigned int               id;
    unsigned int               profileId;
    nvmlGpuInstancePlacement_t placement;
} nvmlGpuInstanceInfo_t;
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
typedef struct nvmlHwbcEntry_st                             nvmlHwbcEntry_t;
typedef struct nvmlLedState_st                              nvmlLedState_t;
typedef struct nvmlMarginTemperature_st                     nvmlMarginTemperature_t;
typedef struct nvmlNvLinkUtilizationControl_st              nvmlNvLinkUtilizationControl_t;
typedef struct nvmlPRMTLV_v1_st                             nvmlPRMTLV_v1_t;
typedef struct nvmlPSUInfo_st                               nvmlPSUInfo_t;
typedef struct nvmlPciInfoExt_st                            nvmlPciInfoExt_t;
/* Versioned like nvmlDeviceAddressingMode_t. The 64-bit value is aligned to
 * 8, so 4 bytes of padding follow version. */
typedef struct nvmlPdi_v1_st
{
    unsigned int       version;
    unsigned long long value;
} nvmlPdi_v1_t;
typedef nvmlPdi_v1_t nvmlPdi_t;
_Static_assert(sizeof(nvmlPdi_v1_t) == 16 && offsetof(nvmlPdi_v1_t, value) == 8,
               "nvmlPdi_v1_t must stay 16 bytes with value at offset 8 to match the go-nvml ABI");
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

/**
 * vGPU. Full definitions so the host's nvmlVgpuInstanceGetVmID and
 * nvmlVgpuInstanceGetLicenseInfo_v2 and the guest's
 * nvmlDeviceGetGridLicensableFeatures_v4 can fill the caller's buffer. Layout
 * matches the upstream NVML public header; see go-nvml's pkg/nvml/nvml.h.
 */
#define NVML_GRID_LICENSE_BUFFER_SIZE       128
#define NVML_GRID_LICENSE_FEATURE_MAX_COUNT 3

typedef enum nvmlVgpuVmIdType {
    NVML_VGPU_VM_ID_DOMAIN_ID = 0,
    NVML_VGPU_VM_ID_UUID      = 1,
} nvmlVgpuVmIdType_t;

typedef unsigned int nvmlGridLicenseFeatureCode_t;

typedef struct nvmlVgpuLicenseExpiry_st {
    unsigned int   year;
    unsigned short month;
    unsigned short day;
    unsigned short hour;
    unsigned short min;
    unsigned short sec;
    unsigned char  status;
} nvmlVgpuLicenseExpiry_t;

typedef struct nvmlVgpuLicenseInfo_st {
    unsigned char           isLicensed;
    nvmlVgpuLicenseExpiry_t licenseExpiry;
    unsigned int            currentState;
} nvmlVgpuLicenseInfo_t;

typedef struct nvmlGridLicenseExpiry_st {
    unsigned int   year;
    unsigned short month;
    unsigned short day;
    unsigned short hour;
    unsigned short min;
    unsigned short sec;
    unsigned char  status;
} nvmlGridLicenseExpiry_t;

typedef struct nvmlGridLicensableFeature_st {
    nvmlGridLicenseFeatureCode_t featureCode;
    unsigned int                 featureState;
    char                         licenseInfo[NVML_GRID_LICENSE_BUFFER_SIZE];
    char                         productName[NVML_GRID_LICENSE_BUFFER_SIZE];
    unsigned int                 featureEnabled;
    nvmlGridLicenseExpiry_t      licenseExpiry;
} nvmlGridLicensableFeature_t;

typedef struct nvmlGridLicensableFeatures_st {
    int                         isGridLicenseSupported;
    unsigned int                licensableFeaturesCount;
    nvmlGridLicensableFeature_t gridLicensableFeatures[NVML_GRID_LICENSE_FEATURE_MAX_COUNT];
} nvmlGridLicensableFeatures_t;

/* Callers allocate these from go-nvml's VgpuLicenseInfo and
 * GridLicensableFeatures, which the bridge overwrites whole. */
_Static_assert(sizeof(nvmlVgpuLicenseInfo_t) == 24,
               "nvmlVgpuLicenseInfo_t must stay 24 bytes to match the go-nvml ABI");
_Static_assert(sizeof(nvmlGridLicensableFeatures_t) == 860,
               "nvmlGridLicensableFeatures_t must stay 860 bytes to match the go-nvml ABI");

/**
 * NVLink Reduced Bandwidth Mode (RBM). Full definitions needed so the
 * nvmlDeviceGetNvlink*BwMode exports can populate the caller's struct.
 * Layout matches the upstream NVML public header; see go-nvml's
 * pkg/nvml/nvml.h.
 *
 * Only v1 of these three exists upstream, but they still carry a version
 * field that the caller sets on the way in, so the exports validate it and
 * answer NVML_ERROR_ARGUMENT_VERSION_MISMATCH on anything else.
 */
#define NVML_NVLINK_TOTAL_SUPPORTED_BW_MODES 23

typedef struct nvmlNvlinkSupportedBwModes_v1_st
{
    unsigned int  version;
    unsigned char bwModes[NVML_NVLINK_TOTAL_SUPPORTED_BW_MODES];
    unsigned char totalBwModes;
} nvmlNvlinkSupportedBwModes_v1_t;
typedef nvmlNvlinkSupportedBwModes_v1_t nvmlNvlinkSupportedBwModes_t;
/* Callers allocate these from go-nvml, so a field added or reordered here would
 * make the bridge write past — or into the wrong offset of — the caller's
 * allocation. The sizes are also what the version tags are built from:
 * NVML_STRUCT_VERSION encodes sizeof in its low 24 bits, so a size change
 * silently starts rejecting every caller compiled against the real header. */
_Static_assert(sizeof(nvmlNvlinkSupportedBwModes_v1_t) == 28,
               "nvmlNvlinkSupportedBwModes_v1_t must stay 28 bytes: version(4) + bwModes[23] + totalBwModes(1)");
/* Size alone is too weak here: tail padding absorbs a one-element change to
 * bwModes, and a reorder keeps the size identical. Pin the offsets and the
 * array bound, which is what the bridge's write loop is bounded by. */
_Static_assert(offsetof(nvmlNvlinkSupportedBwModes_v1_t, bwModes) == 4,
               "nvmlNvlinkSupportedBwModes_v1_t.bwModes must stay at offset 4");
_Static_assert(offsetof(nvmlNvlinkSupportedBwModes_v1_t, totalBwModes) == 27,
               "nvmlNvlinkSupportedBwModes_v1_t.totalBwModes must stay at offset 27, i.e. bwModes must hold 23 entries");

typedef struct nvmlNvlinkGetBwMode_v1_st
{
    unsigned int  version;
    unsigned int  bIsBest;
    unsigned char bwMode;
} nvmlNvlinkGetBwMode_v1_t;
typedef nvmlNvlinkGetBwMode_v1_t nvmlNvlinkGetBwMode_t;
_Static_assert(sizeof(nvmlNvlinkGetBwMode_v1_t) == 12,
               "nvmlNvlinkGetBwMode_v1_t must stay 12 bytes: version(4) + bIsBest(4) + bwMode(1) + 3 tail pad");
_Static_assert(offsetof(nvmlNvlinkGetBwMode_v1_t, bIsBest) == 4 && offsetof(nvmlNvlinkGetBwMode_v1_t, bwMode) == 8,
               "nvmlNvlinkGetBwMode_v1_t field order must stay version/bIsBest/bwMode: swapping the last two keeps sizeof at 12");

typedef struct nvmlNvlinkSetBwMode_v1_st
{
    unsigned int  version;
    unsigned int  bSetBest;
    unsigned char bwMode;
} nvmlNvlinkSetBwMode_v1_t;
typedef nvmlNvlinkSetBwMode_v1_t nvmlNvlinkSetBwMode_t;
_Static_assert(sizeof(nvmlNvlinkSetBwMode_v1_t) == 12,
               "nvmlNvlinkSetBwMode_v1_t must stay 12 bytes: version(4) + bSetBest(4) + bwMode(1) + 3 tail pad");
_Static_assert(offsetof(nvmlNvlinkSetBwMode_v1_t, bSetBest) == 4 && offsetof(nvmlNvlinkSetBwMode_v1_t, bwMode) == 8,
               "nvmlNvlinkSetBwMode_v1_t field order must stay version/bSetBest/bwMode: swapping the last two keeps sizeof at 12");

/**
 * Per-device NVLink information. isNvleEnabled is NVLink encryption, which
 * `nvidia-smi nvlink --info` renders as its " NVLE:" row.
 *
 * Two versions exist upstream, and v2 only appends firmwareInfo — version and
 * isNvleEnabled sit at the same offsets in both. The mock reports NVLE and
 * nothing else, so nvmlDeviceGetNvLinkInfo accepts either tag and writes only
 * those two fields; a caller that passed a v2 buffer keeps its own
 * firmwareInfo bytes untouched.
 *
 * nvmlNvLinkInfo_t aliases v2, as upstream does, so a caller sees the same
 * public layout the real header gives it. v1 stays declared under its own name
 * because the accepted version tags are computed from both sizes.
 */
typedef struct nvmlNvLinkInfo_v1_st
{
    unsigned int version;
    unsigned int isNvleEnabled;
} nvmlNvLinkInfo_v1_t;
_Static_assert(sizeof(nvmlNvLinkInfo_v1_t) == 8,
               "nvmlNvLinkInfo_v1_t must stay 8 bytes: version(4) + isNvleEnabled(4)");
_Static_assert(offsetof(nvmlNvLinkInfo_v1_t, isNvleEnabled) == 4,
               "nvmlNvLinkInfo_v1_t.isNvleEnabled must stay at offset 4");

typedef struct nvmlNvlinkFirmwareVersion_st
{
    unsigned char ucodeType;
    unsigned int  major;
    unsigned int  minor;
    unsigned int  subMinor;
} nvmlNvlinkFirmwareVersion_t;
_Static_assert(sizeof(nvmlNvlinkFirmwareVersion_t) == 16,
               "nvmlNvlinkFirmwareVersion_t must stay 16 bytes: ucodeType(1) + 3 pad + major/minor/subMinor(12)");

#define NVML_NVLINK_FIRMWARE_VERSION_LENGTH 100

/* ucodeType values, which pick the label nvidia-smi prints for a row. */
#define NVML_NVLINK_FIRMWARE_UCODE_TYPE_MSE        0x1
#define NVML_NVLINK_FIRMWARE_UCODE_TYPE_NETIR      0x2
#define NVML_NVLINK_FIRMWARE_UCODE_TYPE_NETIR_UPHY 0x3
#define NVML_NVLINK_FIRMWARE_UCODE_TYPE_NETIR_CLN  0x4
#define NVML_NVLINK_FIRMWARE_UCODE_TYPE_NETIR_DLN  0x5

typedef struct nvmlNvlinkFirmwareInfo_st
{
    nvmlNvlinkFirmwareVersion_t firmwareVersion[NVML_NVLINK_FIRMWARE_VERSION_LENGTH];
    unsigned int                numValidEntries;
} nvmlNvlinkFirmwareInfo_t;
_Static_assert(sizeof(nvmlNvlinkFirmwareInfo_t) == 1604,
               "nvmlNvlinkFirmwareInfo_t must stay 1604 bytes: firmwareVersion[100](1600) + numValidEntries(4)");

typedef struct nvmlNvLinkInfo_v2_st
{
    unsigned int             version;
    unsigned int             isNvleEnabled;
    nvmlNvlinkFirmwareInfo_t firmwareInfo;
} nvmlNvLinkInfo_v2_t;
typedef nvmlNvLinkInfo_v2_t nvmlNvLinkInfo_t;
/* v2's size is load-bearing even though the mock never writes firmwareInfo: it
 * is what nvmlDeviceGetNvLinkInfo's accepted v2 version tag is computed from. */
_Static_assert(sizeof(nvmlNvLinkInfo_v2_t) == 1612,
               "nvmlNvLinkInfo_v2_t must stay 1612 bytes: version(4) + isNvleEnabled(4) + firmwareInfo(1604)");
_Static_assert(offsetof(nvmlNvLinkInfo_v2_t, isNvleEnabled) == offsetof(nvmlNvLinkInfo_v1_t, isNvleEnabled),
               "isNvleEnabled must sit at one offset in both layouts: the export serves a v1 or v2 caller from the same write");

/**
 * NVLink low-power threshold. Units come from
 * NVML_FI_DEV_NVLINK_GET_POWER_THRESHOLD_UNITS (the mock reports 50 us).
 */
typedef struct nvmlNvLinkPowerThres_st
{
    unsigned int lowPwrThreshold;
} nvmlNvLinkPowerThres_t;
_Static_assert(sizeof(nvmlNvLinkPowerThres_t) == 4,
               "nvmlNvLinkPowerThres_t must stay 4 bytes: lowPwrThreshold(4)");

/* Power smoothing setter inputs. Both are read, never written, so drift
 * misreads the request rather than overrunning the caller; the double in the
 * profile struct is what makes its size 24 rather than 20, and a layout that
 * dropped the padding would read the value four bytes early. Layout matches
 * go-nvml's pkg/nvml/nvml.h (v1), pinned by power_policy_layout_test.go. */
typedef struct nvmlPowerSmoothingProfile_st
{
    unsigned int version;   //!< IN: NVML_STRUCT_VERSION(PowerSmoothingProfile, 1)
    unsigned int profileId; //!< IN: the preset profile
    unsigned int paramId;   //!< IN: NVML_POWER_SMOOTHING_PROFILE_PARAM_*
    double       value;     //!< IN: the parameter's new value
} nvmlPowerSmoothingProfile_t;
_Static_assert(sizeof(nvmlPowerSmoothingProfile_t) == 24,
               "nvmlPowerSmoothingProfile_t must stay 24 bytes to match the go-nvml ABI");
typedef struct nvmlPowerSmoothingState_st
{
    unsigned int      version; //!< IN: NVML_STRUCT_VERSION(PowerSmoothingState, 1)
    nvmlEnableState_t state;   //!< IN: 0/DISABLED or 1/ENABLED
} nvmlPowerSmoothingState_t;
_Static_assert(sizeof(nvmlPowerSmoothingState_t) == 8,
               "nvmlPowerSmoothingState_t must stay 8 bytes to match the go-nvml ABI");
/* A plain enum in nvml.h, not a struct. */
typedef unsigned int nvmlPowerSource_t;
typedef struct nvmlPowerValue_v2_st                         nvmlPowerValue_v2_t;
/* Process detail list — full definitions so
 * nvmlDeviceGetRunningProcessDetailList can read the requested mode and fill
 * the caller's procArray. Layouts match the upstream NVML header. */
typedef struct
{
    unsigned int       pid;                       //!< OUT: process ID
    unsigned long long usedGpuMemory;             //!< OUT: memory used, bytes
    unsigned int       gpuInstanceId;             //!< OUT: GPU instance, 0xFFFFFFFF without MIG
    unsigned int       computeInstanceId;         //!< OUT: compute instance, 0xFFFFFFFF without MIG
    unsigned long long usedGpuCcProtectedMemory;  //!< OUT: confidential-compute protected memory, bytes
} nvmlProcessDetail_v1_t;
#define NVML_PROCESS_MODE_COMPUTE  0
#define NVML_PROCESS_MODE_GRAPHICS 1
#define NVML_PROCESS_MODE_MPS      2
#define NVML_PROCESS_MODE_ALL      3
typedef struct
{
    unsigned int            version;              //!< IN: NVML_STRUCT_VERSION(ProcessDetailList, 1)
    unsigned int            mode;                 //!< IN: NVML_PROCESS_MODE_*
    unsigned int            numProcArrayEntries;  //!< IN: procArray size; OUT: processes reported
    nvmlProcessDetail_v1_t *procArray;            //!< OUT: caller-allocated
} nvmlProcessDetailList_v1_t;
typedef nvmlProcessDetailList_v1_t nvmlProcessDetailList_t;
_Static_assert(sizeof(nvmlProcessDetail_v1_t) == 32,
               "nvmlProcessDetail_v1_t must stay 32 bytes to match the go-nvml ABI");
_Static_assert(sizeof(nvmlProcessDetailList_v1_t) == 24 && offsetof(nvmlProcessDetailList_v1_t, procArray) == 16,
               "nvmlProcessDetailList_v1_t must stay 24 bytes with procArray at offset 16 to match the go-nvml ABI");
typedef struct nvmlProcessInfo_v1_st                        nvmlProcessInfo_v1_t;
/* Upstream declares the v2 and unversioned process infos as one type. */
typedef nvmlProcessInfo_t                                   nvmlProcessInfo_v2_t;

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
/* Per-process utilization (v1) — full definitions so
 * nvmlDeviceGetProcessesUtilizationInfo can fill the caller's procUtilArray.
 * Layouts match the upstream NVML header; the element carries four bytes of
 * tail padding, so its stride is 40, not 36. */
typedef struct
{
    unsigned long long timeStamp;  //!< OUT: CPU timestamp, usec
    unsigned int       pid;        //!< OUT: process ID
    unsigned int       smUtil;     //!< OUT: SM utilization, percent
    unsigned int       memUtil;    //!< OUT: frame buffer utilization, percent
    unsigned int       encUtil;    //!< OUT: encoder utilization, percent
    unsigned int       decUtil;    //!< OUT: decoder utilization, percent
    unsigned int       jpgUtil;    //!< OUT: JPEG utilization, percent
    unsigned int       ofaUtil;    //!< OUT: OFA utilization, percent
} nvmlProcessUtilizationInfo_v1_t;
typedef struct
{
    unsigned int                     version;              //!< IN: NVML_STRUCT_VERSION(ProcessesUtilizationInfo, 1)
    unsigned int                     processSamplesCount;  //!< IN: procUtilArray size; OUT: samples reported
    unsigned long long               lastSeenTimeStamp;    //!< IN: report only samples newer than this, usec
    nvmlProcessUtilizationInfo_v1_t *procUtilArray;        //!< OUT: caller-allocated
} nvmlProcessesUtilizationInfo_v1_t;
typedef nvmlProcessesUtilizationInfo_v1_t nvmlProcessesUtilizationInfo_t;
_Static_assert(sizeof(nvmlProcessUtilizationInfo_v1_t) == 40,
               "nvmlProcessUtilizationInfo_v1_t must stay 40 bytes to match the go-nvml ABI");
_Static_assert(sizeof(nvmlProcessesUtilizationInfo_v1_t) == 24 && offsetof(nvmlProcessesUtilizationInfo_v1_t, procUtilArray) == 16,
               "nvmlProcessesUtilizationInfo_v1_t must stay 24 bytes with procUtilArray at offset 16 to match the go-nvml ABI");
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
/* Sample — full definition so nvmlDeviceGetSamples can fill the caller's
 * buffer. Layout matches the upstream NVML header. */
typedef struct nvmlSample_st
{
    unsigned long long timeStamp;    //!< OUT: CPU timestamp, usec
    nvmlValue_t        sampleValue;  //!< OUT: read as the returned nvmlValueType_t
} nvmlSample_t;
_Static_assert(sizeof(nvmlSample_t) == 16,
               "nvmlSample_t must stay 16 bytes to match the go-nvml ABI");
typedef struct nvmlSystemConfComputeSettings_st             nvmlSystemConfComputeSettings_t;
typedef struct nvmlSystemDriverBranchInfo_st                nvmlSystemDriverBranchInfo_t;
typedef struct nvmlSystemEventSetCreateRequest_st           nvmlSystemEventSetCreateRequest_t;
typedef struct nvmlSystemEventSetFreeRequest_st             nvmlSystemEventSetFreeRequest_t;
typedef struct nvmlSystemEventSetWaitRequest_st             nvmlSystemEventSetWaitRequest_t;
typedef struct nvmlSystemRegisterEventRequest_st            nvmlSystemRegisterEventRequest_t;
/* The UUID nvmlDeviceGetHandleByUUIDV reads, either as the ASCII string
 * nvmlDeviceGetUUID returns or as its 16 bytes; type says which. Versioned
 * like nvmlDeviceAddressingMode_t. */
#define NVML_DEVICE_UUID_ASCII_LEN  41
#define NVML_DEVICE_UUID_BINARY_LEN 16
#define NVML_UUID_TYPE_ASCII        1
#define NVML_UUID_TYPE_BINARY       2
typedef union
{
    char          str[NVML_DEVICE_UUID_ASCII_LEN];
    unsigned char bytes[NVML_DEVICE_UUID_BINARY_LEN];
} nvmlUUIDValue_t;
typedef struct nvmlUUID_v1_st
{
    unsigned int    version;
    unsigned int    type;
    nvmlUUIDValue_t value;
} nvmlUUID_v1_t;
typedef nvmlUUID_v1_t nvmlUUID_t;
_Static_assert(sizeof(nvmlUUID_v1_t) == 52 && offsetof(nvmlUUID_v1_t, value) == 8,
               "nvmlUUID_v1_t must stay 52 bytes with value at offset 8 to match the go-nvml ABI");
/* Fan readings for a whole S-class unit — full definition so
 * nvmlUnitGetFanSpeedInfo can populate the caller's buffer. The fans array is
 * fixed at 24 by the ABI regardless of how many the chassis has, which is what
 * count reports. Mokka enumerates no units, so this is reached only by a
 * caller holding a handle NVML itself would refuse to hand out; the definition
 * exists so the refusal comes from the unit lookup rather than from a missing
 * type. Layout matches the upstream NVML header, pinned by
 * fans_layout_test.go. */
typedef struct nvmlUnitFanInfo_st
{
    unsigned int speed;   //!< OUT: fan speed in RPM
    unsigned int state;   //!< OUT: nvmlFanState_t
} nvmlUnitFanInfo_t;
typedef struct nvmlUnitFanSpeeds_st
{
    nvmlUnitFanInfo_t fans[24];
    unsigned int      count;
} nvmlUnitFanSpeeds_t;
/* Callers allocate this buffer from go-nvml's UnitFanSpeeds, so a field added
 * here — or a different array bound — would make the bridge write past the
 * caller's allocation. */
_Static_assert(sizeof(nvmlUnitFanInfo_t) == 8,
               "nvmlUnitFanInfo_t must stay 8 bytes to match the go-nvml ABI");
_Static_assert(sizeof(nvmlUnitFanSpeeds_t) == 196,
               "nvmlUnitFanSpeeds_t must stay 196 bytes (24 fans plus count) to match the go-nvml ABI");
typedef struct nvmlUnitInfo_st                              nvmlUnitInfo_t;
typedef struct nvmlVgpuCreatablePlacementInfo_st            nvmlVgpuCreatablePlacementInfo_t;
typedef struct nvmlVgpuHeterogeneousMode_st                 nvmlVgpuHeterogeneousMode_t;
typedef struct nvmlVgpuInstanceUtilizationSample_st         nvmlVgpuInstanceUtilizationSample_t;
typedef struct nvmlVgpuInstancesUtilizationInfo_st          nvmlVgpuInstancesUtilizationInfo_t;
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
 * NVML additions (go-nvml v0.13.4-1, NVML 13.4.92). Every function taking
 * these types reaches the bridge only as a NOT_SUPPORTED stub in
 * stubs_generated.go, which never dereferences its arguments, so opaque
 * forward declarations are sufficient and ABI-safe. Giving one of these
 * functions a real implementation needs the full struct body, copied verbatim
 * from go-nvml's pkg/nvml/nvml.h.
 */
typedef struct nvmlAdaptiveTgpModeInfo_v1_st                            nvmlAdaptiveTgpModeInfo_v1_t;
typedef struct nvmlEccBankRemapperStatus_v1_st                          nvmlEccBankRemapperStatus_v1_t;
typedef struct nvmlEventSetGetContextCount_v1_st                        nvmlEventSetGetContextCount_v1_t;
typedef struct nvmlEventSetGetContextData_v1_st                         nvmlEventSetGetContextData_v1_t;
typedef struct nvmlEventSetGetContextInfo_v1_st                         nvmlEventSetGetContextInfo_v1_t;
typedef struct nvmlEventSetGetGpuOperationalEventContextLegacyXid_v1_st nvmlEventSetGetGpuOperationalEventContextLegacyXid_v1_t;
typedef struct nvmlEventSetWait_v3_st                                   nvmlEventSetWait_v3_t;
typedef struct nvmlGetMemoryLimits_v1_st                                nvmlGetMemoryLimits_v1_t;
typedef struct nvmlGpuFabricInfo_v4_st                                  nvmlGpuFabricInfo_v4_t;
typedef struct nvmlGpuOperationalEventConfig_v1_st                      nvmlGpuOperationalEventConfig_v1_t;
typedef struct nvmlNvlinkSetBwModeAsync_v1_st                           nvmlNvlinkSetBwModeAsync_v1_t;
typedef struct nvmlNvlinkTelemetrySamples_v1_st                         nvmlNvlinkTelemetrySamples_v1_t;
typedef struct nvmlPerfMetricsSamples_v1_st                             nvmlPerfMetricsSamples_v1_t;
typedef struct nvmlSetMemoryLimits_v1_st                                nvmlSetMemoryLimits_v1_t;

/*
 * Completed bodies for the system event set, HIC, driver branch and hostname
 * types. The forward declarations earlier in this header only name the
 * typedefs; these complete the struct tags so the bridge can read and write
 * their fields. Layouts mirror go-nvml's pkg/nvml/nvml.h (v1). `version` is an
 * input the caller stamps with the NVML_STRUCT_VERSION macro; the bridge
 * accepts any value because go-nvml stamps it on the caller's behalf.
 */
#define NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE 80
#define NVML_DEVICE_HOSTNAME_BUFFER_SIZE 64

/*
 * The only two system event types NVML defines. Unlike the per-device event
 * mask these are driver bind / unbind transitions, not Xid classes.
 */
#define nvmlSystemEventTypeGpuDriverUnbind 0x0000000000000001LL
#define nvmlSystemEventTypeGpuDriverBind   0x0000000000000002LL
#define nvmlSystemEventTypeCount 2

typedef struct
{
    struct nvmlSystemEventSet_st* handle;
} nvmlSystemEventSet_t;

typedef struct
{
    unsigned long long eventType; //!< OUT: which system event occurred
    unsigned int       gpuId;     //!< OUT: gpuId in PCI format
} nvmlSystemEventData_v1_t;

struct nvmlSystemEventSetCreateRequest_st
{
    unsigned int         version; //!< IN: NVML_STRUCT_VERSION(SystemEventSetCreateRequest, 1)
    nvmlSystemEventSet_t set;     //!< OUT: the created system event set
};

struct nvmlSystemEventSetFreeRequest_st
{
    unsigned int         version; //!< IN: NVML_STRUCT_VERSION(SystemEventSetFreeRequest, 1)
    nvmlSystemEventSet_t set;     //!< IN: the system event set to release
};

struct nvmlSystemRegisterEventRequest_st
{
    unsigned int         version;    //!< IN: NVML_STRUCT_VERSION(SystemRegisterEventRequest, 1)
    unsigned long long   eventTypes; //!< IN: bitmask of nvmlSystemEventType* to record
    nvmlSystemEventSet_t set;        //!< IN: set the event types are added to
};

struct nvmlSystemEventSetWaitRequest_st
{
    unsigned int              version;   //!< IN/OUT: NVML_STRUCT_VERSION(SystemEventSetWaitRequest, 1)
    unsigned int              timeoutms; //!< IN: milliseconds to block; 0 polls
    nvmlSystemEventSet_t      set;       //!< IN: system event set to wait on
    nvmlSystemEventData_v1_t *data;      //!< IN/OUT: caller-owned event array
    unsigned int              dataSize;  //!< IN: number of entries in data
    unsigned int              numEvent;  //!< OUT: number of events collected
};

struct nvmlHwbcEntry_st
{
    unsigned int hwbcId;             //!< OUT: host interface card id
    char         firmwareVersion[32]; //!< OUT: HIC firmware version string
};

struct nvmlSystemDriverBranchInfo_st
{
    unsigned int version;                                        //!< IN: NVML_STRUCT_VERSION(SystemDriverBranchInfo, 1)
    char         branch[NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE];  //!< OUT: driver branch
};

struct nvmlHostname_v1_st
{
    char value[NVML_DEVICE_HOSTNAME_BUFFER_SIZE]; //!< IN/OUT: NUL-terminated hostname
};

#ifdef __cplusplus
}
#endif

#endif /* MOCK_NVML_TYPES_H */
