/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * libibmockrdma.so -- LD_PRELOAD in-process libibverbs provider that carries
 * the RDMA verbs DATA path (issue #374) over the mock-ib JSON/Unix-socket
 * relay. It complements the control-path shims (libibmocksys / libibmockverbs
 * / libibmockumad): those make `ibv_devinfo`/`ibstat` see the mock fabric;
 * this one lets stock `perftest` (`ib_write_bw`, ...) actually create QPs,
 * register MRs, and move bytes between pods with NO RDMA hardware.
 *
 * It is gated on MOCK_IB_RDMA: when that env is not a truthy value ("1",
 * "true", "on", case-insensitive) every interposed symbol is a byte-for-byte
 * passthrough to the real libibverbs via dlsym(RTLD_NEXT, ...), so a pod that
 * does not opt in behaves exactly as before. When enabled, the shim becomes a
 * minimal rdma-core provider: ibv_open_device returns a real
 * `struct verbs_context` (abi_compat = __VERBS_ABI_IS_EXTENDED) so the
 * extended-verbs inlines (ibv_create_qp_ex, ___ibv_query_port) dispatch into
 * us, and it fills BOTH dispatch tables proven necessary by the spike
 * (docs/magi/spike/FINDINGS.md):
 *
 *   - classic:  ctx->ops.{post_send,post_recv,poll_cq,req_notify_cq}
 *   - extended: the ibv_qp_ex op table (ibv_wr_*) + interposed ibv_qp_to_qp_ex
 *
 * so stock perftest 4.5 (which defaults to the ibv_wr_* qp_ex API) runs with
 * no flags. Device ENUMERATION is intentionally NOT interposed (unlike the
 * spike, which faked a bare container): in a real pod the mock sysfs tree +
 * libibmocksys.so already make ibv_get_device_list return the mock HCAs.
 *
 * Data path (per pkg/network/mockib/protocol/verbs.go):
 *   - create_qp  -> assign a shim QPN, send verbs_qp_create, open a dedicated
 *                   verbs_attach connection with an inbound reader thread.
 *   - modify_qp(->RTR) -> send verbs_qp_connect with the peer QPN + dlid/dgid.
 *   - post WRITE/READ/SEND -> chunk the payload under VerbsSegMax with the same
 *                   Offset/More semantics as protocol.ChunkVerbsOp and send
 *                   verbs_op frames on the egress connection; generate the local
 *                   send completion honoring selective signaling.
 *   - inbound (reader thread): WRITE => bounds-check rkey/remote_addr against a
 *                   registered MR then memcpy; read_req => read local MR and
 *                   reply read_resp; send => satisfy a posted recv + completion.
 *
 * ABI coupling: this is a minimal rdma-core provider tied to the PRIVATE
 * verbs_context layout, so it must be compiled against the image's pinned
 * libibverbs-dev; the _Static_assert guards below fail the build if the
 * cast-compatibility assumptions the spike relied on ever regress.
 *
 * NOTE: the bandwidth perftest reports over this path is a functional artifact
 * of a JSON/TCP relay, NOT an InfiniBand measurement. See the README.
 */

#define _GNU_SOURCE
#include <arpa/inet.h>
#include <dlfcn.h>
#include <errno.h>
#include <pthread.h>
#include <stdarg.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#include <infiniband/verbs.h>
/* ibv_query_port is a macro in verbs.h that rewrites to ___ibv_query_port;
 * undef it so we can DEFINE the real exported 3-arg fallback symbol. */
#undef ibv_query_port

/* ---- compile-time ABI guards (the spike's load-bearing assumptions) ---- */
_Static_assert(offsetof(struct ibv_qp_ex, qp_base) == 0,
               "ibv_qp_ex.qp_base must be first member (cast ibv_qp*<->qp_ex*)");

#define MOCK_DEFAULT_SOCK "/run/mock-ib.sock"
#define MOCK_MAX_FRAME (1 << 20) /* mirrors protocol.MaxFrameSize (1 MiB) */
#define VERBS_SEG_MAX (512 * 1024) /* mirrors protocol.VerbsSegMax */

/* ---- opcode / status strings (mirror protocol/verbs.go) ---- */
#define OP_WRITE "write"
#define OP_READ_REQ "read_req"
#define OP_READ_RESP "read_resp"
#define OP_SEND "send"
#define ST_SUCCESS "success"
#define ST_REM_ACCESS "rem_access_err"
#define ST_REM_INV "rem_inv_req_err"

static const char k_b64[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* ===================== configuration / gating ===================== */

static pthread_once_t cfg_once = PTHREAD_ONCE_INIT;
static int g_enabled = 0;
static char g_sock[108];

static int truthy(const char *v) {
    return v && (!strcasecmp(v, "1") || !strcasecmp(v, "true") ||
                 !strcasecmp(v, "on") || !strcasecmp(v, "yes"));
}

static void init_cfg(void) {
    g_enabled = truthy(getenv("MOCK_IB_RDMA"));
    const char *sock = getenv("MOCK_IB_PING_SOCKET");
    if (!sock || sock[0] == '\0') {
        sock = MOCK_DEFAULT_SOCK;
    }
    snprintf(g_sock, sizeof(g_sock), "%s", sock);
    if (g_enabled) {
        static int banner = 0;
        if (!banner) {
            banner = 1;
            fprintf(stderr,
                    "[mock-ib] RDMA verbs data path ENABLED (MOCK_IB_RDMA): "
                    "bandwidth is a functional JSON/TCP relay artifact, NOT an "
                    "InfiniBand measurement.\n");
        }
    }
}

static int enabled(void) {
    pthread_once(&cfg_once, init_cfg);
    return g_enabled;
}

/* Diagnostic logging gate, matching the daemon's MOCK_IB_DEBUG_VERBS knob. A
 * misconfigured / unreadable sysfs tree (which would silently break routing)
 * is logged here so it is diagnosable without being noisy by default. */
static int verbs_debug(void) {
    static int d = -1;
    if (d < 0) d = (getenv("MOCK_IB_DEBUG_VERBS") != NULL &&
                    !strcmp(getenv("MOCK_IB_DEBUG_VERBS"), "1"));
    return d;
}
#define DBG(...)                                                               \
    do {                                                                       \
        if (verbs_debug()) {                                                   \
            fprintf(stderr, "[mock-ib rdma] " __VA_ARGS__);                    \
            fputc('\n', stderr);                                               \
        }                                                                      \
    } while (0)

/* ===================== mock sysfs (per-port LID/GID) ===================== */

/* The mock advertises a real per-port LID/GID in its sysfs tree, rooted at
 * MOCK_IB_ROOT (same files the Go daemon's sysfs.Scan reads). perftest exchanges
 * these over its OOB channel, so the shim MUST surface the real values or the
 * peer's modify_qp(->RTR) carries lid 0 / empty gid and the daemon registry can
 * never resolve a route. */
static const char *mock_ib_root(void) {
    const char *r = getenv("MOCK_IB_ROOT");
    return (r && r[0]) ? r : "/var/lib/nvml-mock/ib";
}

static const char *ctx_ca_name(struct ibv_context *c) {
    if (c && c->device && c->device->name[0]) return c->device->name;
    return "mlx5_0";
}

/* Reads <root>/sys/class/infiniband/<ca>/ports/<port>/lid (text "0x0102\n" or
 * decimal). Returns 1 and sets *lid on success, 0 otherwise (logged). Mirrors
 * the Go parseLID: 0x-prefixed is hex, otherwise decimal. */
static int read_sysfs_lid(const char *ca, unsigned port, uint16_t *lid) {
    char path[512];
    snprintf(path, sizeof(path), "%s/sys/class/infiniband/%s/ports/%u/lid",
             mock_ib_root(), ca, port);
    FILE *f = fopen(path, "r");
    if (!f) {
        DBG("lid: open %s failed: %s", path, strerror(errno));
        return 0;
    }
    char buf[64] = {0};
    size_t n = fread(buf, 1, sizeof(buf) - 1, f);
    fclose(f);
    if (n == 0) {
        DBG("lid: empty %s", path);
        return 0;
    }
    char *s = buf;
    while (*s == ' ' || *s == '\t') s++;
    unsigned long v = (s[0] == '0' && (s[1] == 'x' || s[1] == 'X'))
                          ? strtoul(s, NULL, 16)
                          : strtoul(s, NULL, 10);
    *lid = (uint16_t)v;
    return 1;
}

/* Reads <root>/sys/class/infiniband/<ca>/ports/<port>/gids/0 (8 colon-separated
 * 16-bit hex groups, big-endian per group) into out[16]. Returns 1 on success. */
static int read_sysfs_gid(const char *ca, unsigned port, uint8_t out[16]) {
    char path[512];
    snprintf(path, sizeof(path), "%s/sys/class/infiniband/%s/ports/%u/gids/0",
             mock_ib_root(), ca, port);
    FILE *f = fopen(path, "r");
    if (!f) {
        DBG("gid: open %s failed: %s", path, strerror(errno));
        return 0;
    }
    char buf[128] = {0};
    size_t n = fread(buf, 1, sizeof(buf) - 1, f);
    fclose(f);
    if (n == 0) {
        DBG("gid: empty %s", path);
        return 0;
    }
    unsigned g[8];
    int got = sscanf(buf, "%x:%x:%x:%x:%x:%x:%x:%x", &g[0], &g[1], &g[2], &g[3],
                     &g[4], &g[5], &g[6], &g[7]);
    if (got != 8) {
        DBG("gid: parse %s got %d/8 groups", path, got);
        return 0;
    }
    for (int i = 0; i < 8; i++) {
        out[i * 2] = (uint8_t)((g[i] >> 8) & 0xff);
        out[i * 2 + 1] = (uint8_t)(g[i] & 0xff);
    }
    return 1;
}

/* dlsym(RTLD_NEXT) helper for passthrough when the data path is disabled. */
#define NEXT(name) ((__typeof__(&name))dlsym(RTLD_NEXT, #name))

/* ===================== base64 ===================== */

static int b64_encode(const uint8_t *in, size_t in_len, char *out, size_t cap) {
    size_t o = 0;
    for (size_t i = 0; i < in_len; i += 3) {
        uint32_t a = in[i];
        uint32_t b = (i + 1 < in_len) ? in[i + 1] : 0;
        uint32_t c = (i + 2 < in_len) ? in[i + 2] : 0;
        uint32_t t = (a << 16) | (b << 8) | c;
        if (o + 4 >= cap) return -1;
        out[o++] = k_b64[(t >> 18) & 0x3F];
        out[o++] = k_b64[(t >> 12) & 0x3F];
        out[o++] = (i + 1 < in_len) ? k_b64[(t >> 6) & 0x3F] : '=';
        out[o++] = (i + 2 < in_len) ? k_b64[t & 0x3F] : '=';
    }
    out[o] = '\0';
    return (int)o;
}

/* Decode base64 between p and the next double-quote into a malloc'd buffer. */
static uint8_t *b64_decode_field(const char *p, size_t *out_len) {
    int T[256];
    memset(T, -1, sizeof(T));
    for (int i = 0; i < 64; i++) T[(unsigned char)k_b64[i]] = i;
    size_t cap = 64, o = 0;
    uint8_t *buf = malloc(cap);
    if (!buf) return NULL;
    uint32_t acc = 0;
    int bits = 0;
    for (const char *q = p; *q && *q != '"'; q++) {
        if (*q == '=') break;
        int v = T[(unsigned char)*q];
        if (v < 0) continue;
        acc = (acc << 6) | (uint32_t)v;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            if (o >= cap) {
                cap *= 2;
                uint8_t *nb = realloc(buf, cap);
                if (!nb) { free(buf); return NULL; }
                buf = nb;
            }
            buf[o++] = (uint8_t)((acc >> bits) & 0xFF);
        }
    }
    *out_len = o;
    return buf;
}

/* ===================== minimal JSON field readers ===================== */

static const char *json_find(const char *json, const char *key) {
    char pat[64];
    snprintf(pat, sizeof(pat), "\"%s\":", key);
    return strstr(json, pat) ? strstr(json, pat) + strlen(pat) : NULL;
}
static int json_u64(const char *json, const char *key, uint64_t *out) {
    const char *p = json_find(json, key);
    if (!p) return -1;
    *out = strtoull(p, NULL, 10);
    return 0;
}
static int json_u32(const char *json, const char *key, uint32_t *out) {
    uint64_t v;
    if (json_u64(json, key, &v) < 0) return -1;
    *out = (uint32_t)v;
    return 0;
}
static int json_bool(const char *json, const char *key) {
    const char *p = json_find(json, key);
    return p && !strncmp(p, "true", 4);
}
/* Copies the string value of key into out (NUL-terminated). Returns 0 / -1. */
static int json_str(const char *json, const char *key, char *out, size_t cap) {
    const char *p = json_find(json, key);
    if (!p) return -1;
    while (*p == ' ') p++;
    if (*p != '"') return -1;
    p++;
    size_t o = 0;
    while (*p && *p != '"' && o + 1 < cap) out[o++] = *p++;
    out[o] = '\0';
    return 0;
}

/* ===================== framed I/O ===================== */

static int write_all(int fd, const void *buf, size_t len) {
    const uint8_t *p = buf;
    while (len) {
        ssize_t w = write(fd, p, len);
        if (w <= 0) return -1;
        p += w;
        len -= (size_t)w;
    }
    return 0;
}
static int read_all(int fd, void *buf, size_t len) {
    uint8_t *p = buf;
    while (len) {
        ssize_t r = read(fd, p, len);
        if (r <= 0) return -1;
        p += r;
        len -= (size_t)r;
    }
    return 0;
}
static int write_frame(int fd, const char *payload, size_t len) {
    uint32_t n = htonl((uint32_t)len);
    if (write_all(fd, &n, 4) < 0) return -1;
    return write_all(fd, payload, len);
}
/* Reads one frame into a malloc'd NUL-terminated buffer. */
static char *read_frame(int fd, size_t *out_len) {
    uint32_t nbe;
    if (read_all(fd, &nbe, 4) < 0) return NULL;
    uint32_t n = ntohl(nbe);
    if (n == 0 || n > MOCK_MAX_FRAME) return NULL;
    char *buf = malloc(n + 1);
    if (!buf) return NULL;
    if (read_all(fd, buf, n) < 0) { free(buf); return NULL; }
    buf[n] = '\0';
    if (out_len) *out_len = n;
    return buf;
}

static int dial_daemon(void) {
    pthread_once(&cfg_once, init_cfg);
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -1;
    struct sockaddr_un a;
    memset(&a, 0, sizeof(a));
    a.sun_family = AF_UNIX;
    snprintf(a.sun_path, sizeof(a.sun_path), "%s", g_sock);
    if (connect(fd, (struct sockaddr *)&a, sizeof(a)) < 0) {
        close(fd);
        return -1;
    }
    return fd;
}

/* ---- egress connection: shared, fire-and-forget, reconnect on error ---- */
static pthread_mutex_t egress_mu = PTHREAD_MUTEX_INITIALIZER;
static int egress_fd = -1;

static int egress_send(const char *json, size_t len) {
    pthread_mutex_lock(&egress_mu);
    int rc = -1;
    for (int attempt = 0; attempt < 2; attempt++) {
        if (egress_fd < 0) egress_fd = dial_daemon();
        if (egress_fd < 0) break;
        if (write_frame(egress_fd, json, len) == 0) {
            rc = 0;
            break;
        }
        close(egress_fd);
        egress_fd = -1; /* retry once with a fresh dial */
    }
    pthread_mutex_unlock(&egress_mu);
    return rc;
}

/* ===================== provider object model ===================== */

#define MR_MAX 256
struct mr_rec {
    void *addr;
    size_t length;
    uint32_t lkey;
    uint32_t rkey;
    int used;
};
static struct mr_rec g_mr[MR_MAX];
static pthread_mutex_t mr_mu = PTHREAD_MUTEX_INITIALIZER;

#define CQ_RING 8192
struct cq_ext {
    struct ibv_cq cq;
    struct ibv_wc ring[CQ_RING];
    int head, tail;
    pthread_mutex_t m;
};
_Static_assert(offsetof(struct cq_ext, cq) == 0,
               "ibv_cq must be first member of cq_ext (cast ibv_cq*<->cq_ext*)");

#define RECV_MAX 1024
struct recv_wr {
    uint64_t wr_id;
    uint64_t addr;
    uint32_t length;
};

#define WR_MAX 256
struct staged_wr {
    uint64_t wr_id;
    unsigned flags;
    enum ibv_wr_opcode op;
    uint32_t rkey;
    uint64_t raddr;
    uint64_t laddr;
    uint32_t llen;
};

/* qp_ext embeds ibv_qp_ex; ex.qp_base is the ibv_qp and the FIRST member, so a
 * struct ibv_qp* from any dispatch path casts straight to qp_ext*. */
struct qp_ext {
    struct ibv_qp_ex ex;
    int sig_all;

    /* extended (ibv_wr_*) staging */
    struct staged_wr staged[WR_MAX];
    int nstaged;
    pthread_mutex_t wrm;

    /* data path */
    uint32_t dst_qpn;
    int attach_fd;
    pthread_t reader;
    int reader_running;

    /* posted receive queue (for inbound SEND) */
    struct recv_wr recvq[RECV_MAX];
    int rq_head, rq_tail;
    pthread_mutex_t rqm;
};

/* QP registry: qp_num -> qp_ext, so an inbound read_resp / fabric op can find
 * the owning QP. perftest is one QP per process here but keep it general. */
#define QP_MAX 256
static struct qp_ext *g_qp[QP_MAX];
static int g_qp_n;
static pthread_mutex_t qp_mu = PTHREAD_MUTEX_INITIALIZER;

/* pending RDMA READ requests awaiting a read_resp (keyed by op_id). */
#define PR_MAX 256
struct pending_read {
    int used;
    uint64_t op_id;
    uint64_t laddr;
    uint32_t llen;
    uint64_t wr_id;
    int signaled;
    struct ibv_cq *cq;
    uint32_t qpn;
    uint32_t got; /* bytes copied so far */
};
static struct pending_read g_pr[PR_MAX];
static pthread_mutex_t pr_mu = PTHREAD_MUTEX_INITIALIZER;

static uint32_t g_qpn = 0x100;
static uint64_t g_opid = 1;

/* ===================== completion queue ===================== */

static void push_wc(struct ibv_cq *cq, uint64_t wr_id, enum ibv_wc_opcode op,
                    uint32_t qpn, uint32_t byte_len, enum ibv_wc_status st) {
    if (!cq) return;
    struct cq_ext *c = (struct cq_ext *)cq;
    pthread_mutex_lock(&c->m);
    int ni = (c->head + 1) % CQ_RING;
    if (ni != c->tail) {
        struct ibv_wc *w = &c->ring[c->head];
        memset(w, 0, sizeof(*w));
        w->wr_id = wr_id;
        w->status = st;
        w->opcode = op;
        w->qp_num = qpn;
        w->byte_len = byte_len;
        c->head = ni;
    }
    pthread_mutex_unlock(&c->m);
}

static int my_poll_cq(struct ibv_cq *cq, int n, struct ibv_wc *wc) {
    struct cq_ext *c = (struct cq_ext *)cq;
    int got = 0;
    pthread_mutex_lock(&c->m);
    while (got < n && c->tail != c->head) {
        wc[got++] = c->ring[c->tail];
        c->tail = (c->tail + 1) % CQ_RING;
    }
    pthread_mutex_unlock(&c->m);
    return got;
}
static int my_req_notify_cq(struct ibv_cq *cq, int s) {
    (void)cq;
    (void)s;
    return 0;
}

/* ===================== MR bounds checking ===================== */

/* Returns 1 if [remote_addr, remote_addr+length) lies inside a registered MR
 * whose rkey matches; 0 otherwise. Never dereferences out of bounds. */
static int mr_check(uint32_t rkey, uint64_t remote_addr, uint64_t length) {
    pthread_mutex_lock(&mr_mu);
    int ok = 0;
    for (int i = 0; i < MR_MAX; i++) {
        if (!g_mr[i].used || g_mr[i].rkey != rkey) continue;
        uint64_t base = (uint64_t)(uintptr_t)g_mr[i].addr;
        /* No-wrap bounds test: a crafted remote_addr near 2^64 must not pass via
         * an overflowing `remote_addr + length`. base + g_mr[i].length cannot
         * overflow (it is a real allocation within the address space). */
        if (length <= g_mr[i].length && remote_addr >= base &&
            remote_addr <= base + g_mr[i].length - length) {
            ok = 1;
            break;
        }
    }
    pthread_mutex_unlock(&mr_mu);
    return ok;
}

/* ===================== egress: build + send a verbs_op chunk ===================== */

static void send_verbs_op(uint32_t src_qpn, uint32_t dst_qpn, const char *opcode,
                          uint64_t op_id, uint64_t remote_addr, uint32_t rkey,
                          uint32_t total_len, uint32_t offset, int more,
                          const char *status, const uint8_t *data,
                          uint32_t data_len) {
    /* base64 expands by 4/3; add envelope slack. */
    size_t b64cap = (size_t)data_len * 4 / 3 + 16;
    char *b64 = malloc(b64cap);
    if (!b64) return;
    b64[0] = '\0';
    if (data_len) {
        if (b64_encode(data, data_len, b64, b64cap) < 0) {
            free(b64);
            return;
        }
    }
    size_t cap = b64cap + 512;
    char *msg = malloc(cap);
    if (!msg) {
        free(b64);
        return;
    }
    int n = snprintf(
        msg, cap,
        "{\"type\":\"verbs_op\",\"body\":{"
        "\"op_id\":%llu,\"src_qpn\":%u,\"dst_qpn\":%u,\"opcode\":\"%s\","
        "\"remote_addr\":%llu,\"rkey\":%u,\"length\":%u,\"offset\":%u,"
        "\"more\":%s,\"status\":\"%s\",\"data\":\"%s\"}}",
        (unsigned long long)op_id, src_qpn, dst_qpn, opcode,
        (unsigned long long)remote_addr, rkey, total_len, offset,
        more ? "true" : "false", status ? status : ST_SUCCESS, b64);
    if (n > 0 && (size_t)n < cap) {
        egress_send(msg, (size_t)n);
    }
    free(msg);
    free(b64);
}

/* Chunk payload under VERBS_SEG_MAX mirroring protocol.ChunkVerbsOp: a zero
 * length payload still emits a single empty chunk so bytes-free ops traverse. */
static void egress_chunked(uint32_t src_qpn, uint32_t dst_qpn, const char *opcode,
                           uint64_t op_id, uint64_t remote_addr, uint32_t rkey,
                           const uint8_t *payload, uint32_t total,
                           const char *status) {
    if (total == 0) {
        send_verbs_op(src_qpn, dst_qpn, opcode, op_id, remote_addr, rkey, 0, 0, 0,
                      status, NULL, 0);
        return;
    }
    for (uint32_t off = 0; off < total; off += VERBS_SEG_MAX) {
        uint32_t end = off + VERBS_SEG_MAX;
        if (end > total) end = total;
        send_verbs_op(src_qpn, dst_qpn, opcode, op_id, remote_addr, rkey, total,
                      off, end < total, status, payload + off, end - off);
    }
}

/* ===================== inbound apply (reader thread) ===================== */

/* Apply one inbound WRITE chunk: bounds-check then memcpy into the local MR. */
static void apply_write(const char *body) {
    uint64_t remote_addr = 0;
    uint32_t rkey = 0, length = 0, offset = 0;
    json_u64(body, "remote_addr", &remote_addr);
    json_u32(body, "rkey", &rkey);
    json_u32(body, "length", &length);
    json_u32(body, "offset", &offset);
    const char *dp = json_find(body, "data");
    size_t dlen = 0;
    uint8_t *data = NULL;
    if (dp) {
        while (*dp == ' ') dp++;
        if (*dp == '"') data = b64_decode_field(dp + 1, &dlen);
    }
    if (!mr_check(rkey, remote_addr, length)) {
        free(data);
        return; /* bad rkey / OOB: never memcpy out of bounds */
    }
    /* Defensive local guard so this apply path is self-defending even when the
     * upstream relay forwards without validateVerbsOp (no-wrap). */
    if (data && dlen && (uint64_t)offset + dlen <= length) {
        memcpy((void *)(uintptr_t)(remote_addr + offset), data, dlen);
    }
    free(data);
}

/* Apply one inbound read_req: read local MR and reply with read_resp chunks. */
static void apply_read_req(struct qp_ext *qp, const char *body) {
    uint64_t op_id = 0, remote_addr = 0;
    uint32_t src_qpn = 0, rkey = 0, length = 0;
    json_u64(body, "op_id", &op_id);
    json_u32(body, "src_qpn", &src_qpn);
    json_u64(body, "remote_addr", &remote_addr);
    json_u32(body, "rkey", &rkey);
    json_u32(body, "length", &length);
    uint32_t reply_dst = src_qpn; /* daemon overwrites by route, but be explicit */
    if (!mr_check(rkey, remote_addr, length)) {
        egress_chunked(qp->ex.qp_base.qp_num, reply_dst, OP_READ_RESP, op_id,
                       remote_addr, rkey, NULL, 0, ST_REM_ACCESS);
        return;
    }
    egress_chunked(qp->ex.qp_base.qp_num, reply_dst, OP_READ_RESP, op_id,
                   remote_addr, rkey, (const uint8_t *)(uintptr_t)remote_addr,
                   length, ST_SUCCESS);
}

/* Apply one inbound read_resp: copy into the requester's local buffer and, on
 * the last chunk, generate the deferred RDMA_READ completion. */
static void apply_read_resp(const char *body) {
    uint64_t op_id = 0;
    uint32_t offset = 0, length = 0;
    int more = json_bool(body, "more");
    char status[32] = ST_SUCCESS;
    json_u64(body, "op_id", &op_id);
    json_u32(body, "offset", &offset);
    json_u32(body, "length", &length);
    json_str(body, "status", status, sizeof(status));
    const char *dp = json_find(body, "data");
    size_t dlen = 0;
    uint8_t *data = NULL;
    if (dp) {
        while (*dp == ' ') dp++;
        if (*dp == '"') data = b64_decode_field(dp + 1, &dlen);
    }
    pthread_mutex_lock(&pr_mu);
    struct pending_read *pr = NULL;
    for (int i = 0; i < PR_MAX; i++) {
        if (g_pr[i].used && g_pr[i].op_id == op_id) {
            pr = &g_pr[i];
            break;
        }
    }
    if (pr) {
        int bad = strcmp(status, ST_SUCCESS) != 0;
        /* offset is attacker-controlled and independent of got: bound it
         * explicitly against the local buffer length (no-wrap). */
        if (!bad && data && dlen && (uint64_t)offset + dlen <= pr->llen) {
            memcpy((void *)(uintptr_t)(pr->laddr + offset), data, dlen);
            pr->got += (uint32_t)dlen;
        }
        if (!more) {
            if (pr->signaled) {
                push_wc(pr->cq, pr->wr_id, IBV_WC_RDMA_READ, pr->qpn, pr->got,
                        bad ? IBV_WC_REM_ACCESS_ERR : IBV_WC_SUCCESS);
            }
            pr->used = 0;
        }
    }
    pthread_mutex_unlock(&pr_mu);
    free(data);
}

/* Apply one inbound SEND chunk: consume a posted recv WR and copy bytes; on the
 * last chunk generate the IBV_WC_RECV completion. */
static void apply_send(struct qp_ext *qp, const char *body) {
    uint32_t length = 0, offset = 0;
    int more = json_bool(body, "more");
    json_u32(body, "length", &length);
    json_u32(body, "offset", &offset);
    const char *dp = json_find(body, "data");
    size_t dlen = 0;
    uint8_t *data = NULL;
    if (dp) {
        while (*dp == ' ') dp++;
        if (*dp == '"') data = b64_decode_field(dp + 1, &dlen);
    }
    pthread_mutex_lock(&qp->rqm);
    if (qp->rq_head != qp->rq_tail) {
        struct recv_wr *rw = &qp->recvq[qp->rq_tail];
        if (data && dlen && (uint64_t)offset + dlen <= rw->length) {
            memcpy((void *)(uintptr_t)(rw->addr + offset), data, dlen);
        }
        if (!more) {
            uint64_t wr_id = rw->wr_id;
            qp->rq_tail = (qp->rq_tail + 1) % RECV_MAX;
            pthread_mutex_unlock(&qp->rqm);
            push_wc(qp->ex.qp_base.recv_cq, wr_id, IBV_WC_RECV,
                    qp->ex.qp_base.qp_num, length, IBV_WC_SUCCESS);
            free(data);
            return;
        }
    }
    pthread_mutex_unlock(&qp->rqm);
    free(data);
}

static void *reader_thread(void *arg) {
    struct qp_ext *qp = arg;
    for (;;) {
        size_t len = 0;
        char *frame = read_frame(qp->attach_fd, &len);
        if (!frame) break;
        /* frame is an Envelope {"type":"verbs_op","body":{...}}; the readers
         * above scan the whole frame, which contains exactly one body. */
        char opcode[16] = {0};
        json_str(frame, "opcode", opcode, sizeof(opcode));
        if (!strcmp(opcode, OP_WRITE)) {
            apply_write(frame);
        } else if (!strcmp(opcode, OP_READ_REQ)) {
            apply_read_req(qp, frame);
        } else if (!strcmp(opcode, OP_READ_RESP)) {
            apply_read_resp(frame);
        } else if (!strcmp(opcode, OP_SEND)) {
            apply_send(qp, frame);
        }
        free(frame);
    }
    return NULL;
}

/* ===================== egress: post one WR ===================== */

static void post_one(struct qp_ext *qp, enum ibv_wr_opcode op, uint64_t wr_id,
                     unsigned flags, uint32_t rkey, uint64_t raddr,
                     const uint8_t *local, uint32_t len) {
    uint32_t qpn = qp->ex.qp_base.qp_num;
    uint64_t op_id = __sync_fetch_and_add(&g_opid, 1);
    int signaled = qp->sig_all || (flags & IBV_SEND_SIGNALED);

    if (op == IBV_WR_RDMA_WRITE || op == IBV_WR_RDMA_WRITE_WITH_IMM) {
        egress_chunked(qpn, qp->dst_qpn, OP_WRITE, op_id, raddr, rkey, local, len,
                       ST_SUCCESS);
        if (signaled) {
            push_wc(qp->ex.qp_base.send_cq, wr_id, IBV_WC_RDMA_WRITE, qpn, len,
                    IBV_WC_SUCCESS);
        }
    } else if (op == IBV_WR_SEND || op == IBV_WR_SEND_WITH_IMM) {
        egress_chunked(qpn, qp->dst_qpn, OP_SEND, op_id, 0, 0, local, len,
                       ST_SUCCESS);
        if (signaled) {
            push_wc(qp->ex.qp_base.send_cq, wr_id, IBV_WC_SEND, qpn, len,
                    IBV_WC_SUCCESS);
        }
    } else if (op == IBV_WR_RDMA_READ) {
        /* defer completion until read_resp arrives. */
        pthread_mutex_lock(&pr_mu);
        for (int i = 0; i < PR_MAX; i++) {
            if (!g_pr[i].used) {
                g_pr[i].used = 1;
                g_pr[i].op_id = op_id;
                g_pr[i].laddr = (uint64_t)(uintptr_t)local;
                g_pr[i].llen = len;
                g_pr[i].wr_id = wr_id;
                g_pr[i].signaled = signaled;
                g_pr[i].cq = qp->ex.qp_base.send_cq;
                g_pr[i].qpn = qpn;
                g_pr[i].got = 0;
                break;
            }
        }
        pthread_mutex_unlock(&pr_mu);
        /* A read_req carries the requested length but no payload bytes. */
        send_verbs_op(qpn, qp->dst_qpn, OP_READ_REQ, op_id, raddr, rkey, len, 0,
                      0, ST_SUCCESS, NULL, 0);
    }
}

/* Gather scattered sges into one contiguous buffer (caller frees). */
static uint8_t *gather(const struct ibv_sge *sg, int n, uint32_t *out_len) {
    uint32_t total = 0;
    for (int i = 0; i < n; i++) total += sg[i].length;
    *out_len = total;
    if (!total) return NULL;
    uint8_t *buf = malloc(total);
    if (!buf) return NULL;
    uint32_t o = 0;
    for (int i = 0; i < n; i++) {
        memcpy(buf + o, (void *)(uintptr_t)sg[i].addr, sg[i].length);
        o += sg[i].length;
    }
    return buf;
}

/* ===================== classic data-path ops (ctx->ops) ===================== */

static int my_post_send(struct ibv_qp *qp, struct ibv_send_wr *wr,
                        struct ibv_send_wr **bad) {
    struct qp_ext *qe = (struct qp_ext *)qp;
    for (; wr; wr = wr->next) {
        uint32_t len = 0;
        uint8_t *buf = NULL;
        if (wr->opcode == IBV_WR_RDMA_WRITE ||
            wr->opcode == IBV_WR_RDMA_WRITE_WITH_IMM ||
            wr->opcode == IBV_WR_SEND || wr->opcode == IBV_WR_SEND_WITH_IMM) {
            buf = gather(wr->sg_list, wr->num_sge, &len);
        } else if (wr->opcode == IBV_WR_RDMA_READ) {
            for (int i = 0; i < wr->num_sge; i++) len += wr->sg_list[i].length;
        }
        uint64_t raddr = wr->wr.rdma.remote_addr;
        uint32_t rkey = wr->wr.rdma.rkey;
        const uint8_t *local =
            (wr->opcode == IBV_WR_RDMA_READ && wr->num_sge)
                ? (const uint8_t *)(uintptr_t)wr->sg_list[0].addr
                : buf;
        post_one(qe, wr->opcode, wr->wr_id, wr->send_flags, rkey, raddr, local,
                 len);
        free(buf);
    }
    if (bad) *bad = NULL;
    return 0;
}

static int my_post_recv(struct ibv_qp *qp, struct ibv_recv_wr *wr,
                        struct ibv_recv_wr **bad) {
    struct qp_ext *qe = (struct qp_ext *)qp;
    for (; wr; wr = wr->next) {
        pthread_mutex_lock(&qe->rqm);
        int ni = (qe->rq_head + 1) % RECV_MAX;
        if (ni != qe->rq_tail) {
            struct recv_wr *rw = &qe->recvq[qe->rq_head];
            rw->wr_id = wr->wr_id;
            rw->addr = wr->num_sge ? wr->sg_list[0].addr : 0;
            rw->length = wr->num_sge ? wr->sg_list[0].length : 0;
            qe->rq_head = ni;
        }
        pthread_mutex_unlock(&qe->rqm);
    }
    if (bad) *bad = NULL;
    return 0;
}

/* ===================== extended (ibv_wr_*) op table ===================== */

static struct staged_wr *cur_staged(struct qp_ext *e) {
    return e->nstaged ? &e->staged[e->nstaged - 1] : NULL;
}
static void wr_start(struct ibv_qp_ex *qpx) {
    struct qp_ext *e = (struct qp_ext *)qpx;
    pthread_mutex_lock(&e->wrm);
    e->nstaged = 0;
}
static void stage(struct ibv_qp_ex *qpx, enum ibv_wr_opcode op, uint32_t rkey,
                  uint64_t raddr) {
    struct qp_ext *e = (struct qp_ext *)qpx;
    if (e->nstaged < WR_MAX) {
        struct staged_wr *w = &e->staged[e->nstaged++];
        w->wr_id = qpx->wr_id;
        w->flags = qpx->wr_flags;
        w->op = op;
        w->rkey = rkey;
        w->raddr = raddr;
        w->laddr = 0;
        w->llen = 0;
    }
}
static void wr_rdma_write(struct ibv_qp_ex *qpx, uint32_t rkey, uint64_t raddr) {
    stage(qpx, IBV_WR_RDMA_WRITE, rkey, raddr);
}
static void wr_rdma_write_imm(struct ibv_qp_ex *qpx, uint32_t rkey,
                              uint64_t raddr, __be32 imm) {
    (void)imm;
    stage(qpx, IBV_WR_RDMA_WRITE, rkey, raddr);
}
static void wr_rdma_read(struct ibv_qp_ex *qpx, uint32_t rkey, uint64_t raddr) {
    stage(qpx, IBV_WR_RDMA_READ, rkey, raddr);
}
static void wr_send(struct ibv_qp_ex *qpx) { stage(qpx, IBV_WR_SEND, 0, 0); }
static void wr_send_imm(struct ibv_qp_ex *qpx, __be32 imm) {
    (void)imm;
    stage(qpx, IBV_WR_SEND, 0, 0);
}
static void wr_set_sge(struct ibv_qp_ex *qpx, uint32_t lkey, uint64_t addr,
                       uint32_t length) {
    (void)lkey;
    struct qp_ext *e = (struct qp_ext *)qpx;
    struct staged_wr *w = cur_staged(e);
    if (w) {
        w->laddr = addr;
        w->llen = length;
    }
}
static void wr_set_sge_list(struct ibv_qp_ex *qpx, size_t n,
                            const struct ibv_sge *sg) {
    struct qp_ext *e = (struct qp_ext *)qpx;
    struct staged_wr *w = cur_staged(e);
    if (w && n) {
        /* single-buffer fast path; multi-sge collapses to the first for the
         * staged cursor (perftest uses one sge). */
        w->laddr = sg[0].addr;
        w->llen = sg[0].length;
    }
}
static int wr_complete(struct ibv_qp_ex *qpx) {
    struct qp_ext *e = (struct qp_ext *)qpx;
    for (int i = 0; i < e->nstaged; i++) {
        struct staged_wr *w = &e->staged[i];
        const uint8_t *local = (const uint8_t *)(uintptr_t)w->laddr;
        post_one(e, w->op, w->wr_id, w->flags, w->rkey, w->raddr, local, w->llen);
    }
    e->nstaged = 0;
    pthread_mutex_unlock(&e->wrm);
    return 0;
}
static void wr_abort(struct ibv_qp_ex *qpx) {
    struct qp_ext *e = (struct qp_ext *)qpx;
    e->nstaged = 0;
    pthread_mutex_unlock(&e->wrm);
}

/* ===================== context / provider plumbing ===================== */

static int my_query_port_ex(struct ibv_context *c, uint8_t port,
                            struct ibv_port_attr *a, size_t sz);
static struct ibv_qp *my_create_qp_ex(struct ibv_context *c,
                                      struct ibv_qp_init_attr_ex *a);

static struct ibv_context *make_ctx(struct ibv_device *dev) {
    struct verbs_context *v = calloc(1, sizeof(*v));
    if (!v) return NULL;
    v->sz = sizeof(*v);
    v->query_port = my_query_port_ex;
    v->create_qp_ex = my_create_qp_ex;
    struct ibv_context *c = &v->context;
    c->device = dev;
    c->cmd_fd = -1;
    c->async_fd = -1;
    c->num_comp_vectors = 1;
    pthread_mutex_init(&c->mutex, NULL);
    c->abi_compat = __VERBS_ABI_IS_EXTENDED;
    c->ops.poll_cq = my_poll_cq;
    c->ops.req_notify_cq = my_req_notify_cq;
    c->ops.post_send = my_post_send;
    c->ops.post_recv = my_post_recv;
    return c;
}

static void fill_port(struct ibv_context *c, uint8_t port,
                      struct ibv_port_attr *a, size_t sz) {
    memset(a, 0, sz < sizeof(*a) ? sz : sizeof(*a));
    a->state = IBV_PORT_ACTIVE;
    a->max_mtu = IBV_MTU_4096;
    a->active_mtu = IBV_MTU_4096;
    a->gid_tbl_len = 8;
    a->pkey_tbl_len = 1;
    a->active_width = 2;
    a->active_speed = 32;
    a->phys_state = 5;
    a->link_layer = IBV_LINK_LAYER_INFINIBAND;
    /* Surface the REAL per-port LID so perftest's OOB exchange carries a value
     * the daemon registry holds (LID-mode routing). Fail graceful -> lid 0. */
    unsigned p = port ? port : 1;
    uint16_t lid = 0;
    if (read_sysfs_lid(ctx_ca_name(c), p, &lid)) {
        a->lid = lid;
        a->sm_lid = lid;
    } else {
        DBG("query_port: no sysfs LID for %s port %u; advertising lid 0",
            ctx_ca_name(c), p);
    }
}

static int my_query_port_ex(struct ibv_context *c, uint8_t port,
                            struct ibv_port_attr *a, size_t sz) {
    fill_port(c, port, a, sz);
    return 0;
}

/* ===================== interposed exported symbols ===================== */

struct ibv_context *ibv_open_device(struct ibv_device *d) {
    if (!enabled()) return NEXT(ibv_open_device)(d);
    return make_ctx(d);
}
int ibv_close_device(struct ibv_context *c) {
    if (!enabled()) return NEXT(ibv_close_device)(c);
    struct verbs_context *v =
        (struct verbs_context *)((uint8_t *)c -
                                 offsetof(struct verbs_context, context));
    free(v);
    return 0;
}

int ibv_query_device(struct ibv_context *c, struct ibv_device_attr *a) {
    if (!enabled()) return NEXT(ibv_query_device)(c, a);
    (void)c;
    memset(a, 0, sizeof(*a));
    a->max_qp = 256;
    a->max_qp_wr = 16384;
    a->max_cq = 256;
    a->max_cqe = 65536;
    a->max_mr = 256;
    a->max_pd = 256;
    a->max_sge = 16;
    a->max_sge_rd = 16;
    a->max_qp_rd_atom = 16;
    a->max_qp_init_rd_atom = 16;
    a->max_res_rd_atom = 256;
    a->phys_port_cnt = 1;
    a->vendor_id = 0x02c9;
    a->vendor_part_id = 4123;
    a->max_mr_size = ~0ULL;
    a->page_size_cap = 0xfffff000;
    return 0;
}

/* 3-arg exported fallback (___ibv_query_port casts ibv_port_attr to
 * _compat_ibv_port_attr* and calls this). */
int ibv_query_port(struct ibv_context *c, uint8_t port,
                   struct _compat_ibv_port_attr *cap) {
    if (!enabled()) {
        return ((int (*)(struct ibv_context *, uint8_t,
                         struct _compat_ibv_port_attr *))dlsym(RTLD_NEXT,
                                                               "ibv_query_port"))(
            c, port, cap);
    }
    fill_port(c, port, (struct ibv_port_attr *)cap, sizeof(struct ibv_port_attr));
    return 0;
}

int ibv_query_gid(struct ibv_context *c, uint8_t port, int idx,
                  union ibv_gid *g) {
    if (!enabled()) return NEXT(ibv_query_gid)(c, port, idx, g);
    memset(g, 0, sizeof(*g));
    /* Only gids/0 (the default GID) is modeled; surface it for any index so a
     * gid-index addressing run still resolves by the port GUID it carries. */
    (void)idx;
    uint8_t raw[16];
    if (read_sysfs_gid(ctx_ca_name(c), port ? port : 1, raw)) {
        memcpy(g->raw, raw, sizeof(raw));
    } else {
        DBG("query_gid: no sysfs GID for %s port %u idx %d; advertising zero gid",
            ctx_ca_name(c), port ? port : 1, idx);
    }
    return 0;
}

struct ibv_pd *ibv_alloc_pd(struct ibv_context *c) {
    if (!enabled()) return NEXT(ibv_alloc_pd)(c);
    struct ibv_pd *p = calloc(1, sizeof(*p));
    if (!p) return NULL;
    p->context = c;
    p->handle = 1;
    return p;
}
int ibv_dealloc_pd(struct ibv_pd *p) {
    if (!enabled()) return NEXT(ibv_dealloc_pd)(p);
    free(p);
    return 0;
}

/* Both the ibv_reg_mr macro and the __ibv_reg_mr inline funnel to this real
 * exported symbol, so interposing it covers all reg_mr call sites. */
struct ibv_mr *ibv_reg_mr_iova2(struct ibv_pd *pd, void *addr, size_t len,
                                uint64_t iova, unsigned int acc) {
    if (!enabled()) return NEXT(ibv_reg_mr_iova2)(pd, addr, len, iova, acc);
    (void)iova;
    (void)acc;
    struct ibv_mr *m = calloc(1, sizeof(*m));
    if (!m) return NULL;
    m->context = pd->context;
    m->pd = pd;
    m->addr = addr;
    m->length = len;
    static uint32_t g_key = 0x1000;
    m->lkey = __sync_fetch_and_add(&g_key, 1);
    m->rkey = __sync_fetch_and_add(&g_key, 1);
    pthread_mutex_lock(&mr_mu);
    for (int i = 0; i < MR_MAX; i++) {
        if (!g_mr[i].used) {
            g_mr[i].used = 1;
            g_mr[i].addr = addr;
            g_mr[i].length = len;
            g_mr[i].lkey = m->lkey;
            g_mr[i].rkey = m->rkey;
            break;
        }
    }
    pthread_mutex_unlock(&mr_mu);
    return m;
}
int ibv_dereg_mr(struct ibv_mr *m) {
    if (!enabled()) return NEXT(ibv_dereg_mr)(m);
    pthread_mutex_lock(&mr_mu);
    for (int i = 0; i < MR_MAX; i++) {
        if (g_mr[i].used && g_mr[i].lkey == m->lkey) {
            g_mr[i].used = 0;
            break;
        }
    }
    pthread_mutex_unlock(&mr_mu);
    free(m);
    return 0;
}

struct ibv_cq *ibv_create_cq(struct ibv_context *c, int cqe, void *uctx,
                             struct ibv_comp_channel *ch, int vec) {
    if (!enabled()) return NEXT(ibv_create_cq)(c, cqe, uctx, ch, vec);
    (void)vec;
    struct cq_ext *e = calloc(1, sizeof(*e));
    if (!e) return NULL;
    e->cq.context = c;
    e->cq.channel = ch;
    e->cq.cq_context = uctx;
    e->cq.cqe = cqe;
    pthread_mutex_init(&e->cq.mutex, NULL);
    pthread_cond_init(&e->cq.cond, NULL);
    pthread_mutex_init(&e->m, NULL);
    return &e->cq;
}
int ibv_destroy_cq(struct ibv_cq *cq) {
    if (!enabled()) return NEXT(ibv_destroy_cq)(cq);
    free(cq);
    return 0;
}

struct ibv_comp_channel *ibv_create_comp_channel(struct ibv_context *c) {
    if (!enabled()) return NEXT(ibv_create_comp_channel)(c);
    struct ibv_comp_channel *ch = calloc(1, sizeof(*ch));
    if (!ch) return NULL;
    ch->context = c;
    ch->fd = -1;
    ch->refcnt = 0;
    return ch;
}
int ibv_destroy_comp_channel(struct ibv_comp_channel *ch) {
    if (!enabled()) return NEXT(ibv_destroy_comp_channel)(ch);
    free(ch);
    return 0;
}

static struct qp_ext *new_qp(struct ibv_context *ctx, struct ibv_pd *pd,
                             struct ibv_cq *scq, struct ibv_cq *rcq,
                             struct ibv_srq *srq, void *qpctx,
                             enum ibv_qp_type type, int sig_all) {
    struct qp_ext *e = calloc(1, sizeof(*e));
    if (!e) return NULL;
    struct ibv_qp *q = &e->ex.qp_base;
    q->context = ctx;
    q->pd = pd;
    q->send_cq = scq;
    q->recv_cq = rcq;
    q->srq = srq;
    q->qp_num = __sync_fetch_and_add(&g_qpn, 1);
    q->state = IBV_QPS_RESET;
    q->qp_type = type;
    q->qp_context = qpctx;
    q->handle = q->qp_num;
    e->sig_all = sig_all;
    e->attach_fd = -1;
    pthread_mutex_init(&q->mutex, NULL);
    pthread_cond_init(&q->cond, NULL);
    pthread_mutex_init(&e->wrm, NULL);
    pthread_mutex_init(&e->rqm, NULL);

    /* extended op table so the ibv_wr_* inlines dispatch into us. */
    e->ex.wr_start = wr_start;
    e->ex.wr_complete = wr_complete;
    e->ex.wr_abort = wr_abort;
    e->ex.wr_rdma_write = wr_rdma_write;
    e->ex.wr_rdma_write_imm = wr_rdma_write_imm;
    e->ex.wr_rdma_read = wr_rdma_read;
    e->ex.wr_send = wr_send;
    e->ex.wr_send_imm = wr_send_imm;
    e->ex.wr_set_sge = wr_set_sge;
    e->ex.wr_set_sge_list = wr_set_sge_list;

    pthread_mutex_lock(&qp_mu);
    if (g_qp_n < QP_MAX) g_qp[g_qp_n++] = e;
    pthread_mutex_unlock(&qp_mu);

    /* announce the QP and open its dedicated inbound attach stream. Read the
     * device name directly from the struct (the layout is fixed by verbs.h)
     * rather than calling the real ibv_get_device_name, which this provider
     * does not own and may not be safe to invoke on our context. */
    const char *ca =
        (ctx && ctx->device && ctx->device->name[0]) ? ctx->device->name
                                                      : "mlx5_0";
    char msg[256];
    int n = snprintf(msg, sizeof(msg),
                     "{\"type\":\"verbs_qp_create\",\"body\":{\"qpn\":%u,"
                     "\"ca_name\":\"%s\",\"port\":1}}",
                     q->qp_num, ca);
    egress_send(msg, (size_t)n);

    int afd = dial_daemon();
    if (afd >= 0) {
        char am[128];
        int an = snprintf(am, sizeof(am),
                          "{\"type\":\"verbs_attach\",\"body\":{\"qpn\":%u}}",
                          q->qp_num);
        if (write_frame(afd, am, (size_t)an) == 0) {
            e->attach_fd = afd;
            e->reader_running = 1;
            if (pthread_create(&e->reader, NULL, reader_thread, e) != 0) {
                e->reader_running = 0;
                close(afd);
                e->attach_fd = -1;
            }
        } else {
            close(afd);
        }
    }
    return e;
}

struct ibv_qp *ibv_create_qp(struct ibv_pd *pd, struct ibv_qp_init_attr *a) {
    if (!enabled()) return NEXT(ibv_create_qp)(pd, a);
    struct qp_ext *e = new_qp(pd->context, pd, a->send_cq, a->recv_cq, a->srq,
                              a->qp_context, a->qp_type, a->sq_sig_all);
    return e ? &e->ex.qp_base : NULL;
}
static struct ibv_qp *my_create_qp_ex(struct ibv_context *c,
                                      struct ibv_qp_init_attr_ex *a) {
    struct ibv_pd *pd = (a->comp_mask & IBV_QP_INIT_ATTR_PD) ? a->pd : NULL;
    struct qp_ext *e = new_qp(c, pd, a->send_cq, a->recv_cq, a->srq,
                              a->qp_context, a->qp_type, a->sq_sig_all);
    return e ? &e->ex.qp_base : NULL;
}
struct ibv_qp_ex *ibv_qp_to_qp_ex(struct ibv_qp *qp) {
    if (!enabled()) return NEXT(ibv_qp_to_qp_ex)(qp);
    return (struct ibv_qp_ex *)qp;
}

int ibv_modify_qp(struct ibv_qp *qp, struct ibv_qp_attr *attr, int mask) {
    if (!enabled()) return NEXT(ibv_modify_qp)(qp, attr, mask);
    struct qp_ext *e = (struct qp_ext *)qp;
    if (mask & IBV_QP_STATE) qp->state = attr->qp_state;
    /* At ->RTR perftest supplies the remote endpoint (dest QPN + AV). Relay it
     * as verbs_qp_connect so the daemon can resolve a route. */
    if ((mask & IBV_QP_STATE) && attr->qp_state == IBV_QPS_RTR) {
        e->dst_qpn = attr->dest_qp_num;
        char dgid[64] = "";
        if (attr->ah_attr.is_global) {
            uint8_t *r = attr->ah_attr.grh.dgid.raw;
            snprintf(dgid, sizeof(dgid),
                     "%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:"
                     "%02x%02x:%02x%02x",
                     r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8], r[9],
                     r[10], r[11], r[12], r[13], r[14], r[15]);
        }
        char msg[512];
        int n = snprintf(
            msg, sizeof(msg),
            "{\"type\":\"verbs_qp_connect\",\"body\":{\"local_qpn\":%u,"
            "\"dest_qpn\":%u,\"dlid\":%u,\"dgid\":\"%s\",\"link_layer\":\"IB\"}}",
            qp->qp_num, attr->dest_qp_num, attr->ah_attr.dlid, dgid);
        egress_send(msg, (size_t)n);
    }
    return 0;
}

