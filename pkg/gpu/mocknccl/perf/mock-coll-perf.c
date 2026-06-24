/* pkg/gpu/mocknccl/perf/mock-coll-perf.c
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * Minimal nccl-tests-style driver for the mock libnccl + libcuda. MPI-free:
 * rank/world come from RANK / WORLD_SIZE env; the comm rendezvous (when
 * WORLD_SIZE>1) is handled inside mock ncclCommInitRank via MOCK_NCCL_RDZV.
 *
 * Usage: mock-coll-perf [all_reduce|all_gather|reduce_scatter|broadcast|reduce]
 *                       -b <minBytes> -e <maxBytes> -f <factor> -n <iters>
 */
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <cuda_runtime.h>
#include <nccl.h>

static long env_long(const char *k, long def) {
    const char *v = getenv(k);
    return v && *v ? strtol(v, NULL, 10) : def;
}

static double busbw_factor(const char *op, int n) {
    if (n <= 1) return 0.0;
    if (!strcmp(op, "all_reduce")) return 2.0 * (n - 1) / n;
    if (!strcmp(op, "all_gather") || !strcmp(op, "reduce_scatter")) return (double)(n - 1) / n;
    return 1.0; /* broadcast, reduce */
}

static ncclResult_t run_one(const char *op, void *sbuf, void *rbuf,
                            size_t count, ncclComm_t comm, cudaStream_t s) {
    if (!strcmp(op, "all_reduce"))     return ncclAllReduce(sbuf, rbuf, count, ncclFloat32, ncclSum, comm, s);
    if (!strcmp(op, "all_gather"))     return ncclAllGather(sbuf, rbuf, count, ncclFloat32, comm, s);
    if (!strcmp(op, "reduce_scatter")) return ncclReduceScatter(sbuf, rbuf, count, ncclFloat32, ncclSum, comm, s);
    if (!strcmp(op, "broadcast"))      return ncclBroadcast(sbuf, rbuf, count, ncclFloat32, 0, comm, s);
    if (!strcmp(op, "reduce"))         return ncclReduce(sbuf, rbuf, count, ncclFloat32, ncclSum, 0, comm, s);
    return ncclInvalidArgument;
}

int main(int argc, char **argv) {
    const char *op = (argc > 1 && argv[1][0] != '-') ? argv[1] : "all_reduce";
    long minB = 1024, maxB = 64L * 1024 * 1024, factor = 2, iters = 20;
    for (int i = 1; i < argc - 1; i++) {
        if (!strcmp(argv[i], "-b")) minB = strtol(argv[++i], NULL, 10);
        else if (!strcmp(argv[i], "-e")) maxB = strtol(argv[++i], NULL, 10);
        else if (!strcmp(argv[i], "-f")) factor = strtol(argv[++i], NULL, 10);
        else if (!strcmp(argv[i], "-n")) iters = strtol(argv[++i], NULL, 10);
    }
    if (factor < 2) factor = 2; /* guard against a non-advancing size loop */

    int rank = (int)env_long("RANK", 0);
    int world = (int)env_long("WORLD_SIZE", 1);

    cudaSetDevice(rank);
    int ver = 0; ncclGetVersion(&ver);

    ncclComm_t comm;
    ncclUniqueId id;
    ncclGetUniqueId(&id);
    if (ncclCommInitRank(&comm, world, id, rank) != ncclSuccess) {
        fprintf(stderr, "ncclCommInitRank failed\n");
        return 1;
    }

    cudaStream_t stream; cudaStreamCreate(&stream);
    cudaEvent_t start, stop; cudaEventCreate(&start); cudaEventCreate(&stop);

    if (rank == 0) {
        printf("# nReps %ld  nRanks %d  nccl %d  op %s\n", iters, world, ver, op);
        printf("#%11s %11s %8s %8.8s %8s %9s\n",
               "size(B)", "count", "type", "redop", "time(us)", "busbw(GB/s)");
    }

    double avg = 0.0; int rows = 0;
    for (long bytes = minB; bytes <= maxB; bytes *= factor) {
        size_t count = bytes / sizeof(float);
        void *sbuf, *rbuf;
        cudaMalloc(&sbuf, bytes);
        cudaMalloc(&rbuf, bytes);

        cudaEventRecord(start, stream);
        for (long it = 0; it < iters; it++) {
            if (run_one(op, sbuf, rbuf, count, comm, stream) != ncclSuccess) {
                fprintf(stderr, "collective failed\n");
                return 1;
            }
        }
        cudaEventRecord(stop, stream);
        cudaEventSynchronize(stop);

        float ms = 0.0f; cudaEventElapsedTime(&ms, start, stop);
        double t_us = (ms * 1000.0) / (double)iters;
        double algbw = t_us > 0 ? (double)bytes / (t_us * 1e3) : 0.0; /* GB/s */
        double busbw = algbw * busbw_factor(op, world);
        if (rank == 0) {
            printf("%12ld %11zu %8s %8s %8.1f %9.2f\n",
                   bytes, count, "float", "sum", t_us, busbw);
        }
        avg += busbw; rows++;
        cudaFree(sbuf); cudaFree(rbuf);
    }

    if (rank == 0 && rows > 0) {
        printf("# Avg bus bandwidth : %.4f\n", avg / rows);
    }

    cudaEventDestroy(start); cudaEventDestroy(stop);
    cudaStreamDestroy(stream);
    ncclCommDestroy(comm);
    return 0;
}
