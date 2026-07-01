# Node-Wide Mock GPU Injection (Every Pod) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the mock GPU environment ambient on every Kind node so any pod (no GPU request, no annotations) can run `nvidia-smi`, `ibnetdiscover`, `ibstat`, etc., via a mutating admission webhook that overlays the DaemonSet-materialized host tree at `/opt/nvml-mock` and injects env — with no containerd/CDI or `runtimeClassName` changes.

**Architecture:** The DaemonSet keeps materializing host artifacts and additionally stages the IB discovery tools + their shared libs + the `LD_PRELOAD` shims into the on-host driver root, and moves the mock-ib socket to a host path. A new `nvml-mock-injector` webhook rewrites every pod's spec to add one read-only hostPath overlay volume + per-container mount + env (`PATH`/`LD_LIBRARY_PATH`/`LD_PRELOAD`/`MOCK_*`). It is fail-open, self-namespace-excluded, idempotent, honors an opt-out annotation, and adds privileged `/dev/nvidia*` device nodes only for pods that opt in.

**Tech Stack:** Go 1.26 (`k8s.io/api/admission/v1`, `k8s.io/api/core/v1`), C (`LD_PRELOAD` shim mirroring `pkg/network/mockib/c/shim.c`), shell (`setup.sh`), Helm 3 (self-signed CA via `genSignedCert`), Kind, testify/Ginkgo.

**Design spec:** [`docs/designs/2026-07-01-node-wide-mock-injection-every-pod-design.md`](../2026-07-01-node-wide-mock-injection-every-pod-design.md)

---

## File Map

| File | Responsibility |
|------|----------------|
| `pkg/system/mockpcisysfs/c/shim.c` | **New** — PCI sysfs `LD_PRELOAD` redirect (`libpcimocksys.so`) |
| `pkg/system/mockpcisysfs/Makefile` | **New** — build `libpcimocksys.so` |
| `pkg/system/mockpcisysfs/shim/shim_test.go` | **New** — integration test for PCI redirect |
| `deployments/nvml-mock/Dockerfile` | Build + copy `libpcimocksys.so` |
| `pkg/network/mockib/daemon/env.go` | Default socket → host path |
| `cmd/mock-ib/main.go` | `-socket` flag default → host path |
| `deployments/nvml-mock/scripts/start-mock-ib.sh` | `SOCKET` default → host path |
| `deployments/nvml-mock/scripts/setup.sh` | Stage IB tools + libs + shims into driver root; ensure `run/` dir |
| `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml` | Set `MOCK_IB_PING_SOCKET` to host path |
| `tests/e2e/validate-ibnetdiscover.sh`, `validate-sminfo.sh`, `validate-ibping.sh` | Socket default → host path |
| `cmd/nvml-mock-injector/inject/inject.go` | **New** — pure mutation logic (returns JSON patch ops) |
| `cmd/nvml-mock-injector/inject/inject_test.go` | **New** — unit tests |
| `cmd/nvml-mock-injector/main.go` | **New** — TLS admission HTTP server |
| `deployments/nvml-mock/helm/nvml-mock/values.yaml` | `injector:` block |
| `deployments/nvml-mock/helm/nvml-mock/templates/injector-*.yaml` | **New** — Deployment, Service, webhook, RBAC, TLS secret |
| `deployments/nvml-mock/helm/nvml-mock/tests/injector_test.yaml` | **New** — Helm unittest |
| `tests/e2e/injection-test-pods.yaml` | **New** — ambient / opt-out / device pods |
| `tests/e2e/validate-injection.sh` | **New** — E2E validation |
| `deployments/nvml-mock/helm/nvml-mock/README.md`, `docs/quickstart.md` | Docs |

---

## Task 1: PCI Sysfs `LD_PRELOAD` Shim (`libpcimocksys.so`)

**Files:**
- Create: `pkg/system/mockpcisysfs/c/shim.c`
- Create: `pkg/system/mockpcisysfs/Makefile`
- Create: `pkg/system/mockpcisysfs/shim/shim_test.go`
- Modify: `deployments/nvml-mock/Dockerfile`

- [ ] **Step 1: Write the failing integration test**

Create `pkg/system/mockpcisysfs/shim/shim_test.go`:

```go
//go:build integration

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package shim_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
	"github.com/stretchr/testify/require"
)

func TestReadlink_PCIRedirect(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires linux")
	}
	wd, err := os.Getwd()
	require.NoError(t, err)
	shim := filepath.Join(wd, "..", "libpcimocksys.so")
	if _, statErr := os.Stat(shim); statErr != nil {
		t.Skipf("shim not built: %v (run make -C pkg/system/mockpcisysfs)", statErr)
	}

	root := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID:       "pci0000:00",
			NUMANode: 0,
			Devices:  []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, render.Render(render.Options{Topology: topo, Output: root}))

	cmd := exec.Command("readlink", "/sys/bus/pci/devices/0000:07:00.0")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "readlink failed: %s", out)
	require.Contains(t, string(out), "pci0000:00/0000:07:00.0")
}
```

- [ ] **Step 2: Run test to verify it skips (shim not built yet)**

Run: `go test -tags=integration ./pkg/system/mockpcisysfs/shim/... -v`
Expected: SKIP (`shim not built`).

- [ ] **Step 3: Implement the shim**

Create `pkg/system/mockpcisysfs/c/shim.c`. This mirrors `pkg/network/mockib/c/shim.c`
exactly except for (a) the prefix list, and (b) activation reads `MOCK_PCI_ROOT`
instead of `MOCK_IB`/`MOCK_IB_ROOT`. The rewrite rule is identical: splice the
root in front of the absolute path (so `/sys/bus/pci/...` becomes
`$MOCK_PCI_ROOT/sys/bus/pci/...`, and the rendered tree lives at
`$MOCK_PCI_ROOT/sys/...`).

