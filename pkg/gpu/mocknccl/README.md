# mocknccl

Hardware-free NCCL collective-communications simulation for Kubernetes
testing. Ships a mock `libnccl.so.2` so unmodified NCCL consumers
(`all_reduce_perf`, the bundled `mock-coll-perf`, anything that links
`libnccl`) run on Kind / CI with **no GPUs and no NVLink/InfiniBand
hardware**, and still report a plausible, non-zero `busbw`.

This package is the NCCL counterpart of [`pkg/gpu/mocknvml`](../mocknvml/)
and [`pkg/gpu/mockcuda`](../mockcuda/): mocknvml fakes `libnvidia-ml.so`,
mockcuda fakes `libcuda.so`, and mocknccl fakes `libnccl.so.2`. Together
they let a GPU collective benchmark execute end-to-end without a real
device.

**Acceptance goal:** a 2-pod `all_reduce`-style run on Kind completes the
MPI-free rendezvous and the rank-0 pod prints a positive
`# Avg bus bandwidth` line.

## Components

```
pkg/gpu/mocknccl/
├── bridge/                 # libnccl.so.2 — cgo c-shared NCCL ABI exports
│   ├── nccl.go             #   comm lifecycle + collectives -> engine
│   ├── helpers.go          #   main(), elementSize, maxSleep, errStr
│   ├── nccl_types.h        #   minimal NCCL ABI types (result/datatype/redop)
│   └── nccl.h              #   public prototypes for driver consumers
├── engine/                 # pure-Go cost model + comm state + rendezvous
│   ├── types.go            #   Collective enum + BusBWFactor
│   ├── model.go            #   linear cost model (EffectiveBusBW, OpTime)
│   ├── config.go           #   profile YAML + MOCK_NCCL_* env resolution
│   ├── comm.go             #   RunCollective (model -> capped host sleep)
│   └── rendezvous.go       #   MPI-free TCP barrier (rank 0 serves)
├── perf/
│   └── mock-coll-perf.c    #   nccl-tests-style driver (links mock nccl+cuda)
└── Makefile                #   builds libnccl.so.2
```

The design is the same engine/bridge split used across the repo:

1. **Bridge (`libnccl.so.2`).** A cgo `c-shared` library that exports the
   common NCCL C ABI. Each collective looks up its communicator and
   delegates to the engine; comm handles are backed by real C memory
   (keyed by pointer) so consumers can hold an opaque `ncclComm_t`.

2. **Engine (Go).** Owns the cost model, the resolved communicator
   (rank / world size / intra- vs inter-node scope), and the MPI-free
   rendezvous. It performs no data movement — it computes a *modeled*
   duration and sleeps the host for that long (subject to a cap).

3. **Timing borrows mockcuda CUDA events.** mocknccl does **not** measure
   time itself. The driver brackets its collective loop with
   `cudaEventRecord` / `cudaEventElapsedTime`, which
   [`pkg/gpu/mockcuda`](../mockcuda/engine/timing.go) implements as host
   monotonic timestamps. So the *reported* time is the real wall-clock of
   the engine's host sleep, and `busbw` is derived from that — exactly how
   nccl-tests derives it on real hardware.

## Cost model

The engine models one collective call with a single linear term:

```
time = latency + msgBytes / algbw
algbw = EffectiveBusBW / busbwFactor
```

where:

- `EffectiveBusBW` is the topology bandwidth after an efficiency factor:
  `EffectiveBusBW = (interNode ? InterBytesPerSec : IntraBytesPerSec) * efficiency`.
  Intra-node uses the aggregate NVLink bandwidth per GPU; inter-node uses
  the InfiniBand-derived bandwidth. Scope (intra vs inter) is decided by
  the rendezvous: more than one distinct pod IP ⇒ inter-node.
- `busbwFactor` is the nccl-tests bus-bandwidth factor for the collective
  (table below).

A consumer that times the call and computes
`busbw = (msgBytes / time) * busbwFactor` recovers `EffectiveBusBW` for
large messages and is latency-bound for small ones — matching real
nccl-tests reporting. When the factor is `0` (single rank), `msgBytes <= 0`,
or the effective bandwidth is `0`, only `latency` applies.

