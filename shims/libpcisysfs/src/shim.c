/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * libpcisysfs.so redirects PCI sysfs lookups to a fake tree under
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


/* ── Infrastructure: prefix table, path rewriting, helper macros ──────────── */

/*
 * Both trailing-slash and bare forms are required: callers that open an exact
 * directory path (e.g. opendir("/sys/bus/pci")) use the bare form; callers
 * that open a file inside (e.g. open("/sys/bus/pci/devices/0000:07:00.0/..."))
 * use the slash form. Listing the longer prefixes first means the most specific
 * match wins when two entries could both apply.
 *
 * "/sys/devices/pci" has no trailing slash because the kernel names host-bridge
 * directories "pciDDDD:BB" — the hex domain immediately follows "pci" with no
 * separator, so a slash or '\0' boundary check (see rewrite_path) would reject
 * every real path.
 */
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
        if (strncmp(path, p, plen) != 0) continue;
        /* For bare prefixes, require a word boundary ('\0' or '/') to
         * avoid matching "/sys/bus/pcifoo" when the prefix is "/sys/bus/pci".
         * Exception: "/sys/devices/pci" is immediately followed by hex digits
         * (kernel naming: pci0000:00), so no boundary can be required there. */
        if (p[plen - 1] != '/' && strcmp(p, "/sys/devices/pci") != 0) {
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

/*
 * RESOLVE_OR_FAIL runs rewrite_path and yields the path to hand to the real
 * syscall: the rewritten buffer on a match, the original path otherwise. On
 * overflow (rewrite_path returns -1) it sets errno = ENAMETOOLONG and makes
 * the caller return `failret` instead of silently falling back to the real
 * host path — the same silent-escape class the prefix bug caused. The path
 * argument is always a plain parameter, so evaluating it twice is safe.
 */
#define RESOLVE_OR_FAIL(pathexpr, buf, failret)                         \
    ({                                                                  \
        int _rc = rewrite_path((pathexpr), (buf), sizeof(buf));         \
        if (_rc < 0) {                                                  \
            errno = ENAMETOOLONG;                                       \
            return (failret);                                           \
        }                                                               \
        _rc == 1 ? (const char *)(buf) : (pathexpr);                    \
    })

/*
 * REAL/LOAD_REAL: lazy-load a real symbol via dlsym(RTLD_NEXT).
 * No null check after LOAD_REAL because every intercepted symbol (open, stat,
 * fopen, …) is guaranteed present in glibc. statx is the sole exception — it
 * may be absent on old kernels — and is handled with its own explicit null
 * check below rather than through these macros.
 * The lazy-write pattern (assign only when NULL) is intentionally racy: two
 * threads may both call dlsym and assign the same pointer, but pointer-width
 * writes are atomic on every supported arch and dlsym always returns the same
 * value for a given symbol, so the race is harmless.
 */
#define REAL(name) static __typeof__(name) *real_##name = NULL
#define LOAD_REAL(name)                                                 \
    do {                                                                \
        if (!real_##name) {                                             \
            real_##name = (__typeof__(name) *)dlsym(RTLD_NEXT, #name);  \
        }                                                               \
    } while (0)


/* ── File openers: open / openat (varargs forms) ──────────────────────────── */

/*
 * extract_mode pulls the optional `mode_t` argument from a varargs open call.
 * POSIX requires mode only when O_CREAT is set; glibc also requires it for
 * O_TMPFILE (a two-flag value: __O_TMPFILE | O_DIRECTORY), so a plain
 * `flags & O_CREAT` check is insufficient.
 */
static mode_t extract_mode(int flags, va_list ap) {
    if ((flags & O_CREAT) || (flags & O_TMPFILE) == O_TMPFILE) {
        return (mode_t)va_arg(ap, unsigned int);
    }
    return 0;
}

REAL(open);
REAL(open64);
REAL(openat);
REAL(openat64);

int open(const char *path, int flags, ...) {
    LOAD_REAL(open);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_open(target, flags, mode);
}

int open64(const char *path, int flags, ...) {
    LOAD_REAL(open64);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_open64(target, flags, mode);
}

int openat(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_openat(dirfd, target, flags, mode);
}

int openat64(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat64);
    char buf[PATH_MAX];
    va_list ap;
    va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_openat64(dirfd, target, flags, mode);
}

/*
 * _FORTIFY_SOURCE variants. When a caller invokes open()/openat() with a
 * *non-constant* flags argument (e.g. libpci opening a PCI `config` file with
 * an intent-derived O_RDONLY/O_RDWR), glibc's fortify headers rewrite the call
 * to __open_2 / __openat_2 (and the 64-bit forms) instead of open / openat.
 * These are distinct exported symbols, so interposing open/openat alone leaves
 * such calls hitting the real host path — the exact reason `lspci` printed
 * "pcilib: Cannot open .../config" even with the redirector loaded. The
 * fortified forms never carry a mode argument (they abort if O_CREAT is set
 * without one), so they take no varargs. Declare them ourselves: fcntl.h only
 * exposes them under _FORTIFY_SOURCE, which this shim is not built with.
 *
 * These functions cannot use REAL()/LOAD_REAL() because the fortified symbols
 * have non-standard signatures that __typeof__ cannot deduce from the public
 * header declarations. Each wrapper carries its own inline static pointer.
 */
