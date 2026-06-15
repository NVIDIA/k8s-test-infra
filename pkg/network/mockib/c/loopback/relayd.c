/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * relayd -- a minimal stand-in for the mock-ib daemon's verbs router, used
 * ONLY by the host-side loopback test for libibmockrdma.so. It speaks the same
 * length-prefixed JSON wire protocol as pkg/network/mockib/daemon/verbs_fabric.go
 * but implements just enough routing to relay verbs_op frames between two
 * software QPs on one host:
 *
 *   - verbs_qp_create / verbs_qp_destroy : logged, no reply.
 *   - verbs_qp_connect                   : record route[local_qpn] = dest_qpn
 *                                          (the real daemon resolves this via
 *                                          the registry by GID/LID; the message
 *                                          already carries dest_qpn, so the test
 *                                          relay skips registry resolution).
 *   - verbs_attach                       : keep this connection as the inbound
 *                                          stream for its QPN.
 *   - verbs_op (on the egress conn)      : look up route[src_qpn] -> dst, then
 *                                          forward the frame verbatim to the
 *                                          dst QPN's attach connection.
 *
 * It is a test fixture, not product code; the real routing/backpressure lives
 * in verbs_fabric.go.
 */

#define _GNU_SOURCE
#include <arpa/inet.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#define MAXQP 256
#define MAXFRAME (1 << 20)

static pthread_mutex_t g_mu = PTHREAD_MUTEX_INITIALIZER;
struct route {
    int used;
    uint32_t local, dest;
};
static struct route g_routes[MAXQP];
struct attach {
    int used;
    uint32_t qpn;
    int fd;
};
static struct attach g_attach[MAXQP];
static long g_op_count;

static int read_all(int fd, void *b, size_t n) {
    uint8_t *p = b;
    while (n) {
        ssize_t r = read(fd, p, n);
        if (r <= 0) return -1;
        p += r;
        n -= (size_t)r;
    }
    return 0;
}
static int write_all(int fd, const void *b, size_t n) {
    const uint8_t *p = b;
    while (n) {
        ssize_t w = write(fd, p, n);
        if (w <= 0) return -1;
        p += w;
        n -= (size_t)w;
    }
    return 0;
}
static char *read_frame(int fd, uint32_t *len) {
    uint32_t nbe;
    if (read_all(fd, &nbe, 4) < 0) return NULL;
    uint32_t n = ntohl(nbe);
    if (n == 0 || n > MAXFRAME) return NULL;
    char *buf = malloc(n + 1);
    if (!buf) return NULL;
    if (read_all(fd, buf, n) < 0) {
        free(buf);
        return NULL;
    }
    buf[n] = '\0';
    *len = n;
    return buf;
}
static int write_frame(int fd, const char *b, uint32_t n) {
    uint32_t nbe = htonl(n);
    if (write_all(fd, &nbe, 4) < 0) return -1;
    return write_all(fd, b, n);
}

static const char *jfind(const char *j, const char *k) {
    char pat[64];
    snprintf(pat, sizeof(pat), "\"%s\":", k);
    const char *p = strstr(j, pat);
    return p ? p + strlen(pat) : NULL;
}
static uint32_t ju32(const char *j, const char *k) {
    const char *p = jfind(j, k);
    return p ? (uint32_t)strtoul(p, NULL, 10) : 0;
}
static int has_type(const char *j, const char *t) {
    char pat[64];
    snprintf(pat, sizeof(pat), "\"type\":\"%s\"", t);
    return strstr(j, pat) != NULL;
}

