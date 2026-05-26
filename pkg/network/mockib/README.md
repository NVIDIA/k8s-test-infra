# mockib

Mock InfiniBand fabric for Kubernetes testing. Lets unmodified userspace
tools (`ibstat`, `ibstatus`, `iblinkinfo`, `libibverbs` consumers, ...)
read realistic per-HCA topology data on hosts that have no IB hardware.

This package is the InfiniBand counterpart of [`pkg/gpu/mocknvml`](../../gpu/mocknvml/):
mocknvml ships a fake `libnvidia-ml.so` so `nvidia-smi` works without
GPUs; mockib ships a fake sysfs tree so `ibstat` works without HCAs.

## Components

```
pkg/network/mockib/
├── c/shim.c          # LD_PRELOAD: redirects libc paths to MOCK_IB_ROOT
├── c/umad_shim.c     # LD_PRELOAD: forwards libibumad to mock-ib
├── Makefile          # builds libibmocksys.so and libibmockumad.so (in this dir)
├── config/           # YAML schema for the `infiniband:` profile block
├── render/           # writes a kernel-faithful sysfs tree from the schema
├── sysfs/            # scans rendered tree (port GUID, LID, GID)
├── protocol/         # JSON wire format (Unix socket + TCP fabric)
├── registry/         # cross-pod port GUID → peer routing table
└── daemon/           # mock-ib server (loopback + fabric)

cmd/mock-ib/     # renders sysfs from profile; UMAD daemon + optional TCP fabric
```

Two pieces work together:

1. **`libibmocksys.so` (LD_PRELOAD shim, C).** Hooks ~15 libc functions
   (`open`, `openat`, `opendir`, `scandir`/`scandir64`, `stat`, `lstat`,
   `fstatat`, `statx`, `fopen`, `access`, `faccessat`, `readlink`,
   `readlinkat`, `chdir`) and rewrites paths starting with
   `/sys/class/infiniband*`, `/sys/class/infiniband_mad/`,
   `/sys/class/infiniband_verbs/`, or `/dev/infiniband` to point under
   `$MOCK_IB_ROOT/...`. Everything else passes through to real libc via
   `dlsym(RTLD_NEXT, ...)`. C is the right choice here — see
   [Why C, not Go](#why-c-not-go).

2. **`mock-ib` (Go binary).** With `-config`, reads the `infiniband:`
   block from the mock-nvml profile YAML and writes a kernel-faithful tree
   under `-ib-root`: per-CA `node_type`, `node_guid`, `sys_image_guid`,
   `fw_ver`, `hw_rev`, `board_id`, `hca_type`, `node_desc`; per-port
   `state`, `phys_state`, `rate`, `lid`, `sm_lid`, `cap_mask`,
   `link_layer`, `gids/0`, `pkeys/0`, `counters/*`; plus
   `infiniband_mad/{abi_version,umad*,issm*}` for `libibumad`,
   `infiniband_verbs/{abi_version,uverbs*}` for `libibverbs`, and the
   matching `dev/infiniband/*` files.

In the nvml-mock DaemonSet:

- The Dockerfile builds both pieces and installs `infiniband-diags` +
  `rdma-core` for the real userspace tools.
- `setup.sh` invokes `mock-ib -render-only` (or starts the full
  daemon when `MOCK_IB_PING=1`) to populate `/host/var/lib/nvml-mock/ib`.
- The pod env sets `LD_PRELOAD=/usr/local/lib/libibmocksys.so.1` and
  `MOCK_IB_ROOT=/var/lib/nvml-mock/ib`, so every process in the
  container — including `kubectl exec ... ibstat` — sees the fake fabric.

Set `MOCK_IB_DISABLE=1` in any process to bypass the shim (escape hatch
for debugging the host filesystem).

## Mock ibping

With sysfs mocking alone, real `ibping` fails at UMAD `ioctl` time because
`/dev/infiniband/umad*` entries in the rendered tree are regular files, not
character devices. The ping mock adds a second LD_PRELOAD shim and an in-pod
daemon so the **real `ibping` binary** from `infiniband-diags` can send and
receive management MADs without IB hardware.

### Components

| Component | Location | Role |
|-----------|----------|------|
| `libibmocksys.so` | `shim.c` | Sysfs/dev path redirect (`MOCK_IB_ROOT`) |
| `libibmockumad.so` | `umad_shim.c` | Intercept `libibumad` calls used by `ibping` when `MOCK_IB_PING=1` |
| `mock-ib` | `cmd/mock-ib/` | Renders sysfs from profile; UMAD over Unix socket; optional TCP fabric relay |

When ping mock is enabled, **`libibmockumad.so.1` must precede
`libibmocksys.so.1` in `LD_PRELOAD`** so UMAD ioctls are intercepted before
sysfs path rewriting.

### Environment variables

| Variable | Default | Meaning |
|----------|---------|---------|
| `MOCK_IB_PING` | `0` | Enable `libibmockumad` shim and start `mock-ib` |
| `MOCK_IB_PING_SOCKET` | `/run/mock-ib.sock` | Unix socket between shim and daemon |
| `MOCK_IB_PING_FABRIC` | `0` | Enable Phase 2 TCP fabric relay between pods |
| `MOCK_IB_PEERS` | (unset) | Comma-separated peer pod IPs for fabric registration (optional when Service discovery is used) |

Related: `MOCK_IB_PING_PORT` (default `18515`) sets the TCP fabric listen
port; `MOCK_IB_ROOT` must point at the rendered sysfs tree (same as for
`libibmocksys`).

### Phase 1 loopback vs Phase 2 fabric

**Phase 1 (loopback):** `mock-ib` serves `libibmockumad` over a Unix
socket and synthesizes RECV payloads for MADs destined to local port GUIDs,
GIDs, or LIDs. Integration test:
`go test -tags=integration ./pkg/network/mockib/render/ -run TestIbping`.

**Phase 2 (fabric):** With `MOCK_IB_PING_FABRIC=1`, each daemon listens on
TCP (default port 18515), registers local ports with peers, and relays
`PING`/`PONG` between nvml-mock pods on different nodes. The nvml-mock Helm
chart exposes a ClusterIP Service and sets the env vars when
`infiniband.ping.enabled=true` (default off).

### What works and what does not

- **LID-based `ibping` works** for same-pod loopback and cross-node fabric
  (`ibping -S` on the server, `ibping -c N <lid>` on the client). E2E:
  `tests/e2e/validate-ibping.sh`.
- **`ibping -G <port_guid>` is still limited** for cross-node use. The fabric
  path is exercised via LID in CI; GUID-based client/server modes
  (`ibping -G`, `ibping -S -G`) are not fully supported across pods.
- Other `ibping` flags (`-R`, multicast, batch modes) are out of scope.

The renderer assigns **node-unique LIDs and port GUIDs** (FNV-1a of
`NODE_NAME`) so identities do not collide across Kind workers.

### Build notes

Both shims build on **Linux only** (glibc, `dlfcn.h`). On macOS, build inside
a Linux container:

```bash
docker run --rm -v "$PWD/../../..:/work" -w /work/pkg/network/mockib \
  debian:bookworm-slim sh -c \
  'apt-get update -qq && apt-get install -y -qq build-essential >/dev/null && make'
```

This produces `libibmocksys.so*` and `libibmockumad.so*`. Build the daemon
from the repo root:

```bash
go build -o /tmp/mock-ib ./cmd/mock-ib
```

The nvml-mock Dockerfile builds and installs all three artifacts.

## YAML schema

The renderer consumes the `infiniband:` block of a mock-nvml profile.
Defaults are applied per-field, so `enabled: true` is the minimum useful
configuration.

| Field                | Default              | Description                                                                |
|----------------------|----------------------|----------------------------------------------------------------------------|
| `enabled`            | `false`              | Must be `true` to render any tree.                                         |
| `hca_type`           | `MT4129`             | Mellanox HCA model. Shows up as `CA type` in `ibstat`.                     |
| `fw_version`         | `28.39.2048`         | Firmware version string.                                                   |
| `hw_rev`             | `0x0`                | Hardware revision.                                                         |
| `board_id`           | `MT_0000000838`      | Mellanox board ID.                                                         |
| `link_layer`         | `InfiniBand`         | `InfiniBand` (true IB) or `Ethernet` (RoCE).                               |
| `rate_gbps`          | `400`                | One of `100` (EDR), `200` (HDR), `400` (NDR), `800` (XDR).                 |
| `port_state`         | `ACTIVE`             | `DOWN`, `INIT`, `ARMED`, `ACTIVE`, `ACTIVE_DEFER`.                         |
| `phys_state`         | `LinkUp`             | `Disabled`, `Polling`, `Training`, `LinkUp`, `LinkErrorRecovery`, ...      |
| `hcas_per_gpu`       | `1`                  | Total HCAs = `gpu.count * hcas_per_gpu`.                                   |
| `hca_count`          | `0`                  | If non-zero, used instead of `gpu.count * hcas_per_gpu`.                   |
| `guid_prefix`        | `a088c2:0300:ab`     | First 12 hex digits of every node/port GUID; HCA index encodes lower bytes. |
| `node_desc_template` | `{node_name} mlx5_{idx}` | `{node_name}` and `{idx}` are interpolated.                            |

### Defaults per built-in profile

| Profile  | Enabled | HCA model              | Speed        | HCAs / GPU |
|----------|---------|------------------------|--------------|------------|
| `a100`   | yes     | ConnectX-6 (`MT4123`)  | HDR 200 Gb/s | 1          |
| `h100`   | yes     | ConnectX-7 (`MT4129`)  | NDR 400 Gb/s | 1          |
| `b200`   | yes     | ConnectX-7 (`MT4129`)  | NDR 400 Gb/s | 1          |
| `gb200`  | yes     | ConnectX-7 (`MT4129`)  | NDR 400 Gb/s | 1          |
| `l40s`   | no      | —                      | —            | —          |
| `t4`     | no      | —                      | —            | —          |

## Standalone usage

You can use mockib outside the nvml-mock chart — for example, in a
unit test or on a developer laptop running Linux.

### 1. Build the shim

```bash
cd pkg/network/mockib
make
# produces libibmocksys.so -> libibmocksys.so.1 -> libibmocksys.so.1.0.0
```

The shim only builds on Linux (uses `dlfcn.h`, glibc-specific signatures
like `statx`). On macOS, build inside a Linux container:

```bash
docker run --rm -v "$PWD/../../..:/work" -w /work/pkg/network/mockib \
  debian:bookworm-slim sh -c \
  'apt-get update -qq && apt-get install -y -qq build-essential >/dev/null && make'
```

### 2. Render a tree

```bash
go build -o /tmp/mock-ib ./cmd/mock-ib

cat > /tmp/profile.yaml <<EOF
infiniband:
  enabled: true
  hca_type: MT4129
  rate_gbps: 400
  hcas_per_gpu: 1
EOF

/tmp/mock-ib \
  -config /tmp/profile.yaml \
  -gpu-count 4 \
  -node-name dev-laptop \
  -ib-root /tmp/ibroot \
  -render-only
```

### 3. Run real `ibstat` against it

```bash
sudo apt-get install -y infiniband-diags

LD_PRELOAD=$PWD/libibmocksys.so.1 \
  MOCK_IB_ROOT=/tmp/ibroot \
  ibstat
```

You should see four `mlx5_X` HCAs reported as `Active` / `LinkUp` at
400 Gb/sec (4X NDR).

## Tests

```bash
# Unit tests (renderer only, no shim required)
go test ./pkg/network/mockib/...

# Integration test: builds the shim, runs real ibstat against a rendered
# tree, asserts on the output. Linux-only; skipped otherwise.
make -C pkg/network/mockib                       # build both shims
go test -tags=integration ./pkg/network/mockib/render/...

# ibping loopback integration (requires infiniband-diags + mock-ib)
go build -o /tmp/mock-ib ./cmd/mock-ib
go test -tags=integration ./pkg/network/mockib/render/ -run TestIbping -v
```

## Why C, not Go

The shim is loaded into **every process** in the container via
`LD_PRELOAD` — `bash`, `cat`, `kubectl`, `nvidia-smi`, every probe. A
Go-based `c-shared` library would inject the entire Go runtime
(scheduler, GC, signal handlers, ~5–10 MB resident, multiple pthreads)
into every one of those processes. Concretely:

- Go's runtime calls `mmap`, `open`, `stat` during init — our hooks would
  intercept its own filesystem accesses, risking re-entrancy / deadlock.
- Go installs its own `SIGSEGV`/`SIGURG`/`SIGPROF` handlers, which would
  hijack signal handling in unrelated host binaries.
- Variadic and function-pointer signatures (`open(..., flags, ...)`,
  `scandir(path, namelist, filter, compar)`, `statx(...)`) cannot be
  expressed cleanly through cgo — we'd write C trampolines anyway.
- Each cgo barrier crossing costs ~100 ns vs. ~5 ns for a direct call;
  `ls -R /` would slow down measurably.

The current C file is ~300 lines, mechanical, and isolated. Path-rewrite
logic is the only state. This is the textbook LD_PRELOAD-shim shape.

## See also

- [Helm chart README](../../../deployments/nvml-mock/helm/nvml-mock/README.md) — install instructions, profile reference, troubleshooting.
- [Standalone demo](../../../docs/demo/standalone/README.md) — full Kind walkthrough that exercises both `nvidia-smi` and `ibstat`.
- [`pkg/gpu/mocknvml`](../../gpu/mocknvml/) — sister package that mocks the NVML library.