```c
/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * libpcimocksys.so -- LD_PRELOAD shim that redirects PCI sysfs lookups to a
 * fake tree under $MOCK_PCI_ROOT. Real topology consumers (readlink on
 * /sys/bus/pci/devices/<bdf>, reads of numa_node) are rewritten by splicing
 * $MOCK_PCI_ROOT in front of the absolute path and forwarding to the next
 * libc. A no-op when $MOCK_PCI_ROOT is unset/empty. Mirrors
 * pkg/network/mockib/c/shim.c; see that file for the design notes on
 * dlsym(RTLD_NEXT), the thread-local buffer, and the variadic open() mode
 * extraction pattern.
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
    "/sys/bus/pci/devices/",
    "/sys/bus/pci/",
    "/sys/devices/pci",
    /* Bare-directory forms (no trailing slash) for opendir/stat. */
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
#define LOAD_REAL(name)                                              \
    do {                                                             \
        if (!real_##name) {                                          \
            real_##name = (__typeof__(name) *)dlsym(RTLD_NEXT, #name); \
        }                                                            \
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
    va_list ap; va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_open(rc == 1 ? buf : path, flags, mode);
}

int open64(const char *path, int flags, ...) {
    LOAD_REAL(open64);
    char buf[PATH_MAX];
    va_list ap; va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_open64(rc == 1 ? buf : path, flags, mode);
}

int openat(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat);
    char buf[PATH_MAX];
    va_list ap; va_start(ap, flags);
    mode_t mode = extract_mode(flags, ap);
    va_end(ap);
    int rc = rewrite_path(path, buf, sizeof(buf));
    return real_openat(dirfd, rc == 1 ? buf : path, flags, mode);
}

int openat64(int dirfd, const char *path, int flags, ...) {
    LOAD_REAL(openat64);
    char buf[PATH_MAX];
    va_list ap; va_start(ap, flags);
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

int statx(int dirfd, const char *path, int flags, unsigned int mask,
          struct statx *st) {
    static int (*real)(int, const char *, int, unsigned int, struct statx *) = NULL;
    if (!real) real = dlsym(RTLD_NEXT, "statx");
    if (!real) { errno = ENOSYS; return -1; }
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
```

Create `pkg/system/mockpcisysfs/Makefile`:

```makefile
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

CC      ?= gcc
CFLAGS  ?= -O2 -Wall -Wextra -fPIC -fvisibility=default
LDFLAGS ?= -shared -ldl

LIB_NAME    := libpcimocksys.so
LIB_SONAME  := $(LIB_NAME).1
LIB_VERSION ?= 1.0.0
LIB_REAL    := $(LIB_NAME).$(LIB_VERSION)

.PHONY: all clean
all: $(LIB_NAME)

$(LIB_REAL): c/shim.c
	$(CC) $(CFLAGS) $(LDFLAGS) -Wl,-soname,$(LIB_SONAME) -o $@ $<

$(LIB_NAME): $(LIB_REAL)
	ln -sf $(LIB_REAL) $(LIB_SONAME)
	ln -sf $(LIB_SONAME) $(LIB_NAME)

clean:
	rm -f $(LIB_NAME)*
```

- [ ] **Step 4: Build and run the integration test**

Run:
```bash
make -C pkg/system/mockpcisysfs
go test -tags=integration ./pkg/system/mockpcisysfs/shim/... -v
```
Expected: PASS.

- [ ] **Step 5: Wire the shim into the Dockerfile**

In `deployments/nvml-mock/Dockerfile` builder stage, after the `pkg/network/mockib` build (line 33):

```dockerfile
RUN cd pkg/system/mockpcisysfs && make clean && make
```

In the runtime stage, after the `libibmockverbs` copy (line 50):

```dockerfile
COPY --from=builder /src/pkg/system/mockpcisysfs/libpcimocksys.so.* /usr/local/lib/
```

- [ ] **Step 6: Commit**

```bash
git add pkg/system/mockpcisysfs/c/shim.c pkg/system/mockpcisysfs/Makefile \
  pkg/system/mockpcisysfs/shim/shim_test.go deployments/nvml-mock/Dockerfile
git commit -s -m "feat(mockpcisysfs): add libpcimocksys LD_PRELOAD shim"
```

---

## Task 2: Host-Side Staging + Host mock-ib Socket

The overlay mount exposes `/var/lib/nvml-mock` at `/opt/nvml-mock`. For arbitrary
pods to run the IB tools, the tools **and their shared-library dependencies** and
the `LD_PRELOAD` shims must live under the on-host driver root (today they exist
only inside the image at `/usr/sbin` and `/usr/local/lib`). The mock-ib socket
must also move onto that shared tree.

**Files:**
- Modify: `pkg/network/mockib/daemon/env.go`
- Modify: `cmd/mock-ib/main.go`
- Modify: `deployments/nvml-mock/scripts/start-mock-ib.sh`
- Modify: `deployments/nvml-mock/scripts/setup.sh`
- Modify: `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml`
- Modify: `tests/e2e/validate-ibnetdiscover.sh`, `validate-sminfo.sh`, `validate-ibping.sh`

- [ ] **Step 1: Change the default socket path to the host tree**

In `pkg/network/mockib/daemon/env.go`, locate the `EnvMockIBPingSocket` constant
block and add a default constant next to it:

```go
// EnvMockIBPingSocket names the env var carrying the shim<->daemon socket path.
EnvMockIBPingSocket = "MOCK_IB_PING_SOCKET"

// DefaultMockIBPingSocket lives under the shared /var/lib/nvml-mock tree (not
// pod-local /run) so injected consumer pods reach the DaemonSet's mock-ib
// daemon through the overlay mount.
DefaultMockIBPingSocket = "/var/lib/nvml-mock/run/mock-ib.sock"
```

In `cmd/mock-ib/main.go` line 20, replace the literal default:

```go
socket := flag.String("socket", daemon.EnvOr(daemon.EnvMockIBPingSocket, daemon.DefaultMockIBPingSocket), "Unix socket path")
```

In `deployments/nvml-mock/scripts/start-mock-ib.sh` line 12:

```sh
SOCKET="${MOCK_IB_PING_SOCKET:-/var/lib/nvml-mock/run/mock-ib.sock}"
```

> Note: the compiled-in fallback in `pkg/network/mockib/c/umad_shim.c`
> (`MOCK_DEFAULT_SOCK "/run/mock-ib.sock"`) is only used when
> `MOCK_IB_PING_SOCKET` is unset. Both the DaemonSet (Step 4) and the injector
> (Task 3) always set the env var, so the C default is never hit in practice and
> is left unchanged to avoid a shim rebuild in this task.

- [ ] **Step 2: Stage IB tools + shims into the driver root in `setup.sh`**

In `deployments/nvml-mock/scripts/setup.sh`, after the CUDA lib copy block (after
line 57, before `# 3. Create char device nodes`), add:

```sh
# 2c. Stage the InfiniBand discovery tools, their shared-library dependencies,
#     and the LD_PRELOAD shims into the driver root so the node-wide injector's
#     overlay mount (/opt/nvml-mock) exposes them to arbitrary pods. Without
#     this, only processes inside THIS image (which ships infiniband-diags +
#     rdma-core) could run ibnetdiscover/ibstat. glibc itself is intentionally
#     NOT staged — injected pods use their own loader, so tools run only in pods
#     whose glibc is >= this image's (bookworm 2.36); older images should use
#     the injector opt-out annotation.
mkdir -p "$DRIVER_ROOT/usr/local/lib"
cp -a /usr/local/lib/libibmockumad.so.* /usr/local/lib/libibmockverbs.so.* \
      /usr/local/lib/libibmocksys.so.*  /usr/local/lib/libpcimocksys.so.* \
      "$DRIVER_ROOT/usr/local/lib/" 2>/dev/null || true

# IB discovery tools ship in /usr/sbin (infiniband-diags). Stage each tool plus
# every non-glibc shared object it links, resolved via ldd, into the driver
# root. LD_LIBRARY_PATH (set by the injector) points at usr/lib64, so copy the
# resolved deps there.
for tool in ibnetdiscover ibstat ibstatus iblinkinfo sminfo ibping; do
  src=$(command -v "$tool" 2>/dev/null || echo "/usr/sbin/$tool")
  [ -x "$src" ] || continue
  cp -a "$src" "$DRIVER_ROOT/usr/bin/$tool"
  # Copy dependent libs (skip the dynamic loader and libc/libpthread/libm/libdl,
  # which the target pod already provides via its own glibc).
  ldd "$src" 2>/dev/null | awk '/=>/ {print $3} !/=>/ && /^\// {print $1}' | \
  while read -r lib; do
    case "$lib" in
      ""|*/ld-linux*|*/libc.so*|*/libpthread.so*|*/libm.so*|*/libdl.so*|*/librt.so*) continue ;;
    esac
    [ -e "$lib" ] && cp -a "$lib" "$DRIVER_ROOT/usr/lib64/" 2>/dev/null || true
  done
done
```