static void set_route(uint32_t local, uint32_t dest) {
    pthread_mutex_lock(&g_mu);
    for (int i = 0; i < MAXQP; i++) {
        if (g_routes[i].used && g_routes[i].local == local) {
            g_routes[i].dest = dest;
            pthread_mutex_unlock(&g_mu);
            return;
        }
    }
    for (int i = 0; i < MAXQP; i++) {
        if (!g_routes[i].used) {
            g_routes[i].used = 1;
            g_routes[i].local = local;
            g_routes[i].dest = dest;
            break;
        }
    }
    pthread_mutex_unlock(&g_mu);
}
static int lookup_dest(uint32_t local, uint32_t *dest) {
    pthread_mutex_lock(&g_mu);
    for (int i = 0; i < MAXQP; i++) {
        if (g_routes[i].used && g_routes[i].local == local) {
            *dest = g_routes[i].dest;
            pthread_mutex_unlock(&g_mu);
            return 1;
        }
    }
    pthread_mutex_unlock(&g_mu);
    return 0;
}
static void register_attach(uint32_t qpn, int fd) {
    pthread_mutex_lock(&g_mu);
    for (int i = 0; i < MAXQP; i++) {
        if (!g_attach[i].used) {
            g_attach[i].used = 1;
            g_attach[i].qpn = qpn;
            g_attach[i].fd = fd;
            break;
        }
    }
    pthread_mutex_unlock(&g_mu);
}
static void unregister_attach(int fd) {
    pthread_mutex_lock(&g_mu);
    for (int i = 0; i < MAXQP; i++) {
        if (g_attach[i].used && g_attach[i].fd == fd) g_attach[i].used = 0;
    }
    pthread_mutex_unlock(&g_mu);
}

/* Forward a verbs_op frame to the attach connection owning dst_qpn. */
static void deliver(uint32_t dst, const char *frame, uint32_t len) {
    pthread_mutex_lock(&g_mu);
    int fd = -1;
    for (int i = 0; i < MAXQP; i++) {
        if (g_attach[i].used && g_attach[i].qpn == dst) {
            fd = g_attach[i].fd;
            break;
        }
    }
    if (fd >= 0) write_frame(fd, frame, len);
    pthread_mutex_unlock(&g_mu);
}

static void *conn_thread(void *arg) {
    int fd = (int)(intptr_t)arg;
    int is_attach = 0;
    for (;;) {
        uint32_t len = 0;
        char *frame = read_frame(fd, &len);
        if (!frame) break;
        if (has_type(frame, "verbs_qp_connect")) {
            set_route(ju32(frame, "local_qpn"), ju32(frame, "dest_qpn"));
        } else if (has_type(frame, "verbs_attach")) {
            register_attach(ju32(frame, "qpn"), fd);
            is_attach = 1;
        } else if (has_type(frame, "verbs_op")) {
            uint32_t src = ju32(frame, "src_qpn");
            uint32_t dst = 0;
            if (lookup_dest(src, &dst)) {
                long n = __sync_add_and_fetch(&g_op_count, 1);
                deliver(dst, frame, len);
                if (n == 1 || n % 64 == 0) {
                    fprintf(stderr, "relayd: forwarded %ld verbs_op frames\n", n);
                }
            }
        }
        /* verbs_qp_create / verbs_qp_destroy: nothing to do. */
        free(frame);
    }
    if (is_attach) unregister_attach(fd);
    close(fd);
    return NULL;
}

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: %s <socket-path>\n", argv[0]);
        return 2;
    }
    unlink(argv[1]);
    int s = socket(AF_UNIX, SOCK_STREAM, 0);
    if (s < 0) {
        perror("socket");
        return 1;
    }
    struct sockaddr_un a;
    memset(&a, 0, sizeof(a));
    a.sun_family = AF_UNIX;
    snprintf(a.sun_path, sizeof(a.sun_path), "%s", argv[1]);
    if (bind(s, (struct sockaddr *)&a, sizeof(a)) < 0) {
        perror("bind");
        return 1;
    }
    if (listen(s, 16) < 0) {
        perror("listen");
        return 1;
    }
    fprintf(stderr, "relayd: listening on %s\n", argv[1]);
    for (;;) {
        int c = accept(s, NULL, NULL);
        if (c < 0) continue;
        pthread_t t;
        if (pthread_create(&t, NULL, conn_thread, (void *)(intptr_t)c) == 0) {
            pthread_detach(t);
        } else {
            close(c);
        }
    }
}
