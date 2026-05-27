/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * libibmockumad.so -- LD_PRELOAD shim that forwards libibumad calls used by
 * ibping to mock-ib over a Unix socket when MOCK_IB=1.
 *
 * When MOCK_IB is unset or "0", every hooked symbol delegates to the
 * real libibumad via dlsym(RTLD_NEXT, ...).
 *
 * Wire format matches pkg/network/mockib/protocol: 4-byte big-endian
 * frame length, JSON envelope {"type":"...","body":{...}}.  []byte fields
 * use standard base64 in JSON (encoding/json compatibility).
 */

#define _GNU_SOURCE
#include <arpa/inet.h>
#include <dlfcn.h>
#include <errno.h>
#include <netinet/in.h>
#include <pthread.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#ifndef UMAD_CA_NAME_LEN
#define UMAD_CA_NAME_LEN 20
#endif

/* Minimal copies of infiniband/umad.h layout (ABI v5). */
typedef struct ib_mad_addr {
    uint32_t qpn;
    uint32_t qkey;
    uint16_t lid;
    uint8_t sl;
    uint8_t path_bits;
    uint8_t grh_present;
    uint8_t gid_index;
    uint8_t hop_limit;
    uint8_t traffic_class;
    union {
        uint8_t gid[16];
    };
    uint32_t flow_label;
    uint16_t pkey_index;
    uint8_t reserved[6];
} ib_mad_addr_t;

typedef struct ib_user_mad {
    uint32_t agent_id;
    uint32_t status;
    uint32_t timeout_ms;
    uint32_t retries;
    uint32_t length;
    ib_mad_addr_t addr;
    uint8_t data[0];
} ib_user_mad_t;

#define MOCK_MAX_FRAME (1 << 20)
#define MOCK_MAX_PORTS 32
/* Matches libibumad umad_size() on Debian bookworm (ABI v5, legacy header). */
/* Legacy libibumad umad_size() on Debian bookworm (not sizeof struct ib_user_mad). */
#define MOCK_UMAD_HDR_SZ 56
#define MOCK_IB_MAD_SIZE 256
#define MOCK_DEFAULT_SOCK "/run/mock-ib.sock"