Also ensure the host `run/` directory exists. After the existing
`mkdir -p "$DEV_ROOT" "$CONFIG_DIR"` (line 30), add:

```sh
mkdir -p "$HOST/run"
```

- [ ] **Step 3: Point the DaemonSet at the host socket**

In `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml`, in the env
list (after the `MOCK_IB_PING_PORT` entry near line 110), add:

```yaml
            - name: MOCK_IB_PING_SOCKET
              value: /var/lib/nvml-mock/run/mock-ib.sock
```

No new volume is needed — the `host-nvml-mock` hostPath already mounts
`/var/lib/nvml-mock`, so `/var/lib/nvml-mock/run/mock-ib.sock` is covered.

- [ ] **Step 4: Update E2E socket defaults**

In each of `tests/e2e/validate-ibnetdiscover.sh` (line 29), `validate-sminfo.sh`
(line 28), and `validate-ibping.sh` (line 36), change the default:

```bash
MOCK_IBPING_SOCKET="${MOCK_IB_PING_SOCKET:-/var/lib/nvml-mock/run/mock-ib.sock}"
```

- [ ] **Step 5: Run unit tests**

Run: `go test -race $(go list ./pkg/network/mockib/... ./cmd/mock-ib/... | grep -v vendor)`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/network/mockib/daemon/env.go cmd/mock-ib/main.go \
  deployments/nvml-mock/scripts/start-mock-ib.sh \
  deployments/nvml-mock/scripts/setup.sh \
  deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml \
  tests/e2e/validate-ibnetdiscover.sh tests/e2e/validate-sminfo.sh tests/e2e/validate-ibping.sh
git commit -s -m "feat(nvml-mock): stage IB tools + shims on host, move mock-ib socket to host path"
```

---

## Task 3: Injector Mutation Logic (`cmd/nvml-mock-injector/inject`)

Pure, unit-testable mutation: given a pod and config, return the RFC 6902 JSON
patch operations. Strategy: compute the final `env`/`volumeMounts` per container
in Go and emit a whole-field `add` (field absent) or `replace` (field present)
per path — this avoids brittle per-element array patches and cleanly handles the
`PATH`/`LD_PRELOAD` merge.

**Files:**
- Create: `cmd/nvml-mock-injector/inject/inject.go`
- Create: `cmd/nvml-mock-injector/inject/inject_test.go`

- [ ] **Step 1: Write the failing unit tests**

Create `cmd/nvml-mock-injector/inject/inject_test.go`:

```go
// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func testConfig() Config {
	return Config{
		HostPath:          "/var/lib/nvml-mock",
		MountPath:         "/opt/nvml-mock",
		EnableIB:          true,
		EnablePCI:         true,
		OptOutAnnotation:  "nvml-mock.nvidia.com/inject",
		DevicesAnnotation: "nvml-mock.nvidia.com/devices",
		GPUCount:          2,
	}
}

func opsByPath(ops []PatchOp) map[string]PatchOp {
	m := map[string]PatchOp{}
	for _, o := range ops {
		m[o.Path] = o
	}
	return m
}

func plainPod() *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
	}
}

func TestMutate_PlainPod_AddsOverlay(t *testing.T) {
	ops, err := Mutate(plainPod(), testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)

	// Overlay volume added (spec.volumes absent -> add whole array).
	vol, ok := by["/spec/volumes"]
	require.True(t, ok)
	require.Equal(t, "add", vol.Op)

	// Container 0 gets a volumeMount and env (both absent -> add).
	_, ok = by["/spec/containers/0/volumeMounts"]
	require.True(t, ok)
	envOp, ok := by["/spec/containers/0/env"]
	require.True(t, ok)
	require.Equal(t, "add", envOp.Op)

	// Idempotency/audit annotation recorded.
	_, ok = by["/metadata/annotations"]
	require.True(t, ok)
}

func TestMutate_MergesExistingEnv(t *testing.T) {
	pod := plainPod()
	pod.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "PATH", Value: "/custom/bin"},
		{Name: "LD_PRELOAD", Value: "/x/pre.so"},
		{Name: "FOO", Value: "bar"},
	}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)

	envOp := by["/spec/containers/0/env"]
	require.Equal(t, "replace", envOp.Op) // env existed -> replace whole array
	env := envOp.Value.([]corev1.EnvVar)
	got := map[string]string{}
	for _, e := range env {
		got[e.Name] = e.Value
	}
	require.Equal(t, "/opt/nvml-mock/driver/usr/bin:/custom/bin", got["PATH"])
	require.Contains(t, got["LD_PRELOAD"], "/x/pre.so")
	require.Contains(t, got["LD_PRELOAD"], "libibmockumad.so.1")
	require.Contains(t, got["LD_PRELOAD"], "libpcimocksys.so.1")
	require.Equal(t, "bar", got["FOO"]) // untouched entries preserved
	require.Equal(t, "/opt/nvml-mock", got["MOCK_PCI_ROOT"])
}

func TestMutate_OptOut(t *testing.T) {
	pod := plainPod()
	pod.Annotations = map[string]string{"nvml-mock.nvidia.com/inject": "false"}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	require.Empty(t, ops)
}

func TestMutate_Idempotent(t *testing.T) {
	pod := plainPod()
	pod.Spec.Volumes = []corev1.Volume{{Name: OverlayVolumeName}}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	require.Empty(t, ops)
}

func TestMutate_DeviceOptIn(t *testing.T) {
	pod := plainPod()
	pod.Annotations = map[string]string{"nvml-mock.nvidia.com/devices": "true"}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)

	sc, ok := by["/spec/containers/0/securityContext"]
	require.True(t, ok)
	priv := sc.Value.(*corev1.SecurityContext)
	require.NotNil(t, priv.Privileged)
	require.True(t, *priv.Privileged)

	// GPUCount=2 -> nvidia0, nvidia1 + nvidiactl + uvm(+tools) volumes present.
	vols := by["/spec/volumes"].Value.([]corev1.Volume)
	names := map[string]bool{}
	for _, v := range vols {
		names[v.Name] = true
	}
	require.True(t, names["nvml-mock-dev-nvidia0"])
	require.True(t, names["nvml-mock-dev-nvidia1"])
	require.True(t, names["nvml-mock-dev-nvidiactl"])
}

