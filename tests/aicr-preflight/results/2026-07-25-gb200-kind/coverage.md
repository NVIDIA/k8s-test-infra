# AICR check coverage under Mokka

Generated 2026-07-25T07:29:58Z. Provenance: `sim`. Cluster: `kind-nvml-mock-op`. Profile: `gb200`.

Catalog source: NVIDIA/aicr recipes/validators/catalog.yaml @06256cc8 (21 checks)

## Roll-up

- Checks total: 21 (A: 14, B: 0, C: 4, G: 3)
- Meaningful pre-silicon TODAY: 66.7% (A / total)
- Reachable once tracked gaps close: 81.0% ((A + G) / total). Roadmap, not a current claim.
- Of the 14 bucket-A checks, 9 depend on the GPU stack (unlocked by Mokka) and 5 are GPU-independent (any cluster runs them).
- Executed in this run: 9 of 21 (5 pass, 4 fail, 0 skip). **12 were not run** and carry no evidence either way.
- Bucket B is empty. That is a real result, not an oversight: AICR's checks are written as integration assertions rather than value assertions, so none of them is green purely because the mock answered. The corresponding limitation is that where Mokka's values are synthetic, a check validates the path and stays silent on the values.

## Catalog

| # | Check | Area | Phase | Bucket | GPU-dep | Outcome | Provenance | Gap issue |
|---|---|---|---|---|---|---|---|---|
| 1 | `operator-health` | operator-install | deployment | A | yes | pass | sim | - |
| 2 | `expected-resources` | control-plane | deployment | A | yes | fail | sim | - |
| 3 | `gpu-operator-version` | operator-install | deployment | A | no | pass | sim | - |
| 4 | `check-nvidia-smi` | nvml-surface | deployment | A | yes | pass | sim | - |
| 5 | `nccl-all-reduce-bw` | nccl-nvlink-fabric | performance | C | yes | not-run | sim | - |
| 6 | `nccl-all-reduce-bw-net` | nccl-nvlink-fabric | performance | C | yes | not-run | sim | - |
| 7 | `nccl-all-reduce-bw-nvls` | nccl-nvlink-fabric | performance | C | yes | not-run | sim | - |
| 8 | `inference-perf` | ttft-throughput | performance | C | yes | not-run | sim | - |
| 9 | `dra-support` | dra | conformance | G | yes | fail | sim | #498 |
| 10 | `gang-scheduling` | scheduling | conformance | A | no | not-run | sim | - |
| 11 | `accelerator-metrics` | nvml-surface | conformance | A | yes | pass | sim | - |
| 12 | `ai-service-metrics` | control-plane | conformance | A | yes | fail | sim | - |
| 13 | `inference-gateway` | control-plane | conformance | A | no | not-run | sim | - |
| 14 | `pod-autoscaling` | scheduling | conformance | A | yes | not-run | sim | - |
| 15 | `cluster-autoscaling` | scheduling | conformance | G | no | not-run | sim | #499 |
| 16 | `robust-controller` | control-plane | conformance | A | no | not-run | sim | - |
| 17 | `secure-accelerator-access` | dra | conformance | A | yes | not-run | sim | - |
| 18 | `slinky-slurm-health` | scheduling | conformance | A | yes | not-run | sim | - |
| 19 | `slinky-slurm-imex-channel` | nccl-nvlink-fabric | conformance | G | yes | not-run | sim | #498 |
| 20 | `gpu-operator-health` | operator-install | conformance | A | yes | pass | sim | - |
| 21 | `platform-health` | control-plane | conformance | A | no | fail | sim | - |
