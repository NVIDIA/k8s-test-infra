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

## What it proves

1. A plain workload annotated `nvml-mock.nvidia.com/imex-channels: "true"` sees
   `channel0..15` on **both** workers.
2. `check-fabric` reports the same `clusterUuid` on both workers (consistent
   ComputeDomain identity, injected purely through NRI).
3. The real `nvidia-imex` domain reports `READY`.

## Limitation: presence-only channels

The mock channel nodes are `mknod`'d with a driverless major (default `240`), so
they are **statable and listable** but **not openable** (`open()` → `ENXIO`).
This matches the existing mock `/dev/nvidia*` nodes: consumers use the
`LD_PRELOAD`'d mock libraries rather than opening the real device. Acceptance is
channel *visibility*, not channel I/O.
