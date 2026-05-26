# mockibsysfs

Mock InfiniBand fabric for Kubernetes testing. Lets unmodified userspace
tools read realistic per-HCA topology on hosts without IB hardware.

| Tool class | Supported via |
|------------|----------------|
| `ibstat`, `ibstatus`, `iblinkinfo` | Fake sysfs under `$MOCK_IB_ROOT` |
| **`ibping`** | Synthetic umad fds + vendor ping MAD (`0x32`) |
| `libibverbs` / RDMA workloads | **Not supported** (placeholder device nodes only) |

This package is the InfiniBand counterpart of [`pkg/gpu/mocknvml`](../../gpu/mocknvml/).

## Components

```
pkg/network/mockibsysfs/
├── shim.c            # LD_PRELOAD: sysfs path rewrite + umad fd dispatch
├── umad_mock.c       # ibping-oriented umad(4) emulation
├── mock_ib_root.c    # shared MOCK_IB_ROOT default (/var/lib/nvml-mock/ib)
├── Makefile          # builds libibmocksys.so
├── config/           # YAML schema for the `infiniband:` profile block
└── render/           # writes a kernel-faithful sysfs tree from the schema

cmd/render-ib-sysfs/  # CLI wrapper that reads a profile YAML and invokes render
```

Three pieces work together:

1. **`libibmocksys.so` (LD_PRELOAD shim, C).** Rewrites paths under
   `/sys/class/infiniband*`, `/sys/class/infiniband_mad/`,
   `/sys/class/infiniband_verbs/`, `/sys/class/infiniband_cm/`, and
   `/dev/infiniband` to `$MOCK_IB_ROOT/...`. Opens on `/dev/infiniband/umad*`
   become synthetic fds handled by `umad_mock.c`.

2. **`render-ib-sysfs` (Go).** Writes the fake sysfs tree from the profile
   `infiniband:` block. Does **not** create `umad-bus/` (runtime only).

3. **`umad_mock.c` (C).** Implements `ioctl` / `read` / `write` / `poll` for
   ibping. Same-LID pings are answered in-process; other LIDs use a global
   file bus at `$MOCK_IB_ROOT/umad-bus/{in,out}/` for server/client mode.

In the nvml-mock DaemonSet:

- `setup.sh` renders to `/host/var/lib/nvml-mock/ib`.
- Env: `LD_PRELOAD=/usr/local/lib/libibmocksys.so.1`,
  `MOCK_IB_ROOT=/var/lib/nvml-mock/ib`.
- `infiniband-diags` in the image provides `ibping`, `ibstat`, etc.

Set `MOCK_IB_DISABLE=1` to bypass the shim.

## Limitations

- **Not real InfiniBand** — no `ib_umad` kernel driver, no RDMA, no SM.
- **`ibping` only** for the umad path (OpenIB vendor ping class `0x32`).
  Other `infiniband-diags` tools that need full MAD routing may fail.
- **`umad-bus/`** is shared state on the hostPath volume; isolate mock nodes
  accordingly.
- Default `MOCK_IB_ROOT` when unset: `/var/lib/nvml-mock/ib` (must match
  `render-ib-sysfs --output`).

## YAML schema

See the table in the previous revision — fields unchanged. Built-in profiles:
`a100`, `h100`, `b200`, `gb200` enable IB; `l40s`, `t4` disable it.

## Standalone usage

### Build

```bash
cd pkg/network/mockibsysfs
make
```

### Render

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
  --gpu-count 2 \
  --node-name dev-laptop \
  --output /tmp/ibroot
```

### ibstat

```bash
sudo apt-get install -y infiniband-diags

LD_PRELOAD=$PWD/libibmocksys.so.1 \
  MOCK_IB_ROOT=/tmp/ibroot \
  ibstat -l
```

### ibping

```bash
# Self-ping (mlx5_0 port 1 → LID 1 with default render layout)
LD_PRELOAD=$PWD/libibmocksys.so.1 \
  MOCK_IB_ROOT=/tmp/ibroot \
  ibping -c 1 -C mlx5_0 -P 1 1

# Server on mlx5_1, client on mlx5_0 pinging LID 2
LD_PRELOAD=$PWD/libibmocksys.so.1 MOCK_IB_ROOT=/tmp/ibroot \
  ibping -S -C mlx5_1 -P 1 &
sleep 1
LD_PRELOAD=$PWD/libibmocksys.so.1 MOCK_IB_ROOT=/tmp/ibroot \
  ibping -c 1 -C mlx5_0 -P 1 2
```

## Tests

```bash
go test ./pkg/network/mockibsysfs/...

make -C pkg/network/mockibsysfs
go test -tags=integration ./pkg/network/mockibsysfs/render/...
```

Integration tests require Linux, `infiniband-diags`, and a built shim.

## Why C, not Go

The shim is loaded into **every process** in the container via `LD_PRELOAD`.
A Go `c-shared` library would inject the runtime into `bash`, `kubectl`,
probes, etc. The umad and path-rewrite logic stay in ~1k lines of C; see
history in this package for the full rationale.

## See also

- [Helm chart README](../../../deployments/nvml-mock/helm/nvml-mock/README.md)
- [E2E validation scripts](../../../tests/e2e/validate-ibping.sh)
- [Standalone demo](../../../docs/demo/standalone/README.md)
