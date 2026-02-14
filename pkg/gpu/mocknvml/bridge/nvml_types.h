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

#ifdef __cplusplus
}
#endif

#endif /* MOCK_NVML_TYPES_H */