func TestMutate_InitContainersToo(t *testing.T) {
	pod := plainPod()
	pod.Spec.InitContainers = []corev1.Container{{Name: "init"}}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)
	_, ok := by["/spec/initContainers/0/env"]
	require.True(t, ok)
}
```

- [ ] **Step 2: Run tests to verify they fail (package missing)**

Run: `go test ./cmd/nvml-mock-injector/inject/... -v`
Expected: FAIL/compile error (`inject` package / symbols not defined).

- [ ] **Step 3: Implement `inject.go`**

Create `cmd/nvml-mock-injector/inject/inject.go`:

```go
// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject computes the RFC 6902 JSON patch that overlays the mock GPU
// environment onto an arbitrary pod. It is pure (no I/O) so it can be unit
// tested exhaustively; the HTTP server in package main wraps it.
package inject

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	// OverlayVolumeName is the injected hostPath volume AND the idempotency
	// marker: if a pod already has it, Mutate is a no-op.
	OverlayVolumeName = "nvml-mock-overlay"

	// InjectedAnnotation is recorded on mutated pods for debugging.
	InjectedAnnotation = "nvml-mock.nvidia.com/injected"
)

// Config parameterizes a mutation. Values come from the injector Deployment env
// (wired by Helm).
type Config struct {
	HostPath          string // host source, e.g. /var/lib/nvml-mock
	MountPath         string // in-container path, e.g. /opt/nvml-mock
	EnableIB          bool
	EnablePCI         bool
	OptOutAnnotation  string // e.g. nvml-mock.nvidia.com/inject ("false" opts out)
	DevicesAnnotation string // e.g. nvml-mock.nvidia.com/devices ("true" adds /dev/nvidia*)
	GPUCount          int    // number of nvidiaN device nodes for device opt-in
}

// PatchOp is a single RFC 6902 operation. Value is omitted for removes.
type PatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// Mutate returns the patch ops for pod, or nil when the pod should be left
// untouched (opt-out, already injected).
func Mutate(pod *corev1.Pod, cfg Config) ([]PatchOp, error) {
	if cfg.OptOutAnnotation != "" && strings.EqualFold(pod.Annotations[cfg.OptOutAnnotation], "false") {
		return nil, nil
	}
	for _, v := range pod.Spec.Volumes {
		if v.Name == OverlayVolumeName {
			return nil, nil // idempotent
		}
	}

	devices := cfg.DevicesAnnotation != "" &&
		strings.EqualFold(pod.Annotations[cfg.DevicesAnnotation], "true")

	var ops []PatchOp

	// 1. Volumes: overlay + (optional) device nodes.
	volumes := []corev1.Volume{overlayVolume(cfg)}
	if devices {
		volumes = append(volumes, deviceVolumes(cfg)...)
	}
	ops = append(ops, addOrReplace(len(pod.Spec.Volumes) == 0, "/spec/volumes",
		mergeVolumes(pod.Spec.Volumes, volumes)))

	// 2. Per-container edits (containers + initContainers).
	env := buildEnv(cfg)
	mounts := containerMounts(cfg, devices)
	ops = append(ops, containerOps("/spec/containers", pod.Spec.Containers, env, mounts, devices, cfg)...)
	ops = append(ops, containerOps("/spec/initContainers", pod.Spec.InitContainers, env, mounts, devices, cfg)...)

	// 3. Audit annotation (also documents that injection happened).
	if pod.Annotations == nil {
		ops = append(ops, PatchOp{Op: "add", Path: "/metadata/annotations",
			Value: map[string]string{InjectedAnnotation: "true"}})
	} else {
		ops = append(ops, PatchOp{Op: "add",
			Path:  "/metadata/annotations/" + escapeJSONPointer(InjectedAnnotation),
			Value: "true"})
	}

	return ops, nil
}

func addOrReplace(absent bool, path string, value interface{}) PatchOp {
	if absent {
		return PatchOp{Op: "add", Path: path, Value: value}
	}
	return PatchOp{Op: "replace", Path: path, Value: value}
}

func mergeVolumes(existing, add []corev1.Volume) []corev1.Volume {
	out := make([]corev1.Volume, 0, len(existing)+len(add))
	out = append(out, existing...)
	out = append(out, add...)
	return out
}

func overlayVolume(cfg Config) corev1.Volume {
	hpType := corev1.HostPathDirectoryOrCreate
	return corev1.Volume{
		Name: OverlayVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: cfg.HostPath, Type: &hpType},
		},
	}
}

func containerMounts(cfg Config, devices bool) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{{
		Name:      OverlayVolumeName,
		MountPath: cfg.MountPath,
		ReadOnly:  true,
	}}
	if devices {
		mounts = append(mounts, deviceMounts(cfg)...)
	}
	return mounts
}

func containerOps(base string, ctrs []corev1.Container, env []corev1.EnvVar,
	mounts []corev1.VolumeMount, devices bool, cfg Config) []PatchOp {
	var ops []PatchOp
	for i, c := range ctrs {
		ops = append(ops, addOrReplace(len(c.VolumeMounts) == 0,
			fmt.Sprintf("%s/%d/volumeMounts", base, i),
			append(append([]corev1.VolumeMount{}, c.VolumeMounts...), mounts...)))

		ops = append(ops, addOrReplace(len(c.Env) == 0,
			fmt.Sprintf("%s/%d/env", base, i), mergeEnv(c.Env, env)))

		if devices {
			priv := true
			sc := c.SecurityContext
			if sc == nil {
				sc = &corev1.SecurityContext{}
			}
			sc.Privileged = &priv
			ops = append(ops, PatchOp{Op: "add",
				Path:  fmt.Sprintf("%s/%d/securityContext", base, i),
				Value: sc})
		}
	}
	return ops
}

// buildEnv returns the mock env vars in a deterministic order.
func buildEnv(cfg Config) []corev1.EnvVar {
	driver := cfg.MountPath + "/driver"
	env := []corev1.EnvVar{
		{Name: "PATH", Value: driver + "/usr/bin"},
		{Name: "LD_LIBRARY_PATH", Value: driver + "/usr/lib64"},
		{Name: "MOCK_NVML_CONFIG", Value: driver + "/config/config.yaml"},
	}
	var preload []string
	if cfg.EnableIB {
		preload = append(preload,
			driver+"/usr/local/lib/libibmockumad.so.1",
			driver+"/usr/local/lib/libibmockverbs.so.1",
			driver+"/usr/local/lib/libibmocksys.so.1",
		)
		env = append(env,
			corev1.EnvVar{Name: "MOCK_IB", Value: "full"},
			corev1.EnvVar{Name: "MOCK_IB_ROOT", Value: cfg.MountPath + "/ib"},
			corev1.EnvVar{Name: "MOCK_IB_PING_SOCKET", Value: cfg.MountPath + "/run/mock-ib.sock"},
		)
	}
	if cfg.EnablePCI {
		preload = append(preload, driver+"/usr/local/lib/libpcimocksys.so.1")
		env = append(env, corev1.EnvVar{Name: "MOCK_PCI_ROOT", Value: cfg.MountPath})
	}
	if len(preload) > 0 {
		env = append(env, corev1.EnvVar{Name: "LD_PRELOAD", Value: strings.Join(preload, ":")})
	}
	return env
}

