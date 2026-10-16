# Running the guides from a checkout

The pages under [Guides](../guides/README.md) are written to run without this
repository: they install published charts and images. With a clone you can also
run the same scenarios as scripts that assert the result, or bring them up
through Tilt with Mokka built from source. This page covers those paths. For
the Tilt flags themselves, see
[Local Development](local-development.md#adding-consumers).

## GPU Operator

`run.sh` performs the same install as the
[GPU Operator guide](../guides/gpu-operator.md) and then asserts the result:

```bash
cd docs/guides/with-gpu-operator && ./run.sh
```

It installs Mokka, checks that `nvidia-smi -L` works inside a mock pod,
installs the operator with the same overlay from
[`gpu-operator-values.yaml`](../guides/with-gpu-operator/gpu-operator-values.yaml), then
asserts two things that can only hold if the real operands read the mock: some
node lists `nvidia.com/gpu` as allocatable, and some node carries a
`nvidia.com/gpu.product` label. Both scan every node, because the operands do
not tolerate the control-plane `NoSchedule` taint.

| Variable | Default | Effect |
|---|---|---|
| `GPU_PROFILE` | `gb300` | Any profile under the chart's `profiles/` directory |
| `NAMESPACE` | `mokka-operator` | Namespace for the Mokka release |
| `OPERATOR_NAMESPACE` | `gpu-operator` | Namespace for the GPU Operator release |
| `GPU_OPERATOR_VERSION` | `v26.3.3` | GPU Operator chart version to install |
| `NVML_MOCK_IMAGE` | `ghcr.io/nvidia/nvml-mock:latest` | Published image to install |
| `BUILD_LOCAL` | `false` | Build the image from source and side-load it with `kind load` |
| `HELM_TIMEOUT` | `15m` | Wait budget for each Helm install |
| `DEMO_ASSUME_YES` | `false` | Skip the confirmation prompt and the co-location refusals |

> [!WARNING]
> **One Mokka release per cluster at a time**
>
> The chart's hostPath mounts are fixed and release-independent
> (`/var/lib/nvml-mock`, `/var/run/cdi`, `/run/nvidia`, and the NFD features
> directory), and it tolerates every taint, so two Mokka releases land on
> every node and write the same per-node state whatever they are called —
> including the shared config that any real GPU workload on those nodes then
> reads. `run.sh` refuses with exit `4` if another Mokka release is already
> installed anywhere on the cluster.

The values overlay exists in three copies that no test keeps in step: this
guide's, `tests/e2e/gpu-operator-values.yaml`, and
`local/gpu-operator/gpu-operator.values.yaml`, which the `e2e-gpu-operator` CI
job installs through Tilt. Change a value in all three. The header of the
guide's copy records the same provenance.

## Slinky

With the repository cloned, the Tilt environment builds Mokka from source and
runs the [Slinky guide](../guides/slinky/README.md)'s stack, with SSH into the
`slurmd` pods enabled. Tilt reports ready only once every `slurmd` pod is
`Ready`, and it prints `sinfo` once they are:

```bash
make cluster-create
tilt up -- --slinky
```

## Dynamo

The Tilt environment builds Mokka from source and runs the
[Dynamo guide](../guides/dynamo/README.md)'s stack, with an extra button that
asserts it end to end:

```bash
make cluster-create
tilt up -- --dynamo
```

## Observability (Prometheus + Grafana)

Not a standalone guide. It composes with the GPU Operator rather than replacing
it, so it lives in the Tilt environment instead of shipping its own cluster and
`run.sh`.

Prometheus scrapes the real, unmodified NVIDIA `dcgm-exporter` while it reads the
mock `libnvidia-ml.so`, and Grafana renders the result — on a cluster with no
GPUs. Two manual triggers then inject a temperature or Xid fault and fail if it
never reaches Prometheus, so the scrape path is asserted rather than eyeballed.

```bash
make cluster-create
tilt up -- --observability
```

See [Local Development](local-development.md) for the Tilt environment and every
flag it takes.

## Demos that need a checkout

Three demos are not guides because they cannot run without the repository:

| Demo | What it shows |
|---|---|
| [ComputeDomain](../guides/compute-domain/README.md) | NVLink fabric identity, with a real `nvidia-imex` forming a live domain over mock GPUs |
| [NVSentinel](../guides/nv-sentinel/README.md) | The full health loop: detect a thermal-margin crossing, cordon and drain, then auto-recover on cooldown |
| [Failure injection](../guides/failure-injection/README.md) | Present a broken GPU — uncorrectable ECC, lost, fallen off the bus — and watch consumers react |
