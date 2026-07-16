# nri-imex demo: mock IMEX channels over NRI

Injects mock `/dev/nvidia-caps-imex-channels/channel0..N-1` device nodes into an
ordinary, unprivileged workload via the nvml-mock NRI plugin — no
`nvidia.com/gpu` request, no volumes, no `MOCK_*` env in the pod spec — and shows
a single ComputeDomain spanning two Kind nodes (issue
[#437](https://github.com/NVIDIA/k8s-test-infra/issues/437)).

## Run

```bash
./docs/demo/nri-imex/run.sh
```

## Prerequisites

- Docker, Kind, kubectl, Helm.
- `jq` — Scenario 3 parses `nvidia-imex-ctl -N -j` JSON.

The demo builds a local overlay image (`nvml-mock:nri-imex-real-imex`) that
layers the real `nvidia-imex` / `nvidia-imex-ctl` (NO GPU mode via
`imex-nogpu-shim`) onto the mock stack. This image is **local build only** — it
repackages the proprietary `nvidia-imex` binary and is never published (see
`deployments/nvml-mock/Dockerfile.compute-domain-daemon`).

## What it proves

1. A plain workload annotated `nvml-mock.nvidia.com/imex-channels: "true"` sees
   `channel0..15` on **both** workers.
2. `check-fabric` reports the same `clusterUuid` on both workers (consistent
   ComputeDomain identity, injected purely through NRI).
3. Two real `nvidia-imex` NO-GPU daemons (started via `imex-nogpu-shim`) form a
   domain across both workers: `nvidia-imex-ctl -q` reports `READY` locally and
   `nvidia-imex-ctl -N -j` reports the domain `UP` with both nodes `READY` and
   version `NO_GPU`.

## Limitation: presence-only channels

The mock channel nodes are `mknod`'d with a driverless major (default `240`), so
they are **statable and listable** but **not openable** (`open()` → `ENXIO`).
This matches the existing mock `/dev/nvidia*` nodes: consumers use the
`LD_PRELOAD`'d mock libraries rather than opening the real device. Acceptance is
channel *visibility*, not channel I/O.