static const char k_b64[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

struct mock_port {
    int active;
    int daemon_handle;
    char ca_name[UMAD_CA_NAME_LEN];
    int portnum;
};

static pthread_once_t cfg_once = PTHREAD_ONCE_INIT;
static int mock_ping = -1; /* -1 unknown, 0 off, 1 on */
static char socket_path[108];

static pthread_mutex_t rpc_mu = PTHREAD_MUTEX_INITIALIZER;
static int daemon_fd = -1;
static struct mock_port mock_ports[MOCK_MAX_PORTS];
static int next_local_port = 100;

#define LOAD_SYM(fn, sym)                                        \
    do {                                                         \
        if (!(fn)) {                                             \
            (fn) = dlsym(RTLD_NEXT, (sym));                      \
        }                                                        \
    } while (0)

static void init_cfg(void) {
    const char *v = getenv("MOCK_IB");
    mock_ping = (v && v[0] == '1') ? 1 : 0;
    const char *sock = getenv("MOCK_IB_PING_SOCKET");
    if (!sock || sock[0] == '\0') {
        sock = MOCK_DEFAULT_SOCK;
    }
    snprintf(socket_path, sizeof(socket_path), "%s", sock);
}

static int mock_enabled(void) {
    pthread_once(&cfg_once, init_cfg);
    return mock_ping;
}

/* ---------- base64 (encoding/json []byte) ---------- */

static int b64_encode(const uint8_t *in, size_t in_len, char *out, size_t out_cap) {
    size_t o = 0;
    for (size_t i = 0; i < in_len; i += 3) {
        uint32_t octet_a = in[i];
        uint32_t octet_b = (i + 1 < in_len) ? in[i + 1] : 0;
        uint32_t octet_c = (i + 2 < in_len) ? in[i + 2] : 0;
        uint32_t triple = (octet_a << 16) | (octet_b << 8) | octet_c;
        if (o + 4 >= out_cap) {
            return -1;
        }
        out[o++] = k_b64[(triple >> 18) & 0x3F];
        out[o++] = k_b64[(triple >> 12) & 0x3F];
        out[o++] = (i + 1 < in_len) ? k_b64[(triple >> 6) & 0x3F] : '=';
        out[o++] = (i + 2 < in_len) ? k_b64[triple & 0x3F] : '=';
    }
    if (o >= out_cap) {
        return -1;
    }
    out[o] = '\0';
    return (int)o;
}

static int b64_decode(const char *in, uint8_t *out, size_t out_cap, size_t *out_len) {
    int T[256];
    memset(T, -1, sizeof(T));
    for (int i = 0; i < 64; i++) {
        T[(unsigned char)k_b64[i]] = i;
    }
    size_t o = 0;
    uint32_t acc = 0;
    int acc_bits = 0;
    for (const char *p = in; *p; p++) {
        if (*p == '=') {
            break;
        }
        if (*p == '\n' || *p == '\r' || *p == ' ') {
            continue;
        }
        int v = T[(unsigned char)*p];
        if (v < 0) {
            return -1;
        }
        acc = (acc << 6) | (uint32_t)v;
        acc_bits += 6;
        if (acc_bits >= 8) {
            acc_bits -= 8;
            if (o >= out_cap) {
                return -1;
            }
            out[o++] = (uint8_t)((acc >> acc_bits) & 0xFF);
        }
    }
    if (out_len) {
        *out_len = o;
    }
    return 0;
}

/* ---------- length-prefixed JSON RPC ---------- */

static int write_frame(int fd, const char *payload, size_t len) {
    if (len == 0 || len > MOCK_MAX_FRAME) {
        errno = EINVAL;
        return -1;
    }
    uint32_t n = htonl((uint32_t)len);
    if (write(fd, &n, 4) != 4) {
        return -1;
    }
    size_t off = 0;
    while (off < len) {
        ssize_t w = write(fd, payload + off, len - off);
        if (w <= 0) {
            return -1;
        }
        off += (size_t)w;
    }
    return 0;
}

static int read_frame(int fd, char *buf, size_t cap, size_t *out_len) {
    uint32_t nbe;
    if (read(fd, &nbe, 4) != 4) {
        return -1;
    }
    uint32_t n = ntohl(nbe);
    if (n == 0 || n > MOCK_MAX_FRAME || n >= cap) {
        errno = EMSGSIZE;
        return -1;
    }
    size_t got = 0;
    while (got < n) {
        ssize_t r = read(fd, buf + got, n - got);
        if (r <= 0) {
            return -1;
        }
        got += (size_t)r;
    }
    buf[n] = '\0';
    if (out_len) {
        *out_len = n;
    }
    return 0;
}

static int ensure_daemon(void) {
    if (daemon_fd >= 0) {
        return 0;
    }
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) {
        return -1;
    }
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    if (strlen(socket_path) >= sizeof(addr.sun_path)) {
        close(fd);
        errno = ENAMETOOLONG;
        return -1;
    }
    memcpy(addr.sun_path, socket_path, strlen(socket_path) + 1);
    if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        close(fd);
        return -1;
    }
    daemon_fd = fd;
    return 0;
}

static void drop_daemon(void) {
    if (daemon_fd >= 0) {
        close(daemon_fd);
        daemon_fd = -1;
    }
}

static int rpc_call(const char *req, char *resp, size_t resp_cap) {
    if (pthread_mutex_lock(&rpc_mu) != 0) {
        errno = EIO;
        return -1;
    }
    int rc = -1;
    if (ensure_daemon() < 0) {
        goto out;
    }
    size_t req_len = strlen(req);
    if (write_frame(daemon_fd, req, req_len) < 0) {
        drop_daemon();
        goto out;
    }
    size_t resp_len = 0;
    if (read_frame(daemon_fd, resp, resp_cap, &resp_len) < 0) {
        drop_daemon();
        goto out;
    }
    rc = 0;
out:
    pthread_mutex_unlock(&rpc_mu);
    return rc;
}