extern int __open_2(const char *path, int flags);
extern int __open64_2(const char *path, int flags);
extern int __openat_2(int dirfd, const char *path, int flags);
extern int __openat64_2(int dirfd, const char *path, int flags);

int __open_2(const char *path, int flags) {
    static int (*real)(const char *, int) = NULL;
    if (!real) real = (int (*)(const char *, int))dlsym(RTLD_NEXT, "__open_2");
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real(target, flags);
}

int __open64_2(const char *path, int flags) {
    static int (*real)(const char *, int) = NULL;
    if (!real) real = (int (*)(const char *, int))dlsym(RTLD_NEXT, "__open64_2");
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real(target, flags);
}

int __openat_2(int dirfd, const char *path, int flags) {
    static int (*real)(int, const char *, int) = NULL;
    if (!real) real = (int (*)(int, const char *, int))dlsym(RTLD_NEXT, "__openat_2");
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real(dirfd, target, flags);
}

int __openat64_2(int dirfd, const char *path, int flags) {
    static int (*real)(int, const char *, int) = NULL;
    if (!real) real = (int (*)(int, const char *, int))dlsym(RTLD_NEXT, "__openat64_2");
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real(dirfd, target, flags);
}


/* ── Directory openers ────────────────────────────────────────────────────── */

REAL(opendir);

DIR *opendir(const char *name) {
    LOAD_REAL(opendir);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(name, buf, NULL);
    return real_opendir(target);
}


/* ── stdio openers ────────────────────────────────────────────────────────── */

/*
 * libpci reads a device's `resource` file via fopen(), so a missing hook here
 * leaves `lspci -v` reading the real host path. Only paths under the PCI sysfs
 * prefixes are rewritten; every other fopen (pci.ids, /proc, …) passes through.
 */
REAL(fopen);
REAL(fopen64);

FILE *fopen(const char *path, const char *mode) {
    LOAD_REAL(fopen);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, NULL);
    return real_fopen(target, mode);
}

FILE *fopen64(const char *path, const char *mode) {
    LOAD_REAL(fopen64);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, NULL);
    return real_fopen64(target, mode);
}


/* ── Stat family ──────────────────────────────────────────────────────────── */

REAL(stat);
REAL(stat64);
REAL(lstat);
REAL(lstat64);
REAL(fstatat);
REAL(fstatat64);

int stat(const char *path, struct stat *st) {
    LOAD_REAL(stat);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_stat(target, st);
}

int stat64(const char *path, struct stat64 *st) {
    LOAD_REAL(stat64);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_stat64(target, st);
}

int lstat(const char *path, struct stat *st) {
    LOAD_REAL(lstat);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_lstat(target, st);
}

int lstat64(const char *path, struct stat64 *st) {
    LOAD_REAL(lstat64);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_lstat64(target, st);
}

int fstatat(int dirfd, const char *path, struct stat *st, int flags) {
    LOAD_REAL(fstatat);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_fstatat(dirfd, target, st, flags);
}

int fstatat64(int dirfd, const char *path, struct stat64 *st, int flags) {
    LOAD_REAL(fstatat64);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_fstatat64(dirfd, target, st, flags);
}

/*
 * statx is not routed through REAL/LOAD_REAL because __typeof__(statx) expands
 * to a kernel-internal type (struct statx) that is not available as a plain
 * C type in all include configurations. An explicit function-pointer typedef
 * avoids the issue. The null check is mandatory: statx was added in Linux 4.11
 * and may genuinely be absent on older kernels.
 */
int statx(int dirfd, const char *path, int flags, unsigned int mask, struct statx *st) {
    static int (*real)(int, const char *, int, unsigned int, struct statx *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "statx");
    if (!real) {
        errno = ENOSYS;
        return -1;
    }
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real(dirfd, target, flags, mask, st);
}


/* ── Access and link resolution ───────────────────────────────────────────── */

REAL(access);
REAL(faccessat);

int access(const char *path, int mode) {
    LOAD_REAL(access);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_access(target, mode);
}

int faccessat(int dirfd, const char *path, int mode, int flags) {
    LOAD_REAL(faccessat);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_faccessat(dirfd, target, mode, flags);
}

/*
 * readlink / readlinkat: the DRA device-plugin resolves NUMA affinity by
 * calling readlink("/sys/bus/pci/devices/<bdf>") and parsing the returned
 * relative target ("../../../devices/pci0000:00/<bdf>") to extract the root-
 * complex ID. Without intercepting readlink, the symlink resolves into the
 * real host /sys tree and the root-complex lookup finds actual hardware
 * instead of the mock topology.
 */
REAL(readlink);
REAL(readlinkat);

ssize_t readlink(const char *path, char *out, size_t out_size) {
    LOAD_REAL(readlink);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_readlink(target, out, out_size);
}

ssize_t readlinkat(int dirfd, const char *path, char *out, size_t out_size) {
    LOAD_REAL(readlinkat);
    char buf[PATH_MAX];
    const char *target = RESOLVE_OR_FAIL(path, buf, -1);
    return real_readlinkat(dirfd, target, out, out_size);
}
