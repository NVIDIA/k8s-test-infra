/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * libibmocksys.so -- LD_PRELOAD shim that redirects InfiniBand sysfs/dev
 * lookups to a fake tree under $MOCK_IB_ROOT (default /var/lib/nvml-mock).
 *
 * Real `ibstat`, `ibstatus`, `iblinkinfo`, `ibv_devinfo`, ... read from
 * /sys/class/infiniband*, /sys/class/infiniband_mad*,
 * /sys/class/infiniband_verbs*, /sys/class/infiniband_cm*, and
 * /dev/infiniband*. We hook the libc syscall wrappers, and for any path
 * starting with one of those prefixes we splice $MOCK_IB_ROOT in front and
 * forward to the next libc.
 *
 * Design notes:
 *   - dlsym(RTLD_NEXT, ...) is resolved lazily on first call and cached.
 *   - Path rewriting uses a thread-local fixed buffer (PATH_MAX) -- no
 *     allocations on the hot path.
 *   - When MOCK_IB_DISABLE=1 (or root is unset and "/var/lib/nvml-mock" does
 *     not exist) the shim becomes a true no-op.
 *   - Variadic open()/openat() are handled with the `mode_t` extraction
 *     pattern recommended by glibc's headers (only valid when O_CREAT or
 *     O_TMPFILE is set in flags).
 *
 * Targeted at glibc 2.36+ (Debian bookworm). Older `__xstat` family symbols
 * are also intercepted for safety; if libc does not export them the linker
 * resolves to NULL and our fallback path is taken.
 */

#define _GNU_SOURCE
#include <dlfcn.h>
#include <dirent.h>
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
    "/sys/class/infiniband/",
    "/sys/class/infiniband_mad/",
    "/sys/class/infiniband_verbs/",
    "/sys/class/infiniband_cm/",
    "/dev/infiniband/",
    /* Bare-directory forms (no trailing slash) for opendir/stat. */
    "/sys/class/infiniband",
    "/sys/class/infiniband_mad",
    "/sys/class/infiniband_verbs",
    "/sys/class/infiniband_cm",
    "/dev/infiniband",
    NULL,
};

static const char *root_cached = NULL;
static size_t root_len_cached = 0;
static int disabled_cached = -1;
static pthread_once_t init_once = PTHREAD_ONCE_INIT;

static void init_root(void) {
    const char *disable = getenv("MOCK_IB_DISABLE");
    if (disable && disable[0] != '\0' && disable[0] != '0') {
        disabled_cached = 1;
        return;
    }
    const char *root = getenv("MOCK_IB_ROOT");
    if (!root || root[0] == '\0') {
        root = "/var/lib/nvml-mock";
    }
    root_cached = root;
    root_len_cached = strlen(root);
    disabled_cached = 0;
}

/*
 * Returns 1 if `path` starts with any redirected prefix and writes the
 * rewritten path into `out` (size `out_size`). Returns 0 otherwise (and
 * leaves `out` untouched). Returns -1 on overflow (caller should fall back
 * to the original path; errno is preserved).
 */
static int rewrite_path(const char *path, char *out, size_t out_size) {
    if (!path) return 0;
    pthread_once(&init_once, init_root);
    if (disabled_cached) return 0;
    if (path[0] != '/') return 0; /* only absolute paths */

    for (size_t i = 0; k_prefixes[i] != NULL; ++i) {
        const char *p = k_prefixes[i];
        size_t plen = strlen(p);
        /* Match if path == prefix, or path starts with prefix+'/', or
         * prefix ends in '/' and path starts with prefix. */
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
#define LOAD_REAL(name)                                          \
    do {                                                         \
        if (!real_##name) {                                      \
            real_##name = (__typeof__(name) *)dlsym(RTLD_NEXT, #name); \
        }                                                        \
    } while (0)

/* ---------- open / openat ---------- */

REAL(open);
REAL(open64);
REAL(openat);
REAL(openat64);

static int extract_mode(int flags, va_list ap) {
    if ((flags & O_CREAT) || (flags & __O_TMPFILE)) {
        return va_arg(ap, int);
    }
    return 0;
}

int open(const char *path, int flags, ...) {
    LOAD_REAL(open);
    char buf[PATH_MAX];
    int mode = 0;
    va_list ap;
    va_start(ap, flags);
    mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    if (rc == 1) {
        return real_open(buf, flags, mode);
    }
    return real_open(path, flags, mode);
}

int open64(const char *path, int flags, ...) {
    LOAD_REAL(open64);
    char buf[PATH_MAX];
    int mode = 0;
    va_list ap;
    va_start(ap, flags);
    mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    if (rc == 1) {
        return real_open64(buf, flags, mode);
    }
    return real_open64(path, flags, mode);
}

int openat(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat);
    char buf[PATH_MAX];
    int mode = 0;
    va_list ap;
    va_start(ap, flags);
    mode = extract_mode(flags, ap);
    va_end(ap);
    /* Only rewrite absolute paths; relative paths use dirfd which already
     * points at a redirected directory if appropriate. */
    int rc = rewrite_path(path, buf, sizeof(buf));
    if (rc == 1) {
        return real_openat(dirfd, buf, flags, mode);
    }
    return real_openat(dirfd, path, flags, mode);
}

int openat64(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat64);
    char buf[PATH_MAX];
    int mode = 0;
    va_list ap;
    va_start(ap, flags);
    mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    if (rc == 1) {
        return real_openat64(dirfd, buf, flags, mode);
    }
    return real_openat64(dirfd, path, flags, mode);
}