int ibv_query_qp(struct ibv_qp *qp, struct ibv_qp_attr *attr, int mask,
                 struct ibv_qp_init_attr *ia) {
    if (!enabled()) return NEXT(ibv_query_qp)(qp, attr, mask, ia);
    (void)mask;
    memset(attr, 0, sizeof(*attr));
    attr->qp_state = qp->state;
    if (ia) memset(ia, 0, sizeof(*ia));
    return 0;
}

int ibv_destroy_qp(struct ibv_qp *qp) {
    if (!enabled()) return NEXT(ibv_destroy_qp)(qp);
    struct qp_ext *e = (struct qp_ext *)qp;
    char msg[128];
    int n = snprintf(msg, sizeof(msg),
                     "{\"type\":\"verbs_qp_destroy\",\"body\":{\"qpn\":%u}}",
                     qp->qp_num);
    egress_send(msg, (size_t)n);
    if (e->reader_running) {
        shutdown(e->attach_fd, SHUT_RDWR);
        close(e->attach_fd);
        pthread_join(e->reader, NULL);
        e->reader_running = 0;
        e->attach_fd = -1;
    }
    pthread_mutex_lock(&qp_mu);
    for (int i = 0; i < g_qp_n; i++) {
        if (g_qp[i] == e) {
            g_qp[i] = g_qp[--g_qp_n];
            break;
        }
    }
    pthread_mutex_unlock(&qp_mu);
    free(e);
    return 0;
}

struct ibv_ah *ibv_create_ah(struct ibv_pd *pd, struct ibv_ah_attr *a) {
    if (!enabled()) return NEXT(ibv_create_ah)(pd, a);
    (void)a;
    struct ibv_ah *ah = calloc(1, sizeof(*ah));
    if (!ah) return NULL;
    ah->context = pd->context;
    ah->pd = pd;
    return ah;
}
int ibv_destroy_ah(struct ibv_ah *ah) {
    if (!enabled()) return NEXT(ibv_destroy_ah)(ah);
    free(ah);
    return 0;
}

struct ibv_srq *ibv_create_srq(struct ibv_pd *pd, struct ibv_srq_init_attr *a) {
    if (!enabled()) return NEXT(ibv_create_srq)(pd, a);
    (void)a;
    struct ibv_srq *s = calloc(1, sizeof(*s));
    if (!s) return NULL;
    s->context = pd->context;
    s->pd = pd;
    return s;
}
int ibv_destroy_srq(struct ibv_srq *s) {
    if (!enabled()) return NEXT(ibv_destroy_srq)(s);
    free(s);
    return 0;
}
