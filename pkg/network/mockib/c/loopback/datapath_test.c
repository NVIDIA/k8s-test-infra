/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * datapath_test -- focused, deterministic proof that libibmockrdma.so actually
 * MOVES BYTES across the JSON relay (not just that perftest's completion
 * accounting is satisfied). It is LD_PRELOADed with the real shim and drives
 * two software RC QPs (A and B) on one host through the relay:
 *
 *   1. RDMA WRITE  A -> B : assert B's MR received A's payload.
 *   2. RDMA READ   A <- B : assert A's buffer received B's MR payload.
 *   3. SEND        A -> B : assert B's posted recv buffer received A's payload.
 *
 * Each case bounds-checks against the registered MR in the shim, so a passing
 * run proves the rkey/remote_addr path end to end. Exit 0 on success.
 */

#define _GNU_SOURCE
#include <infiniband/verbs.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static void msleep(int ms) {
    struct timespec ts = {ms / 1000, (long)(ms % 1000) * 1000000L};
    nanosleep(&ts, NULL);
}

/* Poll cq until one completion arrives or timeout; returns 1 on success. */
static int poll_one(struct ibv_cq *cq, struct ibv_wc *wc, int timeout_ms) {
    for (int i = 0; i < timeout_ms; i++) {
        int n = ibv_poll_cq(cq, 1, wc);
        if (n > 0) return 1;
        msleep(1);
    }
    return 0;
}

static struct ibv_qp *mk_qp(struct ibv_pd *pd, struct ibv_cq *scq,
                            struct ibv_cq *rcq) {
    struct ibv_qp_init_attr ia;
    memset(&ia, 0, sizeof(ia));
    ia.send_cq = scq;
    ia.recv_cq = rcq;
    ia.qp_type = IBV_QPT_RC;
    ia.sq_sig_all = 1;
    ia.cap.max_send_wr = 64;
    ia.cap.max_recv_wr = 64;
    ia.cap.max_send_sge = 4;
    ia.cap.max_recv_sge = 4;
    return ibv_create_qp(pd, &ia);
}

/* Move qp to RTR with the given remote QPN (sends verbs_qp_connect), then RTS. */
static void connect_qp(struct ibv_qp *qp, uint32_t dest_qpn) {
    struct ibv_qp_attr attr;
    memset(&attr, 0, sizeof(attr));
    attr.qp_state = IBV_QPS_RTR;
    attr.dest_qp_num = dest_qpn;
    attr.ah_attr.dlid = 1;
    attr.ah_attr.port_num = 1;
    attr.ah_attr.is_global = 0;
    ibv_modify_qp(qp, &attr,
                  IBV_QP_STATE | IBV_QP_AV | IBV_QP_DEST_QPN | IBV_QP_PATH_MTU |
                      IBV_QP_RQ_PSN | IBV_QP_MAX_DEST_RD_ATOMIC | IBV_QP_MIN_RNR_TIMER);
    memset(&attr, 0, sizeof(attr));
    attr.qp_state = IBV_QPS_RTS;
    ibv_modify_qp(qp, &attr, IBV_QP_STATE);
}

#define BUFSZ 4096
#define FAIL(msg)                                                              \
    do {                                                                       \
        fprintf(stderr, "FAIL: %s\n", msg);                                    \
        return 1;                                                              \
    } while (0)