static int json_find_int(const char *json, const char *key, int *val) {
    char pat[64];
    snprintf(pat, sizeof(pat), "\"%s\":", key);
    const char *p = strstr(json, pat);
    if (!p) {
        return -1;
    }
    p += strlen(pat);
    while (*p == ' ') {
        p++;
    }
    *val = (int)strtol(p, NULL, 10);
    return 0;
}

static int json_find_bool(const char *json, const char *key) {
    char pat[64];
    snprintf(pat, sizeof(pat), "\"%s\":true", key);
    return strstr(json, pat) != NULL;
}

static const char *json_find_string(const char *json, const char *key) {
    char pat[64];
    snprintf(pat, sizeof(pat), "\"%s\":\"", key);
    return strstr(json, pat);
}

static const char *json_string_value(const char *json, const char *key) {
    const char *p = json_find_string(json, key);
    if (!p) {
        return NULL;
    }
    p += strlen(key) + 4; /* skip "key":" */
    return p;
}

static int json_unescape_string(const char *quoted, char *out, size_t out_cap) {
    if (!quoted || *quoted != '"') {
        return -1;
    }
    quoted++;
    size_t o = 0;
    for (const char *p = quoted; *p && *p != '"'; p++) {
        char c = *p;
        if (c == '\\' && p[1]) {
            p++;
            c = *p;
        }
        if (o + 1 >= out_cap) {
            return -1;
        }
        out[o++] = c;
    }
    out[o] = '\0';
    return 0;
}

static int build_message(char *buf, size_t cap, const char *type, const char *body) {
    int n = snprintf(buf, cap, "{\"type\":\"%s\",\"body\":%s}", type, body);
    if (n < 0 || (size_t)n >= cap) {
        return -1;
    }
    return 0;
}

static int port_slot(int portid) {
    if (portid < next_local_port || portid >= next_local_port + MOCK_MAX_PORTS) {
        return -1;
    }
    return portid - next_local_port;
}

static int alloc_local_port(int daemon_handle, const char *ca_name, int portnum) {
    for (int i = 0; i < MOCK_MAX_PORTS; i++) {
        if (!mock_ports[i].active) {
            mock_ports[i].active = 1;
            mock_ports[i].daemon_handle = daemon_handle;
            snprintf(mock_ports[i].ca_name, sizeof(mock_ports[i].ca_name), "%s", ca_name);
            mock_ports[i].portnum = portnum;
            return i + next_local_port;
        }
    }
    errno = EMFILE;
    return -1;
}

static void free_local_port(int portid) {
    int slot = port_slot(portid);
    if (slot < 0 || !mock_ports[slot].active) {
        return;
    }
    memset(&mock_ports[slot], 0, sizeof(mock_ports[slot]));
}

static int local_daemon_handle(int portid) {
    int slot = port_slot(portid);
    if (slot < 0 || !mock_ports[slot].active) {
        return -1;
    }
    return mock_ports[slot].daemon_handle;
}

/* ---------- hooked libibumad API ---------- */

static int (*real_umad_init)(void);
static int (*real_umad_done)(void);
static int (*real_umad_open_port)(const char *, int);
static int (*real_umad_close_port)(int);
static int (*real_umad_register)(int, int, int, uint8_t, long *);
static int (*real_umad_send)(int, int, void *, int, int, int);
static int (*real_umad_recv)(int, void *, int *, int);
static size_t (*real_umad_size)(void);

int umad_init(void) {
    LOAD_SYM(real_umad_init, "umad_init");
    if (!mock_enabled()) {
        if (!real_umad_init) {
            return 0;
        }
        return real_umad_init();
    }
    /* Real init still needed so libibumad can enumerate CAs via sysfs (MOCK_IB_ROOT). */
    if (real_umad_init) {
        return real_umad_init();
    }
    return 0;
}

