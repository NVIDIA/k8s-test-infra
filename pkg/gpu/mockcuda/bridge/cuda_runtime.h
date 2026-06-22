/*
 * cuda_runtime.h - public CUDA Runtime API surface for the mock libcuda.
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
 * Licensed under the Apache License, Version 2.0.
 *
 * Aggregates the mock CUDA types plus the runtime function prototypes the
 * mock-coll-perf driver uses. The implementations are exported from
 * pkg/gpu/mockcuda/bridge (libcuda.so).
 */
#ifndef MOCK_CUDA_RUNTIME_H
#define MOCK_CUDA_RUNTIME_H

#include <stddef.h>
#include "cuda_types.h"

#ifdef __cplusplus
extern "C" {
#endif

extern cudaError_t cudaSetDevice(int device);
extern cudaError_t cudaMalloc(void **devPtr, size_t size);
extern cudaError_t cudaFree(void *devPtr);
extern cudaError_t cudaStreamCreate(cudaStream_t *pStream);
extern cudaError_t cudaStreamDestroy(cudaStream_t stream);
extern cudaError_t cudaEventCreate(cudaEvent_t *event);
extern cudaError_t cudaEventDestroy(cudaEvent_t event);
extern cudaError_t cudaEventRecord(cudaEvent_t event, cudaStream_t stream);
extern cudaError_t cudaEventSynchronize(cudaEvent_t event);
extern cudaError_t cudaEventElapsedTime(float *ms, cudaEvent_t start, cudaEvent_t stop);

#ifdef __cplusplus
}
#endif
#endif /* MOCK_CUDA_RUNTIME_H */