int main(void) {
    struct ibv_device dev;
    memset(&dev, 0, sizeof(dev));
    snprintf(dev.name, sizeof(dev.name), "mlx5_0");

    struct ibv_context *ctx = ibv_open_device(&dev);
    if (!ctx) FAIL("ibv_open_device");

    /* B1 proof: the shim must surface the REAL per-port LID/GID from the mock
     * sysfs tree (MOCK_IB_ROOT fixture written by run.sh), not lid=1 / zero gid.
     * Without this, perftest's OOB exchange carries keys the daemon registry
     * cannot resolve and every egress op is silently dropped. */
    {
        struct ibv_port_attr pa;
        if (ibv_query_port(ctx, 1, &pa)) FAIL("ibv_query_port");
        if (pa.lid != 0x0102) {
            fprintf(stderr, "FAIL: surfaced LID 0x%04x, want 0x0102 (sysfs)\n",
                    pa.lid);
            return 1;
        }
        union ibv_gid g;
        if (ibv_query_gid(ctx, 1, 0, &g)) FAIL("ibv_query_gid");
        static const uint8_t want[16] = {0xfe, 0x80, 0, 0, 0, 0, 0, 0,
                                         0, 1, 0, 2, 0, 3, 0, 4};
        if (memcmp(g.raw, want, 16) != 0) FAIL("surfaced GID != sysfs gids/0");
        fprintf(stderr, "PASS: surfaced LID 0x%04x + GID from mock sysfs\n",
                pa.lid);
    }

    struct ibv_pd *pd = ibv_alloc_pd(ctx);
    if (!pd) FAIL("ibv_alloc_pd");
    struct ibv_cq *cqA = ibv_create_cq(ctx, 64, NULL, NULL, 0);
    struct ibv_cq *cqB = ibv_create_cq(ctx, 64, NULL, NULL, 0);
    if (!cqA || !cqB) FAIL("ibv_create_cq");

    /* Buffers + MRs. */
    char *srcA = aligned_alloc(BUFSZ, BUFSZ); /* A's write source */
    char *dstB = aligned_alloc(BUFSZ, BUFSZ); /* B's write target */
    char *rdsrcB = aligned_alloc(BUFSZ, BUFSZ); /* B's read source */
    char *rddstA = aligned_alloc(BUFSZ, BUFSZ); /* A's read dest */
    char *sndA = aligned_alloc(BUFSZ, BUFSZ); /* A's send source */
    char *rcvB = aligned_alloc(BUFSZ, BUFSZ); /* B's recv target */
    memset(dstB, 0, BUFSZ);
    memset(rddstA, 0, BUFSZ);
    memset(rcvB, 0, BUFSZ);
    for (int i = 0; i < BUFSZ; i++) {
        srcA[i] = (char)(i & 0xff);
        rdsrcB[i] = (char)((i * 7 + 3) & 0xff);
        sndA[i] = (char)((i * 13 + 5) & 0xff);
    }

    int acc = IBV_ACCESS_LOCAL_WRITE | IBV_ACCESS_REMOTE_WRITE |
              IBV_ACCESS_REMOTE_READ;
    struct ibv_mr *mrSrcA = ibv_reg_mr(pd, srcA, BUFSZ, acc);
    struct ibv_mr *mrDstB = ibv_reg_mr(pd, dstB, BUFSZ, acc);
    struct ibv_mr *mrRdSrcB = ibv_reg_mr(pd, rdsrcB, BUFSZ, acc);
    struct ibv_mr *mrRdDstA = ibv_reg_mr(pd, rddstA, BUFSZ, acc);
    struct ibv_mr *mrSndA = ibv_reg_mr(pd, sndA, BUFSZ, acc);
    struct ibv_mr *mrRcvB = ibv_reg_mr(pd, rcvB, BUFSZ, acc);
    if (!mrSrcA || !mrDstB || !mrRdSrcB || !mrRdDstA || !mrSndA || !mrRcvB)
        FAIL("ibv_reg_mr");

    struct ibv_qp *qpA = mk_qp(pd, cqA, cqA);
    struct ibv_qp *qpB = mk_qp(pd, cqB, cqB);
    if (!qpA || !qpB) FAIL("ibv_create_qp");
    msleep(50); /* let attach reader threads register */

    connect_qp(qpA, qpB->qp_num);
    connect_qp(qpB, qpA->qp_num);
    msleep(50); /* let the relay learn both routes */

    struct ibv_wc wc;

    /* ---- 1. RDMA WRITE A -> B ---- */
    {
        struct ibv_sge sge = {(uintptr_t)srcA, BUFSZ, mrSrcA->lkey};
        struct ibv_send_wr wr, *bad;
        memset(&wr, 0, sizeof(wr));
        wr.wr_id = 0x1001;
        wr.sg_list = &sge;
        wr.num_sge = 1;
        wr.opcode = IBV_WR_RDMA_WRITE;
        wr.send_flags = IBV_SEND_SIGNALED;
        wr.wr.rdma.remote_addr = (uintptr_t)dstB;
        wr.wr.rdma.rkey = mrDstB->rkey;
        if (ibv_post_send(qpA, &wr, &bad)) FAIL("post_send WRITE");
        if (!poll_one(cqA, &wc, 2000)) FAIL("no WRITE completion");
        if (wc.status != IBV_WC_SUCCESS) FAIL("WRITE completion status");
        int ok = 0;
        for (int i = 0; i < 2000 && !ok; i++) {
            if (memcmp(srcA, dstB, BUFSZ) == 0) ok = 1; else msleep(1);
        }
        if (!ok) FAIL("WRITE bytes not delivered into B's MR");
        fprintf(stderr, "PASS: RDMA WRITE delivered %d bytes A->B\n", BUFSZ);
    }

    /* ---- 2. RDMA READ A <- B ---- */
    {
        struct ibv_sge sge = {(uintptr_t)rddstA, BUFSZ, mrRdDstA->lkey};
        struct ibv_send_wr wr, *bad;
        memset(&wr, 0, sizeof(wr));
        wr.wr_id = 0x2002;
        wr.sg_list = &sge;
        wr.num_sge = 1;
        wr.opcode = IBV_WR_RDMA_READ;
        wr.send_flags = IBV_SEND_SIGNALED;
        wr.wr.rdma.remote_addr = (uintptr_t)rdsrcB;
        wr.wr.rdma.rkey = mrRdSrcB->rkey;
        if (ibv_post_send(qpA, &wr, &bad)) FAIL("post_send READ");
        if (!poll_one(cqA, &wc, 2000)) FAIL("no READ completion");
        if (wc.status != IBV_WC_SUCCESS) FAIL("READ completion status");
        if (memcmp(rdsrcB, rddstA, BUFSZ) != 0)
            FAIL("READ bytes not delivered into A's buffer");
        fprintf(stderr, "PASS: RDMA READ delivered %d bytes A<-B\n", BUFSZ);
    }

    /* ---- 3. SEND A -> B ---- */
    {
        struct ibv_sge rsge = {(uintptr_t)rcvB, BUFSZ, mrRcvB->lkey};
        struct ibv_recv_wr rwr, *rbad;
        memset(&rwr, 0, sizeof(rwr));
        rwr.wr_id = 0x3003;
        rwr.sg_list = &rsge;
        rwr.num_sge = 1;
        if (ibv_post_recv(qpB, &rwr, &rbad)) FAIL("post_recv");
        msleep(20);

        struct ibv_sge sge = {(uintptr_t)sndA, BUFSZ, mrSndA->lkey};
        struct ibv_send_wr wr, *bad;
        memset(&wr, 0, sizeof(wr));
        wr.wr_id = 0x3004;
        wr.sg_list = &sge;
        wr.num_sge = 1;
        wr.opcode = IBV_WR_SEND;
        wr.send_flags = IBV_SEND_SIGNALED;
        if (ibv_post_send(qpA, &wr, &bad)) FAIL("post_send SEND");
        if (!poll_one(cqA, &wc, 2000)) FAIL("no SEND completion");
        if (!poll_one(cqB, &wc, 2000)) FAIL("no RECV completion");
        if (wc.opcode != IBV_WC_RECV) FAIL("expected IBV_WC_RECV");
        if (memcmp(sndA, rcvB, BUFSZ) != 0)
            FAIL("SEND bytes not delivered into B's recv buffer");
        fprintf(stderr, "PASS: SEND delivered %d bytes A->B\n", BUFSZ);
    }

    ibv_destroy_qp(qpA);
    ibv_destroy_qp(qpB);
    fprintf(stderr, "ALL DATA-PATH CASES PASSED\n");
    return 0;
}