int umad_done(void) {
    if (!mock_enabled()) {
        LOAD_SYM(real_umad_done, "umad_done");
        if (!real_umad_done) {
            return 0;
        }
        return real_umad_done();
    }
    pthread_mutex_lock(&rpc_mu);
    for (int i = 0; i < MOCK_MAX_PORTS; i++) {
        mock_ports[i].active = 0;
    }
    drop_daemon();
    pthread_mutex_unlock(&rpc_mu);
    return 0;
}

int umad_open_port(const char *ca_name, int portnum) {
    if (!mock_enabled()) {
        LOAD_SYM(real_umad_open_port, "umad_open_port");
        return real_umad_open_port(ca_name, portnum);
    }
    if (!ca_name || portnum <= 0) {
        ca_name = "mlx5_0";
        portnum = 1;
    }
    char body[256];
    snprintf(body, sizeof(body), "{\"ca_name\":\"%s\",\"port\":%d}", ca_name, portnum);
    char req[384];
    if (build_message(req, sizeof(req), "open", body) < 0) {
        errno = ENOMEM;
        return -1;
    }
    char resp[MOCK_MAX_FRAME];
    if (rpc_call(req, resp, sizeof(resp)) < 0) {
        errno = EIO;
        return -EIO;
    }
    const char *errp = json_find_string(resp, "error");
    if (errp) {
        char errbuf[256];
        const char *q = strchr(errp, '"');
        if (q && json_unescape_string(q, errbuf, sizeof(errbuf)) == 0) {
            errno = EINVAL;
        } else {
            errno = EINVAL;
        }
        return -EINVAL;
    }
    int handle = 0;
    if (json_find_int(resp, "handle", &handle) < 0 || handle <= 0) {
        errno = EINVAL;
        return -EINVAL;
    }
    int local = alloc_local_port(handle, ca_name, portnum);
    if (local < 0) {
        return -1;
    }
    return local;
}

int umad_close_port(int portid) {
    if (!mock_enabled()) {
        LOAD_SYM(real_umad_close_port, "umad_close_port");
        return real_umad_close_port(portid);
    }
    int dh = local_daemon_handle(portid);
    if (dh < 0) {
        errno = EINVAL;
        return -1;
    }
    char body[128];
    snprintf(body, sizeof(body), "{\"handle\":%d}", dh);
    char req[256];
    if (build_message(req, sizeof(req), "close", body) < 0) {
        errno = EIO;
        return -1;
    }
    char resp[4096];
    if (rpc_call(req, resp, sizeof(resp)) < 0) {
        errno = EIO;
        return -1;
    }
    free_local_port(portid);
    return 0;
}

int umad_register(int portid, int mgmt_class, int mgmt_version, uint8_t rmpp_version,
                  long method_mask[16 / sizeof(long)]) {
    (void)mgmt_class;
    (void)mgmt_version;
    (void)rmpp_version;
    (void)method_mask;
    if (!mock_enabled()) {
        LOAD_SYM(real_umad_register, "umad_register");
        return real_umad_register(portid, mgmt_class, mgmt_version, rmpp_version,
                                  method_mask);
    }
    if (local_daemon_handle(portid) < 0) {
        errno = EINVAL;
        return -EINVAL;
    }
    /* Phase 1: daemon does not track agents; return a fixed agent id. */
    return 1;
}

