/*
 * CUDA Type Definitions for Mock Library
 *
 * Minimal type definitions for the CUDA Runtime and Driver APIs,
 * sufficient for the 10 functions implemented in this mock.
 *
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
 * Licensed under the Apache License, Version 2.0.
 */

#ifndef MOCK_CUDA_TYPES_H
#define MOCK_CUDA_TYPES_H

#ifdef __cplusplus
extern "C" {
#endif

/*
 * CUDA error codes (cudaError_t / CUresult)
 */
typedef enum cudaError_enum {
    cudaSuccess                     = 0,
    cudaErrorInvalidValue           = 1,
    cudaErrorMemoryAllocation       = 2,
    cudaErrorInitializationError    = 3,
    cudaErrorInvalidDevice          = 10,
    cudaErrorInvalidMemcpyDirection = 21,
    cudaErrorNotReady               = 34,
    cudaErrorUnknown                = 999
} cudaError_t;

/* CUresult mirrors cudaError_t for driver API */
typedef cudaError_t CUresult;

/*
 * Memory copy direction
 */
typedef enum cudaMemcpyKind_enum {
    cudaMemcpyHostToHost     = 0,
    cudaMemcpyHostToDevice   = 1,
    cudaMemcpyDeviceToHost   = 2,
    cudaMemcpyDeviceToDevice = 3,
    cudaMemcpyDefault        = 4
} cudaMemcpyKind;

/*
 * dim3 - grid/block dimensions for kernel launches
 */
typedef struct {
    unsigned int x;
    unsigned int y;
    unsigned int z;
} dim3;

/*
 * cudaStream_t - opaque stream handle
 */
typedef struct CUstream_st* cudaStream_t;

#ifdef __cplusplus
}
#endif

#endif /* MOCK_CUDA_TYPES_H */