/* ---------- fopen ---------- */

REAL(fopen);
REAL(fopen64);

FILE *fopen(const char *path, const char *mode) {
    LOAD_REAL(fopen);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_fopen(rc == 1 ? buf : path, mode);
}

FILE *fopen64(const char *path, const char *mode) {
    LOAD_REAL(fopen64);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_fopen64(rc == 1 ? buf : path, mode);
}

/* ---------- opendir / scandir ---------- */

REAL(opendir);
REAL(scandir);
REAL(scandir64);

DIR *opendir(const char *name) {
    LOAD_REAL(opendir);
    char buf[PATH_MAX];
    int rc = rewrite_path(name, buf, sizeof(buf));
    return real_opendir(rc == 1 ? buf : name);
}

/* glibc's scandir() internally uses a hidden __opendir symbol that bypasses
 * our opendir() hook -- libibumad uses scandir() to enumerate HCAs and ports,
 * so we must intercept here too. */
int scandir(const char *path, struct dirent ***namelist,
            int (*filter)(const struct dirent *),
            int (*compar)(const struct dirent **, const struct dirent **)) {
    LOAD_REAL(scandir);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_scandir(rc == 1 ? buf : path, namelist, filter, compar);
}

int scandir64(const char *path, struct dirent64 ***namelist,
              int (*filter)(const struct dirent64 *),
              int (*compar)(const struct dirent64 **, const struct dirent64 **)) {
    LOAD_REAL(scandir64);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_scandir64(rc == 1 ? buf : path, namelist, filter, compar);
}

/* ---------- stat / lstat / fstatat ---------- */

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

/* statx(2) -- used directly by modern coreutils (ls, stat, ...) bypassing
 * the classic stat() glibc wrapper. Exported by glibc 2.28+. */
int statx(int dirfd, const char *path, int flags, unsigned int mask,
          struct statx *st) {
    static int (*real)(int, const char *, int, unsigned int, struct statx *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "statx");
    if (!real) { errno = ENOSYS; return -1; }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(dirfd, rc == 1 ? buf : path, flags, mask, st);
}

/* Legacy __xstat family (glibc < 2.33). On modern systems these may not be
 * exported by libc.so.6; in that case dlsym returns NULL and we just don't
 * register the hook (the binary won't call us either). */

int __xstat(int ver, const char *path, struct stat *st) {
    static int (*real)(int, const char *, struct stat *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "__xstat");
    if (!real) { errno = ENOSYS; return -1; }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(ver, rc == 1 ? buf : path, st);
}

int __xstat64(int ver, const char *path, struct stat64 *st) {
    static int (*real)(int, const char *, struct stat64 *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "__xstat64");
    if (!real) { errno = ENOSYS; return -1; }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(ver, rc == 1 ? buf : path, st);
}

int __lxstat(int ver, const char *path, struct stat *st) {
    static int (*real)(int, const char *, struct stat *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "__lxstat");
    if (!real) { errno = ENOSYS; return -1; }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(ver, rc == 1 ? buf : path, st);
}

int __lxstat64(int ver, const char *path, struct stat64 *st) {
    static int (*real)(int, const char *, struct stat64 *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "__lxstat64");
    if (!real) { errno = ENOSYS; return -1; }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(ver, rc == 1 ? buf : path, st);
}

int __fxstatat(int ver, int dirfd, const char *path, struct stat *st, int flag) {
    static int (*real)(int, int, const char *, struct stat *, int) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "__fxstatat");
    if (!real) { errno = ENOSYS; return -1; }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(ver, dirfd, rc == 1 ? buf : path, st, flag);
}

int __fxstatat64(int ver, int dirfd, const char *path, struct stat64 *st, int flag) {
    static int (*real)(int, int, const char *, struct stat64 *, int) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "__fxstatat64");
    if (!real) { errno = ENOSYS; return -1; }
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real(ver, dirfd, rc == 1 ? buf : path, st, flag);
}

/* ---------- chdir ---------- */

REAL(chdir);

int chdir(const char *path) {
    LOAD_REAL(chdir);
    char buf[PATH_MAX];
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_chdir(rc == 1 ? buf : path);
}

/* ---------- access / readlink ---------- */

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
