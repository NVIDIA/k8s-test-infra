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
├── c/shim.c          # libibmocksys.so   – LD_PRELOAD: redirects libc paths to MOCK_IB_ROOT
├── c/umad_shim.c     # libibmockumad.so  – LD_PRELOAD: proxies libibumad ↔ mock-ib
├── c/verbs_shim.c    # libibmockverbs.so – LD_PRELOAD: proxies /dev/infiniband/uverbsN ↔ mock-ib
├── Makefile          # builds all three .so libraries (in this dir)
├── config/           # YAML schema for the `infiniband:` profile block
├── render/           # writes a kernel-faithful sysfs tree from the schema
├── sysfs/            # scans rendered tree (port GUID, LID, GID)
├── protocol/         # JSON wire format (Unix socket + TCP fabric)
├── registry/         # cross-pod port GUID → peer routing table
├── fabric/           # in-memory port graph + DR neighbor selection
├── subnet/           # SMP synthesis (NODE_DESC/NODE_INFO/PORT_INFO, DR path resolution)
└── daemon/           # mock-ib server (UMAD loopback + SMP synth + verbs RPC + TCP fabric)

cmd/mock-ib/     # renders sysfs from profile; UMAD daemon + optional TCP fabric
```

Three pieces work together:

1. **`libibmocksys.so` (LD_PRELOAD shim, C).** Hooks ~15 libc functions
   (`open`, `openat`, `opendir`, `scandir`/`scandir64`, `stat`, `lstat`,
   `fstatat`, `statx`, `fopen`, `access`, `faccessat`, `readlink`,
   `readlinkat`, `chdir`) and rewrites paths starting with
   `/sys/class/infiniband*`, `/sys/class/infiniband_mad/`,
   `/sys/class/infiniband_verbs/`, or `/dev/infiniband` to point under
   `$MOCK_IB_ROOT/...`. Everything else passes through to real libc via
   `dlsym(RTLD_NEXT, ...)`. C is the right choice here — see
   [Why C, not Go](#why-c-not-go).

2. **`libibmockumad.so` / `libibmockverbs.so` (LD_PRELOAD shims, C).**
   Proxy `libibumad`'s `umad_send`/`umad_recv` and `open`/`read`/`write`
   on `/dev/infiniband/uverbsN` to the `mock-ib` daemon over a Unix
   socket. Used by `ibping`, `iblinkinfo`, `ibv_devinfo -l`, and the
   `infiniband-diags` SMP commands.

3. **`mock-ib` (Go binary).** With `-config`, reads the `infiniband:`
   block from the mock-nvml profile YAML and writes a kernel-faithful tree
   under `-ib-root`: per-CA `node_type`, `node_guid`, `sys_image_guid`,
   `fw_ver`, `hw_rev`, `board_id`, `hca_type`, `node_desc`,
   `device/modalias` (in the strict kernel grammar
   `pci:v000015B3d00001017sv*sd*bc*sc*i*` so libibverbs providers claim
   each HCA); per-port `state`, `phys_state`, `rate`, `lid`, `sm_lid`,
   `cap_mask`, `link_layer`, `gids/0`, `pkeys/0`, `counters/*`; plus
   `infiniband_mad/{abi_version,umad*,issm*}` for `libibumad`,
   `infiniband_verbs/{abi_version,uverbs*}` for `libibverbs`, and the
   matching `dev/infiniband/*` files. At runtime the daemon also
   synthesizes SMP responses (NODE_DESC, NODE_INFO, PORT_INFO) for the
   `iblinkinfo` direct-route walk over the in-memory fabric graph.

In the nvml-mock DaemonSet:

- The Dockerfile builds both pieces and installs `infiniband-diags` +
  `rdma-core` for the real userspace tools.
- `setup.sh` invokes `mock-ib -render-only` (or starts the full
  daemon when `MOCK_IB=1`) to populate `/host/var/lib/nvml-mock/ib`.
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
| `libibmockumad.so` | `umad_shim.c` | Intercept `libibumad` calls used by `ibping` when `MOCK_IB=1` |
| `mock-ib` | `cmd/mock-ib/` | Renders sysfs from profile; UMAD over Unix socket; optional TCP fabric relay |

When mock IB is enabled, preload order is
**`libibmockumad.so.1:libibmockverbs.so.1:libibmocksys.so.1`** so UMAD/verbs
intercepts run before sysfs path rewriting.

- **`ibstat`, `ibstatus`** — sysfs/libibumad only. Always works.
- **`ibping`** — UMAD over Unix socket; LID and GUID modes. See
  [Mock ibping](#mock-ibping).
- **`iblinkinfo`** — UMAD plus SMP direct-route synthesis. The daemon
  resolves DR paths from `path[1..hop_count]` per IB spec Vol 1
  §14.2.1.2 (`path[0]` reserved), responds to NODE_DESC / NODE_INFO /
  PORT_INFO using libibmad's `BITSOFFS`-style word layout, and selects a
  consistent remote neighbor for each outbound port so `libibnetdisc`
  walks the topology without duplicate-port collisions. See
  [iblinkinfo and ibv_devinfo](#iblinkinfo-and-ibv_devinfo).
- **`ibv_devinfo -l`, `ibv_devices`** — libibverbs device enumeration.
  Works when the rendered `device/modalias` matches a provider's
  `fnmatch` table and `ibverbs-providers` (libmlx5 etc.) is installed in
  the container; both are guaranteed by `render.go` + the chart image.
  Full per-device `ibv_devinfo` (without `-l`) is intentionally not
  supported — see the same section below.

### Environment variables

| Variable | Default | Meaning |
|----------|---------|---------|
| `MOCK_IB` | `0` | Enable `libibmockumad` + `libibmockverbs` shims and start `mock-ib` |
| `MOCK_IB_PING_SOCKET` | `/run/mock-ib.sock` | Unix socket between shim and daemon |
| `MOCK_IB_PING_FABRIC` | `0` | Enable Phase 2 TCP fabric relay between pods |
| `MOCK_IB_PEERS` | (unset) | Comma-separated peer pod IPs for fabric registration (optional when Service discovery is used) |
| `MOCK_IB_DEBUG_SMP` | `0` | When `1`, the daemon logs every synthesized SMP (attribute, DLID, hop count, first 4 outbound path bytes, resolved target). Useful for debugging `iblinkinfo` / DR-walk regressions. |

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
`PING`/`PONG` between nvml-mock pods on different nodes. The nvml-mock Helm chart always exposes a headless Service and sets the env vars.

### What works and what does not

- **LID-based `ibping`** works for same-pod loopback and cross-node fabric
  (`ibping -c N <lid>`). E2E: `tests/e2e/validate-ibping.sh`.
- **GUID-based `ibping`** (`ibping -G -c N <port_guid>`) resolves the
  destination via a mocked SA PathRecord query, then uses the same fabric
  path as LID mode. Pass the port GUID as a single hex value (e.g.
  `0xa088c20300abfa01`); colon-separated sysfs values must be normalized
  (ibdiag uses `strtoull` and stops at `:`).
- **`ibping -S` / `ibping -S -G`** (server mode) are not supported by the
  UMAD shim; cross-node validation uses client-mode pings only.
- Other `ibping` flags (`-R`, multicast, batch modes) are out of scope.

The renderer assigns **node-unique LIDs and port GUIDs** (FNV-1a of
`NODE_NAME`) so identities do not collide across Kind workers.

## iblinkinfo and ibv_devinfo

Beyond `ibstat` / `ibstatus` (sysfs-only) and `ibping` (UMAD loopback +
TCP fabric), the mock also drives the two diagnostic flows that real
clusters rely on for topology checks.

### iblinkinfo (DR-walk fabric scan)

`iblinkinfo` issues subnet-management packets (SMPs) along a direct
route to enumerate every link in the fabric. The daemon synthesizes
**NODE_DESC** (0x0010), **NODE_INFO** (0x0011), and **PORT_INFO**
(0x0015) responses for each hop using the field layout that
`libibmad` actually parses. Two concrete details matter and are easy to
get wrong:

- **`BITSOFFS` word swap.** `libibmad` reads sub-32-bit fields with
  `BITSOFFS(off, w) = (off &~ 31) | (32 - (off & 31) - w)`. `subnet/madfield.go`
  exposes a `SetFieldSpec` / `GetFieldSpec` pair so the synth code stays
  in IB-spec offsets (e.g. PORT_INFO `LID @ bit 128, width 16`) while
  the on-wire bytes match `libibmad`'s `_get_field`.
- **DR path indexing.** `path[0]` is reserved per IB spec Vol 1
  §14.2.1.2; outbound ports start at `path[1]`. The DR resolver reads
  `mad[128 + (i+1)]` for the `i`th hop. `path[0]` is ignored, never
  used as an outport — getting this wrong was a head-scratcher because
  it routes every neighbor query back to the local CA and causes
  `libibnetdisc` to abort with "Duplicate Port" once the second hop
  reads back the originating port GUID.

Peer selection (`fabric/drpath.go`) prefers a remote port of the same
HCA name (`mlx5_N`) for the first hop, and a different remote PodIP for
each subsequent hop, so `libibnetdisc`'s breadth-first walk converges.

```bash
kubectl exec "$POD" -- iblinkinfo
```

End-to-end validation: [`tests/e2e/validate-iblinkinfo.sh`](../../../tests/e2e/validate-iblinkinfo.sh).

### ibv_devinfo (libibverbs discovery)

`ibv_devinfo -l` (and `ibv_devices`) enumerate HCAs via libibverbs.
libibverbs matches each rendered sysfs device against the
`fnmatch`-based modalias tables of every loaded provider; for the mock
to be claimed by `libmlx5`, three things must line up:

1. **Provider package installed** — the nvml-mock image installs
   `ibverbs-providers` and pre-creates `/etc/libibverbs.d/` so libibverbs
   stops printing `couldn't open config directory '/etc/libibverbs.d'`.
2. **Strict modalias grammar** — `render.go` emits
   `pci:v000015B3d00001017sv000015B3sd00000008bc02sc00i00` (eight-digit
   upper-case hex, with the `bc` class byte). `libmlx5`'s match pattern
   is `pci:v000015B3d*sv*sd*bc*sc*i*`; any deviation (lower-case hex,
   missing zero padding, missing `bc`) causes the device to be silently
   skipped and `ibv_devinfo -l` reports `0 HCAs found`.
3. **NETLINK_RDMA short-circuit blocked** — `libibverbs >= v44` discovers
   devices via `NETLINK_RDMA` first and only falls back to walking
   `/sys/class/infiniband_verbs/*` when that socket fails. On any host
   whose kernel exposes a real RDMA device (GitHub Actions runners,
   bare-metal nodes with `mlx5_core` loaded, …) the netlink dump leaks
   that real device into the pod and our mocks are never enumerated.
   `libibmockverbs.so` therefore intercepts
   `socket(AF_NETLINK, *, NETLINK_RDMA)` while `MOCK_IB=1` and returns
   `-1 / EPROTONOSUPPORT`. libibverbs then falls back to the sysfs scan
   that `libibmocksys.so` already redirects to `MOCK_IB_ROOT`. The
   intercept is intentionally surgical: every other `socket(…)` call
   (AF_UNIX, AF_INET, NETLINK_ROUTE, NETLINK_KOBJECT_UEVENT, …) is
   forwarded untouched so Kubernetes networking remains unaffected.

```bash
kubectl exec "$POD" -- ibv_devinfo -l   # lists mlx5_0..mlx5_N
kubectl exec "$POD" -- ibv_devices      # device → node GUID table
```

End-to-end validation: [`tests/e2e/validate-ibv-devinfo.sh`](../../../tests/e2e/validate-ibv-devinfo.sh).

The **full** `ibv_devinfo` (without `-l`) is intentionally not
supported: after `ibv_get_device_list` claims a device, libmlx5's
`verbs_open_device` issues real uverbs `ioctl()`s on
`/dev/infiniband/uverbsN`. Faking the entire mlx5 uverbs ioctl surface
from a userspace `LD_PRELOAD` shim is well beyond the scope of this
mock; per-port state (which `ibv_devinfo` would otherwise print) is
covered by `ibstatus`, which reads the same sysfs files.

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
# produces three sonamed libs (each with .so -> .so.1 -> .so.1.0.0):
#   libibmocksys.so    – sysfs/dev path redirect
#   libibmockumad.so   – libibumad proxy for ibping / iblinkinfo
#   libibmockverbs.so  – /dev/infiniband/uverbsN proxy for ibv_devinfo
```

The shims only build on Linux (use `dlfcn.h`, glibc-specific signatures
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
# Unit tests (renderer + daemon + subnet, no shim required)
go test ./pkg/network/mockib/...

# Integration test: builds the shim, runs real ibstat against a rendered
# tree, asserts on the output. Linux-only; skipped otherwise.
make -C pkg/network/mockib                       # build all three shims
go test -tags=integration ./pkg/network/mockib/render/...

# ibping loopback integration (requires infiniband-diags + mock-ib)
go build -o /tmp/mock-ib ./cmd/mock-ib
go test -tags=integration ./pkg/network/mockib/render/ -run TestIbping -v
```

### Cluster-level dev loop

[`tests/redeploy.sh`](../../../tests/redeploy.sh) rebuilds the image
(always `--no-cache`, so C shim and renderer changes are never masked
by Docker's layer cache), loads it into Kind, and rolls out the
DaemonSet. The image is tagged with the current timestamp so each run
is distinct:

```bash
./tests/redeploy.sh                  # full rebuild + redeploy
./tests/redeploy.sh --skip-build     # only load/helm/restart an already-built image
./tests/redeploy.sh --tag dev-foo    # fixed tag for reproducible debugging
```

Companion E2E validators:

| Script | Validates |
|--------|-----------|
| [`tests/e2e/validate-ibping.sh`](../../../tests/e2e/validate-ibping.sh) | Cross-node `ibping` (LID + GUID modes). |
| [`tests/e2e/validate-iblinkinfo.sh`](../../../tests/e2e/validate-iblinkinfo.sh) | DR-walk fabric scan reports the peer's port GUID. |
| [`tests/e2e/validate-ibv-devinfo.sh`](../../../tests/e2e/validate-ibv-devinfo.sh) | `ibv_devinfo -l` claims every rendered HCA; `ibstatus` shows ACTIVE / LinkUp ports. |

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
