# mockibsysfs

Mock InfiniBand fabric for Kubernetes testing. Lets unmodified userspace
tools (`ibstat`, `ibstatus`, `iblinkinfo`, `libibverbs` consumers, ...)
read realistic per-HCA topology data on hosts that have no IB hardware.

This package is the InfiniBand counterpart of [`pkg/gpu/mocknvml`](../../gpu/mocknvml/):
mocknvml ships a fake `libnvidia-ml.so` so `nvidia-smi` works without
GPUs; mockibsysfs ships a fake sysfs tree so `ibstat` works without HCAs.

## Components

```
pkg/network/mockibsysfs/
├── shim.c            # LD_PRELOAD library: redirects libc filesystem calls
├── Makefile          # builds libibmocksys.so from shim.c
├── config/           # YAML schema for the `infiniband:` profile block
└── render/           # writes a kernel-faithful sysfs tree from the schema

cmd/render-ib-sysfs/  # CLI wrapper that reads a profile YAML and invokes render
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

2. **`render-ib-sysfs` (Go binary).** Reads the `infiniband:` block from
   the active mock-nvml profile YAML and writes a kernel-faithful tree
   under `--output`: per-CA `node_type`, `node_guid`, `sys_image_guid`,
   `fw_ver`, `hw_rev`, `board_id`, `hca_type`, `node_desc`; per-port
   `state`, `phys_state`, `rate`, `lid`, `sm_lid`, `cap_mask`,
   `link_layer`, `gids/0`, `pkeys/0`, `counters/*`; plus
   `infiniband_mad/{abi_version,umad*,issm*}` for `libibumad`,
   `infiniband_verbs/{abi_version,uverbs*}` for `libibverbs`, and the
   matching `dev/infiniband/*` files.

In the nvml-mock DaemonSet:

- The Dockerfile builds both pieces and installs `infiniband-diags` +
  `rdma-core` for the real userspace tools.
- `setup.sh` invokes `render-ib-sysfs` once at startup against
  `/host/var/lib/nvml-mock/ib`.
- The pod env sets `LD_PRELOAD=/usr/local/lib/libibmocksys.so.1` and
  `MOCK_IB_ROOT=/var/lib/nvml-mock/ib`, so every process in the
  container — including `kubectl exec ... ibstat` — sees the fake fabric.

Set `MOCK_IB_DISABLE=1` in any process to bypass the shim (escape hatch
for debugging the host filesystem).

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

You can use mockibsysfs outside the nvml-mock chart — for example, in a
unit test or on a developer laptop running Linux.

### 1. Build the shim

```bash
cd pkg/network/mockibsysfs
make
# produces libibmocksys.so -> libibmocksys.so.1 -> libibmocksys.so.1.0.0
```

The shim only builds on Linux (uses `dlfcn.h`, glibc-specific signatures
like `statx`). On macOS, build inside a Linux container:

```bash
docker run --rm -v "$PWD/../../..:/work" -w /work/pkg/network/mockibsysfs \
  debian:bookworm-slim sh -c \
  'apt-get update -qq && apt-get install -y -qq build-essential >/dev/null && make'
```

### 2. Render a tree

```bash
go build -o /tmp/render-ib-sysfs ./cmd/render-ib-sysfs

cat > /tmp/profile.yaml <<EOF
infiniband:
  enabled: true
  hca_type: MT4129
  rate_gbps: 400
  hcas_per_gpu: 1
EOF

/tmp/render-ib-sysfs \
  --config /tmp/profile.yaml \
  --gpu-count 4 \
  --node-name dev-laptop \
  --output /tmp/ibroot
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
go test ./pkg/network/mockibsysfs/...

# Integration test: builds the shim, runs real ibstat against a rendered
# tree, asserts on the output. Linux-only; skipped otherwise.
make -C pkg/network/mockibsysfs                       # build shim
go test -tags=integration ./pkg/network/mockibsysfs/render/...
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