// mergeEnv layers our env onto the container's existing list. PATH/LD_LIBRARY_PATH
// prepend, LD_PRELOAD appends, everything else sets-if-absent. Existing entries
// we don't touch are preserved in place.
func mergeEnv(existing, ours []corev1.EnvVar) []corev1.EnvVar {
	out := append([]corev1.EnvVar{}, existing...)
	idx := map[string]int{}
	for i, e := range out {
		idx[e.Name] = i
	}
	for _, e := range ours {
		i, found := idx[e.Name]
		if !found {
			out = append(out, e)
			idx[e.Name] = len(out) - 1
			continue
		}
		switch e.Name {
		case "PATH", "LD_LIBRARY_PATH":
			out[i].Value = e.Value + ":" + out[i].Value
		case "LD_PRELOAD":
			if out[i].Value == "" {
				out[i].Value = e.Value
			} else {
				out[i].Value = out[i].Value + ":" + e.Value
			}
		default:
			// Leave a user-provided MOCK_* value untouched.
		}
	}
	return out
}

func deviceVolumes(cfg Config) []corev1.Volume {
	char := corev1.HostPathCharDev
	dev := cfg.HostPath + "/driver/dev"
	mk := func(name, file string) corev1.Volume {
		return corev1.Volume{
			Name: "nvml-mock-dev-" + name,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: dev + "/" + file, Type: &char},
			},
		}
	}
	vols := []corev1.Volume{
		mk("nvidiactl", "nvidiactl"),
		mk("nvidia-uvm", "nvidia-uvm"),
		mk("nvidia-uvm-tools", "nvidia-uvm-tools"),
	}
	for i := 0; i < cfg.GPUCount; i++ {
		vols = append(vols, mk(fmt.Sprintf("nvidia%d", i), fmt.Sprintf("nvidia%d", i)))
	}
	return vols
}

func deviceMounts(cfg Config) []corev1.VolumeMount {
	mk := func(name, file string) corev1.VolumeMount {
		return corev1.VolumeMount{Name: "nvml-mock-dev-" + name, MountPath: "/dev/" + file}
	}
	mounts := []corev1.VolumeMount{
		mk("nvidiactl", "nvidiactl"),
		mk("nvidia-uvm", "nvidia-uvm"),
		mk("nvidia-uvm-tools", "nvidia-uvm-tools"),
	}
	for i := 0; i < cfg.GPUCount; i++ {
		mounts = append(mounts, mk(fmt.Sprintf("nvidia%d", i), fmt.Sprintf("nvidia%d", i)))
	}
	return mounts
}

// escapeJSONPointer encodes '~' and '/' per RFC 6901 for use in a patch path.
func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./cmd/nvml-mock-injector/inject/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/nvml-mock-injector/inject/
git commit -s -m "feat(injector): pure mock-overlay pod mutation logic"
```

---

## Task 4: Injector Server + Vendor + Dockerfile

**Files:**
- Create: `cmd/nvml-mock-injector/main.go`
- Modify: `deployments/nvml-mock/Dockerfile`
- Modify: `go.mod`, `go.sum`, `vendor/` (vendor `k8s.io/api/admission/v1`)

- [ ] **Step 1: Implement the admission HTTP server**

Create `cmd/nvml-mock-injector/main.go`:

```go
// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command nvml-mock-injector is a mutating admission webhook that overlays the
// mock GPU environment onto every pod (see package inject). It is deployed by
// the nvml-mock Helm chart with failurePolicy: Ignore.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/k8s-test-infra/cmd/nvml-mock-injector/inject"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig() inject.Config {
	gpu, _ := strconv.Atoi(envOr("GPU_COUNT", "0"))
	return inject.Config{
		HostPath:          envOr("OVERLAY_HOST_PATH", "/var/lib/nvml-mock"),
		MountPath:         envOr("OVERLAY_MOUNT_PATH", "/opt/nvml-mock"),
		EnableIB:          !strings.EqualFold(os.Getenv("OVERLAY_IB"), "false"),
		EnablePCI:         !strings.EqualFold(os.Getenv("OVERLAY_PCI"), "false"),
		OptOutAnnotation:  envOr("OPT_OUT_ANNOTATION", "nvml-mock.nvidia.com/inject"),
		DevicesAnnotation: envOr("DEVICES_ANNOTATION", "nvml-mock.nvidia.com/devices"),
		GPUCount:          gpu,
	}
}

func main() {
	cfg := loadConfig()
	cert := envOr("TLS_CERT_FILE", "/tls/tls.crt")
	key := envOr("TLS_KEY_FILE", "/tls/tls.key")
	addr := envOr("LISTEN_ADDR", ":8443")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/mutate", func(w http.ResponseWriter, r *http.Request) {
		handleMutate(w, r, cfg)
	})

	log.Printf("nvml-mock-injector listening on %s (mountPath=%s ib=%t pci=%t gpuCount=%d)",
		addr, cfg.MountPath, cfg.EnableIB, cfg.EnablePCI, cfg.GPUCount)
	log.Fatal(http.ListenAndServeTLS(addr, cert, key, mux))
}

func handleMutate(w http.ResponseWriter, r *http.Request, cfg inject.Config) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil || review.Request == nil {
		http.Error(w, "invalid AdmissionReview", http.StatusBadRequest)
		return
	}
	req := review.Request

	resp := &admissionv1.AdmissionResponse{UID: req.UID, Allowed: true}
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		// Fail open: allow unmodified rather than block scheduling.
		log.Printf("decode pod %s/%s: %v (allowing unmodified)", req.Namespace, req.Name, err)
		writeReview(w, review, resp)
		return
	}

	ops, err := inject.Mutate(&pod, cfg)
	if err != nil {
		log.Printf("mutate pod %s/%s: %v (allowing unmodified)", req.Namespace, req.Name, err)
		writeReview(w, review, resp)
		return
	}
	if len(ops) > 0 {
		patch, mErr := json.Marshal(ops)
		if mErr != nil {
			log.Printf("marshal patch: %v (allowing unmodified)", mErr)
			writeReview(w, review, resp)
			return
		}
		pt := admissionv1.PatchTypeJSONPatch
		resp.PatchType = &pt
		resp.Patch = patch
	}
	writeReview(w, review, resp)
}

