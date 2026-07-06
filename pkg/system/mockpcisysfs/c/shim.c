/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * libpcimocksys.so redirects PCI sysfs lookups to a fake tree under
 * $MOCK_PCI_ROOT. It is a no-op when MOCK_PCI_ROOT is unset.
 */

#define _GNU_SOURCE
#include <dirent.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef PATH_MAX
#define PATH_MAX 4096
#endif

static const char *const k_prefixes[] = {
    "/sys/bus/pci/devices/",
    "/sys/bus/pci/",
    "/sys/devices/pci",
    "/sys/bus/pci/devices",
    "/sys/bus/pci",
    NULL,
};

static const char *root_cached = NULL;
static size_t root_len_cached = 0;
static int disabled_cached = -1;
static pthread_once_t init_once = PTHREAD_ONCE_INIT;

static void init_root(void) {
    const char *root = getenv("MOCK_PCI_ROOT");
    if (!root || root[0] == '\0') {
        disabled_cached = 1;
        return;
    }
    root_cached = root;
    root_len_cached = strlen(root);
    disabled_cached = 0;
}

static int rewrite_path(const char *path, char *out, size_t out_size) {
    if (!path) return 0;
    pthread_once(&init_once, init_root);
    if (disabled_cached) return 0;
    if (path[0] != '/') return 0;

    for (size_t i = 0; k_prefixes[i] != NULL; ++i) {
        const char *p = k_prefixes[i];
        size_t plen = strlen(p);
        if (p[plen - 1] == '/') {
            if (strncmp(path, p, plen) != 0) continue;
        } else {
            if (strncmp(path, p, plen) != 0) continue;
            if (path[plen] != '\0' && path[plen] != '/') continue;
        }
        size_t total = root_len_cached + strlen(path);
        if (total + 1 > out_size) return -1;
        memcpy(out, root_cached, root_len_cached);
        memcpy(out + root_len_cached, path, strlen(path) + 1);
        return 1;
    }
    return 0;
}

#define REAL(name) static __typeof__(name) *real_##name = NULL
#define LOAD_REAL(name)                                                 \
    do {                                                                \
        if (!real_##name) {                                             \
            real_##name = (__typeof__(name) *)dlsym(RTLD_NEXT, #name);  \
        }                                                               \
    } while (0)

REAL(open);
REAL(open64);
REAL(openat);
REAL(openat64);

static mode_t extract_mode(int flags, va_list ap) {
    if ((flags & O_CREAT) || (flags & O_TMPFILE) == O_TMPFILE) {
        return (mode_t)va_arg(ap, unsigned int);
    }
    return 0;
}

int open(const char *path, int flags, ...) {
    LOAD_REAL(open);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_open(rc == 1 ? buf : path, flags, mode);
}

int open64(const char *path, int flags, ...) {
    LOAD_REAL(open64);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_open64(rc == 1 ? buf : path, flags, mode);
}

int openat(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_openat(dirfd, rc == 1 ? buf : path, flags, mode);
}

int openat64(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat64);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_openat64(dirfd, rc == 1 ? buf : path, flags, mode);
}

REAL(opendir);

DIR *opendir(const char *name) {
    LOAD_REAL(opendir);
    char buf[PATH_MAX];
    int rc = rewrite_path(name, buf, sizeof(buf));
    return real_opendir(rc == 1 ? buf : name);
}

REAL(stat);
REAL(stat64);
REAL(lstat);
REAL(lstat64);
REAL(fstatat);
REAL(fstatat64);

int stat(const char *path, struct stat *st) {
    LOAD_REAL(stat);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_stat(rc == 1 ? buf : path, st);
}

int stat64(const char *path, struct stat64 *st) {
    LOAD_REAL(stat64);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_stat64(rc == 1 ? buf : path, st);
}

int lstat(const char *path, struct stat *st) {
    LOAD_REAL(lstat);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_lstat(rc == 1 ? buf : path, st);
}

int lstat64(const char *path, struct stat64 *st) {
    LOAD_REAL(lstat64);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_lstat64(rc == 1 ? buf : path, st);
}

int fstatat(int dirfd, const char *path, struct stat *st, int flags) {
    LOAD_REAL(fstatat);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_fstatat(dirfd, rc == 1 ? buf : path, st, flags);
}

int fstatat64(int dirfd, const char *path, struct stat64 *st, int flags) {
    LOAD_REAL(fstatat64);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_fstatat64(dirfd, rc == 1 ? buf : path, st, flags);
}

int statx(int dirfd, const char *path, int flags, unsigned int mask, struct statx *st) {
    static int (*real)(int, const char *, int, unsigned int, struct statx *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "statx");
    if (!real) {
        errno = ENOSYS;
        return -1;
    }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(dirfd, rc == 1 ? buf : path, flags, mask, st);
}

REAL(access);
REAL(faccessat);
REAL(readlink);
REAL(readlinkat);

int access(const char *path, int mode) {
    LOAD_REAL(access);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_access(rc == 1 ? buf : path, mode);
}

int faccessat(int dirfd, const char *path, int mode, int flags) {
    LOAD_REAL(faccessat);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_faccessat(dirfd, rc == 1 ? buf : path, mode, flags);
}

ssize_t readlink(const char *path, char *out, size_t out_size) {
    LOAD_REAL(readlink);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_readlink(rc == 1 ? buf : path, out, out_size);
}

ssize_t readlinkat(int dirfd, const char *path, char *out, size_t out_size) {
    LOAD_REAL(readlinkat);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_readlinkat(dirfd, rc == 1 ? buf : path, out, out_size);
}
