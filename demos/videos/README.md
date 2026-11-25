# Mokka GPU workflows video

*Test Kubernetes GPU workflows on CPU-only nodes* is a 60-second video about
using unmodified GPU tooling with Mokka's NVML library and simulated driver
footprint. The [quick start](../../docs/quickstart.md) shows the captured
`nvidia-smi -L` output verbatim. The
[GPU Operator](../../docs/guides/gpu-operator.md) and
[DRA](../../docs/guides/dra.md) scenes show results from separate Kind clusters.
The [CI/CD scene](../../docs/guides/ci-cd.md) shows GitHub Actions setup with
a pinned Mokka chart.

The closing feature roundup covers
[MIG partitioning](../../docs/guides/mig/README.md),
[NRI injection](../../docs/guides/nri-injection.md),
[failure injection](../../docs/guides/failure-injection/README.md), and example
[integrations](../../docs/guides/README.md#scenarios). The end card points to
the [GitHub repository](https://github.com/NVIDIA/k8s-test-infra) and the
[guides website](https://nvidia.github.io/k8s-test-infra/guides/).

- [Episode source](episodes/01-mokka-gpu-workflows.json)
- [Rebuild the video](REBUILD.md)