func writeReview(w http.ResponseWriter, in admissionv1.AdmissionReview, resp *admissionv1.AdmissionResponse) {
	out := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Response: resp,
	}
	_ = in
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 2: Vendor the admission API**

`k8s.io/api/admission/v1` is part of the already-required `k8s.io/api` module but
is not yet vendored. Run:

```bash
go mod tidy && go mod vendor
```

Expected: `vendor/k8s.io/api/admission/v1/` now exists; `vendor/modules.txt`
lists it. No `go.mod` version change (same `k8s.io/api v0.36.2`).

- [ ] **Step 3: Build the whole module + the new command**

Run:
```bash
go build ./cmd/nvml-mock-injector/...
go test -race $(go list ./cmd/nvml-mock-injector/... | grep -v vendor)
```
Expected: build succeeds; tests PASS.

- [ ] **Step 4: Add the injector to the Dockerfile**

In `deployments/nvml-mock/Dockerfile` builder stage, add the source copy near the
other `COPY cmd/...` lines (after line 30):

```dockerfile
COPY cmd/nvml-mock-injector/ cmd/nvml-mock-injector/
```

Add to the `go build` chain (inside the `RUN mkdir -p /out && go build ...` block,
append one more build):

```dockerfile
    && go build -mod=vendor -o /out/nvml-mock-injector ./cmd/nvml-mock-injector
```

In the runtime stage, after the `check-fabric` copy (line 69):

```dockerfile
COPY --from=builder /out/nvml-mock-injector /usr/local/bin/nvml-mock-injector
```

- [ ] **Step 5: Commit**

```bash
git add cmd/nvml-mock-injector/main.go deployments/nvml-mock/Dockerfile \
  go.mod go.sum vendor/
git commit -s -m "feat(injector): admission HTTP server + vendor admission/v1"
```

---

## Task 5: Helm Chart — Injector Deployment, Service, Webhook, TLS, RBAC

**Files:**
- Modify: `deployments/nvml-mock/helm/nvml-mock/values.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-tls-secret.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-deployment.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-service.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-webhook.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-rbac.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/tests/injector_test.yaml`

- [ ] **Step 1: Add `injector` values**

Append to `deployments/nvml-mock/helm/nvml-mock/values.yaml`:

```yaml
# Node-wide mock GPU injection. When enabled, a mutating admission webhook
# overlays the DaemonSet-materialized mock tree (/var/lib/nvml-mock) at
# /opt/nvml-mock in EVERY pod and injects PATH/LD_LIBRARY_PATH/LD_PRELOAD/MOCK_*
# env, so any pod can run nvidia-smi / ibnetdiscover without a GPU request.
#
# Safety: the webhook is fail-open (failurePolicy: Ignore) and excludes its own
# release namespace. Pods opt out with annotation
# `nvml-mock.nvidia.com/inject: "false"`; pods opt into privileged /dev/nvidia*
# device nodes with `nvml-mock.nvidia.com/devices: "true"`.
injector:
  enabled: true
  replicaCount: 1
  port: 8443
  overlay:
    hostPath: /var/lib/nvml-mock
    mountPath: /opt/nvml-mock
    ib: true
    pci: true
  optOutAnnotation: nvml-mock.nvidia.com/inject
  devicesAnnotation: nvml-mock.nvidia.com/devices
  # Namespaces never mutated (release namespace is always excluded). Add e.g.
  # kube-system here if control-plane pods misbehave with the overlay.
  excludedNamespaces: []
  resources: {}
```

- [ ] **Step 2: TLS secret (self-signed CA)**

Create `deployments/nvml-mock/helm/nvml-mock/templates/injector-tls-secret.yaml`:

```yaml
{{- if .Values.injector.enabled }}
{{- /*
Generate a self-signed cert whose CA we also stamp into the webhook caBundle.
genSignedCert(cn, ips, dnsNames, days, ca) with a freshly generated CA gives us
a matching (cert, key, caCert) triple in one render pass.
*/}}
{{- $fullname := include "nvml-mock.fullname" . }}
{{- $svc := printf "%s-injector" $fullname }}
{{- $cn := printf "%s.%s.svc" $svc .Release.Namespace }}
{{- $altNames := list $cn (printf "%s.%s.svc.cluster.local" $svc .Release.Namespace) }}
{{- $ca := genCA (printf "%s-ca" $svc) 3650 }}
{{- $cert := genSignedCert $cn nil $altNames 3650 $ca }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ $svc }}-tls
  labels:
    {{- include "nvml-mock.labels" . | nindent 4 }}
  annotations:
    # Stash the CA so the webhook template can read it back for caBundle.
    "nvml-mock.nvidia.com/ca-cert": {{ $ca.Cert | b64enc | quote }}
type: kubernetes.io/tls
data:
  tls.crt: {{ $cert.Cert | b64enc }}
  tls.key: {{ $cert.Key | b64enc }}
  ca.crt: {{ $ca.Cert | b64enc }}
{{- end }}
```

> Note: Helm renders each template independently, so the webhook template
> below re-generates its own CA rather than reading this one. To keep the CA
> consistent across both templates in a single `helm install`, generate the CA
> **once** in `injector-webhook.yaml` and reference the same secret. To avoid the
> cross-template sharing problem entirely, this plan generates the full triple in
> the webhook template (Step 4) and has the Deployment mount that same secret.
> Therefore: **delete the `genCA`/`genSignedCert` lines from this file** and
> instead have this secret be *created by* the webhook template. Implement it as
> described in Step 4 (single template owns cert generation) and make this file a
> no-op placeholder OR fold the Secret into `injector-webhook.yaml`. Simplest:
> put the Secret + MutatingWebhookConfiguration in one file (Step 4) and skip
> this separate file.

**Decision (implement this):** Do **not** create a separate TLS secret template.
Generate the cert once inside `injector-webhook.yaml` (Step 4), emitting both the
`Secret` and the `MutatingWebhookConfiguration` from the same `$ca`/`$cert` pair.
Remove this file if created.

- [ ] **Step 3: Deployment + Service**

Create `deployments/nvml-mock/helm/nvml-mock/templates/injector-deployment.yaml`:

