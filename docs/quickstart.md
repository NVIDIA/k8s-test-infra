# Quick Start

Install Mokka on a Kubernetes cluster and watch `nvidia-smi` report GPUs that do
not exist. Five minutes, no NVIDIA hardware.

## Prerequisites

- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/) — the chart is served from an OCI registry.
- [kubectl](https://kubernetes.io/docs/reference/kubectl/), pointed at the cluster you want to use
- Docker and [Kind](https://kind.sigs.k8s.io/), only if you want a throwaway, local cluster

## Install

```bash
kind create cluster --name mokka \
    --image ghcr.io/nvidia/mokka-kind-node:latest

helm install nvml-mock \
    oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka \
    --create-namespace \
    --wait --timeout 2m
```

Already have a cluster? Skip the `kind` line when its container runtime has NRI
enabled. Managed clusters need runtime preparation before Mokka can inject the
mock into workloads; see [Installation](helm-chart.md#prerequisites) for the
containerd setting and the [Amazon EKS guide](guides/install/aws/eks/README.md)
for a validated managed-cluster setup.

`latest` follows Mokka's main branch and is the simplest way to try it. For
repeatable CI, select a published release tag or digest from the
[`mokka-kind-node` package](https://github.com/NVIDIA/k8s-test-infra/pkgs/container/mokka-kind-node)
instead.

## Verify

Create an ordinary workload with no GPU request, host mount, or Mokka-specific
environment. The default NRI component supplies the mock stack when containerd
creates it:

```bash
kubectl run mokka-check --image=debian:bookworm-slim \
  --restart=Never --command -- nvidia-smi -L
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/mokka-check \
  --timeout=120s
kubectl logs mokka-check
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
kubectl delete pod mokka-check --ignore-not-found
kind delete cluster --name mokka          # if you created one above
```

## Next steps

You have a node that *looks* like it has GPUs. The interesting part is pointing
real software at it.

| To do this | Go to |
|---|---|
| Schedule GPU workloads with the device plugin, DRA or the GPU Operator | [Installation](helm-chart.md) |
| Break a GPU and watch consumers react | [Failure injection](guides/failure-injection/README.md) |
| Understand how ordinary pods receive the mock | [NRI Plugin](components/nri-plugin.md) |
| Change temperature, power or health on a running node | [Runtime control](nvml-mock-ctl.md) |
| Understand what is actually happening | [Architecture](architecture.md) |

The published KIND node image enables the Node Resource Interface (NRI), and
includes the NVIDIA container runtime with the Container Device Interface
(CDI) enabled. This is the runtime setup used by the
[device plugin](guides/device-plugin.md),
[DRA](guides/dra.md), and [GPU Operator](guides/gpu-operator.md) paths. On a
managed cluster, runtime support is provider- and node-image-specific; the
[Amazon EKS guide](guides/install/aws/eks/README.md) shows the required worker
bootstrap.