### Bus-bandwidth factor

From `engine/types.go` (`BusBWFactor(op, n)`), for `n` ranks:

| Collective | Factor | Notes |
|------------|--------|-------|
| `AllReduce` | `2*(n-1)/n` | moves the buffer twice (reduce-scatter + all-gather) |
| `AllGather` | `(n-1)/n` | |
| `ReduceScatter` | `(n-1)/n` | |
| `Broadcast` | `1` | full buffer once |
| `Reduce` | `1` | full buffer once |
| any, `n <= 1` | `0` | no inter-rank traffic (single-rank ⇒ `busbw = 0`) |

The `mock-coll-perf` driver reimplements the identical factor in C, so the
busbw it prints is consistent with the engine's modeled time.

## Configuration

Configuration is resolved in three layers, each overriding the previous:

1. **Built-in defaults** (a bare run still produces non-zero busbw).
2. **Profile YAML** at `MOCK_NCCL_CONFIG` — the `nvlink:` / `infiniband:`
   blocks derive the bandwidths, and an optional `nccl:` block tunes the
   model directly.
3. **`MOCK_NCCL_*` environment variables** — final override.

### Defaults

| Parameter | Default | Source |
|-----------|---------|--------|
| Latency | `6.0` µs | `DefaultLatencyUS` |
| Efficiency | `0.90` | `DefaultEfficiency` |
| Reported version | `22304` (NCCL 2.23.4) | `DefaultVersion` |
| Intra-node BW (fallback) | `450 GB/s` (`450e9` B/s, ≈18×25 GB/s NVLink) | `fallbackIntraBytesPerSec` |
| Inter-node BW (fallback) | `50 GB/s` (`400e9/8` B/s, one 400 Gbps NDR HCA) | `fallbackInterBytesPerSec` |

### Profile YAML — `nccl:` block

These keys are read from the profile referenced by `MOCK_NCCL_CONFIG`
(the same mock-nvml profile that drives the rest of the chart):

| Key | Type | Effect |
|-----|------|--------|
| `nccl.enabled` | bool | Mock NCCL enabled flag (default `true`). |
| `nccl.version` | int | Version returned by `ncclGetVersion` (e.g. `22304`). |
| `nccl.latency_us` | float | Per-collective latency term, microseconds. |
| `nccl.efficiency` | float | Bandwidth efficiency multiplier (0–1). |
| `nccl.intra_node_bandwidth_gbps` | float | Intra-node BW in **Gbit/s** (divided by 8 → bytes/s). Overrides the NVLink-derived value. |
| `nccl.inter_node_bandwidth_gbps` | float | Inter-node BW in **Gbit/s** (divided by 8 → bytes/s). Overrides the IB-derived value. |

### Profile-derived bandwidths (`nvlink:` / `infiniband:`)

When `nccl.intra_node_bandwidth_gbps` / `nccl.inter_node_bandwidth_gbps`
are not set, the model derives bandwidths from the same profile blocks the
NVML and IB mocks already use:

- **Intra-node (NVLink):**
  `intraBytesPerSec = nvlink.links_per_gpu * nvlink.bandwidth_per_link_gbps * 1e9`.
  Note `bandwidth_per_link_gbps` here is treated as **GB/s (bytes)** by
  convention, so there is no `/8`.
- **Inter-node (InfiniBand):**
  `interBytesPerSec = infiniband.rate_gbps * 1e9 / 8 * hca`, where `hca`
  is `infiniband.hca_count` if positive, else `infiniband.hcas_per_gpu`,
  else `1`. `rate_gbps` is Gbit/s, hence the `/8`.

### `MOCK_NCCL_*` environment variables