```yaml
{{- if .Values.injector.enabled }}
{{- $fullname := include "nvml-mock.fullname" . }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ $fullname }}-injector
  labels:
    {{- include "nvml-mock.labels" . | nindent 4 }}
    app.kubernetes.io/component: injector
spec:
  replicaCount: {{ .Values.injector.replicaCount }}
  replicas: {{ .Values.injector.replicaCount }}
  selector:
    matchLabels:
      {{- include "nvml-mock.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: injector
  template:
    metadata:
      labels:
        {{- include "nvml-mock.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: injector
    spec:
      serviceAccountName: {{ $fullname }}-injector
      containers:
        - name: injector
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          command: ["/usr/local/bin/nvml-mock-injector"]
          ports:
            - name: https
              containerPort: {{ .Values.injector.port }}
          env:
            - name: LISTEN_ADDR
              value: ":{{ .Values.injector.port }}"
            - name: OVERLAY_HOST_PATH
              value: {{ .Values.injector.overlay.hostPath | quote }}
            - name: OVERLAY_MOUNT_PATH
              value: {{ .Values.injector.overlay.mountPath | quote }}
            - name: OVERLAY_IB
              value: {{ .Values.injector.overlay.ib | quote }}
            - name: OVERLAY_PCI
              value: {{ .Values.injector.overlay.pci | quote }}
            - name: OPT_OUT_ANNOTATION
              value: {{ .Values.injector.optOutAnnotation | quote }}
            - name: DEVICES_ANNOTATION
              value: {{ .Values.injector.devicesAnnotation | quote }}
            - name: GPU_COUNT
              value: "{{ include "nvml-mock.gpuCount" . }}"
          readinessProbe:
            httpGet:
              path: /healthz
              port: https
              scheme: HTTPS
          volumeMounts:
            - name: tls
              mountPath: /tls
              readOnly: true
          {{- with .Values.injector.resources }}
          resources:
            {{- toYaml . | nindent 12 }}
          {{- end }}
      volumes:
        - name: tls
          secret:
            secretName: {{ $fullname }}-injector-tls
{{- end }}
```

> Remove the stray `replicaCount` key above (Deployment uses `replicas`); it is
> shown only to flag that `spec.replicas` is the correct field — implement with
> `replicas: {{ .Values.injector.replicaCount }}` only.

Create `deployments/nvml-mock/helm/nvml-mock/templates/injector-service.yaml`:

```yaml
{{- if .Values.injector.enabled }}
{{- $fullname := include "nvml-mock.fullname" . }}
apiVersion: v1
kind: Service
metadata:
  name: {{ $fullname }}-injector
  labels:
    {{- include "nvml-mock.labels" . | nindent 4 }}
    app.kubernetes.io/component: injector
spec:
  selector:
    {{- include "nvml-mock.selectorLabels" . | nindent 4 }}
    app.kubernetes.io/component: injector
  ports:
    - name: https
      port: 443
      targetPort: https
{{- end }}
```

- [ ] **Step 4: Webhook config + TLS secret (single template owns the CA)**

Create `deployments/nvml-mock/helm/nvml-mock/templates/injector-webhook.yaml`:

```yaml
{{- if .Values.injector.enabled }}
{{- $fullname := include "nvml-mock.fullname" . }}
{{- $svc := printf "%s-injector" $fullname }}
{{- $cn := printf "%s.%s.svc" $svc .Release.Namespace }}
{{- $altNames := list $cn (printf "%s.%s.svc.cluster.local" $svc .Release.Namespace) }}
{{- $ca := genCA (printf "%s-ca" $svc) 3650 }}
{{- $cert := genSignedCert $cn nil $altNames 3650 $ca }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ $svc }}-tls
  labels:
    {{- include "nvml-mock.labels" . | nindent 4 }}
    app.kubernetes.io/component: injector
type: kubernetes.io/tls
data:
  tls.crt: {{ $cert.Cert | b64enc }}
  tls.key: {{ $cert.Key | b64enc }}
  ca.crt: {{ $ca.Cert | b64enc }}
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: {{ $svc }}
  labels:
    {{- include "nvml-mock.labels" . | nindent 4 }}
    app.kubernetes.io/component: injector
webhooks:
  - name: inject.nvml-mock.nvidia.com
    admissionReviewVersions: ["v1"]
    sideEffects: None
    reinvocationPolicy: Never
    # Fail-open is mandatory: a cluster-wide Fail policy on all pods would make
    # the API server unable to schedule anything (including this webhook) during
    # an outage.
    failurePolicy: Ignore
    clientConfig:
      service:
        name: {{ $svc }}
        namespace: {{ .Release.Namespace }}
        path: /mutate
        port: 443
      caBundle: {{ $ca.Cert | b64enc }}
    namespaceSelector:
      matchExpressions:
        # Never mutate the injector's own namespace (bootstrap deadlock), plus
        # any operator-configured exclusions.
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values:
            - {{ .Release.Namespace }}
            {{- range .Values.injector.excludedNamespaces }}
            - {{ . }}
            {{- end }}
    rules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE"]
        resources: ["pods"]
        scope: "Namespaced"
{{- end }}
```

Create `deployments/nvml-mock/helm/nvml-mock/templates/injector-rbac.yaml`:

```yaml
{{- if .Values.injector.enabled }}
{{- $fullname := include "nvml-mock.fullname" . }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ $fullname }}-injector
  labels:
    {{- include "nvml-mock.labels" . | nindent 4 }}
    app.kubernetes.io/component: injector
{{- end }}
```

> The webhook needs no Kubernetes API permissions at runtime (it only serves
> AdmissionReview HTTP), so a bare ServiceAccount suffices — no ClusterRole.

- [ ] **Step 5: Helm unittest**

Create `deployments/nvml-mock/helm/nvml-mock/tests/injector_test.yaml`:

```yaml
suite: injector
templates:
  - templates/injector-webhook.yaml
  - templates/injector-deployment.yaml
tests:
  - it: renders nothing when disabled
    set:
      injector.enabled: false
    asserts:
      - hasDocuments:
          count: 0
  - it: webhook is fail-open and excludes the release namespace
    template: templates/injector-webhook.yaml
    documentIndex: 1
    set:
      injector.enabled: true
    asserts:
      - isKind:
          of: MutatingWebhookConfiguration
      - equal:
          path: webhooks[0].failurePolicy
          value: Ignore
      - contains:
          path: webhooks[0].namespaceSelector.matchExpressions[0].values
          content: NAMESPACE
  - it: deployment points at the injector binary
    template: templates/injector-deployment.yaml
    set:
      injector.enabled: true
    asserts:
      - isKind:
          of: Deployment
      - equal:
          path: spec.template.spec.containers[0].command[0]
          value: /usr/local/bin/nvml-mock-injector
```

> `helm unittest`'s default release namespace is `NAMESPACE`; adjust the
> `content:` value if the repo's `helm unittest` invocation sets a different one.

Run: `helm unittest deployments/nvml-mock/helm/nvml-mock`
Expected: PASS (including the pre-existing suites).

- [ ] **Step 6: Lint-render the chart**

Run:
```bash
helm template t deployments/nvml-mock/helm/nvml-mock --set injector.enabled=true >/dev/null && echo OK
```
Expected: `OK` (no template errors).

- [ ] **Step 7: Commit**

```bash
git add deployments/nvml-mock/helm/nvml-mock/values.yaml \
  deployments/nvml-mock/helm/nvml-mock/templates/injector-*.yaml \
  deployments/nvml-mock/helm/nvml-mock/tests/injector_test.yaml
git commit -s -m "feat(nvml-mock): helm chart for node-wide injection webhook"
```