int umad_send(int portid, int agentid, void *umad, int length, int timeout_ms, int retries) {
    if (!mock_enabled()) {
        LOAD_SYM(real_umad_send, "umad_send");
        return real_umad_send(portid, agentid, umad, length, timeout_ms, retries);
    }
    int dh = local_daemon_handle(portid);
    if (dh < 0 || !umad || length < 0) {
        errno = EINVAL;
        return -EINVAL;
    }
    /*
     * libibmad _do_madrpc passes len = mad_build_pkt() return value (MAD bytes only),
     * not umad_size()+len. Always forward header + full MAD to mock-ib.
     */
    size_t total = (size_t)length;
    if (total <= (size_t)MOCK_IB_MAD_SIZE) {
        total = (size_t)MOCK_UMAD_HDR_SZ + total;
    }
    char b64[8192];
    if (b64_encode((const uint8_t *)umad, total, b64, sizeof(b64)) < 0) {
        errno = EMSGSIZE;
        return -1;
    }
    char body[9000];
    int bn = snprintf(body, sizeof(body), "{\"handle\":%d,\"mad\":\"%s\"}", dh, b64);
    if (bn < 0 || (size_t)bn >= sizeof(body)) {
        errno = EMSGSIZE;
        return -1;
    }
    char req[9200];
    if (build_message(req, sizeof(req), "send", body) < 0) {
        errno = EIO;
        return -1;
    }
    char resp[4096];
    if (rpc_call(req, resp, sizeof(resp)) < 0) {
        errno = EIO;
        return -EIO;
    }
    if (json_find_string(resp, "error") != NULL) {
        errno = EIO;
        return -EIO;
    }
    if (!json_find_bool(resp, "ok")) {
        errno = EIO;
        return -EIO;
    }
    (void)agentid;
    (void)timeout_ms;
    (void)retries;
    return 0;
}

int umad_recv(int portid, void *umad, int *length, int timeout_ms) {
    if (!mock_enabled()) {
        LOAD_SYM(real_umad_recv, "umad_recv");
        return real_umad_recv(portid, umad, length, timeout_ms);
    }
    int dh = local_daemon_handle(portid);
    if (dh < 0 || !umad || !length || *length < 0) {
        errno = EINVAL;
        return -EINVAL;
    }
    char body[128];
    snprintf(body, sizeof(body), "{\"handle\":%d,\"timeout_ms\":%d}", dh, timeout_ms);
    char req[256];
    if (build_message(req, sizeof(req), "recv", body) < 0) {
        errno = EIO;
        return -1;
    }
    char resp[MOCK_MAX_FRAME];
    if (rpc_call(req, resp, sizeof(resp)) < 0) {
        errno = EIO;
        return -EIO;
    }
    if (strstr(resp, "\"error\":\"") != NULL) {
        errno = EIO;
        return -EIO;
    }
    if (json_find_bool(resp, "timeout")) {
        errno = EWOULDBLOCK;
        return -EWOULDBLOCK;
    }
    const char *start = json_string_value(resp, "mad");
    if (!start) {
        errno = EIO;
        return -EIO;
    }
  {
    const char *end = start;
    while (*end && *end != '"') {
        if (*end == '\\' && end[1]) {
            end += 2;
        } else {
            end++;
        }
    }
    size_t enc_len = (size_t)(end - start);
    char *enc = malloc(enc_len + 1);
    if (!enc) {
        errno = ENOMEM;
        return -1;
    }
    memcpy(enc, start, enc_len);
    enc[enc_len] = '\0';
    uint8_t pkt[4096];
    size_t pkt_len = 0;
    int dec = b64_decode(enc, pkt, sizeof(pkt), &pkt_len);
    free(enc);
    if (dec < 0 || pkt_len < MOCK_UMAD_HDR_SZ) {
        errno = EIO;
        return -EIO;
    }
    size_t data_len = pkt_len - MOCK_UMAD_HDR_SZ;
    if (data_len > MOCK_IB_MAD_SIZE) {
        data_len = MOCK_IB_MAD_SIZE;
    }
    /* libibmad passes *length as IB_SMP_DATA_SIZE (64) while the caller buffer is
     * umad_size()+256 bytes. Do not treat that as the recv cap — always copy the MAD. */
    memcpy(umad, pkt, MOCK_UMAD_HDR_SZ);
    memcpy((uint8_t *)umad + MOCK_UMAD_HDR_SZ, pkt + MOCK_UMAD_HDR_SZ, data_len);
    ((ib_user_mad_t *)umad)->status = 0;
    ((ib_user_mad_t *)umad)->length = (uint32_t)data_len;
    *length = (int)data_len;
    return 1;
  }
}

size_t umad_size(void) {
    if (!mock_enabled()) {
        LOAD_SYM(real_umad_size, "umad_size");
        return real_umad_size();
    }
    return MOCK_UMAD_HDR_SZ;
}