| Variable | Default | Meaning |
|----------|---------|---------|
| `MOCK_NCCL_CONFIG` | (unset) | Path to the profile YAML. Empty ⇒ defaults only. |
| `MOCK_NCCL_VERSION` | `22304` | Override the reported NCCL version (int > 0). |
| `MOCK_NCCL_LATENCY_US` | `6.0` | Override latency term, microseconds (≥ 0). |
| `MOCK_NCCL_EFFICIENCY` | `0.90` | Override efficiency multiplier (> 0). |
| `MOCK_NCCL_INTRA_GBPS` | (profile) | Intra-node BW in **Gbit/s** (`/8` → bytes/s). |
| `MOCK_NCCL_INTER_GBPS` | (profile) | Inter-node BW in **Gbit/s** (`/8` → bytes/s). |
| `MOCK_NCCL_MAX_SLEEP_MS` | `50` | Cap on the per-collective host sleep (ms). `0` or negative disables the cap (sleep the full modeled time). |
| `MOCK_NCCL_RDZV` | (unset) | Rendezvous address `host:port` for multi-rank `ncclCommInitRank`. |

The communicator also reads the standard launch variables:

| Variable | Meaning |
|----------|---------|
| `RANK` | This process's rank (used by the driver and as a `POD_IP` fallback). |
| `WORLD_SIZE` | Total rank count (read by the driver). |
| `POD_IP` | This rank's address for inter-node detection; falls back to `rank-<RANK>` when unset. |

## NCCL ABI surface

The mock `libnccl.so.2` exports the common NCCL collective surface
(`bridge/nccl.go`):

| Category | Functions |
|----------|-----------|
| Version / error | `ncclGetVersion`, `ncclGetUniqueId`, `ncclGetErrorString`, `ncclGetLastError` |
| Comm lifecycle | `ncclCommInitRank`, `ncclCommInitAll`, `ncclCommDestroy`, `ncclCommAbort` |
| Comm queries | `ncclCommCount`, `ncclCommUserRank`, `ncclCommCuDevice` |
| Grouping | `ncclGroupStart`, `ncclGroupEnd` (no-ops) |
| Collectives | `ncclAllReduce`, `ncclAllGather`, `ncclReduceScatter`, `ncclBroadcast`, `ncclBcast`, `ncclReduce` |

`ncclGetUniqueId` returns a zeroed id (the rendezvous is env/TCP-driven, not
id-driven). `ncclGetLastError` always reports success. Collectives compute
`msgBytes = count * elementSize(datatype)` and model the call; no buffers
are read or written.

## Running it

### 1. Two-pod test Job via the Helm chart

The nvml-mock chart ships an opt-in Indexed Job that runs `mock-coll-perf`
across `worldSize` pods, plus a headless Service used as the rendezvous
endpoint:

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set gpu.profile=a100 \
  --set nccl.test.enabled=true \
  --set nccl.test.worldSize=2
```

`nccl.test.*` values (`deployments/nvml-mock/helm/nvml-mock/values.yaml`):

| Key | Default | Meaning |
|-----|---------|---------|
| `nccl.test.enabled` | `false` | Render the test Job + rendezvous Service. |
| `nccl.test.collective` | `all_reduce` | One of `all_reduce`, `all_gather`, `reduce_scatter`, `broadcast`, `reduce`. |
| `nccl.test.worldSize` | `2` | Job completions/parallelism (= rank count). |
| `nccl.test.minBytes` | `1024` | Driver `-b`. |
| `nccl.test.maxBytes` | `67108864` | Driver `-e`. |
| `nccl.test.iters` | `20` | Driver `-n`. |
| `nccl.test.rendezvousPort` | `29500` | Headless Service / `MOCK_NCCL_RDZV` port. |
| `nccl.test.image` | `""` | Defaults to the chart's nvml-mock image. |

The Job runs in `Indexed` completion mode, so each pod derives its rank
from `JOB_COMPLETION_INDEX` (→ `RANK`), `WORLD_SIZE` from the chart value,
and `POD_IP` from the downward API. It points `MOCK_NCCL_RDZV` at the
`<release>-nccl-rdzv:29500` headless Service (whose selector pins
`job-completion-index: "0"`, so it always resolves to rank 0) and mounts
the profile ConfigMap at `MOCK_NCCL_CONFIG=/etc/nvml-mock/config.yaml`.
Rank 0 serves the rendezvous; the others dial it; once all `WORLD_SIZE`
ranks register, every rank gets the roster and the collective loop runs.

### 2. The `mock-coll-perf` driver

```
mock-coll-perf [all_reduce|all_gather|reduce_scatter|broadcast|reduce] \
               -b <minBytes> -e <maxBytes> -f <factor> -n <iters>