---

## Task 6: E2E Injection Tests

**Files:**
- Create: `tests/e2e/injection-test-pods.yaml`
- Create: `tests/e2e/validate-injection.sh`

- [ ] **Step 1: Test pod manifests**

Create `tests/e2e/injection-test-pods.yaml`:

```yaml
# Ambient: plain image, NO annotations, NO GPU request. Must still run
# nvidia-smi and ibnetdiscover thanks to the injector overlay.
apiVersion: v1
kind: Pod
metadata:
  name: injection-ambient
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: ubuntu:22.04
      command:
        - sh
        - -c
        - |
          set -e
          nvidia-smi -L
          ibstat -l
          ibnetdiscover || true   # cross-pod fabric may be empty on single node
---
# Opt-out: must NOT receive the overlay.
apiVersion: v1
kind: Pod
metadata:
  name: injection-optout
  annotations:
    nvml-mock.nvidia.com/inject: "false"
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: ubuntu:22.04
      command: ["sh", "-c", "test ! -e /opt/nvml-mock && echo NO-OVERLAY"]
---
# Device opt-in: privileged + /dev/nvidia0 present.
apiVersion: v1
kind: Pod
metadata:
  name: injection-devices
  annotations:
    nvml-mock.nvidia.com/devices: "true"
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: ubuntu:22.04
      command: ["sh", "-c", "nvidia-smi -L && test -e /dev/nvidia0 && echo HAVE-DEV"]
```

- [ ] **Step 2: Validation script**

Create `tests/e2e/validate-injection.sh`:

```bash
#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

apply() { kubectl apply -f tests/e2e/injection-test-pods.yaml; }
cleanup() { kubectl delete -f tests/e2e/injection-test-pods.yaml --ignore-not-found >/dev/null 2>&1 || true; }
trap cleanup EXIT

expect_phase() {
  local pod="$1" want="$2"
  kubectl wait --for=jsonpath='{.status.phase}'="$want" "pod/$pod" --timeout=120s 2>/dev/null || true
  local got
  got=$(kubectl get pod "$pod" -o jsonpath='{.status.phase}')
  if [ "$got" != "$want" ]; then
    echo "FAIL: $pod phase=$got want=$want"; kubectl logs "$pod" || true; exit 1
  fi
  echo "PASS: $pod ($got)"
}

apply
expect_phase injection-ambient Succeeded
expect_phase injection-optout  Succeeded
expect_phase injection-devices Succeeded

# The ambient pod must carry the injected marker; the opt-out pod must not.
inj=$(kubectl get pod injection-ambient -o jsonpath='{.metadata.annotations.nvml-mock\.nvidia\.com/injected}')
[ "$inj" = "true" ] || { echo "FAIL: ambient pod missing injected marker"; exit 1; }
out=$(kubectl get pod injection-optout -o jsonpath='{.metadata.annotations.nvml-mock\.nvidia\.com/injected}')
[ -z "$out" ] || { echo "FAIL: opt-out pod was injected"; exit 1; }
echo "PASS: injection markers correct"
```

Make it executable: `chmod +x tests/e2e/validate-injection.sh`.

- [ ] **Step 3: Manual Kind smoke test (document only — run locally)**

```bash
kind create cluster --name inject-test
docker build -t nvml-mock:local -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:local --name inject-test
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock --set image.tag=local \
  --set injector.enabled=true --wait
# Wait for the DaemonSet to stage host artifacts, then:
tests/e2e/validate-injection.sh
```
Expected: all `PASS` lines.

- [ ] **Step 4: Commit**

```bash
git add tests/e2e/injection-test-pods.yaml tests/e2e/validate-injection.sh
git commit -s -m "test(e2e): validate node-wide pod injection (ambient/opt-out/devices)"
```

---

## Task 7: Documentation

**Files:**
- Modify: `deployments/nvml-mock/helm/nvml-mock/README.md`
- Modify: `docs/quickstart.md`

- [ ] **Step 1: Helm README — "Node-Wide Injection" section**

Add a section documenting:
- What `injector.enabled` does (overlay every pod at `/opt/nvml-mock`).
- Opt-out annotation `nvml-mock.nvidia.com/inject: "false"` (needed for
  musl/Alpine/distroless/scratch images).
- Device opt-in annotation `nvml-mock.nvidia.com/devices: "true"` (adds
  `privileged` + `/dev/nvidia*`).
- Fail-open behavior and the release-namespace / `excludedNamespaces` exclusion.
- The glibc-version caveat for the staged IB tools.

- [ ] **Step 2: Quickstart snippet**

In `docs/quickstart.md`, add an "Ambient node-wide mock" example: install with
`--set injector.enabled=true`, then run a plain `ubuntu` pod and execute
`nvidia-smi -L` / `ibnetdiscover` with no GPU request.

- [ ] **Step 3: Commit**

```bash
git add deployments/nvml-mock/helm/nvml-mock/README.md docs/quickstart.md
git commit -s -m "docs: document node-wide (every-pod) mock injection"
```

---

## Spec Coverage Checklist

| Spec requirement | Task |
|------------------|------|
| Overlay contract (volume + mounts + env) | Task 3 (`inject.go`), Task 5 (Deployment env) |
| Additive `PATH`/`LD_LIBRARY_PATH`/`LD_PRELOAD` merge | Task 3 (`mergeEnv`) |
| PCI sysfs shim (`libpcimocksys`) | Task 1 |
| IB tools + shims staged on host; host socket | Task 2 |
| `nvml-mock-injector` webhook server | Task 4 |
| `failurePolicy: Ignore` + self-namespace exclusion | Task 5 (webhook) |
| Opt-out annotation | Task 3, Task 6 |
| Device-node opt-in (privileged) | Task 3, Task 6 |
| Idempotency (overlay marker) | Task 3 |
| Helm self-signed CA / caBundle | Task 5 |
| E2E ambient/opt-out/device | Task 6 |
| Docs | Task 7 |

## Self-Review Notes

- `injector-deployment.yaml` in Step 3 shows a stray `replicaCount:` key — the
  implementer must emit only `replicas: {{ .Values.injector.replicaCount }}`.
- TLS: cert generation lives in **one** template (`injector-webhook.yaml`) that
  emits both the Secret and the webhook config from the same `$ca`; do not create
  a second TLS template (Step 2 decision).
- `helm unittest` release namespace literal in `injector_test.yaml` may need to
  match the repo's invocation (`NAMESPACE` by default).
- The staged IB tools inherit the image's glibc (bookworm 2.36); pods on older
  glibc should use the opt-out annotation. This is a documented limitation, not a
  bug.
- `Mutate` returns whole-array `add`/`replace` for `env`/`volumeMounts`; verify
  against a live `kubectl apply` during the Task 6 smoke test, since apiserver
  strategic-merge vs JSONPatch semantics are easy to get subtly wrong.
```
