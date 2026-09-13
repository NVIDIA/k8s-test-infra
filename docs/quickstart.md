# Quick Start

Install Mokka on a Kubernetes cluster and watch `nvidia-smi` report GPUs that do
not exist. Five minutes, no NVIDIA hardware.

## Prerequisites

- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/) — the chart is served from an OCI registry.
- [kubectl](https://kubernetes.io/docs/reference/kubectl/), pointed at the cluster you want to use
- Docker and [Kind](https://kind.sigs.k8s.io/), only if you want a throwaway, local cluster

## Install

```bash
kind create cluster --name mokka

helm install nvml-mock \
    oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka \
    --create-namespace
```

Already have a cluster? Skip the `kind` line — the rest runs against your
current context.

## Verify

```bash
kubectl exec -n mokka ds/nvml-mock -- nvidia-smi -L
```

```text
GPU 0: NVIDIA GB300 NVL (UUID: GPU-...)
GPU 1: NVIDIA GB300 NVL (UUID: GPU-...)
GPU 2: NVIDIA GB300 NVL (UUID: GPU-...)
GPU 3: NVIDIA GB300 NVL (UUID: GPU-...)
```

That is the real `nvidia-smi` binary, unmodified, reading Mokka's driver instead
of a physical one. `nvidia-smi -q` works too, and reports the full profile.

## Change the GPU model

`gb300` is the default. Every node in the cluster takes the same profile:

```bash
helm upgrade nvml-mock \
    oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka \
    --set gpu.profile=a100
```

Seven profiles ship with the chart: `a100`, `b200`, `gb200`, `gb300`, `h100`,
`l40s` and `t4`. [Configuration](configuration.md) covers what each one defines
and how to change individual values.

## Clean up

```bash
helm uninstall nvml-mock --namespace mokka
kind delete cluster --name mokka          # if you created one above
```

## Next steps

You have a node that *looks* like it has GPUs. The interesting part is pointing
real software at it.

| To do this | Go to |
|---|---|
| Schedule GPU workloads with the device plugin, DRA or the GPU Operator | [Installation](helm-chart.md) |
| Break a GPU and watch consumers react | [Failure injection](guides/failure-injection/README.md) |
| Give a pod GPUs without changing its spec | [Node-wide injection](guides/node-wide-injection/README.md) |
| Change temperature, power or health on a running node | [Runtime control](nvml-mock-ctl.md) |
| Understand what is actually happening | [Architecture](architecture.md) |

!!! note "Some consumers need a CDI-enabled cluster"
    The device plugin, DRA driver and GPU Operator paths expect a Kind node
    image with CDI baked into containerd. A plain `kind create cluster` does not
    provide one — [Installation](helm-chart.md) covers building it.
