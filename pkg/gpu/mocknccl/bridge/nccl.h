/*
 * nccl.h - public NCCL API surface for the mock libnccl.
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * Aggregates the mock NCCL types plus the collective/comm prototypes the
 * mock-coll-perf driver uses. Implementations are exported from
 * pkg/gpu/mocknccl/bridge (libnccl.so.2).
 */
#ifndef MOCK_NCCL_H
#define MOCK_NCCL_H

#include <stddef.h>
#include "nccl_types.h"

#ifdef __cplusplus
extern "C" {
#endif

extern ncclResult_t ncclGetVersion(int *version);
extern ncclResult_t ncclGetUniqueId(ncclUniqueId *uniqueId);
extern const char  *ncclGetErrorString(ncclResult_t result);
extern ncclResult_t ncclCommInitRank(ncclComm_t *comm, int nranks, ncclUniqueId commId, int rank);
extern ncclResult_t ncclCommInitAll(ncclComm_t *comm, int ndev, const int *devlist);
extern ncclResult_t ncclCommDestroy(ncclComm_t comm);
extern ncclResult_t ncclCommCount(ncclComm_t comm, int *count);
extern ncclResult_t ncclCommUserRank(ncclComm_t comm, int *rank);
extern ncclResult_t ncclGroupStart(void);
extern ncclResult_t ncclGroupEnd(void);
extern ncclResult_t ncclAllReduce(const void *sendbuff, void *recvbuff, size_t count, ncclDataType_t datatype, ncclRedOp_t op, ncclComm_t comm, cudaStream_t stream);
extern ncclResult_t ncclAllGather(const void *sendbuff, void *recvbuff, size_t sendcount, ncclDataType_t datatype, ncclComm_t comm, cudaStream_t stream);
extern ncclResult_t ncclReduceScatter(const void *sendbuff, void *recvbuff, size_t recvcount, ncclDataType_t datatype, ncclRedOp_t op, ncclComm_t comm, cudaStream_t stream);
extern ncclResult_t ncclBroadcast(const void *sendbuff, void *recvbuff, size_t count, ncclDataType_t datatype, int root, ncclComm_t comm, cudaStream_t stream);
extern ncclResult_t ncclReduce(const void *sendbuff, void *recvbuff, size_t count, ncclDataType_t datatype, ncclRedOp_t op, int root, ncclComm_t comm, cudaStream_t stream);

#ifdef __cplusplus
}
#endif
#endif /* MOCK_NCCL_H */
