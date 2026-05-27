/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * libibmockverbs.so -- LD_PRELOAD shim for /dev/infiniband/uverbs* used by
 * ibv_devinfo. Forwards open/write/read to mock-ib when MOCK_IB=1 or
 * MOCK_IB=1.
 */

#define _GNU_SOURCE
#include <arpa/inet.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#define MOCK_MAX_FRAME (1 << 20)
#define MOCK_MAX_VERBS 32
#define MOCK_VERBS_FD_BASE 700
#define MOCK_DEFAULT_SOCK "/run/mock-ib.sock"

static const char k_b64[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

struct mock_uverbs {
    int active;
    int daemon_handle;
    char dev_name[32];
};

static pthread_once_t cfg_once = PTHREAD_ONCE_INIT;
static int mock_ib = -1;
static char socket_path[108];

static pthread_mutex_t rpc_mu = PTHREAD_MUTEX_INITIALIZER;
static int daemon_fd = -1;
static struct mock_uverbs mock_devs[MOCK_MAX_VERBS];

static int (*real_open)(const char *, int, ...);
static int (*real_openat)(int, const char *, int, ...);
static ssize_t (*real_write)(int, const void *, size_t);
static ssize_t (*real_read)(int, void *, size_t);
static int (*real_close)(int);

static void init_cfg(void) {
    const char *v = getenv("MOCK_IB");
    mock_ib = (v && v[0] == '1') ? 1 : 0;
    const char *sock = getenv("MOCK_IB_PING_SOCKET");
    if (!sock || sock[0] == '\0') {
        sock = MOCK_DEFAULT_SOCK;
    }
    snprintf(socket_path, sizeof(socket_path), "%s", sock);
}

static int mock_enabled(void) {
    pthread_once(&cfg_once, init_cfg);
    return mock_ib;
}

static int b64_encode(const uint8_t *in, size_t in_len, char *out, size_t out_cap) {
    size_t o = 0;
    for (size_t i = 0; i < in_len; i += 3) {
        uint32_t a = in[i];
        uint32_t b = (i + 1 < in_len) ? in[i + 1] : 0;
        uint32_t c = (i + 2 < in_len) ? in[i + 2] : 0;
        uint32_t t = (a << 16) | (b << 8) | c;
        if (o + 4 >= out_cap) {
            return -1;
        }
        out[o++] = k_b64[(t >> 18) & 0x3F];
        out[o++] = k_b64[(t >> 12) & 0x3F];
        out[o++] = (i + 1 < in_len) ? k_b64[(t >> 6) & 0x3F] : '=';
        out[o++] = (i + 2 < in_len) ? k_b64[t & 0x3F] : '=';
    }
    out[o] = '\0';
    return (int)o;
}

static int write_frame(int fd, const char *payload, size_t len) {
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
    if (n == 0 || n >= cap) {
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
    memcpy(addr.sun_path, socket_path, strlen(socket_path) + 1);
    if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        close(fd);
        return -1;
    }
    daemon_fd = fd;
    return 0;
}

static int rpc_call(const char *req, char *resp, size_t resp_cap) {
    if (pthread_mutex_lock(&rpc_mu) != 0) {
        return -1;
    }
    int rc = -1;
    if (ensure_daemon() < 0) {
        goto out;
    }
    if (write_frame(daemon_fd, req, strlen(req)) < 0) {
        close(daemon_fd);
        daemon_fd = -1;
        goto out;
    }
    size_t len = 0;
    if (read_frame(daemon_fd, resp, resp_cap, &len) < 0) {
        close(daemon_fd);
        daemon_fd = -1;
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
    *val = (int)strtol(p, NULL, 10);
    return 0;
}

static int uverbs_slot(int fd) {
    if (fd < MOCK_VERBS_FD_BASE || fd >= MOCK_VERBS_FD_BASE + MOCK_MAX_VERBS) {
        return -1;
    }
    return fd - MOCK_VERBS_FD_BASE;
}

static int parse_uverbs_path(const char *path, char *dev, size_t dev_cap) {
    const char *base = strstr(path, "/dev/infiniband/uverbs");
    if (!base) {
        return -1;
    }
    base += strlen("/dev/infiniband/");
    snprintf(dev, dev_cap, "%s", base);
    return 0;
}

static int verbs_open_dev(const char *dev_name) {
    char body[128];
    snprintf(body, sizeof(body), "{\"dev_name\":\"%s\"}", dev_name);
    char req[256];
    snprintf(req, sizeof(req), "{\"type\":\"verbs_open\",\"body\":%s}", body);
    char resp[MOCK_MAX_FRAME];
    if (rpc_call(req, resp, sizeof(resp)) < 0) {
        return -1;
    }
    if (strstr(resp, "\"error\"")) {
        return -1;
    }
    int handle = 0;
    if (json_find_int(resp, "handle", &handle) < 0) {
        return -1;
    }
    for (int i = 0; i < MOCK_MAX_VERBS; i++) {
        if (!mock_devs[i].active) {
            mock_devs[i].active = 1;
            mock_devs[i].daemon_handle = handle;
            snprintf(mock_devs[i].dev_name, sizeof(mock_devs[i].dev_name), "%s", dev_name);
            return MOCK_VERBS_FD_BASE + i;
        }
    }
    errno = EMFILE;
    return -1;
}

static int verbs_write_dev(int slot, const void *buf, size_t len) {
    char b64[8192];
    if (b64_encode((const uint8_t *)buf, len, b64, sizeof(b64)) < 0) {
        return -1;
    }
    char body[9000];
    snprintf(body, sizeof(body), "{\"handle\":%d,\"data\":\"%s\"}",
             mock_devs[slot].daemon_handle, b64);
    char req[9200];
    snprintf(req, sizeof(req), "{\"type\":\"verbs_write\",\"body\":%s}", body);
    char resp[MOCK_MAX_FRAME];
    if (rpc_call(req, resp, sizeof(resp)) < 0) {
        return -1;
    }
    if (strstr(resp, "\"error\"")) {
        return -1;
    }
    return (int)len;
}

static ssize_t verbs_read_dev(int slot, void *buf, size_t len) {
    char body[128];
    snprintf(body, sizeof(body), "{\"handle\":%d,\"max_len\":%zu}",
             mock_devs[slot].daemon_handle, len);
    char req[256];
    snprintf(req, sizeof(req), "{\"type\":\"verbs_read\",\"body\":%s}", body);
    char resp[MOCK_MAX_FRAME];
    if (rpc_call(req, resp, sizeof(resp)) < 0) {
        return -1;
    }
    const char *p = strstr(resp, "\"data\":\"");
    if (!p) {
        return 0;
    }
    p += 8;
    /* minimal base64 decode inline */
    uint8_t out[4096];
    int T[256];
    memset(T, -1, sizeof(T));
    for (int i = 0; i < 64; i++) {
        T[(unsigned char)k_b64[i]] = i;
    }
    size_t o = 0;
    uint32_t acc = 0;
    int bits = 0;
    for (const char *q = p; *q && *q != '"'; q++) {
        if (*q == '=') {
            break;
        }
        int v = T[(unsigned char)*q];
        if (v < 0) {
            continue;
        }
        acc = (acc << 6) | (uint32_t)v;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            if (o < sizeof(out)) {
                out[o++] = (uint8_t)((acc >> bits) & 0xFF);
            }
        }
    }
    if (o > len) {
        o = len;
    }
    memcpy(buf, out, o);
    return (ssize_t)o;
}

int open(const char *pathname, int flags, ...) {
    if (!real_open) {
        real_open = dlsym(RTLD_NEXT, "open");
    }
    if (mock_enabled() && pathname && strstr(pathname, "/dev/infiniband/uverbs")) {
        char dev[32];
        if (parse_uverbs_path(pathname, dev, sizeof(dev)) == 0) {
            int fd = verbs_open_dev(dev);
            if (fd >= 0) {
                return fd;
            }
        }
    }
    mode_t mode = 0;
    if (flags & O_CREAT) {
        va_list ap;
        va_start(ap, flags);
        mode = va_arg(ap, int);
        va_end(ap);
        return real_open(pathname, flags, mode);
    }
    return real_open(pathname, flags);
}

ssize_t write(int fd, const void *buf, size_t count) {
    if (!real_write) {
        real_write = dlsym(RTLD_NEXT, "write");
    }
    int slot = uverbs_slot(fd);
    if (mock_enabled() && slot >= 0 && mock_devs[slot].active) {
        int n = verbs_write_dev(slot, buf, count);
        return n < 0 ? -1 : (ssize_t)n;
    }
    return real_write(fd, buf, count);
}

ssize_t read(int fd, void *buf, size_t count) {
    if (!real_read) {
        real_read = dlsym(RTLD_NEXT, "read");
    }
    int slot = uverbs_slot(fd);
    if (mock_enabled() && slot >= 0 && mock_devs[slot].active) {
        return verbs_read_dev(slot, buf, count);
    }
    return real_read(fd, buf, count);
}

int close(int fd) {
    if (!real_close) {
        real_close = dlsym(RTLD_NEXT, "close");
    }
    int slot = uverbs_slot(fd);
    if (mock_enabled() && slot >= 0 && mock_devs[slot].active) {
        char body[64];
        snprintf(body, sizeof(body), "{\"handle\":%d}", mock_devs[slot].daemon_handle);
        char req[128];
        snprintf(req, sizeof(req), "{\"type\":\"verbs_close\",\"body\":%s}", body);
        char resp[512];
        rpc_call(req, resp, sizeof(resp));
        mock_devs[slot].active = 0;
        return 0;
    }
    return real_close(fd);
}