```

Defaults: op `all_reduce`, `-b 1024`, `-e 67108864`, `-f 2`, `-n 20`.
`factor` is clamped to `>= 2` so the size loop always advances. `RANK` and
`WORLD_SIZE` come from the environment; for `WORLD_SIZE > 1` the comm
rendezvous runs inside `ncclCommInitRank` via `MOCK_NCCL_RDZV`.

Sample rank-0 output:

```
# nReps 20  nRanks 2  nccl 22304  op all_reduce
#     size(B)       count     type    redop time(us) busbw(GB/s)
        1024         256    float      sum      6.5      0.08
        2048         512    float      sum      6.6      0.15
...
    67108864    16777216    float      sum   1398.2     48.00
# Avg bus bandwidth : 21.7943
```

Only rank 0 prints the table and the final `# Avg bus bandwidth` line; the
E2E validator asserts that average is positive.

### 3. Single-process tools

Tools that build their communicators with `ncclCommInitAll` (e.g. stock
nccl-tests `all_reduce_perf -g N`) work inside a single pod with no
rendezvous: `ncclCommInitAll` creates `N` intra-node comms directly.

## Build

```bash
make -C pkg/gpu/mocknccl
# -> libnccl.so.2.23.4, with libnccl.so.2 and libnccl.so symlinks
#    (go build -buildmode=c-shared ./bridge)
```

ABI smoke test (loads the built `libnccl.so.2` and exercises a
version + `CommInitAll`/`AllReduce`/`CommDestroy` round trip):

```bash
go test -tags=integration ./tests/mocknccl/...
# or: make -C tests/mocknccl   (builds the lib first, then runs the test)
```

## End-to-end test

The E2E coverage is a **shell validator plus a CI job**, not a Go ginkgo
harness:

- [`tests/e2e/validate-nccl.sh`](../../../tests/e2e/validate-nccl.sh) waits
  for the Indexed Job to reach `complete`, finds the rank-0 pod by its
  `batch.kubernetes.io/job-completion-index=0` +
  `app.kubernetes.io/component=nccl-test` labels, reads its log, and fails
  unless the `# Avg bus bandwidth` value is positive.
- The `nccl-multinode` job in
  [`.github/workflows/nvml-mock-e2e.yaml`](../../../.github/workflows/nvml-mock-e2e.yaml)
  builds the image, creates a multi-node Kind cluster, installs the chart
  with `nccl.test.enabled=true nccl.test.worldSize=2` on an IB-enabled
  `a100` profile, and runs the validator. The 2-pod Job spreads across
  hosts via `podAntiAffinity`.

## Limitations

- **No real data movement or numerical correctness.** Collectives do not
  read or write the send/recv buffers; only the message size feeds the cost
  model. The result is a timing/bandwidth simulation, not a computation.
- **Common collective surface only.** `AllReduce`, `AllGather`,
  `ReduceScatter`, `Broadcast`/`Bcast`, and `Reduce` are modeled. There is
  no `AllToAll` and no point-to-point `ncclSend`/`ncclRecv`.
- **Sleep cap.** `MOCK_NCCL_MAX_SLEEP_MS` (default `50` ms) caps the host
  sleep per collective, so the *reported* time equals the *modeled* time
  only while the modeled time stays under the cap. For large messages whose
  modeled time exceeds the cap, the reported (and therefore measured)
  busbw is bounded by the cap rather than the modeled bandwidth — absolute
  busbw is representative, not a real-hardware measurement. Set the cap to
  `0` to sleep the full modeled time.

## See also

- [`pkg/gpu/mockcuda`](../mockcuda/) — mock `libcuda.so`; supplies the CUDA
  event timing this package depends on.
- [`pkg/gpu/mocknvml`](../mocknvml/) — mock `libnvidia-ml.so` and the GPU
  profiles whose `nvlink:` / `infiniband:` blocks feed the cost model.
- [Architecture Guide](../../../docs/architecture.md#mock-nccl-architecture)
  — where mocknccl fits in the overall mock stack.
- [Helm chart README](../../../deployments/nvml-mock/helm/nvml-mock/README.md)
  — install instructions and the `nccl.test.*` reference.
