---
# The page opens with the hero <div>, so MkDocs cannot infer a title from the
# H1 below it and falls back to the filename. Set it explicitly.
title: Overview
---

<div class="mokka-hero" markdown>
![Mokka](img/logo.png)

<p class="mokka-tagline">Simulate your GPU infrastructure on CPU nodes.</p>
</div>

# Mokka

Mokka turns any Kubernetes cluster into a multi-GPU environment for testing. It
implements the NVIDIA driver interfaces that GPU software talks to, so the
device plugin, the DRA driver, the GPU Operator and `nvidia-smi` all behave as
though real hardware were present.

Use it to test scheduling, node labelling, telemetry and failure handling at a
scale — or on a laptop — where real GPUs are not available. It is a test double
for CI and test clusters, not something to run in production.

## Try it

```bash
kind create cluster --name mokka

helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka --create-namespace
```

Every node now reports mock GPUs. The [Quick Start](quickstart.md) takes it from
here.

## Tested consumers

| Consumer | What works |
|---|---|
| Node Feature Discovery | PCI vendor labels derived from the feature file Mokka writes |
| GPU Feature Discovery | Node labels derived from NVML |
| NVIDIA Device Plugin | Allocatable `nvidia.com/gpu` matches the profile, and workloads schedule against it |
| NVIDIA DRA Driver | ResourceSlices report the right GPUs, and a `ResourceClaimTemplate` pod reaches `Running` |
| NVIDIA GPU Operator | The full operand stack installs and its validator starts |
| DCGM / dcgm-exporter | Telemetry, time-varying power, and injected Xid errors |
| Run:ai fake-gpu-operator | Profile ConfigMaps published in the shape its discovery expects |

## Where to go next

<div class="grid cards" markdown>

-   **Get it running**

    Install into a KIND cluster and see simulated GPUs in five minutes.

    [Quick Start](quickstart.md)

-   **Understand how it works**

    The moving parts, how they connect, and how the system behaves.

    [Architecture](architecture.md)

-   **Do something specific**

    Task-oriented walkthroughs: the device plugin, DRA, the GPU Operator,
    failure injection, node-wide injection.

    [Guides](demo/README.md)

-   **Change Mokka**

    Local development with Tilt, the test suites, and how to submit a change.

    [Contributing](contributing/index.md)

</div>

Not sure whether Mokka does what you need? The [FAQ](faq.md) covers what it
simulates, what it does not, and which GPU models it can present.
